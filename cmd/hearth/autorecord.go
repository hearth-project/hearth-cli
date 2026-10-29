package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/client"
)

// autoRecord keeps what a new machine needs up to date without the user
// running `hearth learn`: skills and agents added to the agents, new
// findings (credentials only with [learn] auto_credentials), and the
// user's shell lines. say prints one line per change (log.Printf in the
// daemon, fmt in setup).
func autoRecord(e *client.Engine, say func(string, ...any)) {
	r, err := e.AutoImport()
	if err != nil {
		say("import: %v", err)
	}
	for _, b := range r.Blocked {
		say("library: not added, looks like it contains a credential: %s (move the value into a file, then: hearth lib import <agent>)", b)
	}
	if len(r.Imported) > 0 {
		say("library: added %d item(s) found in the agents: %s", len(r.Imported), joinMax(r.Imported, 8))
		if _, err := e.Render(false); err != nil {
			say("render: %v", err)
		}
		if _, err := e.LibPush(false); err != nil {
			say("library push: %v", err)
		}
	}
	res, err := e.AutoLearn()
	var ok []string
	for _, r := range res {
		if r.Failed {
			say("record %s: %s", r.ID, r.Detail)
		} else {
			ok = append(ok, r.Summary())
		}
	}
	if len(ok) > 0 {
		say("recorded for new machines: %s", joinMax(ok, 6))
	}
	if err != nil {
		say("record: %v", err)
	}
	if n, err := e.RefreshShell(); err != nil {
		say("shell: %v", err)
	} else if n > 0 {
		say("shell: %d restored rc snippet(s) adjusted to this machine (lines for tools or paths it lacks are guarded)", n)
	}
	saved, err := e.SaveShell()
	for _, s := range saved {
		say("shell: %s saved (%d paragraph(s), encrypted)", s.File.Path, s.Paragraphs)
	}
	if err != nil {
		say("shell: %v", err)
	}
}

// daemonRecord runs autoRecord from the daemon, logging each message once
// until it changes.
func daemonRecord(e *client.Engine, seen map[string]string) {
	autoRecord(e, func(f string, a ...any) {
		msg := fmt.Sprintf(f, a...)
		if seen["rec/"+msg] != "" { // the same error or block again
			return
		}
		seen["rec/"+msg] = "1"
		log.Print(msg)
	})
}

// askAutoCredentials asks once (in a terminal) whether new credentials on
// this account's machines are uploaded automatically.
func askAutoCredentials(e *client.Engine) {
	if _, decided := client.AutoCredentials(e.Home); decided || !isTTY() {
		return
	}
	n := 0
	for _, f := range e.LearnPending() {
		if f.Kind == "credential" || f.Kind == "identity" {
			n++
		}
	}
	if n == 0 {
		return
	}
	fmt.Printf("%d credential(s) here are not recorded (SSH keys, agent / git logins, token files).\n", n)
	a, ok := askLine("Keep new credentials of your machines automatically (end-to-end encrypted, restored on every new machine)? [Y/n] ")
	if !ok {
		return
	}
	on := !strings.EqualFold(a, "n") && !strings.EqualFold(a, "no")
	if err := client.SetAutoCredentials(e.Home, on); err != nil {
		fmt.Println("      config:", err)
		return
	}
	if on {
		fmt.Println("      credentials are recorded automatically (off: hearth learn --auto-credentials=false)")
	} else {
		fmt.Println("      credentials only when you choose: hearth learn (on: hearth learn --auto-credentials=true)")
	}
}
