package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/checkpoint"
	"github.com/hearth-project/hearth-cli/internal/client"
)

var handoffTurns = 12

// listWide: session list --wide prints whole titles.
var listWide bool

func sessionCmd() *cobra.Command {
	c := &cobra.Command{Use: "session", Short: "Manage synced sessions"}
	c.AddCommand(sessionListCmd(), sessionFindCmd(), sessionShowCmd(), sessionRestoreCmd(), sessionScopeCmd())

	// Simple per-session actions sharing the "resolve prefix, call API" shape.
	simple := []struct {
		use, short string
		run        func(e *client.Engine, rs client.RemoteSession) (string, error)
	}{
		{"touch <id>", "Mark a session as accessed (resets its expiry clock)", func(e *client.Engine, rs client.RemoteSession) (string, error) {
			_, err := e.API.Touch(rs.ID)
			return "touched", err
		}},
		{"pin <id>", "Never expire this session", func(e *client.Engine, rs client.RemoteSession) (string, error) {
			_, err := e.API.Pin(rs.ID, true)
			return "pinned", err
		}},
		{"unpin <id>", "Allow this session to expire again", func(e *client.Engine, rs client.RemoteSession) (string, error) {
			_, err := e.API.Pin(rs.ID, false)
			return "unpinned", err
		}},
		{"rm <id>", "Delete a session on the server (local files are kept)", func(e *client.Engine, rs client.RemoteSession) (string, error) {
			return "deleted on server", e.API.DeleteSession(rs.ID)
		}},
		{"take <id>", "Force-acquire the write lease (e.g. old machine was killed)", func(e *client.Engine, rs client.RemoteSession) (string, error) {
			return "lease taken", e.Take(rs.ID)
		}},
		{"handoff <id>", "Print an agent-neutral Markdown hand-off of a session (continue it in any agent)", func(e *client.Engine, rs client.RemoteSession) (string, error) {
			data, err := e.SessionData(rs)
			if err != nil {
				return "", err
			}
			fmt.Print(checkpoint.Render(checkpoint.Source{Agent: rs.Meta.Agent, Path: rs.Meta.RelPath, Host: rs.Meta.Host, ID: short(rs.ID)},
				checkpoint.Parse(rs.Meta.Agent, data), handoffTurns))
			return "hand-off written above", nil
		}},
	}
	for _, s := range simple {
		s := s
		c.AddCommand(&cobra.Command{
			Use: s.use, Short: s.short, Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				e, err := engine()
				if err != nil {
					return err
				}
				rs, err := e.Resolve(args[0])
				if err != nil {
					return err
				}
				msg, err := s.run(e, rs)
				if err != nil {
					return err
				}
				fmt.Printf("%s  %s\n", short(rs.ID), msg)
				return nil
			},
		})
	}
	c.AddCommand(&cobra.Command{
		Use:   "revive <full-id>",
		Short: "Allow a deleted session to be uploaded again from local files",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			// Deleted sessions are no longer listed, so the full ID is needed.
			if err := e.API.Revive(args[0]); err != nil {
				return err
			}
			if err := e.Forget(args[0]); err != nil {
				return err
			}
			fmt.Println("revived; the next push will upload it again")
			return nil
		},
	})
	return c
}

func sessionShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <session-id>",
		Short: "Show one session's index entry (title, turns, machine, path, size, status)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			s, err := e.Resolve(args[0])
			if err != nil {
				return err
			}
			row := map[string]any{
				"id": s.ID, "agent": s.Meta.Agent, "title": titleOf(s), "turns": s.Meta.Turns,
				"last_active": s.Meta.LastActive, "active_at": s.ActiveAt, "machine": machineOf(s),
				"device": s.UpdatedBy, "size": s.Size, "status": s.Status, "pinned": s.Pinned,
				"path": s.Meta.RelPath, "cwd": s.Meta.Cwd, "host": s.Meta.Host, "writer": s.Meta.Writer,
				"lease_device": s.LeaseDevice, "lease_expires_at": s.LeaseExpiresAt,
			}
			b, _ := json.MarshalIndent(row, "", "  ")
			fmt.Println(string(b))
			return nil
		},
	}
}

