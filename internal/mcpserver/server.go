// Package mcpserver exposes skill search over MCP (stdio), so any agent can
// discover and load skills on demand instead of keeping every skill
// description in its context (design doc section 9.4).
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hearth-project/hearth-cli/internal/search"
)

// Source is a directory of skills; earlier sources win on name clashes
// (local before shared, design doc 8.3).
type Source struct {
	Name string
	Dir  string
}

// Service holds the index and refreshes it when skill files change.
type Service struct {
	Sources  []Source
	UsageLog string // JSON lines of get_skill calls; "" disables
	// Remote optionally lists shared skills that are not installed locally
	// (trusted sources only); Fetch downloads one so get_skill can return it.
	Remote    func() ([]search.Skill, error)
	Fetch     func(sk search.Skill) (dir string, err error)
	RemoteTTL time.Duration
	// RemoteSearch queries team indexes on the server per request (fast,
	// server-side BM25); results not present locally are offered as
	// "not linked" and fetched on get_skill.
	RemoteSearch func(query string, limit int) []search.Result
	// TeamList lists the teams' shared skills for list_skills (cached for
	// RemoteTTL); the ones not here are fetched on get_skill.
	TeamList func() []search.Skill
	// Setup learning: show the user's restore recipes and let the agent
	// propose a step it just performed with the user (reviewed later).
	Recipes func() string
	Propose func(recipe, stepsTOML, reason string) (string, error)
	// Long-term memory: Recall searches the local index (no network);
	// Remember / Forget keep the user's facts.
	Recall   func(query, cwd string, limit int) (string, error)
	Remember func(text, scope string) (string, error)
	Forget   func(id string) error

	mu        sync.Mutex
	sig       string
	ix        *search.Index
	remote    []search.Skill
	remoteAt  time.Time
	remoteErr error
	hits      map[string]search.Skill // remote search results by name, for get_skill
	team      []search.Skill
	teamAt    time.Time
}

// remember makes a shared skill found remotely loadable by get_skill.
func (s *Service) remember(sk search.Skill) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hits == nil {
		s.hits = map[string]search.Skill{}
	}
	s.hits[sk.Name] = sk
}

func (s *Service) teamSkills() []search.Skill {
	if s.TeamList == nil {
		return nil
	}
	s.mu.Lock()
	fresh := !s.teamAt.IsZero() && time.Since(s.teamAt) < s.ttl()
	list := s.team
	s.mu.Unlock()
	if fresh {
		return list
	}
	list = s.TeamList()
	s.mu.Lock()
	s.team, s.teamAt = list, time.Now()
	s.mu.Unlock()
	return list
}

func (s *Service) ttl() time.Duration {
	if s.RemoteTTL == 0 {
		return 5 * time.Minute
	}
	return s.RemoteTTL
}

// signature changes whenever a SKILL.md is added, removed or modified.
func (s *Service) signature() string {
	var b strings.Builder
	for _, src := range s.Sources {
		entries, _ := os.ReadDir(src.Dir)
		for _, e := range entries {
			if fi, err := os.Stat(filepath.Join(src.Dir, e.Name(), "SKILL.md")); err == nil {
				fmt.Fprintf(&b, "%s/%s:%d:%d;", src.Name, e.Name(), fi.ModTime().UnixNano(), fi.Size())
			}
		}
	}
	return b.String()
}

// Index returns an up-to-date index.
func (s *Service) Index() (*search.Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sig := s.signature()
	if s.Remote != nil {
		if s.remoteAt.IsZero() || time.Since(s.remoteAt) > s.ttl() {
			s.remote, s.remoteErr = s.Remote()
			s.remoteAt = time.Now()
			s.sig = "" // force rebuild
		}
	}
	if s.ix != nil && sig == s.sig {
		return s.ix, nil
	}
	var all []search.Skill
	seen := map[string]bool{}
	for _, src := range s.Sources {
		skills, err := search.LoadDir(src.Dir, src.Name)
		if err != nil {
			return nil, err
		}
		for _, sk := range skills {
			if !seen[sk.Name] {
				seen[sk.Name] = true
				all = append(all, sk)
			}
		}
	}
	for _, sk := range s.remote {
		if !seen[sk.Name] {
			seen[sk.Name] = true
			all = append(all, sk)
		}
	}
	s.ix, s.sig = search.Build(all), sig
	return s.ix, nil
}

