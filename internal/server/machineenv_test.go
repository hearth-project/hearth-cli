package server

import (
	"strings"
	"testing"
)

func TestBehind(t *testing.T) {
	rel := []string{"0.165.0", "0.164.0", "0.163.0", "0.162.1"}
	if l, n, more := behind(rel, "codex-cli 0.163.0"); l != "0.165.0" || n != 2 || more {
		t.Fatalf("%s %d %v", l, n, more)
	}
	if _, n, _ := behind(rel, "0.166.0-alpha"); n != 0 {
		t.Fatalf("newer counted behind: %d", n)
	}
	if _, n, more := behind(rel, "0.100.0"); n != 4 || !more {
		t.Fatalf("older than the list: %d %v", n, more)
	}
}

func TestMachineEnv(t *testing.T) {
	s := &Server{versions: newToolVersions()}
	s.versions.set("codex", []string{"0.165.0", "0.164.0", "0.163.0"})
	s.versions.set("codex-mixin", []string{"0.7.5", "0.7.4", "0.7.3"})
	health := `{"checked_at":1,"agents":{"codex":{"enabled":true,"installed":true,"ready":true}},
	 "tools":{"codex":{"expected":true,"installed":true,"version":"0.163.0"},
	          "codex-mixin":{"expected":true,"installed":true,"version":"0.7.3","ok":false,"detail":"provider not logged in"},
	          "claude":{"expected":true,"installed":false},"node":{"expected":false,"installed":true,"version":"24.4.1"}},
	 "recipe_todo":2,"todo_steps":["codex-mixin-import","gateway"]}`
	env := s.machineEnv(health, "")
	if env.Status != "error" {
		t.Fatalf("status %s", env.Status)
	}
	codes := map[string]EnvProblem{}
	for _, p := range env.Problems {
		codes[p.Code+":"+p.Tool] = p
	}
	if p := codes["outdated:codex"]; p.Behind != 2 || p.Latest != "0.165.0" {
		t.Fatalf("codex outdated: %+v", p)
	}
	if p := codes["outdated:codex-mixin"]; p.Behind != 2 {
		t.Fatalf("mixin outdated: %+v", p)
	}
	for _, k := range []string{"doctor:codex-mixin", "missing:claude", "steps:"} {
		if _, ok := codes[k]; !ok {
			t.Fatalf("missing %s in %+v", k, env.Problems)
		}
	}
	if s.machineEnv("{}", "").Status != "unknown" {
		t.Fatal("no report is unknown")
	}
	ok := s.machineEnv(`{"checked_at":1,"tools":{"codex":{"expected":true,"installed":true,"version":"0.165.0"}}}`, "")
	if ok.Status != "ok" || len(ok.Problems) != 0 {
		t.Fatalf("%+v", ok)
	}
}

// Tools of agents the user does not work with are not judged.
func TestMachineEnvUnusedAgents(t *testing.T) {
	s := &Server{versions: newToolVersions()}
	s.versions.set("claude", []string{"2.1.314", "2.1.313", "2.1.293"})
	s.versions.set("codex", []string{"0.166.0", "0.165.0"})
	env := s.machineEnv(`{"checked_at":1,
	 "agents":{"claude":{"enabled":true,"installed":true,"used":false},"codex":{"enabled":true,"installed":true,"ready":true,"used":true}},
	 "tools":{"claude":{"installed":true,"version":"2.1.293","ok":false},"codex":{"expected":true,"installed":true,"version":"0.165.0"}}}`, "")
	for _, p := range env.Problems {
		if p.Tool == "claude" {
			t.Fatalf("unused claude judged: %+v", p)
		}
	}
	if len(env.Problems) != 1 || env.Problems[0].Code != "outdated" || env.Problems[0].Tool != "codex" {
		t.Fatalf("%+v", env.Problems)
	}
}

func TestMachineEnvCodexBypass(t *testing.T) {
	s := &Server{versions: newToolVersions()}
	health := `{"checked_at":1,"tools":{"codex":{"expected":true,"installed":true,"version":"0.1.0",
	  "route":{"home":"/shared/x/codex_home","set_by":"/shared/x/bashrc:15 (sourced from /root/.bashrc:483)","provider":"openai","base_url":"https://api.openai.com/v1","gateway":false,"reach":false,"detail":"timeout"}},
	  "codex-mixin":{"expected":true,"installed":true,"version":"0.7.3","ok":true}}}`
	env := s.machineEnv(health, "")
	found := false
	for _, p := range env.Problems {
		if p.Code == "bypass" && p.Level == "error" && strings.Contains(p.Detail, "/root/.bashrc:483") {
			found = true
		}
	}
	if !found || env.Status != "error" {
		t.Fatalf("%+v", env)
	}
}
