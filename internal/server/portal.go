package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// The user console (/app, /v1/me/...): every signed-in user sees their own
// machines, library (skills and agents; the library is stored in plaintext
// on the server, sessions and secrets are not readable here), usage and
// teams. Admin pages are added for admins (admin_api.go).

type userCtx = adminCtx

// user wraps a console handler: a valid session cookie of an active
// account; the CSRF header on anything but GET.
func (s *Server) user(h func(http.ResponseWriter, *http.Request, userCtx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(adminCookie)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "not signed in")
			return
		}
		uid, err := s.verifyAdmin(c.Value, "session")
		if err != nil || !s.activeUser(uid) {
			writeErr(w, http.StatusUnauthorized, "session expired; sign in again")
			return
		}
		if r.Method != http.MethodGet && r.Header.Get(adminHeader) != "1" {
			writeErr(w, http.StatusForbidden, "missing "+adminHeader+" header")
			return
		}
		email, _ := s.st.emailOf(uid)
		w.Header().Set("Cache-Control", "no-store")
		h(w, r, userCtx{uid: uid, email: email})
	}
}

func (s *Server) activeUser(uid int64) bool {
	var st string
	return s.st.rdb.QueryRow(`SELECT status FROM users WHERE id=?`, uid).Scan(&st) == nil && st == "active"
}

func (s *Server) portalRoutes(m *http.ServeMux) {
	u := func(p string, h func(http.ResponseWriter, *http.Request, userCtx)) { m.HandleFunc(p, s.user(h)) }
	u("GET /v1/me", s.apiMe)
	u("GET /v1/me/devices", s.apiMyDevices)
	u("DELETE /v1/me/devices/{id}", s.apiRevokeMyDevice)
	u("GET /v1/me/library", s.apiMyLibrary)
	u("GET /v1/me/teams", s.apiMyTeams)
	u("POST /v1/me/teams/{team}/invite", s.apiMyTeamInvite)
	u("DELETE /v1/me/teams/{team}", s.apiLeaveTeam)
	u("POST /v1/me/share", s.apiShareItem)
	u("POST /v1/me/intents", s.apiAddIntent)
	u("GET /v1/me/library/{type}/{name}", s.apiMyItem)
	u("GET /v1/me/teams/{team}/packages/{type}/{name}", s.apiTeamItem)
	u("GET /v1/me/settings", s.apiMySettings)
	u("GET /v1/me/intents", s.apiMyIntents)
	u("DELETE /v1/me/intents/{id}", s.apiCancelIntent)
}

func (s *Server) apiMe(w http.ResponseWriter, _ *http.Request, c userCtx) {
	_ = s.flushActivity()
	u, err := s.User(c.email)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	now := s.cfg.Now().UTC()
	start := now.AddDate(0, 0, -29).Format(time.DateOnly)
	series := make([]daySeries, 30)
	idx := map[string]int{}
	for i := range series {
		d := now.AddDate(0, 0, i-29).Format(time.DateOnly)
		series[i].Day, idx[d] = d, i
	}
	if rows, err := s.st.rdb.Query(`SELECT day, chunks, bytes FROM activity_daily WHERE user_id=? AND day>=?`, c.uid, start); err == nil {
		for rows.Next() {
			var d string
			var ch, b int64
			if rows.Scan(&d, &ch, &b) == nil {
				if i, ok := idx[d]; ok {
					series[i].Chunks, series[i].Bytes = ch, b
				}
			}
		}
		rows.Close()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"email": c.email, "admin": s.isAdmin(c.uid), "account": u, "used_bytes": u.Used(), "series": series,
		"top_skills": s.myTop(c.uid, "skill", start), "top_agents": s.myTop(c.uid, "agent", start),
		"registration": s.registration(), "server_version": s.servedVersion(),
		"install": s.installForConsole(), "feed": s.teamFeed(c.uid, 7), "pending_changes": s.countIntents(c.uid),
	})
}

func (s *Server) servedVersion() string {
	if s.dist == nil {
		return ""
	}
	return s.dist.Version
}

func (s *Server) myTop(uid int64, typ, since string) []nameCount {
	out := []nameCount{}
	rows, err := s.st.rdb.Query(`SELECT name, CAST(SUM(count) AS BIGINT) FROM user_usage_daily WHERE user_id=? AND type=? AND day>=?
		GROUP BY name ORDER BY SUM(count) DESC, name LIMIT 10`, uid, typ, since)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n nameCount
		if rows.Scan(&n.Name, &n.Count) == nil {
			out = append(out, n)
		}
	}
	return out
}

func (s *Server) apiMyDevices(w http.ResponseWriter, _ *http.Request, c userCtx) {
	s.writeDevices(w, c.uid)
}

func (s *Server) apiRevokeMyDevice(w http.ResponseWriter, r *http.Request, c userCtx) {
	if err := s.st.revokeDevice(c.uid, r.PathValue("id")); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.auth.purgeUser(c.uid)
	w.WriteHeader(http.StatusNoContent)
}

