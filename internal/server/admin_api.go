package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// Admin panel API (/v1/admin/...). Every handler runs behind s.admin and
// records what it changed in admin_audit.

func (s *Server) adminRoutes(m *http.ServeMux) {
	// The console: /app for everyone (/admin is the same page).
	for _, base := range []string{"/app", "/admin"} {
		m.HandleFunc("GET "+base, s.handlePanel)
		m.HandleFunc("GET "+base+"/{file}", s.handlePanelAsset)
		m.HandleFunc("GET "+base+"/login", s.handleAdminLogin)
	}
	for _, p := range []string{"/v1/web", "/v1/admin"} {
		m.HandleFunc("POST "+p+"/logout", s.handleAdminLogout)
		m.HandleFunc("POST "+p+"/login", s.limited(s.handleAdminPasswordLogin))
		m.HandleFunc("POST "+p+"/link", s.authed(s.handleAdminLinkAPI))
	}
	s.portalRoutes(m)
	a := func(p string, h func(http.ResponseWriter, *http.Request, adminCtx)) { m.HandleFunc(p, s.admin(h)) }
	a("GET /v1/admin/overview", s.apiOverview)
	a("GET /v1/admin/users", s.apiUsers)
	a("GET /v1/admin/users/{email}/devices", s.apiUserDevices)
	a("POST /v1/admin/users/{email}/status", s.apiUserStatus)
	a("POST /v1/admin/users/{email}/quota", s.apiUserQuota)
	a("POST /v1/admin/users/{email}/admin", s.apiUserAdmin)
	a("POST /v1/admin/users/{email}/revoke-devices", s.apiUserRevoke)
	a("DELETE /v1/admin/users/{email}", s.apiUserDelete)
	a("GET /v1/admin/invites", s.apiInvites)
	a("POST /v1/admin/invites", s.apiCreateInvite)
	a("DELETE /v1/admin/invites/{id}", s.apiRevokeInvite)
	a("GET /v1/admin/teams", s.apiTeams)
	a("GET /v1/admin/teams/{team}", s.apiTeam)
	a("DELETE /v1/admin/teams/{team}", s.apiDeleteTeam)
	a("POST /v1/admin/teams", s.apiCreateTeam)
	a("PUT /v1/admin/teams/{team}/members/{email}", s.apiSetTeamMember)
	a("DELETE /v1/admin/teams/{team}/members/{email}", s.apiRemoveTeamMember)
	a("GET /v1/admin/settings", s.apiSettings)
	a("PUT /v1/admin/settings", s.apiPutSettings)
	a("POST /v1/admin/gc", s.apiGC)
	a("GET /v1/admin/audit", s.apiAudit)
	a("GET /v1/admin/me", func(w http.ResponseWriter, _ *http.Request, c adminCtx) {
		writeJSON(w, http.StatusOK, map[string]string{"email": c.email})
	})
}

type daySeries struct {
	Day         string `json:"day"`
	ActiveUsers int64  `json:"active_users"`
	Chunks      int64  `json:"uploads"`
	Bytes       int64  `json:"bytes"`
	Signups     int64  `json:"signups"`
}

type nameCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
	Users int64  `json:"users,omitempty"`
	// ByUser: who used a skill (top skills); Devices: machines on a
	// version (client versions).
	ByUser  []userCount    `json:"by_user,omitempty"`
	Devices []activeDevice `json:"devices,omitempty"`
}

type userCount struct {
	Email string `json:"email"`
	Count int64  `json:"count"`
}

// activeDevice is a device that talked to the server in the last 30 days.
type activeDevice struct {
	Name     string `json:"name"`
	IP       string `json:"ip"`
	Email    string `json:"email"`
	Version  string `json:"version"` // "" = client older than version reporting
	LastSeen int64  `json:"last_seen"`
	Online   bool   `json:"online"`
}

