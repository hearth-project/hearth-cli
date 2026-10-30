package server

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/api"
)

var pkgNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func validPkgName(s string) bool { return pkgNameRE.MatchString(s) && !strings.Contains(s, "..") }

func (s *Server) teamRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /v1/teams", s.authed(s.handleListTeams))
	m.HandleFunc("POST /v1/teams", s.authed(s.handleCreateTeam))
	m.HandleFunc("POST /v1/teams/join", s.authed(s.handleJoinTeam))
	m.HandleFunc("POST /v1/teams/{team}/invites", s.authed(s.handleCreateTeamInvite))
	m.HandleFunc("GET /v1/teams/{team}/shares", s.authed(s.handleTeamShares))
	m.HandleFunc("PUT /v1/teams/{team}/shares/{other}", s.authed(s.handleShareTeam))
	m.HandleFunc("DELETE /v1/teams/{team}/shares/{other}", s.authed(s.handleShareTeam))
	m.HandleFunc("GET /v1/teams/{team}/members", s.authed(s.handleListMembers))
	m.HandleFunc("PUT /v1/teams/{team}/members/{email}", s.authed(s.handleSetMember))
	m.HandleFunc("DELETE /v1/teams/{team}/members/{email}", s.authed(s.handleRemoveMember))
	m.HandleFunc("GET /v1/teams/{team}/packages", s.authed(s.handleListPackages))
	m.HandleFunc("GET /v1/teams/{team}/search", s.authed(s.handleSearch))
	m.HandleFunc("PUT /v1/usage", s.authed(s.handlePutUserUsage))
	m.HandleFunc("GET /v1/usage", s.authed(s.handleGetUserUsage))
	m.HandleFunc("POST /v1/usage/prune", s.authed(s.handlePruneUserUsage))
	m.HandleFunc("PUT /v1/teams/{team}/usage", s.authed(s.handlePutUsage))
	m.HandleFunc("GET /v1/teams/{team}/usage", s.authed(s.handleGetUsage))
	m.HandleFunc("PUT /v1/teams/{team}/packages/{type}/{name}", s.authed(s.handlePublish))
	m.HandleFunc("DELETE /v1/teams/{team}/packages/{type}/{name}", s.authed(s.handleUnpublish))
	m.HandleFunc("GET /v1/teams/{team}/packages/{type}/{name}/{version}", s.authed(s.handleGetPackage))
}

func (s *Server) handleListTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := s.st.listTeams(userOf(r))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if teams == nil {
		teams = []teamRow{}
	}
	writeJSON(w, http.StatusOK, teams)
}

func (s *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	var req api.CreateTeamRequest
	if !readJSON(w, r, &req) {
		return
	}
	if !validPkgName(req.Name) {
		writeErr(w, http.StatusBadRequest, "invalid team name (lowercase letters, digits, . _ -)")
		return
	}
	if err := s.st.createTeam(userOf(r), req.Name, s.now()); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, teamRow{Name: req.Name, Role: "owner"})
}

// team loads the {team} path value with the caller's role check.
func (s *Server) team(w http.ResponseWriter, r *http.Request, min string) (teamRow, bool) {
	o, err := s.st.membership(userOf(r), r.PathValue("team"), min)
	if err != nil {
		s.writeStoreErr(w, err)
		return o, false
	}
	return o, true
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	o, ok := s.team(w, r, "member")
	if !ok {
		return
	}
	ms, err := s.st.listMembers(o.ID)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ms)
}

func (s *Server) handleSetMember(w http.ResponseWriter, r *http.Request) {
	o, ok := s.team(w, r, "owner")
	if !ok {
		return
	}
	var req api.SetMemberRequest
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.st.setMember(o.ID, normEmail(r.PathValue("email")), req.Role, s.now()); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	// Owners can remove anyone; everyone can leave.
	o, err := s.st.membership(userOf(r), r.PathValue("team"), "member")
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	email := normEmail(r.PathValue("email"))
	if o.Role != "owner" {
		me, err := s.st.emailOf(userOf(r))
		if err != nil || me != email {
			writeErr(w, http.StatusForbidden, "only owners can remove other members")
			return
		}
	}
	if err := s.st.removeMember(o.ID, email); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListPackages(w http.ResponseWriter, r *http.Request) {
	o, ok := s.team(w, r, "reader")
	if !ok {
		return
	}
	ps, err := s.st.listPackages(o.ID)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	// Hottest first (members' use, last 30 days): clients keep a team's top
	// items in every agent (hearth: [teams] use_top).
	writeJSON(w, http.StatusOK, s.teamHeat(o.ID, ps))
}

func pkgPath(r *http.Request) (typ, name string, ok bool) {
	typ, name = r.PathValue("type"), r.PathValue("name")
	return typ, name, (typ == "skill" || typ == "agent") && validPkgName(name)
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	o, ok := s.team(w, r, "maintainer")
	if !ok {
		return
	}
	typ, name, ok := pkgPath(r)
	sum := r.Header.Get(api.HeaderLibrarySHA256)
	if !ok || len(sum) != 64 {
		writeErr(w, http.StatusBadRequest, "invalid package type, name or sha256")
		return
	}
	desc, _ := url.QueryUnescape(r.Header.Get(api.HeaderDescription))
	if len(desc) > 2000 {
		desc = desc[:2000]
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.MaxLibraryBytes))
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusRequestEntityTooLarge, "package too large or empty")
		return
	}
	v, created, err := s.st.publish(o.ID, userOf(r), typ, name, desc, sum, data, packageText(data), s.now())
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if created {
		s.search.invalidate(o.ID)
	}
	writeJSON(w, http.StatusOK, api.PublishResponse{Version: v, Created: created})
}