// libItem is a skill or agent of a library, as the console shows it.
type libItem struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Resident    bool   `json:"resident,omitempty"`
	Size        int64  `json:"size"`
	Uses        int64  `json:"uses"` // last 30 days, all the user's machines
}

type libSummary struct {
	Version  int64     `json:"version"`
	Items    []libItem `json:"items"`
	Resident []string  `json:"resident"`
}

// libCache keeps parsed libraries by content hash.
var libCache = struct {
	sync.Mutex
	m map[string]libSummary
}{m: map[string]libSummary{}}

// summarizeLibrary lists the skills and agents of a library bundle.
func summarizeLibrary(data []byte) (libSummary, error) {
	var out libSummary
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	tr := tar.NewReader(zr)
	skills := map[string]*libItem{}
	var resident []string
	for n := 0; n < 50000; n++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, err
		}
		name := path.Clean(hdr.Name)
		parts := strings.Split(name, "/")
		switch {
		case len(parts) >= 3 && parts[0] == "skills":
			it := skills[parts[1]]
			if it == nil {
				it = &libItem{Type: "skill", Name: parts[1]}
				skills[parts[1]] = it
			}
			it.Size += hdr.Size
			if len(parts) == 3 && parts[2] == "SKILL.md" {
				b, _ := io.ReadAll(io.LimitReader(tr, 64<<10))
				it.Description = library.SkillDescription(b)
			}
		case len(parts) == 2 && parts[0] == "agents" && strings.HasSuffix(parts[1], ".md"):
			b, _ := io.ReadAll(io.LimitReader(tr, 256<<10))
			it := libItem{Type: "agent", Name: strings.TrimSuffix(parts[1], ".md"), Size: hdr.Size}
			if a, err := library.ParseAgent(b); err == nil {
				it.Name, it.Description = a.Name, a.Description
			}
			out.Items = append(out.Items, it)
		case name == "config.toml":
			b, _ := io.ReadAll(io.LimitReader(tr, 1<<20))
			var c library.Config
			if toml.Unmarshal(b, &c) == nil && c.Skills != nil {
				resident = c.Skills.Resident
			}
		}
	}
	isRes := map[string]bool{}
	for _, r := range resident {
		isRes[r] = true
	}
	for _, it := range skills {
		it.Resident = len(resident) == 0 || isRes[it.Name]
		out.Items = append(out.Items, *it)
	}
	sort.Slice(out.Items, func(i, j int) bool {
		if out.Items[i].Type != out.Items[j].Type {
			return out.Items[i].Type == "skill"
		}
		return out.Items[i].Name < out.Items[j].Name
	})
	out.Resident = resident
	if out.Resident == nil {
		out.Resident = []string{}
	}
	return out, nil
}

func (s *Server) apiMyLibrary(w http.ResponseWriter, _ *http.Request, c userCtx) {
	lib, err := s.st.getLibrary(c.uid)
	if errors.Is(err, errNotFound) {
		writeJSON(w, http.StatusOK, libSummary{Items: []libItem{}, Resident: []string{}})
		return
	}
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	libCache.Lock()
	sum, ok := libCache.m[lib.SHA256]
	libCache.Unlock()
	if !ok {
		data, err := s.st.readObject(lib.Object)
		if err != nil {
			s.writeStoreErr(w, err)
			return
		}
		if sum, err = summarizeLibrary(data); err != nil {
			writeErr(w, http.StatusInternalServerError, "cannot read the library: "+err.Error())
			return
		}
		libCache.Lock()
		if len(libCache.m) > 1000 {
			libCache.m = map[string]libSummary{}
		}
		libCache.m[lib.SHA256] = sum
		libCache.Unlock()
	}
	sum.Version = lib.Version
	// Most used first (a copy: the cached summary is shared).
	items := append([]libItem{}, sum.Items...)
	uses := map[string]int64{}
	since := s.cfg.Now().UTC().AddDate(0, 0, -29).Format(time.DateOnly)
	if rows, err := s.st.rdb.Query(`SELECT type, name, CAST(SUM(count) AS BIGINT) FROM user_usage_daily WHERE user_id=? AND day>=? GROUP BY type, name`, c.uid, since); err == nil {
		for rows.Next() {
			var typ, name string
			var n int64
			if rows.Scan(&typ, &name, &n) == nil {
				uses[typ+"/"+name] = n
			}
		}
		rows.Close()
	}
	for i := range items {
		items[i].Uses = uses[items[i].Type+"/"+items[i].Name]
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Type != items[j].Type {
			return items[i].Type == "skill"
		}
		if items[i].Uses != items[j].Uses {
			return items[i].Uses > items[j].Uses
		}
		return items[i].Name < items[j].Name
	})
	sum.Items = items
	writeJSON(w, http.StatusOK, sum)
}