func sessionFindCmd() *cobra.Command {
	var agent, host, status string
	var asJSON, files bool
	limit := 20
	c := &cobra.Command{
		Use:   "find <query>",
		Short: "Search stored sessions by title, path or working directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSessionList(listOpts{agent: agent, query: args[0], host: host, status: status, limit: limit, json: asJSON, files: files})
		},
	}
	c.Flags().StringVar(&agent, "agent", "", "filter by agent")
	c.Flags().StringVar(&host, "host", "", "filter by machine or device")
	c.Flags().StringVar(&status, "status", "", "filter by status (active, expired)")
	c.Flags().IntVarP(&limit, "limit", "n", limit, "show the N most recently active matches (0 = all)")
	c.Flags().BoolVar(&asJSON, "json", false, "print the index as JSON")
	c.Flags().BoolVar(&files, "files", false, "also list backed-up files and memory notes (default: conversations only)")
	c.Flags().BoolVarP(&listWide, "wide", "w", false, "show full titles (no cut to the terminal width)")
	return c
}

func sessionRestoreCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "restore <session-id> [session-id ...]",
		Short: "Restore chosen sessions (any unique id prefix) and take their lease",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			only, err := restoreKeys(e, args)
			if err != nil {
				return err
			}
			if err := restoreAll(e, "", force, 0, only); err != nil {
				return err
			}
			for _, ref := range args {
				rs, err := e.Resolve(ref)
				if err != nil {
					return err
				}
				if err := e.Take(rs.ID); err != nil {
					return err
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite local files that differ from the server copy")
	return c
}

func sessionListCmd() *cobra.Command {
	var agent, query, host, status string
	var asJSON, files bool
	limit := 20
	c := &cobra.Command{
		Use:   "list",
		Short: "List the session index, most recently active first",
		Long: "Lists the searchable index of stored sessions: title, turns, last write, machine, size, status. " +
			"Filter with --search (title, path, cwd) and --host. Restore chosen ones with `hearth restore <id>...`.",
		RunE: func(*cobra.Command, []string) error {
			return runSessionList(listOpts{agent: agent, query: query, host: host, status: status, limit: limit, json: asJSON, files: files})
		},
	}
	c.Flags().StringVar(&agent, "agent", "", "filter by agent")
	c.Flags().StringVarP(&query, "search", "s", "", "match title, path or working directory")
	c.Flags().StringVar(&host, "host", "", "filter by machine or device")
	c.Flags().StringVar(&status, "status", "", "filter by status (active, expired)")
	c.Flags().IntVarP(&limit, "limit", "n", limit, "show the N most recently active sessions (0 = all)")
	c.Flags().BoolVar(&asJSON, "json", false, "print the index as JSON")
	c.Flags().BoolVar(&files, "files", false, "also list backed-up files and memory notes (default: conversations only)")
	c.Flags().BoolVarP(&listWide, "wide", "w", false, "show full titles (no cut to the terminal width)")
	c.AddCommand(&cobra.Command{
		Use:   "find <query>",
		Short: "Search the session index by title, path or working directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSessionList(listOpts{agent: agent, query: args[0], host: host, status: status, limit: limit, json: asJSON, files: files})
		},
	})
	return c
}

type listOpts struct {
	agent, query, host, status string
	limit                      int
	json, files                bool
}

// conversationRow is one conversation of the index: the files of a
// multi-part thread (Codex rollouts of one thread) count once.
type conversationRow struct {
	client.RemoteSession
	Key   string
	Parts int
	Bytes int64
	// Folded: subagent threads and turn-less fork copies (such as the copy
	// Codex Desktop shows) listed under this conversation; their keys.
	Folded []string
	// Subagents: the folded threads restored with the conversation (fork
	// copies are not: the restore makes its own Desktop copy).
	Subagents []string
}

// listConversations filters the index (newest activity first) and merges
// the parts of a conversation into its newest file.
func listConversations(all []client.RemoteSession, o listOpts) []conversationRow {
	q := strings.ToLower(o.query)
	var rows []conversationRow
	at := map[string]int{}
	for _, s := range all {
		if !o.files && ((!conversation(s) && o.agent == "") || emptyConversation(s)) {
			continue
		}
		if o.agent != "" && s.Meta.Agent != o.agent {
			continue
		}
		if o.status != "" && s.Status != strings.ToLower(o.status) {
			continue
		}
		machine := machineOf(s)
		if o.host != "" && !strings.Contains(strings.ToLower(machine), strings.ToLower(o.host)) &&
			!strings.Contains(strings.ToLower(s.UpdatedBy), strings.ToLower(o.host)) {
			continue
		}
		if q != "" {
			hay := strings.ToLower(strings.Join([]string{s.ID, titleOf(s), s.Meta.RelPath, s.Meta.Cwd, s.Meta.OrigPath, machine}, "\n"))
			if !strings.Contains(hay, q) {
				continue
			}
		}
		k := s.ID
		if conversation(s) {
			k = client.ConversationKey(s.Meta.Agent, s.Meta.RelPath, s.ID)
		}
		if i, ok := at[k]; ok {
			r := &rows[i]
			r.Parts++
			r.Bytes += s.Size
			if s.Meta.Turns > r.Meta.Turns {
				r.Meta.Turns = s.Meta.Turns
			}
			if r.Meta.Title == "" && s.Meta.Title != "" {
				r.Meta.Title = s.Meta.Title
			}
			continue
		}
		at[k] = len(rows)
		rows = append(rows, conversationRow{RemoteSession: s, Key: k, Parts: 1, Bytes: s.Size})
	}
	if o.files {
		return rows
	}
	return foldChildren(rows)
}

