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

// setupHeader names this machine in one line.
func setupHeader() string {
	f := client.Facts()
	hw := f.OS + " " + f.Arch
	if f.GPU != "" {
		hw += fmt.Sprintf(", %d x %s", f.GPUCount, f.GPUName)
	}
	return fmt.Sprintf("Hearth setup on %s (%s)", shortHost(f.Hostname), hw)
}

// reportProxy says once when this process reaches the server through a
// proxy it picked by itself.
func reportProxy(out *setupOut) {
	if client.AutoProxy != "" && !proxyReported {
		proxyReported = true
		out.line("network", "no direct route to the server; using proxy "+client.AutoProxy+" (remembered)")
	}
}

var proxyReported bool

func printRecoveryKey(email, rk string) {
	fmt.Printf("  %-12s %s created. Recovery key, shown once: store it offline (a password manager),\n"+
		"%15snever in a chat or ticket. Lost or leaked: hearth recovery-key rotate\n%15s%s\n", "account", email, "", "", rk)
}

// libraryCounts are the skills and agents this machine has (own and linked).
func libraryCounts(home string) (skills, agents int) {
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
	return
}

// printSetupSummary ends setup with the few next steps that apply,
// numbered, the one that matters most first.
func printSetupSummary(out *setupOut, e *client.Engine, envLeft, withSkill []string, codexTrust bool) {
	home := e.Home
	var next []string
	if len(envLeft) > 0 {
		agent := "Codex"
		if ma := client.MachineAgents(home); len(ma) > 0 && ma[0] != "codex" {
			agent = map[string]string{"claude": "Claude Code", "comate": "Comate"}[ma[0]]
		}
		alias := client.MachineAlias(e.Cfg.DeviceName, factsMap())
		next = append(next, fmt.Sprintf("Set up the software (%s): on any machine with Hearth, tell its agent\n     \"恢复 %s 的 %s 环境\"",
			joinMax(envLeft, 3), alias, agent))
	}
	if exe, err := os.Executable(); err == nil {
		if p, err := exec.LookPath("hearth"); err != nil || filepath.Dir(p) != filepath.Dir(exe) {
			// Installed in a directory this terminal's PATH does not have yet.
			next = append(next, fmt.Sprintf("Open a new terminal (this one does not find hearth yet), or: export PATH=\"%s:$PATH\"", filepath.Dir(exe)))
		}
	}
	if codexTrust {
		next = append(next, "Codex asks once at its next start to trust Hearth's memory hooks: choose \"Trust all and continue\"")
	}
	if n := len(e.LearnPending()); n > 0 {
		next = append(next, fmt.Sprintf("hearth learn: %s here need your choice", plural(n, "item", "items")))
	}
	fmt.Println()
	if len(next) > 0 {
		fmt.Println("Next")
		for i, n := range next {
			fmt.Printf("  %d. %s\n", i+1, n)
		}
	}
	where := "your coding agent"
	if len(withSkill) > 0 {
		where = strings.Join(withSkill, " / ")
	}
	tail := []string{"Anything else: ask " + where + " \"用 hearth\"", "hearth status"}
	if h := out.logHint(); h != "" {
		tail = append(tail, h)
	}
	fmt.Println("  " + strings.Join(tail, " · "))
	if inContainer() {
		fmt.Println("  In a container: add `hearth flush` as a preStop hook so a graceful stop loses nothing")
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