// handleUnpublish removes a package and every version of it (a skill
// shared with something it should not carry, or no longer wanted).
func (s *Server) handleUnpublish(w http.ResponseWriter, r *http.Request) {
	o, ok := s.team(w, r, "maintainer")
	if !ok {
		return
	}
	typ, name, ok := pkgPath(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid package type or name")
		return
	}
	n, err := s.st.unpublish(o.ID, typ, name)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.search.invalidate(o.ID)
	writeJSON(w, http.StatusOK, map[string]int{"versions": n})
}

func (s *Server) handleGetPackage(w http.ResponseWriter, r *http.Request) {
	o, ok := s.team(w, r, "reader")
	if !ok {
		return
	}
	typ, name, ok := pkgPath(r)
	var version int64
	if v := r.PathValue("version"); v != "latest" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			ok = false
		}
		version = n
	}
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid package reference")
		return
	}
	p, err := s.st.getPackage(o.ID, typ, name, version)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	data, err := s.st.readObject(p.object)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.Header().Set(api.HeaderLibraryVersion, strconv.FormatInt(p.Version, 10))
	w.Header().Set(api.HeaderLibrarySHA256, p.SHA256)
	w.Header().Set("Content-Type", "application/gzip")
	w.Write(data)
}

// handleCreateTeamInvite: maintainers invite members and maintainers,
// owners also owners.
func (s *Server) handleCreateTeamInvite(w http.ResponseWriter, r *http.Request) {
	o, ok := s.team(w, r, "maintainer")
	if !ok {
		return
	}
	var req api.CreateInviteRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Role == "" {
		req.Role = "maintainer"
	}
	if _, known := roleRank[req.Role]; !known || req.Role == "reader" {
		writeErr(w, http.StatusBadRequest, "role must be member, maintainer or owner")
		return
	}
	if roleRank[req.Role] > roleRank[o.Role] {
		writeErr(w, http.StatusForbidden, "you cannot invite with a higher role than yours")
		return
	}
	if req.TTLHours <= 0 {
		req.TTLHours = 168
	}
	if req.TTLHours > 8760 || req.MaxUses < 0 {
		writeErr(w, http.StatusBadRequest, "ttl_hours at most 8760, max_uses >= 0")
		return
	}
	exp := s.now() + int64(req.TTLHours)*3600
	code, err := s.st.createTeamInvite(o.ID, userOf(r), req.Role, s.now(), exp, req.MaxUses)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, api.Invite{Code: code, Team: o.Name, Role: req.Role, ExpiresAt: exp, MaxUses: req.MaxUses})
}

func (s *Server) handleJoinTeam(w http.ResponseWriter, r *http.Request) {
	var req api.JoinTeamRequest
	if !readJSON(w, r, &req) {
		return
	}
	if !s.limiter.allow("join:"+clientIP(r), s.cfg.Now()) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again in a minute")
		return
	}
	o, err := s.st.joinTeam(userOf(r), req.Code, s.now())
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) handleTeamShares(w http.ResponseWriter, r *http.Request) {
	o, ok := s.team(w, r, "member")
	if !ok {
		return
	}
	with, from, err := s.st.teamShares(o.ID)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.TeamShares{SharedWith: with, SharedFrom: from})
}

// handleShareTeam (owners): PUT gives the members of {other} read access
// to this team's skills and agents, DELETE takes it back.
func (s *Server) handleShareTeam(w http.ResponseWriter, r *http.Request) {
	o, ok := s.team(w, r, "owner")
	if !ok {
		return
	}
	if err := s.st.shareTeam(o.ID, r.PathValue("other"), r.Method == http.MethodPut, s.now()); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	s.search.invalidate(o.ID)
	w.WriteHeader(http.StatusNoContent)
}