// foldChildren moves subagent threads and fork copies without turns of
// their own under their parent, when the parent is listed too.
func foldChildren(rows []conversationRow) []conversationRow {
	at := map[string]int{}
	for i, r := range rows {
		at[r.Key] = i
	}
	parentOf := func(r conversationRow) (int, bool) {
		if r.Meta.Parent == "" || (r.Meta.ParentKind != "subagent" && r.Meta.Turns > 0) {
			return 0, false
		}
		i, ok := at[r.Meta.Agent+"/"+r.Meta.Parent]
		return i, ok && rows[i].Key != r.Key
	}
	// A chain (copy of a copy) folds into its first listed ancestor.
	root := func(i int) int {
		for n := 0; n < 8; n++ {
			p, ok := parentOf(rows[i])
			if !ok {
				break
			}
			i = p
		}
		return i
	}
	folded := map[int]bool{}
	for i := range rows {
		if r := root(i); r != i {
			rows[r].Folded = append(rows[r].Folded, rows[i].Key)
			if rows[i].Meta.ParentKind == "subagent" {
				rows[r].Subagents = append(rows[r].Subagents, rows[i].Key)
			}
			rows[r].Bytes += rows[i].Bytes
			if la := lastActive(rows[i].RemoteSession); la > lastActive(rows[r].RemoteSession) {
				rows[r].Meta.LastActive = la
			}
			folded[i] = true
		}
	}
	out := rows[:0:0]
	for i, r := range rows {
		if !folded[i] {
			out = append(out, r)
		}
	}
	return out
}

