package server

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/api"
)

// Team repositories: a team owner binds a git repository and picks one of
// their own machines to sync it. That machine (its daemon, with the
// owner's git credentials) fetches the repository every day or when a
// member asks, publishes its skills and agents into the team, and pushes
// the team's own new items back as a review. The server only keeps the
// binding and the last result; it never sees a git credential.

// repoSyncEvery: a binding is due again this long after its last sync.
const repoSyncEvery = 24 * 60 * 60

var repoRefRE = regexp.MustCompile(`^[A-Za-z0-9._/-]{0,200}$`)

// validRepoURL accepts ssh://, https:// and scp-like git URLs.
func validRepoURL(u string) bool {
	if u == "" || len(u) > 500 || strings.ContainsAny(u, " \t\n\"'`$;|&<>\\") || strings.HasPrefix(u, "-") {
		return false
	}
	if p, err := url.Parse(u); err == nil && (p.Scheme == "ssh" || p.Scheme == "https" || p.Scheme == "http") {
		return p.Host != ""
	}
	// user@host:path
	at, colon := strings.Index(u, "@"), strings.Index(u, ":")
	return at > 0 && colon > at+1 && colon < len(u)-1
}

func (s *Server) teamRepoRoutes(m *http.ServeMux) {
	dev := func(h func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
		return s.authed(func(w http.ResponseWriter, r *http.Request) { h(w, r, userOf(r)) })
	}
	m.HandleFunc("GET /v1/teams/{team}/repo", dev(s.handleGetTeamRepo))
	m.HandleFunc("PUT /v1/teams/{team}/repo", dev(s.handlePutTeamRepo))
	m.HandleFunc("DELETE /v1/teams/{team}/repo", dev(s.handleDeleteTeamRepo))
	m.HandleFunc("POST /v1/teams/{team}/repo/sync", dev(s.handleSyncTeamRepo))
	m.HandleFunc("PUT /v1/teams/{team}/repo/status", s.authed(s.handleTeamRepoStatus))
	m.HandleFunc("GET /v1/devices/self/team-repos", s.authed(s.handleDeviceTeamRepos))
}

// teamRepoPortal: the same for the console (the macOS app's team page).
func (s *Server) teamRepoPortal(u func(string, func(http.ResponseWriter, *http.Request, userCtx))) {
	wrap := func(h func(http.ResponseWriter, *http.Request, int64)) func(http.ResponseWriter, *http.Request, userCtx) {
		return func(w http.ResponseWriter, r *http.Request, c userCtx) { h(w, r, c.uid) }
	}
	u("GET /v1/me/teams/{team}/repo", wrap(s.handleGetTeamRepo))
	u("PUT /v1/me/teams/{team}/repo", wrap(s.handlePutTeamRepo))
	u("DELETE /v1/me/teams/{team}/repo", wrap(s.handleDeleteTeamRepo))
	u("POST /v1/me/teams/{team}/repo/sync", wrap(s.handleSyncTeamRepo))
}

// teamOf loads the {team} path value with uid's role check.
func (s *Server) teamOf(w http.ResponseWriter, r *http.Request, uid int64, min string) (teamRow, bool) {
	o, err := s.st.membership(uid, r.PathValue("team"), min)
	if err != nil {
		s.writeStoreErr(w, err)
		return o, false
	}
	return o, true
}

func (s *Server) loadTeamRepo(teamID int64, team string) (api.TeamRepo, error) {
	r := api.TeamRepo{Team: team}
	var alias string
	err := s.st.rdb.QueryRow(`SELECT r.url,r.ref,r.push,r.new_dir,r.device_id,u.email,COALESCE(d.name,''),COALESCE(d.alias,''),r.requested_at,r.synced_at,r.status,r.detail,r.commit_id,r.review
		FROM team_repos r JOIN users u ON u.id=r.user_id LEFT JOIN devices d ON d.id=r.device_id WHERE r.team_id=?`, teamID).
		Scan(&r.URL, &r.Ref, &r.Push, &r.NewDir, &r.Device, &r.Owner, &r.DeviceName, &alias, &r.Requested, &r.SyncedAt, &r.Status, &r.Detail, &r.Commit, &r.Review)
	if errors.Is(err, sql.ErrNoRows) {
		return r, errNotFound
	}
	if alias != "" {
		r.DeviceName = alias
	}
	return r, err
}

