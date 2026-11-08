package main

import (
	"fmt"
	"log"

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