// runSessionList prints the decrypted index of the server's sessions.
func runSessionList(o listOpts) error {
	e, err := engine()
	if err != nil {
		return err
	}
	all, err := e.List()
	if err != nil {
		return err
	}
	picked := listConversations(all, o)
	total := len(picked)
	if o.limit > 0 && len(picked) > o.limit {
		picked = picked[:o.limit]
	}
	if o.json {
		type row struct {
			ID         string `json:"id"`
			Agent      string `json:"agent"`
			Title      string `json:"title"`
			Turns      int    `json:"turns"`
			LastActive int64  `json:"last_active"`
			Machine    string `json:"machine"`
			Device     string `json:"device,omitempty"`
			Size       int64  `json:"size"`
			Parts      int    `json:"parts,omitempty"`
			Sub        int    `json:"sub_threads,omitempty"` // subagent threads and fork copies folded in
			Status     string `json:"status"`
			Path       string `json:"path"`
			Cwd        string `json:"cwd,omitempty"`
		}
		out := make([]row, 0, len(picked))
		for _, s := range picked {
			parts := 0
			if s.Parts > 1 {
				parts = s.Parts
			}
			out = append(out, row{s.ID, s.Meta.Agent, titleOf(s.RemoteSession), s.Meta.Turns, lastActive(s.RemoteSession),
				machineOf(s.RemoteSession), s.UpdatedBy, s.Bytes, parts, len(s.Folded), s.Status, s.Meta.RelPath, s.Meta.Cwd})
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if total == 0 {
		if o.query != "" || o.host != "" || o.agent != "" || o.status != "" {
			fmt.Println("no matching conversations")
		} else {
			fmt.Println("no conversations on the server yet (the daemon uploads them as you work: hearth status)")
		}
		return nil
	}
	online := e.OnlineDevices()
	printConversations(picked, false, e.Cfg.DeviceID, online)
	if len(online) > 0 {
		fmt.Println("● = machine still online; hearth restore leaves its conversations there unless you name them (or --include-online)")
	}
	if len(picked) < total {
		fmt.Printf("showing %d of %d (newest first); more: -n 0 · search: -s <text> · restore: hearth restore <id> ...\n", len(picked), total)
	} else {
		fmt.Printf("%d conversation(s); restore: hearth restore <id> ...\n", total)
	}
	return nil
}

// emptyConversation: a thread opened but never used (no request, no
// name); listed with --files only, never counted by a bulk restore.
func emptyConversation(s client.RemoteSession) bool {
	return conversation(s) && s.Meta.Turns == 0 && (s.Meta.Title == "" || s.Meta.Title == s.Meta.RelPath) &&
		(s.Meta.Thread == nil || s.Meta.Thread.Name == "")
}

func lastActive(s client.RemoteSession) int64 {
	if s.Meta.LastActive > 0 {
		return s.Meta.LastActive
	}
	return s.ActiveAt
}

// printConversations prints the session table; numbered adds a "#"
// column for the interactive picker. The title gets what the terminal
// width leaves.
// thisDevice is this machine's device id (rows it wrote last say so).
func printConversations(rows []conversationRow, numbered bool, thisDevice string, online map[string]string) {
	host, _ := os.Hostname()
	t := &table{}
	head := []string{"ID", "AGENT", "UPDATED", "TURNS", "SIZE", "MACHINE", "TITLE"}
	if numbered {
		head = append([]string{"#"}, head...)
	}
	t.add(head...)
	for i, s := range rows {
		turns := "-"
		if s.Meta.Turns > 0 {
			turns = strconv.Itoa(s.Meta.Turns)
		}
		m := machineOf(s.RemoteSession)
		if (s.UpdatedBy != "" && s.UpdatedBy == thisDevice) || (s.UpdatedBy == "" && m == host) {
			m = "this machine"
		} else {
			m = truncW(shortHost(m), 28)
			if _, ok := online[s.UpdatedBy]; ok {
				m += " ●" // still online: not in the default restore
			}
		}
		cols := []string{short(s.ID), s.Meta.Agent, agoUnix(lastActive(s.RemoteSession)), turns,
			humanize.IBytes(uint64(s.Bytes)), m, ""}
		if numbered {
			cols = append([]string{strconv.Itoa(i + 1)}, cols...)
		}
		t.add(cols...)
	}
	// Width left for the title once the other columns are laid out.
	used := 0
	for c := range head[:len(head)-1] {
		w := 0
		for _, r := range t.rows {
			if n := dispWidth(r[c]); n > w {
				w = n
			}
		}
		used += w + 2
	}
	tw := termWidth() - used
	if listWide {
		tw = 1 << 20 // full titles
	}
	if tw < 24 {
		tw = 24
	}
	for i, s := range rows {
		title := titleOf(s.RemoteSession)
		switch {
		case s.Pinned:
			title = "[pinned] " + title
		case s.Status != "" && s.Status != "active":
			title = "[" + s.Status + "] " + title
		}
		suffix := ""
		if n := len(s.Folded); n > 0 {
			suffix = fmt.Sprintf(" (+%d sub)", n)
		}
		t.rows[i+1][len(head)-1] = truncW(title, tw-dispWidth(suffix)) + suffix
	}
	t.print()
}

func titleOf(s client.RemoteSession) string {
	if s.MetaErr != nil {
		return "<cannot decrypt>"
	}
	if s.Meta.Title != "" && s.Meta.Title != s.Meta.RelPath {
		return s.Meta.Title
	}
	if s.Meta.Thread != nil && s.Meta.Thread.Name != "" {
		return s.Meta.Thread.Name
	}
	if !conversation(s) {
		return s.Meta.RelPath
	}
	return "(untitled) " + path.Base(s.Meta.RelPath)
}

func machineOf(s client.RemoteSession) string {
	if s.Meta.Writer != "" {
		return s.Meta.Writer
	}
	if s.Meta.Host != "" {
		return s.Meta.Host
	}
	if s.UpdatedBy != "" {
		return s.UpdatedBy
	}
	return "-"
}

func deviceCmd() *cobra.Command {
	c := &cobra.Command{Use: "device", Short: "Manage devices"}
	c.AddCommand(&cobra.Command{
		Use: "list", Short: "List devices",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			ds, err := e.API.Devices()
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tLAST SEEN")
			for _, d := range ds {
				name := d.Name
				if d.ID == e.Cfg.DeviceID {
					name += " (this)"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", d.ID, name, d.Status, humanize.Time(time.Unix(d.LastSeen, 0)))
			}
			return tw.Flush()
		},
	}, &cobra.Command{
		Use: "revoke <id>", Short: "Revoke a device's access (rotate any credentials it had)", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			if err := e.API.RevokeDevice(args[0]); err != nil {
				return err
			}
			fmt.Println("revoked", args[0])
			return nil
		},
	})
	return c
}
