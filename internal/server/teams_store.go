package server

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var errForbidden = errors.New("forbidden")

// reader: not a member; one of the user's teams was given read access to
// this team's packages (team_shares).
var roleRank = map[string]int{"reader": 1, "member": 2, "maintainer": 3, "owner": 4}

type teamRow struct {
	ID   int64  `json:"-"`
	Name string `json:"name"`
	Role string `json:"role"`
	Via  string `json:"via,omitempty"`
}

func (s *store) createTeam(uid int64, name string, now int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRow(`INSERT INTO teams(name, created_at) VALUES(?,?) RETURNING id`, name, now).Scan(&id); err != nil {
		if isUnique(err) {
			return errExists
		}
		return err
	}
	if _, err := tx.Exec(`INSERT INTO team_members(team_id,user_id,role,added_at) VALUES(?,?,?,?)`, id, uid, "owner", now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *store) listTeams(uid int64) ([]teamRow, error) {
	rows, err := s.rdb.Query(`SELECT o.id, o.name, m.role FROM teams o JOIN team_members m ON m.team_id=o.id WHERE m.user_id=? ORDER BY o.name`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []teamRow
	member := map[int64]bool{}
	for rows.Next() {
		var o teamRow
		if err := rows.Scan(&o.ID, &o.Name, &o.Role); err != nil {
			return nil, err
		}
		member[o.ID] = true
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Teams that share their packages with one of the user's teams.
	shared, err := s.rdb.Query(`SELECT t.id, t.name, MIN(g.name) FROM team_shares sh
		JOIN teams t ON t.id=sh.team_id JOIN teams g ON g.id=sh.grantee_id
		JOIN team_members m ON m.team_id=sh.grantee_id
		WHERE m.user_id=? GROUP BY t.id, t.name ORDER BY t.name`, uid)
	if err != nil {
		return nil, err
	}
	defer shared.Close()
	for shared.Next() {
		var o teamRow
		if err := shared.Scan(&o.ID, &o.Name, &o.Via); err != nil {
			return nil, err
		}
		if !member[o.ID] {
			o.Role = "reader"
			out = append(out, o)
		}
	}
	return out, shared.Err()
}

// membership returns the team and the caller's role, requiring at least min.
// Non-members get errNotFound so team names are not disclosed.
func (s *store) membership(uid int64, team, min string) (teamRow, error) {
	var o teamRow
	err := s.rdb.QueryRow(`SELECT o.id, o.name, m.role FROM teams o JOIN team_members m ON m.team_id=o.id WHERE o.name=? AND m.user_id=?`,
		team, uid).Scan(&o.ID, &o.Name, &o.Role)
	if errors.Is(err, sql.ErrNoRows) && min == "reader" {
		err = s.rdb.QueryRow(`SELECT t.id, t.name FROM team_shares sh JOIN teams t ON t.id=sh.team_id
			JOIN team_members m ON m.team_id=sh.grantee_id WHERE t.name=? AND m.user_id=? LIMIT 1`, team, uid).Scan(&o.ID, &o.Name)
		o.Role = "reader"
	}
	if errors.Is(err, sql.ErrNoRows) {
		return o, errNotFound
	}
	if err != nil {
		return o, err
	}
	if roleRank[o.Role] < roleRank[min] {
		return o, errForbidden
	}
	return o, nil
}

type memberRow struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (s *store) listMembers(teamID int64) ([]memberRow, error) {
	rows, err := s.rdb.Query(`SELECT u.email, m.role FROM team_members m JOIN users u ON u.id=m.user_id WHERE m.team_id=? ORDER BY u.email`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []memberRow
	for rows.Next() {
		var m memberRow
		if err := rows.Scan(&m.Email, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// setMember adds a member or changes their role, keeping at least one owner.
func (s *store) setMember(teamID int64, email, role string, now int64) error {
	if _, ok := roleRank[role]; !ok || role == "reader" {
		return fmt.Errorf("%w: unknown role %q", errBadRequest, role)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var uid int64
	if err := tx.QueryRow(`SELECT id FROM users WHERE email=?`, email).Scan(&uid); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: no registered user %s", errBadRequest, email)
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO team_members(team_id,user_id,role,added_at) VALUES(?,?,?,?)
		ON CONFLICT(team_id,user_id) DO UPDATE SET role=excluded.role`, teamID, uid, role, now); err != nil {
		return err
	}
	if err := requireOwner(tx, teamID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *store) removeMember(teamID int64, email string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM team_members WHERE team_id=? AND user_id=(SELECT id FROM users WHERE email=?)`, teamID, email)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	if err := requireOwner(tx, teamID); err != nil {
		return err
	}
	return tx.Commit()
}

func requireOwner(tx *txx, teamID int64) error {
	// Serialise membership changes per team (PostgreSQL): the count below
	// then sees every committed change.
	var id int64
	if err := tx.QueryRow(`SELECT id FROM teams WHERE id=?`+tx.d.forUpdate(), teamID).Scan(&id); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM team_members WHERE team_id=? AND role='owner'`, teamID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: a team needs at least one owner", errBadRequest)
	}
	return nil
}

type packageRow struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     int64  `json:"version"`
	SHA256      string `json:"sha256"`
	PublishedBy string `json:"published_by"`
	PublishedAt int64  `json:"published_at"`
	object      string
}

const pkgCols = `p.type, p.name, v.description, v.version, v.sha256, COALESCE(u.email,''), v.published_at, v.object`

func scanPkg(sc interface{ Scan(...any) error }) (packageRow, error) {
	var p packageRow
	err := sc.Scan(&p.Type, &p.Name, &p.Description, &p.Version, &p.SHA256, &p.PublishedBy, &p.PublishedAt, &p.object)
	return p, err
}

// listPackages returns the latest version of every package.
func (s *store) listPackages(teamID int64) ([]packageRow, error) {
	rows, err := s.rdb.Query(`SELECT `+pkgCols+` FROM packages p JOIN package_versions v ON v.package_id=p.id AND v.version=p.latest
		LEFT JOIN users u ON u.id=v.published_by WHERE p.team_id=? ORDER BY p.type, p.name`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []packageRow
	for rows.Next() {
		p, err := scanPkg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// getPackage returns one version (0 = latest).
func (s *store) getPackage(teamID int64, typ, name string, version int64) (packageRow, error) {
	q := `SELECT ` + pkgCols + ` FROM packages p JOIN package_versions v ON v.package_id=p.id
		LEFT JOIN users u ON u.id=v.published_by WHERE p.team_id=? AND p.type=? AND p.name=? AND v.version=`
	args := []any{teamID, typ, name}
	if version == 0 {
		q += `p.latest`
	} else {
		q += `?`
		args = append(args, version)
	}
	p, err := scanPkg(s.rdb.QueryRow(q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return p, errNotFound
	}
	return p, err
}

// publish stores a new immutable version unless the content is unchanged.
func (s *store) publish(teamID, uid int64, typ, name, desc, sum string, data []byte, searchText string, now int64) (int64, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO packages(team_id,type,name) VALUES(?,?,?) ON CONFLICT(team_id,type,name) DO NOTHING`, teamID, typ, name); err != nil {
		return 0, false, err
	}
	var pid, latest int64
	if err := tx.QueryRow(`SELECT id, latest FROM packages WHERE team_id=? AND type=? AND name=?`+s.d.forUpdate(), teamID, typ, name).Scan(&pid, &latest); err != nil {
		return 0, false, err
	}
	if latest > 0 {
		var cur string
		if err := tx.QueryRow(`SELECT sha256 FROM package_versions WHERE package_id=? AND version=?`, pid, latest).Scan(&cur); err != nil {
			return 0, false, err
		}
		if cur == sum {
			return latest, false, nil
		}
	}
	next := latest + 1
	obj := fmt.Sprintf("teams/%d/%s/%s/%d.tar.gz", teamID, typ, name, next)
	if err := s.blobs.put(obj, data); err != nil {
		return 0, false, err
	}
	if _, err := tx.Exec(`INSERT INTO package_versions(package_id,version,sha256,object,size_bytes,description,published_by,published_at,search_text)
		VALUES(?,?,?,?,?,?,?,?,?)`, pid, next, sum, obj, len(data), desc, uid, now, searchText); err != nil {
		return 0, false, err
	}
	if _, err := tx.Exec(`UPDATE packages SET latest=?, description=? WHERE id=?`, next, desc, pid); err != nil {
		return 0, false, err
	}
	if err := bumpTeamRev(tx, teamID); err != nil {
		return 0, false, err
	}
	return next, true, tx.Commit()
}

type searchRow struct {
	Type, Name, Description, Text string
	Version                       int64
}

// searchRows returns the latest version of every package with its text.
func (s *store) searchRows(teamID int64) ([]searchRow, error) {
	rows, err := s.rdb.Query(`SELECT p.type, p.name, v.description, v.search_text, v.version FROM packages p
		JOIN package_versions v ON v.package_id=p.id AND v.version=p.latest WHERE p.team_id=?`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []searchRow
	for rows.Next() {
		var r searchRow
		if err := rows.Scan(&r.Type, &r.Name, &r.Description, &r.Text, &r.Version); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// bumpTeamRev marks the team's search index stale on every server instance.
func bumpTeamRev(tx *txx, teamID int64) error {
	_, err := tx.Exec(`UPDATE teams SET rev=rev+1 WHERE id=?`, teamID)
	return err
}

func (s *store) teamRev(teamID int64) (int64, error) {
	var rev int64
	err := s.rdb.QueryRow(`SELECT rev FROM teams WHERE id=?`, teamID).Scan(&rev)
	return rev, err
}

// Invites: a random code; only its SHA-256 is stored.
func hashInvite(code string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(code)))
	return hex.EncodeToString(h[:])
}

func newInviteCode() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "team-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

func (s *store) createTeamInvite(teamID, uid int64, role string, now, expires int64, maxUses int) (string, error) {
	code, err := newInviteCode()
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(`INSERT INTO team_invites(code_hash,team_id,role,created_by,created_at,expires_at,max_uses) VALUES(?,?,?,?,?,?,?)`,
		hashInvite(code), teamID, role, uid, now, expires, maxUses)
	return code, err
}

// joinTeam redeems an invite code. A member keeps a higher role they
// already have.
func (s *store) joinTeam(uid int64, code string, now int64) (teamRow, error) {
	var o teamRow
	tx, err := s.db.Begin()
	if err != nil {
		return o, err
	}
	defer tx.Rollback()
	var expires, maxUses, uses int64
	err = tx.QueryRow(`SELECT i.team_id, t.name, i.role, i.expires_at, i.max_uses, i.uses FROM team_invites i JOIN teams t ON t.id=i.team_id
		WHERE i.code_hash=?`, hashInvite(code)).Scan(&o.ID, &o.Name, &o.Role, &expires, &maxUses, &uses)
	if errors.Is(err, sql.ErrNoRows) {
		return o, fmt.Errorf("%w: unknown invite code", errBadRequest)
	}
	if err != nil {
		return o, err
	}
	if expires < now || (maxUses > 0 && uses >= maxUses) {
		return o, fmt.Errorf("%w: this invite code expired or was used up; ask for a new one", errBadRequest)
	}
	var cur string
	err = tx.QueryRow(`SELECT role FROM team_members WHERE team_id=? AND user_id=?`, o.ID, uid).Scan(&cur)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.Exec(`INSERT INTO team_members(team_id,user_id,role,added_at) VALUES(?,?,?,?)`, o.ID, uid, o.Role, now); err != nil {
			return o, err
		}
	case err != nil:
		return o, err
	case roleRank[cur] >= roleRank[o.Role]:
		o.Role = cur
	default:
		if _, err := tx.Exec(`UPDATE team_members SET role=? WHERE team_id=? AND user_id=?`, o.Role, o.ID, uid); err != nil {
			return o, err
		}
	}
	if _, err := tx.Exec(`UPDATE team_invites SET uses=uses+1 WHERE code_hash=?`, hashInvite(code)); err != nil {
		return o, err
	}
	return o, tx.Commit()
}

// shareTeam lets the members of grantee read teamID's packages.
func (s *store) shareTeam(teamID int64, grantee string, on bool, now int64) error {
	var gid int64
	if err := s.db.QueryRow(`SELECT id FROM teams WHERE name=?`, grantee).Scan(&gid); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: no team %s", errBadRequest, grantee)
	} else if err != nil {
		return err
	}
	if gid == teamID {
		return fmt.Errorf("%w: a team always sees its own packages", errBadRequest)
	}
	var err error
	if on {
		_, err = s.db.Exec(`INSERT INTO team_shares(team_id,grantee_id,created_at) VALUES(?,?,?) ON CONFLICT(team_id,grantee_id) DO NOTHING`, teamID, gid, now)
	} else {
		_, err = s.db.Exec(`DELETE FROM team_shares WHERE team_id=? AND grantee_id=?`, teamID, gid)
	}
	return err
}

func (s *store) teamShares(teamID int64) (with, from []string, err error) {
	q := func(sqlText string) ([]string, error) {
		rows, err := s.rdb.Query(sqlText, teamID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				return nil, err
			}
			out = append(out, n)
		}
		return out, rows.Err()
	}
	if with, err = q(`SELECT t.name FROM team_shares sh JOIN teams t ON t.id=sh.grantee_id WHERE sh.team_id=? ORDER BY t.name`); err != nil {
		return nil, nil, err
	}
	from, err = q(`SELECT t.name FROM team_shares sh JOIN teams t ON t.id=sh.team_id WHERE sh.grantee_id=? ORDER BY t.name`)
	return with, from, err
}
