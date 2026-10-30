package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/library"
)

// joinMax joins up to n items and says how many more there are.
func joinMax(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(" and %d more", len(items)-n)
}

// askRestoreOnline: in a terminal, offers the conversations the default
// restore left on machines still online (usually the machine the user is
// moving from). Returns how many were written.
func askRestoreOnline(e *client.Engine, force bool, last int) int {
	left := lastRestore.LeftOnline
	if len(left) == 0 || !isTTY() {
		return 0
	}
	n := len(left)
	if last > 0 && n > last {
		n = last
	}
	fmt.Printf("%d conversation(s) were last written on machines that are still online (left there in case you work on them):\n", len(left))
	for i, r := range left[:n] {
		if i == 5 {
			fmt.Printf("  ... and %d more\n", n-5)
			break
		}
		fmt.Printf("  %s  %-6s %s (%s, %s)\n", short(r.ID), r.Meta.Agent, truncW(titleOf(r.RemoteSession), 50),
			shortHost(machineOf(r.RemoteSession)), agoUnix(lastActive(r.RemoteSession)))
	}
	a, ok := askLine(fmt.Sprintf("Restore the %d newest of them here too? [Y/n] ", n))
	if !ok || strings.EqualFold(a, "n") || strings.EqualFold(a, "no") {
		fmt.Println("      left there; later: hearth restore --include-online (or one: hearth restore <id>)")
		return 0
	}
	only := map[string]bool{}
	for _, r := range left[:n] {
		only[r.Key] = true
		for _, k := range r.Subagents {
			only[k] = true
		}
	}
	if err := restoreAll(e, "", force, 0, only); err != nil {
		fmt.Println("      restore:", err)
	}
	return lastRestore.Written
}

// printSetupSummary ends setup with what this machine has now and the few
// next steps that apply.
func printSetupSummary(e *client.Engine, conversations, secretFiles int, envLeft []string, daemon bool, withSkill []string) {
	home := e.Home
	skills, agents := 0, 0
	if c, err := (library.Library{Root: client.LibraryDir(home)}).Load(); err == nil {
		skills, agents = len(c.Skills), len(c.Agents)
	}
	if ls, err := client.LoadLinks(home); err == nil {
		for _, l := range ls {
			if l.Type == "skill" {
				skills++
			} else {
				agents++
			}
		}
	}
	sync := "background sync on"
	if !daemon {
		sync = "background sync off (hearth daemon start)"
	}
	fmt.Printf("\ndone: %d conversation(s) restored; library: %d skill(s), %d agent(s); %d credential / config file(s) written; %s.\n",
		conversations, skills, agents, secretFiles, sync)
	var next []string
	where := "your coding agent"
	if len(withSkill) > 0 {
		where = strings.Join(withSkill, " / ")
	}
	if len(envLeft) > 0 {
		agent := "Codex"
		if ma := client.MachineAgents(home); len(ma) > 0 && ma[0] != "codex" {
			agent = map[string]string{"claude": "Claude Code", "comate": "Comate"}[ma[0]]
		}
		alias := client.MachineAlias(e.Cfg.DeviceName, factsMap())
		next = append(next, fmt.Sprintf("software of this machine (%d step(s): %s). On any other machine with Hearth, tell its agent:\n"+
			"      \"恢复 %s 的 %s 环境\"\n"+
			"    it picks the restore flow for this kind of machine and sets it up here over hearth ssh, checking each step (or here, in %s: \"用 hearth 恢复这台机器的环境\")",
			len(envLeft), joinMax(envLeft, 4), alias, agent, where))
	}
	if len(withSkill) > 0 {
		next = append(next, fmt.Sprintf("in %s, ask \"用 hearth\" for anything else (e.g. \"恢复上一台机器的会话\"); restart the agent if it was open", where))
	}
	if ps, _ := client.Proposals(home); len(ps) > 0 {
		next = append(next, fmt.Sprintf("%d setup step(s) proposed by agents: hearth recipe proposals", len(ps)))
	}
	if n := len(e.LearnPending()); n > 0 {
		next = append(next, fmt.Sprintf("hearth learn: %d item(s) here need your choice (changed since recorded, or credentials); the rest is recorded automatically", n))
	}
	next = append(next, "hearth status: check everything")
	if exe, err := os.Executable(); err == nil {
		if p, err := exec.LookPath("hearth"); err != nil || filepath.Dir(p) != filepath.Dir(exe) {
			// Installed in a directory this terminal's PATH does not have yet.
			next = append([]string{fmt.Sprintf("this terminal does not find hearth yet: open a new terminal, or run  export PATH=\"%s:$PATH\"", filepath.Dir(exe))}, next...)
		}
	}
	fmt.Println("next:")
	for _, n := range next {
		fmt.Println("  - " + n)
	}
	if inContainer() {
		fmt.Println("in a container: add `hearth flush` as a preStop hook so a graceful stop loses nothing")
	}
}

// inContainer guesses whether this runs in a container (Docker, Kubernetes).
func inContainer() bool {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	b, _ := os.ReadFile("/proc/1/cgroup")
	return strings.Contains(string(b), "docker") || strings.Contains(string(b), "kubepods")
}
