package client

import (
	"strings"

	"github.com/hearth-project/hearth-cli/internal/adapter"
)

// A Codex thread the user deleted (Desktop or `codex delete`) loses both its
// rollout files and its row in Codex's state database; an archived one only
// moves. expireDeleted marks such sessions expired on the server, so they
// are not restored on other machines and the lifecycle GC removes them
// after its grace period. Only sessions this machine uploaded or restored
// are considered, and only when Codex's database is readable.
func (e *Engine) expireDeleted(present map[string]bool) (int, error) {
	root := e.CodexHome()
	if root == "" {
		return 0, nil
	}
	known, ok := adapter.CodexKnownThreads(root)
	if !ok {
		return 0, nil
	}
	type cand struct {
		sid string
		fs  fileState
	}
	var gone []cand
	tracked := 0
	all := e.st.all()
	byThread := map[string]string{} // thread id -> sid
	for sid, fs := range all {
		if id := adapter.CodexThreadID(fs.Logical); id != "" && !fs.Gone {
			byThread[id] = sid
		}
	}
	var forks desktopForks
	_ = loadJSON(e.forksPath(), &forks)
	for sid, fs := range all {
		if fs.Gone || !strings.HasPrefix(fs.Logical, "codex/") {
			continue
		}
		// Only files seen in this Codex home (not restored elsewhere, e.g.
		// with another CODEX_HOME).
		if !under(fs.Path, []string{root}) {
			continue
		}
		tracked++
		id := adapter.CodexThreadID(fs.Logical)
		if id == "" || present[sid] || known[id] || CodexRolloutExists(root, id) {
			continue
		}
		gone = append(gone, cand{sid, fs})
		// Deleting the visible copy hearth made for a restored thread
		// deletes the conversation: its archived original goes too.
		for orig, fork := range forks {
			if fork == id {
				if osid, ok := byThread[orig]; ok && osid != sid {
					gone = append(gone, cand{osid, all[osid]})
				}
			}
		}
	}
	// Many sessions vanishing at once is a moved or wiped CODEX_HOME, not
	// the user deleting conversations: leave the server alone.
	if len(gone) >= 3 && len(gone)*2 > tracked {
		if !e.warnedMassDelete {
			e.warnedMassDelete = true
			e.Logf("%d of %d synced Codex sessions are missing locally; not marking them deleted (moved CODEX_HOME?)", len(gone), tracked)
		}
		return 0, nil
	}
	n := 0
	done := map[string]bool{}
	for _, c := range gone {
		if done[c.sid] {
			continue
		}
		done[c.sid] = true
		if _, err := e.API.Expire(c.sid); err != nil && StatusOf(err) != 404 && StatusOf(err) != 410 {
			return n, err
		}
		fs := c.fs
		fs.Gone, fs.Deleted = true, true
		if err := e.st.put(c.sid, &fs); err != nil {
			return n, err
		}
		e.Logf("%s was deleted in Codex; marked expired on the server", c.fs.Logical)
		n++
	}
	return n, nil
}
