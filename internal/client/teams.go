package client

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/library"
)

func op(team string) string { return "/v1/teams/" + url.PathEscape(team) }

func (a *API) Teams() ([]api.Team, error) {
	var r []api.Team
	_, err := a.do("GET", "/v1/teams", nil, nil, nil, &r)
	return r, err
}

func (a *API) CreateTeam(name string) error {
	_, err := a.do("POST", "/v1/teams", api.CreateTeamRequest{Name: name}, nil, nil, nil)
	return err
}

func (a *API) Members(team string) ([]api.Member, error) {
	var r []api.Member
	_, err := a.do("GET", op(team)+"/members", nil, nil, nil, &r)
	return r, err
}

func (a *API) SetMember(team, email, role string) error {
	_, err := a.do("PUT", op(team)+"/members/"+url.PathEscape(email), api.SetMemberRequest{Role: role}, nil, nil, nil)
	return err
}

func (a *API) RemoveMember(team, email string) error {
	_, err := a.do("DELETE", op(team)+"/members/"+url.PathEscape(email), nil, nil, nil, nil)
	return err
}

func (a *API) Packages(team string) ([]api.Package, error) {
	var r []api.Package
	_, err := a.do("GET", op(team)+"/packages", nil, nil, nil, &r)
	return r, err
}

func (a *API) Publish(team, typ, name, desc, sum string, data []byte) (api.PublishResponse, error) {
	var r api.PublishResponse
	_, err := a.do("PUT", fmt.Sprintf("%s/packages/%s/%s", op(team), typ, name), nil, data, map[string]string{
		api.HeaderLibrarySHA256: sum, api.HeaderDescription: url.QueryEscape(desc)}, &r)
	return r, err
}

// GetPackage downloads one version (0 = latest).
func (a *API) GetPackage(team, typ, name string, version int64) (data []byte, v int64, sum string, err error) {
	ver := "latest"
	if version > 0 {
		ver = strconv.FormatInt(version, 10)
	}
	hdr, err := a.do("GET", fmt.Sprintf("%s/packages/%s/%s/%s", op(team), typ, name, ver), nil, nil, nil, &data)
	if err == nil {
		v, _ = strconv.ParseInt(hdr.Get(api.HeaderLibraryVersion), 10, 64)
		sum = hdr.Get(api.HeaderLibrarySHA256)
	}
	return data, v, sum, err
}

func (a *API) CreateTeamInvite(team string, req api.CreateInviteRequest) (api.Invite, error) {
	var r api.Invite
	_, err := a.do("POST", op(team)+"/invites", req, nil, nil, &r)
	return r, err
}

func (a *API) JoinTeam(code string) (api.Team, error) {
	var r api.Team
	_, err := a.do("POST", "/v1/teams/join", api.JoinTeamRequest{Code: code}, nil, nil, &r)
	return r, err
}

func (a *API) TeamShares(team string) (api.TeamShares, error) {
	var r api.TeamShares
	_, err := a.do("GET", op(team)+"/shares", nil, nil, nil, &r)
	return r, err
}

func (a *API) ShareTeam(team, other string, on bool) error {
	m := "PUT"
	if !on {
		m = "DELETE"
	}
	_, err := a.do(m, op(team)+"/shares/"+url.PathEscape(other), nil, nil, nil, nil)
	return err
}

// SyncTeamSources makes every team the user belongs to, or can read, a
// trusted source of skills and agents (searched by the skill-search MCP,
// linkable with hearth lib link), and drops the automatic ones of teams
// left. Sources the user added by hand are left alone. Returns the teams.
func (e *Engine) SyncTeamSources() ([]api.Team, bool, error) {
	teams, err := e.API.Teams()
	if err != nil {
		return nil, false, err
	}
	ss, err := LoadSources(e.Home)
	if err != nil {
		return teams, false, err
	}
	want := map[string]bool{}
	for _, t := range teams {
		want[t.Name] = true
	}
	have := map[string]bool{}
	changed := false
	out := ss[:0:0]
	for _, s := range ss {
		if s.Type == "team" {
			have[s.Team] = true
		}
		if s.Auto && !want[s.Team] {
			changed = true
			continue
		}
		out = append(out, s)
	}
	taken := map[string]bool{}
	for _, s := range out {
		taken[s.Name] = true
	}
	for _, t := range teams {
		if have[t.Name] {
			continue
		}
		name := t.Name
		if taken[name] {
			name = "team-" + t.Name
		}
		if taken[name] || !library.ValidName(name) {
			continue
		}
		out = append(out, Source{Name: name, Type: "team", Team: t.Name, Trust: "trusted", Auto: true})
		taken[name], changed = true, true
	}
	if changed {
		err = saveSources(e.Home, out)
	}
	return teams, changed, err
}

// AdminLink asks for an admin panel sign-in path (admins only).
func (a *API) AdminLink() (string, error) {
	var r struct {
		Path string `json:"path"`
	}
	_, err := a.do("POST", "/v1/web/link", struct{}{}, nil, nil, &r)
	return r.Path, err
}