func (s *Server) apiOverview(w http.ResponseWriter, _ *http.Request, _ adminCtx) {
	_ = s.flushActivity() // include the last minute
	st, err := s.Stats()
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	var pending, teamSkills, teamAgents int64
	_ = s.st.rdb.QueryRow(`SELECT COUNT(*) FROM users WHERE status='pending'`).Scan(&pending)
	_ = s.st.rdb.QueryRow(`SELECT COALESCE(SUM(CASE WHEN type='skill' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN type='agent' THEN 1 ELSE 0 END),0) FROM packages`).Scan(&teamSkills, &teamAgents)
	now := s.cfg.Now().UTC()
	const days = 30
	start := now.AddDate(0, 0, -(days - 1)).Format(time.DateOnly)
	series := make([]daySeries, days)
	idx := map[string]int{}
	for i := range series {
		d := now.AddDate(0, 0, i-(days-1)).Format(time.DateOnly)
		series[i].Day, idx[d] = d, i
	}
	if rows, err := s.st.rdb.Query(`SELECT day, COUNT(DISTINCT user_id), SUM(chunks), SUM(bytes) FROM activity_daily WHERE day>=? GROUP BY day`, start); err == nil {
		for rows.Next() {
			var d string
			var a, c, b int64
			if rows.Scan(&d, &a, &c, &b) == nil {
				if i, ok := idx[d]; ok {
					series[i].ActiveUsers, series[i].Chunks, series[i].Bytes = a, c, b
				}
			}
		}
		rows.Close()
	}
	since := now.AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour).Unix()
	if rows, err := s.st.rdb.Query(`SELECT created_at FROM users WHERE created_at>=?`, since); err == nil {
		for rows.Next() {
			var c int64
			if rows.Scan(&c) == nil {
				if i, ok := idx[time.Unix(c, 0).UTC().Format(time.DateOnly)]; ok {
					series[i].Signups++
				}
			}
		}
		rows.Close()
	}
	skills := s.topSkills(start, 15)
	agents := s.topUsage("agent", start, 15)
	devices, versions := s.activeDevices(now.Unix() - 30*86400)
	served := ""
	if s.dist != nil {
		served = s.dist.Version
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stats": st, "pending_users": pending, "online_devices": countOnline(devices), "series": series, "top_skills": skills, "top_agents": agents, "team_skills": teamSkills, "team_agents": teamAgents, "client_versions": versions, "devices": devices,
		"served_version": served, "settings": s.Settings(), "started_at": s.started.Unix(),
	})
}

type adminUser struct {
	UserUsage
	Used   int64          `json:"used_bytes"`
	Admin  bool           `json:"admin"`
	Online []activeDevice `json:"online"` // clients online now
}

// onlineByUser returns each user's online devices, newest first.
func (s *Server) onlineByUser() map[string][]activeDevice {
	out := map[string][]activeDevice{}
	ds, _ := s.activeDevices(s.now() - int64(onlineWindow.Seconds()))
	for _, d := range ds {
		if d.Online {
			out[d.Email] = append(out[d.Email], d)
		}
	}
	return out
}

func (s *Server) apiUsers(w http.ResponseWriter, _ *http.Request, _ adminCtx) {
	us, err := s.Users()
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	admins, _ := s.Admins()
	isAdmin := map[string]bool{}
	for _, a := range admins {
		isAdmin[a] = true
	}
	online := s.onlineByUser()
	out := make([]adminUser, 0, len(us))
	for _, u := range us {
		on := online[u.Email]
		if on == nil {
			on = []activeDevice{}
		}
		out = append(out, adminUser{UserUsage: u, Used: u.Used(), Admin: isAdmin[u.Email], Online: on})
	}
	// Waiting for approval first, then by storage used (Users' order).
	sort.SliceStable(out, func(i, j int) bool { return out[i].Status == "pending" && out[j].Status != "pending" })
	writeJSON(w, http.StatusOK, out)
}

type adminDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	LastSeen  int64  `json:"last_seen"`
	Version   string `json:"version"`
	IP        string `json:"ip"`
	Online    bool   `json:"online"`
	// Conversations this device wrote last (count) and its newest activity.
	Sessions   int64 `json:"sessions"`
	LastActive int64 `json:"last_active,omitempty"`
	// Env: the environment check from its latest health report.
	Env *MachineEnv `json:"env,omitempty"`
	// Facts: OS, architecture and GPU it reported.
	Facts map[string]any `json:"facts,omitempty"`
}

