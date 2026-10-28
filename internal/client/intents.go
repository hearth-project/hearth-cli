package client

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// Console changes (see server intents.go): the daemon applies them to the
// library on this machine and pushes it, so every machine gets them.

type Intent struct {
	ID   int64           `json:"id"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// HeartbeatIntents sends the heartbeat and returns how many console changes
// wait (0 from servers without intents).
func (a *API) HeartbeatIntents() (int, error) {
	var r struct {
		Intents int `json:"intents"`
	}
	_, err := a.do("POST", "/v1/devices/self/heartbeat", struct{}{}, nil, nil, &r)
	return r.Intents, err
}

// ApplyIntents carries out the waiting console changes; it returns one line
// per change for the log.
func (e *Engine) ApplyIntents() ([]string, error) {
	var list []Intent
	if _, err := e.API.do("GET", "/v1/intents", nil, nil, nil, &list); err != nil {
		return nil, err
	}
	var out []string
	changed := false
	// Start from the newest library so the push below does not conflict.
	if _, err := e.LibAuto(); err != nil {
		out = append(out, "library: "+err.Error())
	}
	for _, it := range list {
		msg, err := e.applyIntent(it)
		if err != nil {
			msg = fmt.Sprintf("%s: %v", it.Kind, err)
		} else {
			changed = true
		}
		out = append(out, msg)
		// Done either way: a change that cannot apply must not retry forever.
		_, _ = e.API.do("DELETE", "/v1/intents/"+url.PathEscape(strconv.FormatInt(it.ID, 10)), nil, nil, nil, nil)
	}
	if changed {
		if _, err := e.Render(false); err != nil {
			out = append(out, "render: "+err.Error())
		}
		if _, err := e.LibPush(false); err != nil {
			out = append(out, "library push: "+err.Error())
		}
	}
	return out, nil
}

func (e *Engine) applyIntent(it Intent) (string, error) {
	switch it.Kind {
	case "link":
		var d struct{ Type, Name, Team string }
		if err := json.Unmarshal(it.Data, &d); err != nil {
			return "", err
		}
		if _, _, err := e.SyncTeamSources(); err != nil {
			return "", err
		}
		src := ""
		ss, _ := LoadSources(e.Home)
		for _, s := range ss {
			if s.Type == "team" && s.Team == d.Team {
				src = s.Name
				break
			}
		}
		if src == "" {
			return "", fmt.Errorf("team %s is not one of your sources", d.Team)
		}
		cand, err := e.ResolvePackage(src+":"+d.Name, d.Type)
		if err != nil {
			return "", err
		}
		_, sum, err := e.Fetch(cand)
		if err != nil {
			return "", err
		}
		if _, err := e.AddLink(cand, sum); err != nil {
			return "", err
		}
		return fmt.Sprintf("linked %s %s from team %s", d.Type, d.Name, d.Team), nil
	case "unlink":
		var d struct{ Type, Name string }
		if err := json.Unmarshal(it.Data, &d); err != nil {
			return "", err
		}
		return fmt.Sprintf("unlinked %s %s", d.Type, d.Name), e.Unlink(d.Type, d.Name)
	case "resident":
		var d struct{ Names []string }
		if err := json.Unmarshal(it.Data, &d); err != nil {
			return "", err
		}
		c, err := UserConfig(e.Home)
		if err != nil {
			return "", err
		}
		if c.Skills == nil {
			c.Skills = &library.SkillsConfig{}
		}
		c.Skills.Resident = d.Names
		return fmt.Sprintf("resident skills: %d", len(d.Names)), SaveUserConfig(e.Home, c)
	case "share-top":
		var d struct{ N int }
		if err := json.Unmarshal(it.Data, &d); err != nil {
			return "", err
		}
		c, err := UserConfig(e.Home)
		if err != nil {
			return "", err
		}
		if c.Teams == nil {
			c.Teams = &library.TeamsConfig{}
		}
		n := d.N
		c.Teams.ShareTop = &n
		return fmt.Sprintf("automatic sharing: top %d", n), SaveUserConfig(e.Home, c)
	}
	return "", fmt.Errorf("unknown change %q", it.Kind)
}