// handleGetTeamRepo shows the binding to the team's members.
func (s *Server) handleGetTeamRepo(w http.ResponseWriter, r *http.Request, uid int64) {
	o, ok := s.teamOf(w, r, uid, "member")
	if !ok {
		return
	}
	rep, err := s.loadTeamRepo(o.ID, o.Name)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// handlePutTeamRepo binds (or changes) the team's repository: owners only,
// and the syncing device must be one of the owner's own active devices.
func (s *Server) handlePutTeamRepo(w http.ResponseWriter, r *http.Request, uid int64) {
	o, ok := s.teamOf(w, r, uid, "owner")
	if !ok {
		return
	}
	var req api.TeamRepo
	if !readJSON(w, r, &req) {
		return
	}
	req.URL, req.Ref = strings.TrimSpace(req.URL), strings.TrimSpace(req.Ref)
	if req.Push == "" {
		req.Push = "cr"
	}
	if req.NewDir == "" {
		req.NewDir = "skills"
	}
	switch {
	case !validRepoURL(req.URL):
		writeErr(w, http.StatusBadRequest, "invalid repository URL (ssh://..., https://... or user@host:path)")
		return
	case !repoRefRE.MatchString(req.Ref) || strings.Contains(req.Ref, ".."):
		writeErr(w, http.StatusBadRequest, "invalid branch")
		return
	case req.Push != "cr" && req.Push != "branch" && req.Push != "off":
		writeErr(w, http.StatusBadRequest, "push must be cr, branch or off")
		return
	case !repoRefRE.MatchString(req.NewDir) || strings.Contains(req.NewDir, "..") || !strings.HasPrefix(req.NewDir+"/", "skills/"):
		writeErr(w, http.StatusBadRequest, "new_dir must be skills or a directory under skills/")
		return
	}
	var n int
	if err := s.st.rdb.QueryRow(`SELECT COUNT(*) FROM devices WHERE id=? AND user_id=? AND status='active'`, req.Device, uid).Scan(&n); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if n == 0 {
		writeErr(w, http.StatusBadRequest, "the syncing machine must be one of your own machines (hearth machine list)")
		return
	}
	now := s.now()
	if _, err := s.st.db.Exec(`INSERT INTO team_repos(team_id,user_id,device_id,url,ref,push,new_dir,created_at,requested_at) VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(team_id) DO UPDATE SET user_id=excluded.user_id, device_id=excluded.device_id, url=excluded.url, ref=excluded.ref,
		push=excluded.push, new_dir=excluded.new_dir, requested_at=excluded.requested_at`,
		o.ID, uid, req.Device, req.URL, req.Ref, req.Push, req.NewDir, now, now); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	rep, err := s.loadTeamRepo(o.ID, o.Name)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleDeleteTeamRepo(w http.ResponseWriter, r *http.Request, uid int64) {
	o, ok := s.teamOf(w, r, uid, "owner")
	if !ok {
		return
	}
	res, err := s.st.db.Exec(`DELETE FROM team_repos WHERE team_id=?`, o.ID)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.writeStoreErr(w, errNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSyncTeamRepo asks the syncing machine to sync now (maintainers).
func (s *Server) handleSyncTeamRepo(w http.ResponseWriter, r *http.Request, uid int64) {
	o, ok := s.teamOf(w, r, uid, "maintainer")
	if !ok {
		return
	}
	res, err := s.st.db.Exec(`UPDATE team_repos SET requested_at=? WHERE team_id=?`, s.now(), o.ID)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.writeStoreErr(w, errNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeviceTeamRepos lists the bindings this device syncs, with due set
// when it should sync now. A binding whose owner is no longer the team's
// owner is left out.
func (s *Server) handleDeviceTeamRepos(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.rdb.Query(`SELECT t.id,t.name FROM team_repos r JOIN teams t ON t.id=r.team_id
		JOIN team_members m ON m.team_id=r.team_id AND m.user_id=r.user_id AND m.role='owner'
		WHERE r.device_id=? AND r.user_id=?`, deviceOf(r), userOf(r))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	type tm struct {
		id   int64
		name string
	}
	var ts []tm
	for rows.Next() {
		var x tm
		if rows.Scan(&x.id, &x.name) == nil {
			ts = append(ts, x)
		}
	}
	rows.Close()
	out := []api.TeamRepo{}
	now := s.now()
	for _, t := range ts {
		rep, err := s.loadTeamRepo(t.id, t.name)
		if err != nil {
			continue
		}
		rep.Due = rep.Requested > 0 || now-rep.SyncedAt >= repoSyncEvery
		out = append(out, rep)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleTeamRepoStatus records a sync's result; only the syncing device.
func (s *Server) handleTeamRepoStatus(w http.ResponseWriter, r *http.Request) {
	o, ok := s.team(w, r, "owner")
	if !ok {
		return
	}
	var req api.TeamRepo
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Detail) > 4000 {
		req.Detail = req.Detail[:4000]
	}
	if len(req.Review) > 500 || len(req.Commit) > 64 || len(req.Status) > 20 {
		writeErr(w, http.StatusBadRequest, "invalid status")
		return
	}
	res, err := s.st.db.Exec(`UPDATE team_repos SET synced_at=?, requested_at=0, status=?, detail=?, commit_id=?, review=CASE WHEN ?<>'' THEN ? ELSE review END
		WHERE team_id=? AND device_id=? AND user_id=?`,
		s.now(), req.Status, req.Detail, req.Commit, req.Review, req.Review, o.ID, deviceOf(r), userOf(r))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, http.StatusForbidden, "this machine does not sync the team's repository")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