func (s *Server) apiUserDevices(w http.ResponseWriter, r *http.Request, _ adminCtx) {
	uid, err := s.uidOf(r.PathValue("email"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.writeDevices(w, uid)
}

// writeDevices lists a user's devices, newest activity first.
func (s *Server) writeDevices(w http.ResponseWriter, uid int64) {
	rows, err := s.st.rdb.Query(`SELECT id,name,status,created_at,last_seen,client_version,last_ip,offline_at,machine_json,health_json FROM devices WHERE user_id=? ORDER BY last_seen DESC`, uid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	defer rows.Close()
	type agg struct{ n, last int64 }
	per := map[string]agg{}
	if r2, err := s.st.rdb.Query(`SELECT updated_by, COUNT(*), MAX(active_at) FROM sessions WHERE user_id=? AND status='active' GROUP BY updated_by`, uid); err == nil {
		for r2.Next() {
			var d string
			var a agg
			if r2.Scan(&d, &a.n, &a.last) == nil {
				per[d] = a
			}
		}
		r2.Close()
	}
	out := []adminDevice{}
	for rows.Next() {
		var d adminDevice
		var off int64
		var machineJSON, healthJSON string
		if err := rows.Scan(&d.ID, &d.Name, &d.Status, &d.CreatedAt, &d.LastSeen, &d.Version, &d.IP, &off, &machineJSON, &healthJSON); err != nil {
			s.writeStoreErr(w, err)
			return
		}
		if d.Status == "active" {
			d.Env = s.machineEnv(healthJSON, d.Version)
			var m struct {
				Facts map[string]any `json:"facts"`
			}
			if json.Unmarshal([]byte(machineJSON), &m) == nil {
				d.Facts = m.Facts
			}
		}
		d.Online = d.Status == "active" && d.LastSeen >= s.now()-int64(onlineWindow.Seconds()) && off == 0
		d.Sessions, d.LastActive = per[d.ID].n, per[d.ID].last
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}

// adminDone writes 204 and the audit line, or the error.
func (s *Server) adminDone(w http.ResponseWriter, c adminCtx, err error, action, target, detail string) {
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.Audit(c.email, action, target, detail)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiUserStatus(w http.ResponseWriter, r *http.Request, c adminCtx) {
	var req struct {
		Status string `json:"status"` // active (also: approve) | disabled
	}
	if !readJSON(w, r, &req) {
		return
	}
	email := normEmail(r.PathValue("email"))
	if req.Status != "active" && req.Status != "disabled" {
		writeErr(w, http.StatusBadRequest, "status must be active or disabled")
		return
	}
	prev, _ := s.st.userByEmail(email)
	action := "disable-user"
	if req.Status == "active" {
		action = "enable-user"
		if prev != nil && prev.Status == "pending" {
			action = "approve-user"
		}
	}
	if req.Status == "disabled" && email == c.email {
		writeErr(w, http.StatusBadRequest, "you cannot disable your own account")
		return
	}
	s.adminDone(w, c, s.SetUserStatus(email, req.Status == "active"), action, email, "")
}

func (s *Server) apiUserQuota(w http.ResponseWriter, r *http.Request, c adminCtx) {
	var req struct {
		Bytes int64 `json:"bytes"` // -1 = server default, 0 = unlimited
	}
	if !readJSON(w, r, &req) {
		return
	}
	email := normEmail(r.PathValue("email"))
	s.adminDone(w, c, s.SetUserQuota(email, req.Bytes), "set-quota", email, strconv.FormatInt(req.Bytes, 10))
}

func (s *Server) apiUserAdmin(w http.ResponseWriter, r *http.Request, c adminCtx) {
	var req struct {
		Admin bool `json:"admin"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	email := normEmail(r.PathValue("email"))
	action := "remove-admin"
	if req.Admin {
		action = "add-admin"
	}
	s.adminDone(w, c, s.SetAdmin(email, req.Admin), action, email, "")
}

func (s *Server) apiUserRevoke(w http.ResponseWriter, r *http.Request, c adminCtx) {
	email := normEmail(r.PathValue("email"))
	n, err := s.RevokeUserDevices(email)
	s.adminDone(w, c, err, "revoke-devices", email, fmt.Sprintf("%d device(s)", n))
}

func (s *Server) apiUserDelete(w http.ResponseWriter, r *http.Request, c adminCtx) {
	email := normEmail(r.PathValue("email"))
	if email == c.email {
		writeErr(w, http.StatusBadRequest, "you cannot delete your own account here")
		return
	}
	action := "delete-user"
	if u, err := s.st.userByEmail(email); err == nil && u.Status == "pending" {
		action = "reject-user"
	}
	s.adminDone(w, c, s.DeleteUser(email), action, email, "")
}

func (s *Server) apiInvites(w http.ResponseWriter, _ *http.Request, _ adminCtx) {
	in, err := s.Invites()
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if in == nil {
		in = []InviteInfo{}
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) apiCreateInvite(w http.ResponseWriter, r *http.Request, c adminCtx) {
	var req struct {
		Days int `json:"days"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Days <= 0 || req.Days > 365 {
		req.Days = 7
	}
	code, err := s.CreateInvite(time.Duration(req.Days) * 24 * time.Hour)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.Audit(c.email, "create-invite", "", fmt.Sprintf("%d days", req.Days))
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "days": req.Days})
}

func (s *Server) apiRevokeInvite(w http.ResponseWriter, r *http.Request, c adminCtx) {
	id := r.PathValue("id")
	s.adminDone(w, c, s.RevokeInvite(id), "revoke-invite", id, "")
}

type adminTeam struct {
	Name       string   `json:"name"`
	CreatedAt  int64    `json:"created_at"`
	Members    int64    `json:"members"`
	Owners     []string `json:"owners"`
	Skills     int64    `json:"skills"`
	Agents     int64    `json:"agents"`
	Bytes      int64    `json:"bytes"`
	SharedWith []string `json:"shared_with"`
}

func (s *Server) apiTeams(w http.ResponseWriter, _ *http.Request, _ adminCtx) {
	rows, err := s.st.rdb.Query(`SELECT t.id, t.name, t.created_at,
		(SELECT COUNT(*) FROM team_members m WHERE m.team_id=t.id),
		(SELECT COUNT(*) FROM packages p WHERE p.team_id=t.id AND p.type='skill'),
		(SELECT COUNT(*) FROM packages p WHERE p.team_id=t.id AND p.type='agent'),
		COALESCE((SELECT SUM(v.size_bytes) FROM package_versions v JOIN packages p ON p.id=v.package_id WHERE p.team_id=t.id), 0)
		FROM teams t ORDER BY t.name`)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	out := []adminTeam{}
	var ids []int64
	for rows.Next() {
		var t adminTeam
		var id int64
		if err := rows.Scan(&id, &t.Name, &t.CreatedAt, &t.Members, &t.Skills, &t.Agents, &t.Bytes); err != nil {
			rows.Close()
			s.writeStoreErr(w, err)
			return
		}
		out = append(out, t)
		ids = append(ids, id)
	}
	rows.Close()
	for i, id := range ids {
		out[i].Owners = []string{}
		if ms, err := s.st.listMembers(id); err == nil {
			for _, m := range ms {
				if m.Role == "owner" {
					out[i].Owners = append(out[i].Owners, m.Email)
				}
			}
		}
		if with, _, err := s.st.teamShares(id); err == nil {
			out[i].SharedWith = with
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) teamID(name string) (int64, error) {
	var id int64
	err := s.st.rdb.QueryRow(`SELECT id FROM teams WHERE name=?`, name).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("%w: no team %s", errNotFound, name)
	}
	return id, nil
}

func (s *Server) apiTeam(w http.ResponseWriter, r *http.Request, _ adminCtx) {
	id, err := s.teamID(r.PathValue("team"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	ms, err := s.st.listMembers(id)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	ps, err := s.st.listPackages(id)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if ms == nil {
		ms = []memberRow{}
	}
	if ps == nil {
		ps = []packageRow{}
	}
	with, from, _ := s.st.teamShares(id)
	writeJSON(w, http.StatusOK, map[string]any{"members": ms, "packages": s.teamHeat(id, ps), "shared_with": with, "shared_from": from})
}

// DeleteTeam removes a team, its packages (blobs queued for deletion),
// memberships, invites and shares.
func (s *Server) DeleteTeam(name string) error {
	id, err := s.teamID(name)
	if err != nil {
		return err
	}
	tx, err := s.st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO pending_deletes(object) SELECT v.object FROM package_versions v JOIN packages p ON p.id=v.package_id
		WHERE p.team_id=? ON CONFLICT DO NOTHING`, id); err != nil {
		return err
	}
	for _, q := range []string{`DELETE FROM package_versions WHERE package_id IN (SELECT id FROM packages WHERE team_id=?)`,
		`DELETE FROM usage_daily WHERE team_id=?`, `DELETE FROM teams WHERE id=?`} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.search.invalidate(id)
	return s.st.flushDeletes()
}

func (s *Server) apiDeleteTeam(w http.ResponseWriter, r *http.Request, c adminCtx) {
	name := r.PathValue("team")
	s.adminDone(w, c, s.DeleteTeam(name), "delete-team", name, "")
}

func (s *Server) apiSettings(w http.ResponseWriter, _ *http.Request, _ adminCtx) {
	writeJSON(w, http.StatusOK, s.Settings())
}

func (s *Server) apiPutSettings(w http.ResponseWriter, r *http.Request, c adminCtx) {
	var req Settings
	if !readJSON(w, r, &req) {
		return
	}
	detail := fmt.Sprintf("registration=%s default_quota=%d client_auto_update=%v", req.Registration, req.DefaultQuota, req.ClientAutoUpdate)
	s.adminDone(w, c, s.SetSettings(req), "settings", "", detail)
}

func (s *Server) apiGC(w http.ResponseWriter, r *http.Request, c adminCtx) {
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	gc, err := s.RunGC()
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	sw, err := s.SweepOrphans(time.Hour, req.DryRun)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.Audit(c.email, "gc", "", fmt.Sprintf("expired=%d deleted=%d orphans=%d dry_run=%v", gc.Expired, gc.Deleted, sw.Orphans, req.DryRun))
	writeJSON(w, http.StatusOK, map[string]any{"expired": gc.Expired, "deleted": gc.Deleted, "tombstones_purged": gc.TombstonesPurged,
		"blobs": sw.Blobs, "orphans": sw.Orphans, "orphan_bytes": sw.Bytes, "orphans_deleted": sw.Deleted})
}

type auditRow struct {
	At     int64  `json:"at"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Target string `json:"target"`
	Detail string `json:"detail"`
}

func (s *Server) apiAudit(w http.ResponseWriter, r *http.Request, _ adminCtx) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.st.rdb.Query(`SELECT at,actor,action,target,detail FROM admin_audit ORDER BY at DESC LIMIT ?`, limit)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	defer rows.Close()
	out := []auditRow{}
	for rows.Next() {
		var a auditRow
		if err := rows.Scan(&a.At, &a.Actor, &a.Action, &a.Target, &a.Detail); err != nil {
			s.writeStoreErr(w, err)
			return
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, out)
}

// topSkills ranks skills by use since day, with who used each.
func (s *Server) topSkills(since string, limit int) []nameCount {
	return s.topUsage("skill", since, limit)
}

// topUsage ranks skills or agents (typ) by use since day, with who used each.
func (s *Server) topUsage(typ, since string, limit int) []nameCount {
	out := []nameCount{}
	rows, err := s.st.rdb.Query(`SELECT name, CAST(SUM(count) AS BIGINT), COUNT(DISTINCT user_id) FROM user_usage_daily
		WHERE type=? AND day>=? GROUP BY name ORDER BY SUM(count) DESC, name LIMIT ?`, typ, since, limit)
	if err != nil {
		return out
	}
	at := map[string]int{}
	for rows.Next() {
		var n nameCount
		if rows.Scan(&n.Name, &n.Count, &n.Users) == nil {
			at[n.Name] = len(out)
			out = append(out, n)
		}
	}
	rows.Close()
	if len(out) == 0 {
		return out
	}
	rows, err = s.st.rdb.Query(`SELECT k.name, u.email, CAST(SUM(k.count) AS BIGINT) FROM user_usage_daily k JOIN users u ON u.id=k.user_id
		WHERE k.type=? AND k.day>=? GROUP BY k.name, u.email ORDER BY SUM(k.count) DESC`, typ, since)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var uc userCount
		if rows.Scan(&name, &uc.Email, &uc.Count) != nil {
			continue
		}
		if i, ok := at[name]; ok && len(out[i].ByUser) < 50 {
			out[i].ByUser = append(out[i].ByUser, uc)
		}
	}
	return out
}

// activeDevices lists the devices seen since a time, newest first, and
// groups them by client version (most devices first).
func (s *Server) activeDevices(since int64) ([]activeDevice, []nameCount) {
	devices, versions := []activeDevice{}, []nameCount{}
	rows, err := s.st.rdb.Query(`SELECT d.name, d.last_ip, u.email, d.client_version, d.last_seen, d.offline_at FROM devices d JOIN users u ON u.id=d.user_id
		WHERE d.status='active' AND d.last_seen>=? ORDER BY d.last_seen DESC LIMIT 1000`, since)
	if err != nil {
		return devices, versions
	}
	defer rows.Close()
	at := map[string]int{}
	for rows.Next() {
		var d activeDevice
		var off int64
		if rows.Scan(&d.Name, &d.IP, &d.Email, &d.Version, &d.LastSeen, &off) != nil {
			continue
		}
		d.Online = d.LastSeen >= s.now()-int64(onlineWindow.Seconds()) && off == 0
		devices = append(devices, d)
		if d.LastSeen < s.now()-86400 {
			continue // versions in use: devices seen in the last 24 hours
		}
		i, ok := at[d.Version]
		if !ok {
			i = len(versions)
			at[d.Version] = i
			versions = append(versions, nameCount{Name: d.Version})
		}
		versions[i].Count++
		versions[i].Devices = append(versions[i].Devices, d)
	}
	sort.SliceStable(versions, func(i, j int) bool { return versions[i].Count > versions[j].Count })
	return devices, versions
}

func countOnline(ds []activeDevice) (n int) {
	for _, d := range ds {
		if d.Online {
			n++
		}
	}
	return n
}

// apiCreateTeam creates a team owned by an account (default: the admin).
func (s *Server) apiCreateTeam(w http.ResponseWriter, r *http.Request, c adminCtx) {
	var req struct {
		Name  string `json:"name"`
		Owner string `json:"owner"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !validPkgName(req.Name) {
		writeErr(w, http.StatusBadRequest, "invalid team name (lowercase letters, digits, . _ -)")
		return
	}
	owner := normEmail(req.Owner)
	if owner == "" {
		owner = c.email
	}
	uid, err := s.uidOf(owner)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.createTeam(uid, req.Name, s.now()); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.Audit(c.email, "create-team", req.Name, "owner "+owner)
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name, "owner": owner})
}

// apiSetTeamMember adds an account to a team or changes its role.
func (s *Server) apiSetTeamMember(w http.ResponseWriter, r *http.Request, c adminCtx) {
	var req struct {
		Role string `json:"role"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Role == "" {
		req.Role = "member"
	}
	team, email := r.PathValue("team"), normEmail(r.PathValue("email"))
	id, err := s.teamID(team)
	if err == nil {
		err = s.st.setMember(id, email, req.Role, s.now())
	}
	s.adminDone(w, c, err, "set-team-member", team, email+" "+req.Role)
}

func (s *Server) apiRemoveTeamMember(w http.ResponseWriter, r *http.Request, c adminCtx) {
	team, email := r.PathValue("team"), normEmail(r.PathValue("email"))
	id, err := s.teamID(team)
	if err == nil {
		err = s.st.removeMember(id, email)
	}
	s.adminDone(w, c, err, "remove-team-member", team, email)
}