type myTeam struct {
	Name       string      `json:"name"`
	Role       string      `json:"role"`
	Via        string      `json:"via,omitempty"`
	Members    []memberRow `json:"members"`
	Packages   []teamPkg   `json:"packages"`
	SharedWith []string    `json:"shared_with"`
	SharedFrom []string    `json:"shared_from"`
}

// apiMyTeams lists the user's teams (and teams shared with them) with
// members (members only) and shared packages.
func (s *Server) apiMyTeams(w http.ResponseWriter, _ *http.Request, c userCtx) {
	ts, err := s.st.listTeams(c.uid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	out := []myTeam{}
	for _, t := range ts {
		mt := myTeam{Name: t.Name, Role: t.Role, Via: t.Via, Members: []memberRow{}, Packages: []teamPkg{}}
		if t.Role != "reader" {
			if ms, err := s.st.listMembers(t.ID); err == nil && ms != nil {
				mt.Members = ms
			}
		}
		if ps, err := s.st.listPackages(t.ID); err == nil && ps != nil {
			mt.Packages = s.teamHeat(t.ID, ps)
		}
		mt.SharedWith, mt.SharedFrom, _ = s.st.teamShares(t.ID)
		out = append(out, mt)
	}
	writeJSON(w, http.StatusOK, out)
}

// apiMyTeamInvite: maintainers and owners make an invite code (7 days).
func (s *Server) apiMyTeamInvite(w http.ResponseWriter, r *http.Request, c userCtx) {
	o, err := s.st.membership(c.uid, r.PathValue("team"), "maintainer")
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	exp := s.now() + 7*86400
	code, err := s.st.createTeamInvite(o.ID, c.uid, "maintainer", s.now(), exp, 0)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "expires_at": exp})
}

func (s *Server) apiLeaveTeam(w http.ResponseWriter, r *http.Request, c userCtx) {
	o, err := s.st.membership(c.uid, r.PathValue("team"), "member")
	if err == nil {
		err = s.st.removeMember(o.ID, c.email)
	}
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// teamPkg is a shared package with its heat: uses by the team's members
// in the last 30 days (from their usage counts) and how many used it.
type teamPkg struct {
	packageRow
	Uses  int64 `json:"uses"`
	Users int64 `json:"users"`
}

// teamHeat ranks a team's packages by how much its members use them.
func (s *Server) teamHeat(teamID int64, ps []packageRow) []teamPkg {
	since := s.cfg.Now().UTC().AddDate(0, 0, -29).Format(time.DateOnly)
	heat := map[string][2]int64{}
	if rows, err := s.st.rdb.Query(`SELECT k.type, k.name, CAST(SUM(k.count) AS BIGINT), COUNT(DISTINCT k.user_id)
		FROM user_usage_daily k JOIN team_members m ON m.user_id=k.user_id
		WHERE m.team_id=? AND k.day>=? GROUP BY k.type, k.name`, teamID, since); err == nil {
		for rows.Next() {
			var typ, name string
			var n, u int64
			if rows.Scan(&typ, &name, &n, &u) == nil {
				heat[typ+"/"+name] = [2]int64{n, u}
			}
		}
		rows.Close()
	}
	out := make([]teamPkg, 0, len(ps))
	for _, p := range ps {
		h := heat[p.Type+"/"+p.Name]
		out = append(out, teamPkg{packageRow: p, Uses: h[0], Users: h[1]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Uses != out[j].Uses {
			return out[i].Uses > out[j].Uses
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// installForConsole is the one-line installer for a new machine ("" when
// the server does not serve builds).
func (s *Server) installForConsole() string {
	if s.dist == nil {
		return ""
	}
	return s.InstallCommand()
}

type feedItem struct {
	Team        string `json:"team"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Version     int64  `json:"version"`
	PublishedBy string `json:"published_by"`
	PublishedAt int64  `json:"published_at"`
	Description string `json:"description"`
}

// teamFeed lists what was shared or updated in the user's teams (and teams
// shared with them) in the last days, newest first.
func (s *Server) teamFeed(uid int64, days int) []feedItem {
	out := []feedItem{}
	ts, err := s.st.listTeams(uid)
	if err != nil || len(ts) == 0 {
		return out
	}
	since := s.now() - int64(days)*86400
	for _, t := range ts {
		rows, err := s.st.rdb.Query(`SELECT k.type, k.name, v.version, COALESCE(u.email,''), v.published_at, v.description FROM package_versions v
			JOIN packages k ON k.id=v.package_id LEFT JOIN users u ON u.id=v.published_by
			WHERE k.team_id=? AND v.published_at>=? ORDER BY v.published_at DESC LIMIT 20`, t.ID, since)
		if err != nil {
			continue
		}
		for rows.Next() {
			f := feedItem{Team: t.Name}
			if rows.Scan(&f.Type, &f.Name, &f.Version, &f.PublishedBy, &f.PublishedAt, &f.Description) == nil {
				out = append(out, f)
			}
		}
		rows.Close()
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].PublishedAt > out[j].PublishedAt })
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}
