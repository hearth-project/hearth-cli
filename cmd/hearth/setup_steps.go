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

// setupHeader welcomes the user and names this machine.
func setupHeader() string {
	f := client.Facts()
	hw := f.OS + " " + f.Arch
	if f.GPU != "" {
		hw += fmt.Sprintf(", %d x %s", f.GPUCount, f.GPUName)
	}
	return fmt.Sprintf("\nWelcome to Hearth %s\n\nThis sets up %s (%s) from your other machines.\n", version, shortHost(f.Hostname), hw)
}

// reportProxy says once when this process reaches the server through a
// proxy it picked by itself.
func reportProxy(out *setupOut) {
	if client.AutoProxy != "" && !proxyReported {
		proxyReported = true
		out.fact("network", "no direct route to the server; using proxy "+client.AutoProxy+" (remembered)")
	}
}

var proxyReported bool

func printRecoveryKey(email, rk string) {
	fmt.Printf("  %-*s  %s created\n\n"+
		"Your recovery key, shown once. Store it offline (a password manager), never in a\n"+
		"chat or ticket; it resets the master password. Lost or leaked: hearth recovery-key rotate\n\n"+
		"    %s\n\n", labelWidth, "account", email, rk)
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

// printSetupSummary ends setup: the next steps that apply, numbered, the
// one that matters most first, with the command to type on its own line;
// then where the rest is.
func printSetupSummary(out *setupOut, e *client.Engine, envLeft, withSkill []string, codexTrust, onDemand bool) {
	home := e.Home
	type step struct{ text, cmd string }
	var next []step
	if len(envLeft) > 0 {
		agent := "Codex"
		if ma := client.MachineAgents(home); len(ma) > 0 && ma[0] != "codex" {
			agent = map[string]string{"claude": "Claude Code", "comate": "Comate"}[ma[0]]
		}
		next = append(next, step{fmt.Sprintf("Set up the software (%s).\nOn any machine with Hearth, tell its agent:", joinMax(envLeft, 3)),
			fmt.Sprintf("恢复 %s 的 %s 环境", thisAlias(e), agent)})
	}
	if exe, err := os.Executable(); err == nil {
		if p, err := exec.LookPath("hearth"); err != nil || !sameFile(p, exe) {
			// Installed in a directory this terminal's PATH does not have yet.
			next = append(next, step{"Open a new terminal (this one does not find hearth yet), or run:", fmt.Sprintf("export PATH=\"%s:$PATH\"", filepath.Dir(exe))})
		}
	}
	if codexTrust {
		next = append(next, step{"At its next start, Codex asks once to trust Hearth's memory hooks:\nchoose \"Trust all and continue\".", ""})
	}
	if n := len(e.LearnPending()); n > 0 {
		next = append(next, step{fmt.Sprintf("%s here need your choice:", plural(n, "item", "items")), "hearth learn"})
	}
	fmt.Printf("\nHearth is ready on %s.\n", thisAlias(e))
	if len(next) > 0 {
		fmt.Println("\nNext steps:")
		for i, n := range next {
			fmt.Printf("\n  %d. %s\n", i+1, strings.ReplaceAll(n.text, "\n", "\n     "))
			if n.cmd != "" {
				fmt.Printf("\n       %s\n", n.cmd)
			}
		}
	}
	fmt.Println()
	if onDemand {
		fmt.Println("Conversations, credentials and shell lines come when you ask for them:")
		fmt.Println("hearth restore --last 10, or hearth setup --full for everything now.")
	}
	where := "your coding agent"
	if len(withSkill) > 0 {
		where = strings.Join(withSkill, " / ")
	}
	fmt.Printf("Anything else: ask %s \"用 hearth\", or run hearth status.\n", where)
	if inContainer() {
		fmt.Println("In a container, add `hearth flush` as a preStop hook so a graceful stop loses nothing.")
	}
	if out.logPath != "" {
		fmt.Println("Log: " + strings.TrimPrefix(out.logHint(), "details: "))
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

// sameFile reports whether two paths name the same file after links (the
// installer links hearth into a directory already on PATH, e.g.
// /usr/local/bin/hearth -> ~/.local/bin/hearth).
func sameFile(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}
