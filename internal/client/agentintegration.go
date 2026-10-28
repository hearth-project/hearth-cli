package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/adapter"
)

// AgentIntegration is the optional, agent-specific half of syncing:
// everything the core engine cannot know generically. The core handles
// discovery, encryption, chunks, leases, expiry and restore; an integration
// answers "what is one conversation here?", "what must happen after its
// files are written?" and "can it import another agent's sessions?".
//
// Adding an agent: implement adapter.Adapter (files and paths) plus this
// interface, register it in agentIntegrations, and add an end-to-end test.
// Core commands then pick it up without Codex-specific branches.
type AgentIntegration interface {
	Name() string
	Capabilities() AgentCapabilities
	// ConversationKey groups files of one logical conversation (a Codex
	// thread can span continuations/subagents). "" means one file each.
	ConversationKey(relPath string) string
	// IsPart reports a file that is shown/restored with another file.
	IsPart(relPath string) bool
	// Prepare snapshots agent state before a restore (e.g. which threads
	// its UI already knows); the value is handed back in RestoreOptions.
	Prepare(e *Engine) any
	// AfterRestore runs once for an agent after its files were written:
	// restore dependencies, register the agent's own index/UI, apply names.
	AfterRestore(e *Engine, all []RemoteSession, restored []RestoredSession, opts RestoreOptions) error
	// Import converts sessions of sourceAgent (paths on this machine) into
	// this agent's format. live reports whether a running UI received them.
	Import(e *Engine, sourceAgent string, paths []string) (res []ImportResult, live bool, err error)
	// SessionCwd reads the working directory from a session file ("" if
	// the format has none).
	SessionCwd(absPath string) string
	// RestorePath returns where a restored file currently lives when the
	// agent moved it (e.g. Codex archive/unarchive); false keeps want.
	RestorePath(root, rel, want string) (string, bool)
	// MovedSession maps a local logical path to an existing session id when
	// the agent moved the file (rename/archive) without changing identity.
	MovedSession(e *Engine, logical string) string
	// ExpireDeleted expires sessions the user deleted in this agent.
	ExpireDeleted(e *Engine, present map[string]bool) (int, error)
}

type AgentCapabilities struct {
	Sessions    bool     // conversation files are synced
	Memory      bool     // long-term notes are synced
	Threads     bool     // conversations can span several files
	LiveUI      bool     // an open app can be made to show restored sessions now
	DeleteSync  bool     // deletions in the agent expire the server copy
	PathMap     bool     // rewrite absolute paths between machines on restore
	ClaudePaths bool     // paths are encoded in Claude project directory names
	ImportFrom  []string // agents whose sessions this one can import
	IndexFields []string // extra index fields, e.g. turns/name/section
}

// RestoredSession is one file written by a restore pass.
type RestoredSession struct {
	Remote RemoteSession
	Result RestoreResult
	// Group is ConversationKey of the session (or its id).
	Group string
}

type RestoreOptions struct {
	// Explicit is set when the user named these sessions, rather than a
	// bulk "newest N" restore.
	Explicit bool
	// KnownBefore is the set of conversations the receiving UI knew before
	// the restore (so bulk restore need not show them again).
	KnownBefore map[string]bool
	// Say prints user-facing progress (CLI).
	Say func(format string, args ...any)
	// Prepared holds each integration's Prepare result, keyed by agent.
	Prepared map[string]any
}

func (o RestoreOptions) printf(format string, args ...any) {
	if o.Say != nil {
		o.Say(format, args...)
	} else {
		fmt.Printf(format, args...)
	}
}

// ImportResult is one converted session.
type ImportResult struct {
	Source, Thread, Title, Error string
}

// agentIntegrations is the registry of per-agent behaviour. Keep core code
// free of `if agent == "codex"` branches; add entries here instead.
var agentIntegrations = map[string]AgentIntegration{
	"codex":  codexIntegration{},
	"claude": plainIntegration{name: "claude"},
	"comate": plainIntegration{name: "comate"},
}

// plainIntegration is the baseline for an agent whose files sync one-to-one
// and whose UI needs no registration: Claude Code and Comate today. They can
// grow capabilities by replacing this entry with a dedicated implementation.
type plainIntegration struct{ name string }

func (p plainIntegration) Name() string { return p.name }
func (p plainIntegration) Capabilities() AgentCapabilities {
	c := AgentCapabilities{Sessions: true, Memory: true, IndexFields: []string{"title", "turns", "last_active", "writer"}}
	if p.name == "claude" {
		c.PathMap, c.ClaudePaths = true, true
	}
	return c
}
func (plainIntegration) ConversationKey(string) string { return "" }
func (plainIntegration) IsPart(string) bool            { return false }
func (p plainIntegration) SessionCwd(absPath string) string {
	if p.name != "claude" {
		return ""
	}
	return firstJSONField(absPath, func(m map[string]json.RawMessage) string {
		var s string
		if raw, ok := m["cwd"]; ok && json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	})
}
func (plainIntegration) Prepare(*Engine) any { return nil }
func (plainIntegration) AfterRestore(*Engine, []RemoteSession, []RestoredSession, RestoreOptions) error {
	return nil
}
func (plainIntegration) Import(*Engine, string, []string) ([]ImportResult, bool, error) {
	return nil, false, errors.New("this agent has no importer")
}
func (plainIntegration) RestorePath(string, string, string) (string, bool)   { return "", false }
func (plainIntegration) MovedSession(*Engine, string) string                 { return "" }
func (plainIntegration) ExpireDeleted(*Engine, map[string]bool) (int, error) { return 0, nil }

// Integration returns the integration for an agent, if it has one.
func Integration(agent string) (AgentIntegration, bool) {
	in, ok := agentIntegrations[agent]
	return in, ok
}

// Integrations returns every registered integration, sorted by name.
func Integrations() []AgentIntegration {
	var out []AgentIntegration
	for _, in := range agentIntegrations {
		out = append(out, in)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && strings.Compare(out[j].Name(), out[j-1].Name()) < 0; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ConversationKey is the generic identity used by restore: the
// integration's key when available, else the server session id.
func ConversationKey(agent, relPath, sid string) string {
	if in, ok := Integration(agent); ok {
		if k := in.ConversationKey(relPath); k != "" {
			return agent + "/" + k
		}
	}
	return sid
}

// IsConversation reports whether a remote session is a restorable
// conversation (not a memory note or an unknown/unsupported agent).
func IsConversation(rs RemoteSession) bool {
	in, ok := Integration(rs.Meta.Agent)
	return ok && in.Capabilities().Sessions && rs.MetaErr == nil && rs.Meta.Kind != adapter.KindMemory
}

// SupportsSessions reports whether agent has restorable conversations
// (an agent integration, not a backup source such as workspace files).
func SupportsSessions(agent string) bool {
	in, ok := Integration(agent)
	return ok && in.Capabilities().Sessions
}