func (s *Service) recordUse(skill string) {
	if s.UsageLog == "" {
		return
	}
	f, err := os.OpenFile(s.UsageLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	line, _ := json.Marshal(map[string]any{"time": time.Now().Unix(), "event": "get_skill", "skill": skill})
	f.Write(append(line, '\n'))
}

type searchArgs struct {
	Query string `json:"query" jsonschema:"the task in a few words; include English keywords as well as the user's language, e.g. '压测 吞吐 benchmark throughput'"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of results (default 5)"`
}

type getArgs struct {
	Name string `json:"name" jsonschema:"exact skill name from search_skills or list_skills"`
}

type listArgs struct{}

type recallArgs struct {
	Query string `json:"query" jsonschema:"what to look for, in a few words; include English keywords and the user's language, error messages, file or tool names"`
	Cwd   string `json:"cwd,omitempty" jsonschema:"the current working directory (ranks memories of the same repository higher)"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of results (default 5)"`
}

type rememberArgs struct {
	Text  string `json:"text" jsonschema:"one fact, habit or decision, written so another agent understands it without this conversation"`
	Scope string `json:"scope,omitempty" jsonschema:"a directory (e.g. the repository root) when it only holds there; empty = everywhere"`
}

type forgetArgs struct {
	ID string `json:"id" jsonschema:"the id of a remembered fact (f:...), from recall"`
}

type proposeArgs struct {
	Recipe string `json:"recipe,omitempty" jsonschema:"recipe to extend (default: learned)"`
	Steps  string `json:"steps" jsonschema:"TOML with one or more [[step]] tables, e.g. [[step]] type = \"manual\" text = \"...\"; types: tool, file, env, run, service, check, capture, manual; reference credentials by secret name, never inline them; add when = { profile = \"<name>\" } (or gpu / arch / os) to steps that only some kinds of machines need, see list_setup_recipes"`
	Reason string `json:"reason" jsonschema:"why this step is part of the user's setup"`
}

// All tools only read local skill files.
var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(bool)}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// New builds the MCP server: skill tools plus optional setup tools.
func (s *Service) New(version string) *mcp.Server {
	instr := "Skill library of the user and their teams: many more skills than the ones installed in this agent. " +
		"Before starting a specialised task (benchmarking, profiling, deployment, internal tools and platforms, " +
		"team workflows), call search_skills with the task description; if a result fits, call get_skill and follow its instructions."
	if s.Propose != nil {
		instr += " When you help the user set up this machine (install a tool, log in, configure auth, edit " +
			"shell or agent config), ask whether every new machine needs it and, if so, record it with " +
			"propose_setup_step (check list_setup_recipes first to avoid duplicates; never put secret values in steps)."
	}
	if s.Recall != nil {
		instr += " Long-term memory: call recall when you hit an error, start on an unfamiliar repository, or the user refers to " +
			"earlier work (\"like last time\", \"上次\"); it searches their conversations on every machine and agent. When the user " +
			"asks you to remember something, or agrees to your proposal, call remember."
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "hearth-skills", Version: version}, &mcp.ServerOptions{Instructions: instr})
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search_skills",
		Annotations: readOnly,
		Description: "Find skills (task-specific instructions and scripts) relevant to a task, in the user's library and " +
			"the skills their teams share (more than the ones installed here). " +
			"Call this before specialised work such as benchmarking, profiling, deployment or internal tooling.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, a searchArgs) (*mcp.CallToolResult, any, error) {
		ix, err := s.Index()
		if err != nil {
			return nil, nil, err
		}
		limit := a.Limit
		if limit <= 0 || limit > 20 {
			limit = 5
		}
		res := ix.Search(a.Query, limit)
		if s.RemoteSearch != nil {
			for _, r := range s.RemoteSearch(a.Query, limit) {
				if _, local := ix.Get(r.Name); local {
					continue // the local / linked copy wins
				}
				s.remember(r.Skill)
				res = append(res, r)
			}
		}
		if len(res) == 0 {
			return text("No matching skill. Proceed without one, or call list_skills to browse."), nil, nil
		}
		var b strings.Builder
		for i, r := range res {
			fmt.Fprintf(&b, "%d. %s (score %.2f, %s)\n   %s\n", i+1, r.Name, r.Score, r.Source, oneLine(r.Description))
		}
		b.WriteString("\nCall get_skill with the name to load one.")
		return text(b.String()), nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_skill",
		Annotations: readOnly,
		Description: "Load a skill's full SKILL.md, its directory and its other files (paths relative to that directory, readable or runnable).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, a getArgs) (*mcp.CallToolResult, any, error) {
		ix, err := s.Index()
		if err != nil {
			return nil, nil, err
		}
		sk, ok := ix.Get(a.Name)
		if !ok {
			s.mu.Lock()
			sk, ok = s.hits[a.Name]
			s.mu.Unlock()
		}
		if !ok {
			return text(fmt.Sprintf("No skill named %q. Use search_skills or list_skills.", a.Name)), nil, nil
		}
		if sk.Dir == "" && s.Fetch != nil {
			// Shared skill not installed here: download it on demand.
			if sk.Dir, err = s.Fetch(sk); err != nil {
				return nil, nil, fmt.Errorf("fetch %s: %w", sk.Name, err)
			}
		}
		body, err := os.ReadFile(filepath.Join(sk.Dir, "SKILL.md"))
		if err != nil {
			return nil, nil, err
		}
		s.recordUse(sk.Name)
		var b strings.Builder
		fmt.Fprintf(&b, "Skill directory: %s\n", sk.Dir)
		if files := listFiles(sk.Dir, 200); len(files) > 0 {
			b.WriteString("Files:\n")
			for _, f := range files {
				b.WriteString("  " + f + "\n")
			}
		}
		b.WriteString("\n--- SKILL.md ---\n")
		b.Write(body)
		return text(b.String()), nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_skills",
		Annotations: readOnly,
		Description: "List every available skill with a one-line description, including the teams' shared skills.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ listArgs) (*mcp.CallToolResult, any, error) {
		ix, err := s.Index()
		if err != nil {
			return nil, nil, err
		}
		var b strings.Builder
		line := func(sk search.Skill, from string) {
			d := []rune(oneLine(sk.Description))
			if len(d) > 100 {
				d = append(d[:99], '…')
			}
			fmt.Fprintf(&b, "- %s%s: %s\n", sk.Name, from, string(d))
		}
		for _, sk := range ix.Skills() {
			line(sk, "")
		}
		for _, sk := range s.teamSkills() {
			if _, local := ix.Get(sk.Name); !local {
				s.remember(sk)
				line(sk, " ["+sk.Source+"]")
			}
		}
		return text(b.String()), nil, nil
	})
	if s.Recall != nil {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "recall",
			Annotations: readOnly,
			Description: "Search the user's long-term memory (local, milliseconds): earlier conversations on any of their machines " +
				"and agents (what was asked, how it ended, errors, commands, files), facts and habits they asked to remember, and the " +
				"agents' own notes. Use it when you hit an error, start on an unfamiliar repository, or the user refers to earlier work.",
		}, func(_ context.Context, _ *mcp.CallToolRequest, a recallArgs) (*mcp.CallToolResult, any, error) {
			limit := a.Limit
			if limit <= 0 || limit > 20 {
				limit = 5
			}
			out, err := s.Recall(a.Query, a.Cwd, limit)
			if err != nil {
				return nil, nil, err
			}
			if out == "" {
				out = "Nothing found in memory."
			}
			return text(out), nil, nil
		})
	}
	if s.Remember != nil {
		mcp.AddTool(srv, &mcp.Tool{
			Name: "remember",
			Description: "Remember a fact, habit or decision for all of the user's agents and machines (end-to-end encrypted). Only " +
				"when the user asks you to remember something or agreed to your proposal; never store credentials or secrets.",
		}, func(_ context.Context, _ *mcp.CallToolRequest, a rememberArgs) (*mcp.CallToolResult, any, error) {
			id, err := s.Remember(a.Text, a.Scope)
			if err != nil {
				return text("Not remembered: " + err.Error()), nil, nil
			}
			return text("Remembered as " + id + "; every agent of this user gets it at session start."), nil, nil
		})
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "forget",
			Description: "Forget a remembered fact (id f:... from recall) on all of the user's machines, when the user asks.",
		}, func(_ context.Context, _ *mcp.CallToolRequest, a forgetArgs) (*mcp.CallToolResult, any, error) {
			if err := s.Forget(a.ID); err != nil {
				return text("Not forgotten: " + err.Error()), nil, nil
			}
			return text("Forgotten."), nil, nil
		})
	}
	if s.Recipes != nil {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "list_setup_recipes",
			Description: "Show the user's machine setup recipes (how their environment is restored on a new machine).",
			Annotations: readOnly,
		}, func(context.Context, *mcp.CallToolRequest, listArgs) (*mcp.CallToolResult, any, error) {
			return text(s.Recipes()), nil, nil
		})
	}
	if s.Propose != nil {
		mcp.AddTool(srv, &mcp.Tool{
			Name: "propose_setup_step",
			Description: "After helping the user set up or fix their environment (install a tool, configure auth, " +
				"edit a config), record the step so their next machine is set up the same way. The proposal is saved " +
				"for the user to review with `hearth recipe proposals`; nothing is executed.",
		}, func(_ context.Context, _ *mcp.CallToolRequest, a proposeArgs) (*mcp.CallToolResult, any, error) {
			id, err := s.Propose(a.Recipe, a.Steps, a.Reason)
			if err != nil {
				return text("Not recorded: " + err.Error()), nil, nil
			}
			return text("Recorded as proposal " + id + ". Tell the user to review it with `hearth recipe proposals`."), nil, nil
		})
	}
	return srv
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func listFiles(dir string, max int) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || len(out) >= max {
			return filepath.SkipAll
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && p != dir {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() != "SKILL.md" {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	return out
}
