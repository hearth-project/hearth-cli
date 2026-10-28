# Agent integration

[中文](../zh/agent-integration.md) · [Back to README](../../README.md)

How Hearth supports an agent. Goal: **one core for sync, encryption, index and restore; every agent's differences live in a replaceable integration layer.** Adding an agent adds an integration and tests, never branches in the core.

| Layer | Path | Responsibility |
|---|---|---|
| core engine | `internal/client/engine.go`, `restore.go`, `deleted.go` | discovery, incremental upload, leases, expiry, restore loop, state |
| file adapter | `internal/adapter/adapter.go` | the agent's directory layout, file kinds (session / memory), path safety |
| integration interface | `internal/client/agentintegration.go` | conversation grouping, post-restore hook, moved / deleted files, cross-agent import |
| Codex integration | `internal/client/codexintegr.go`, `codexstate.go`, `codexdesktop.go`, `codeximport.go` | everything Codex-specific |
| CLI | `cmd/hearth/` | `hearth agent list/import`, `hearth session list/find/show/restore` |

## 1. Rules

Core code must not contain `if agent == "codex"`. Agent differences go through two interfaces:

1. `adapter.Adapter`: where the agent's files are and which ones sync.
2. `client.AgentIntegration`: what one conversation is, what has to happen after a restore, how deletions look, how to import other agents.

Minimal change for a new agent:

```text
internal/adapter/xxx.go                 // directories and file kinds
internal/client/xxxintegr.go            // AgentIntegration
internal/client/agentintegration.go     // one registry line
internal/client/xxx_test.go             // unit + end-to-end tests
docs/{en,zh}/agent-integration.md       // capability matrix
```

## 2. The integration checklist

### 2.1 Discovery and kinds

- Config directory, and its override variable (`CODEX_HOME`, `CLAUDE_CONFIG_DIR`)?
- Where are sessions, and how are they laid out (by date, project, cwd)?
- Which files are conversations (incremental upload, restorable)?
- Which are long-term memory (pinned, never expire)?
- Which are settings, skills, agents, hooks, MCP (library / secrets, not sessions)?
- Subagents, continuation segments, temp or lock files to exclude?
- Symlinks, path traversal, long paths?

The adapter returns `SessionFile{Agent, Root, RelPath, AbsPath, Kind, Thread...}`.

### 2.1.1 The user's upload choice

Adapters only discover. Whether a file is uploaded is decided by the core from the user's `config.toml` `[backup]`, taking the first rule the user wrote among `<agent>.<kind>`, `<agent>`, `<kind>` (Claude Code conversations: `@claude.sessions`, `@claude`, `@sessions`). The kind comes from the adapter (sessions / memory). Workspace files are `@workspace`, credentials `@credentials`; `!` path exclusions apply to every file.

Registering an integration that declares `Capabilities().Sessions/Memory` makes `hearth backup enable/disable <agent>[.kind]` work; an integration never implements its own upload switch.

### 2.2 Conversation identity

A conversation may be several files on disk (a Codex thread: main rollout, continuations, subagent rollouts). The integration provides:

```go
ConversationKey(relPath string) string // "" = one file is one conversation
IsPart(relPath string) bool            // shown / restored with its main file
```

The core counts "newest N" and restores whole groups by `ConversationKey`, never by file.

### 2.3 Index

Each conversation has a searchable index stored apart from the content:

| Field | Meaning |
|---|---|
| `title` | the agent's own name (Codex thread name), else the first request |
| `turns` | user requests (not messages) |
| `last_active` | last write time in the content |
| `writer` | last writing machine |
| `path` / `cwd` | original relative path, working directory |
| `agent` | agent name |
| `size` / `status` | from the server row |

- **Incremental**: only bytes after the last scan are parsed, never the whole history each pass.
- A half-written last line waits for the next pass.
- A new `sessionIndexVersion` triggers one full rescan.
- The index is encrypted metadata: titles never reach the server in plaintext.
- Index updates neither change a session's activity time nor revive an expired session.

### 2.3.1 Working directory and path mapping

- `SessionCwd(absPath)` parses the agent's format; the core has no agent-specific JSON rules.
- With `PathMap: true` absolute paths in sessions are rewritten between machines; Claude Code also sets `ClaudePaths: true` (its project directory names encode the cwd).
- Formats without a cwd (Comate) return "", which disables path mapping.

### 2.4 Upload and restore

- Uploads are append chunks with generation, offset, idempotent retries and leases.
- A rewritten or truncated local file starts a new generation.
- Restores are atomic (temp file + rename).
- Restored files get the content's last activity as mtime, so the agent's own ordering is right.
- Paths are translated on restore and appended lines are translated back before upload.
- After a restore the state records that the server has every byte, so the daemon does not upload it again.
- Dependencies (a fork's parent, related files) are restored with the conversation.

### 2.5 After restore

```go
AfterRestore(e *Engine, all []RemoteSession, restored []RestoredSession, opts RestoreOptions) error
```

Typical steps: restore dependencies; let the agent index the files (Codex: `thread/read` over `codex app-server`); apply names, pins, sections; if the agent has a running UI, show the result **in the open window** instead of asking for a restart; remember visible copies so they are not duplicated.

Codex today: connects to the running Desktop through `~/.codex/app-server-control/app-server-control.sock`; `thread/fork` makes the open window list the thread at once (the fork references the parent's history, it does not copy it); the original is archived so a restart does not show it twice; deleting the Desktop copy expires the archived original too.

### 2.6 Deletions

Archiving or moving is not deleting:

- the agent moves a file (Codex `sessions/` ↔ `archived_sessions/`): same session, never uploaded as a new one;
- the user deletes a conversation (Codex: rollout and state-database row both gone): `POST /v1/sessions/{id}/expire`, and `restore` / `setup` skip it;
- many files vanish at once (agent home replaced or wiped): nothing is expired, one warning is logged;
- only files actually seen on this machine count, so sessions restored elsewhere are not mistaken for deletions.

### 2.7 Cross-agent import

```go
Import(e *Engine, sourceAgent string, paths []string) (results []ImportResult, live bool, err error)
```

The target agent's own converter does the conversion, deduplication and new ids; Hearth restores the source sessions, calls the importer and reports. With a running UI the import goes through its official interface so the window shows the result. Codex imports Claude Code sessions via `externalAgentConfig/detect` + `import`.

### 2.8 Capabilities

```go
AgentCapabilities{
    Sessions: true, Memory: true,
    Threads: true,      // a conversation can span files
    LiveUI: true,       // restored sessions can appear in the running app
    DeleteSync: true,   // deletions in the app expire the server copy
    PathMap: true, ClaudePaths: false,
    ImportFrom: []string{"claude"},
    IndexFields: []string{"title", "turns", "last_active", "writer", "thread_name", "section"},
}
```

`hearth agent list` prints the matrix.

## 3. Codex, the reference implementation

| Capability | Implementation |
|---|---|
| discovery | `adapter.Codex()`, `sessions/` + `archived_sessions/` |
| memory | Codex memory files, kind `memory` |
| grouping | `codexIntegration.ConversationKey` (thread id) |
| index | `scanStats`, incremental over `response_item` lines |
| names / sections | `codexstate.go`: read from Codex's state database, applied through `app-server` |
| live UI | `codexdesktop.go`: WebSocket over the unix socket, `thread/fork` |
| fork parents | `CodexForkParent` + `restoreParents` |
| moves | `codexMoved` (by rollout file name) |
| deletions | `codexIntegration.ExpireDeleted` (rollout and state row gone) |
| Claude import | `ImportClaudeIntoCodex` |

## 4. Current matrix

| Agent | sessions | index | multi-file | live UI | delete sync | import |
|---|---|---|---|---|---|---|
| Codex | yes | yes | yes | yes (Desktop running) | yes | from Claude Code |
| Claude Code | yes | yes (generic) | single file | file restore | not yet (never expires by mistake) | not yet |
| Comate | yes | yes (generic) | single file | not yet | not yet (never expires by mistake) | not yet |

## 5. Tests for a new agent

1. Discovery: sessions / memory classified, settings and temp files left out.
2. Incremental index: half lines not counted; turns / title / time right after appends.
3. Upload: new session, append, rewrite (new generation), interrupted retry.
4. Restore: path rewrite between machines, atomic write, mtime, conflicting local file.
5. Multi-file conversations counted and restored as one.
6. Moves inside the agent do not create a second session.
7. Deletions expire the session; index updates do not revive it; mass disappearance is ignored.
8. Live UI shows restored sessions (if `LiveUI`).
9. Import skips repeats and keeps content.
10. Several machines: A uploads, B restores and continues, A sees the new turns.

## 6. Non-goals

- No copy of an agent's private database format in the core; integrations use the agent's official interfaces.
- No plaintext titles, conversation content or credentials on the server.
- No restart needed to see restored sessions in agents with a live UI.
- Lossless tool-call conversion between agents is not promised; cross-agent import is as good as the target agent's converter.
