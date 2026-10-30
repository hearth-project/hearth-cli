# Hearth technical reference

> Detailed behaviour of every part of Hearth. For installation and everyday use start with the [README](../../README.md); commands are listed in [cli.md](../cli.md).

A home for your coding agents. Hearth keeps Codex and Claude Code sessions, skills, agents, credentials and setup steps end-to-end encrypted on a server you host, so any machine can be replaced or killed without losing agent context, and a new one is restored with one command.

Status: encrypted session and memory sync (5–6), the cross-agent library with Codex / Claude Code / Comate rendering (7), teams and remote git sources (8), on-demand skill search over MCP (9), secrets, tools and restore recipes (14). Server storage: SQLite + a directory by default, PostgreSQL and S3 optional. Not yet: LLM-written checkpoints (hand-offs are mechanical), embeddings for skill search, MFA.

## Supported agents

Codex and Claude Code are enabled by default; Comate is implemented but opt-in (`hearth agents enable comate`). Codex is the primary target and is checked end to end against the real CLI by `scripts/codex-e2e.sh` (10 checks: session sync and `codex exec resume` on a new machine, skills / agents / `AGENTS.md` rendered into `CODEX_HOME` and picked up by Codex, MCP registration via `codex mcp add`, `search_skills` + `get_skill` called by Codex).

```bash
hearth agents list | enable <agent> | disable <agent>   # synced setting; HEARTH_AGENTS overrides
```

## Set up a machine by asking your agent

Hearth ships one agent skill, [`skills/hearth`](../../skills/hearth/SKILL.md), embedded in the binary and installed automatically into every coding agent found on the machine (Codex `~/.codex/skills`, Claude Code `~/.claude/skills`, Comate `~/.comate/skills`) by the installer, `hearth setup` and `hearth self-update` (which refreshes it). The skill first checks `hearth onboard status --json`: without Hearth it installs it, when not logged in it guides the user through login and restoring the machine, and when logged in it helps with daily use (find and restore conversations, including into the open Codex Desktop window; choose what is uploaded per agent; import Claude Code sessions into Codex; record the machine's setup; check sync).

The installer prints how to use it (ask the agent "用 hearth"). Hearth-installed copies carry a marker and are updated in place; a user's own skill with the same name is never overwritten, and bundled copies are never imported into the user's library. `HEARTH_NO_SKILLS=1` skips the installation; `hearth onboard install-skill` installs it again (e.g. after installing another agent). The older `hearth-setup` skill is merged into it and removed automatically.

When not logged in, the skill lets the agent set a machine up with the user answering a few questions:

```text
you:   set up hearth on this machine
agent: (checks `hearth onboard status --json`, installs hearth if missing, asks in one message:
        server, email, new or existing account, workspace, which agents)
agent: please paste this into your terminal:  cd ~/work && hearth setup --server https://… --email you@…
you:   done
agent: (registers skill search, installs the skill, shows recipe commands for approval,
        lists your SSH keys / logins / env vars with a recommendation each: all machines,
        only machines like this, or never; records your answers)
```

- The master password and the recovery key never pass through the agent: the one command that needs them runs in the user's own terminal.
- `hearth setup` installs the skill for Codex and Claude Code; `hearth onboard install-skill` does it explicitly. On a machine without Hearth, point the agent at `https://<server>/onboard/SKILL.md` (served by the server, with its install command filled in).
- Built for agents, usable by hand: `hearth onboard status [--json]` (what is set up, ordered next steps, which need the user's terminal), `install.sh --install-only`, and non-interactive learning: `hearth learn --list --json`, then `hearth learn --accept <ids|all> --here <ids> --never <ids> --step '$ cmd' --here-step 'text'`.
- Verified in `scripts/codex-e2e.sh`: on a logged-in machine, Codex follows the skill and records the SSH key and gh login as secrets, writes the learned recipe and registers skill search.

## Deploying the server

One binary, two storage choices. The defaults need nothing else installed:

| | Default | Optional (several instances, large data) |
|---|---|---|
| metadata (`--db`, `$HEARTH_DB`) | SQLite file in `--data` | `postgres://user:pass@host/db` |
| blobs (`--blobs`, `$HEARTH_BLOBS`) | `<data>/objects` | `s3://bucket/prefix?endpoint=host:9000&path_style=1` (AWS S3, MinIO, BOS, OSS, COS, Ceph); keys from `$HEARTH_S3_ACCESS_KEY` / `$HEARTH_S3_SECRET_KEY`, the `AWS_*` variables or the instance role |

```bash
hearth server --data /var/lib/hearth --addr 0.0.0.0:8787          # single machine
docker compose -f deploy/docker-compose.yml up -d                        # same, in a container
docker compose -f deploy/docker-compose.scale.yml up -d --scale hearth=2   # PostgreSQL + MinIO + replicas
hearth server migrate --to-db postgres://... --to-blobs s3://...      # grow later: copy everything
hearth server backup /backups/hearth-$(date +%F)                   # consistent online backup (SQLite + files)
```

- Keep it running without systemd (containers): `hearth server start <server flags>` runs the server under a supervisor that restarts it when it exits or stops answering `/healthz` 3 times in a row (checked every 30 s), with backoff from 1 s to 1 min. `hearth server status | stop | restart --data <dir>`. Running `start` again is a no-op, so it is safe in a container entrypoint or an `@reboot` cron job. With systemd, `hearth server install-service <server flags>` prints a unit (`Restart=always`).
- Logs stay small: no per-request logging; startup prints a few lines, GC one line per pass that changed something. Repeated errors (a failing disk, scanners on a public port) are collapsed to 5 similar lines a minute plus a count, TLS handshake noise is dropped. Under the supervisor the log is `<data>/server.log`, rotated at 10 MiB with 5 files kept (60 MiB at most).
- SQLite + a directory loses no features. Its limits are operational: one instance (no failover), one machine's disk, and in containers the data volume must be persistent.
- With PostgreSQL + S3 any number of instances can run behind a load balancer. Per-instance caches are bounded: a revoked token stops working on other instances within 30 s, a publish shows up in other instances' search within 2 s, GC runs on one instance at a time (advisory lock).
- Every server flag also has an environment variable (`HEARTH_DATA`, `HEARTH_ADDR`, `HEARTH_REGISTRATION`, `HEARTH_PUBLIC_URL`), so passwords stay off the command line. `deploy/hearth.service` is a systemd unit.
- The client has no database: its state is JSON files.

## Administration and quotas

```bash
hearth server --quota 20GiB ...                  # default quota per account (sessions + library + secrets); default unlimited
hearth server admin stats                        # users, devices, sessions, bytes, teams
hearth server admin users                        # every account: status, used / quota, sessions, devices, last seen
hearth server admin user alice@example.com       # one account, including teams
hearth server admin quota alice@example.com 50GiB   # per-account quota (unlimited | default)
hearth server admin disable alice@example.com    # block login, recovery and every token; data kept (enable to undo)
hearth server admin revoke-devices alice@example.com
hearth server admin delete-user alice@example.com --yes   # all sessions, library, secrets; refused while sole team owner
hearth server admin invites [revoke <id>]
hearth server admin gc [--dry-run]               # expiry pass now + delete orphaned blobs (older than --min-age 1h)
```

- Admin commands open the server's storage directly (same `--data` / `--db` / `--blobs` or `HEARTH_*` variables as `hearth server`), like `server invite`: whoever can read the storage can administer it, and there is no admin HTTP API or password to protect. They work while the server runs; running instances pick up disabled accounts and revoked devices within 30 s. Pass the server's `--quota` (or set `HEARTH_QUOTA`) so default quotas are displayed correctly.
- Quotas are checked from an in-memory usage counter per account (re-read every 30 s and before any refusal), so the hot upload path costs the same with and without quotas (~3,100 appends/s either way on SQLite). Over quota, uploads get HTTP 507; the client pauses uploads for 10 minutes instead of resending, and `hearth status` shows `used of quota`. Team packages do not count against personal quotas.

## Server performance

Design goal: the server must stay fast on the hot paths (daemon polling, listing, restore, team search).

- SQLite in WAL mode with one writer connection and a pool of read-only connections, so reads never queue behind writes; `synchronous=NORMAL` (a power cut can lose the last commits, which the sync protocol recovers from).
- Validated tokens are cached in memory; token expiry and device last-seen are written at most every 5 minutes instead of on every request. Revoking a device or resetting an account purges the cache.
- Restore downloads a whole session in one streamed request (`/content`) instead of one request per chunk; uploads use 4 MiB chunks.
- Team search runs on the server against an in-memory inverted BM25 index per team, rebuilt lazily after a publish. The MCP server queries it per request (1.5 s budget) instead of downloading package lists; git sources reuse a checkout for 10 minutes.

`go test -bench . ./internal/server ./internal/search` on this machine (128 cores, real HTTP, parallel clients; PostgreSQL 16 and MinIO on the same host):

| Path | SQLite + dir | PostgreSQL + dir | PostgreSQL + MinIO |
|---|---|---|---|
| list sessions (200 sessions, auth + list) | ~9,400 req/s | ~8,800 req/s | ~9,400 req/s |
| library poll (304) | ~29,600 req/s | ~23,100 req/s | ~24,800 req/s |
| append chunk (fsync per chunk) | ~3,100 req/s | ~11,000 req/s | ~4,700 req/s |
| restore a 64-chunk session (one request) | ~1.0 ms | ~0.95 ms | ~5.4 ms |
| team search over 5,000 skills (HTTP) | ~15,900 req/s | ~14,400 req/s | ~14,100 req/s |
| in-process search over 5,000 skills | ~87 µs/query | | |

SQLite serialises writes on one connection, so concurrent appends scale better on PostgreSQL. Restores from S3 fetch up to 32 chunks in parallel; per-request latency still dominates for small chunks (real sessions use 4 MiB chunks).

## What works

- Account and device login with client-side key derivation (Argon2id → HKDF). The server only stores an auth hash and the vault key wrapped by the password-derived KEK and by a one-time recovery key.
- `hearth login` takes the account from what the machine already knows: a valid existing login first, else `$HEARTH_IDENTITY`, the organisation's SSO token caches (`site/learn.json` `token_files`, JWT `username` claim) and Comate's `login-user` files. `$COMATE_USERNAME` / `$SYS_USER_NAME` are only hints: on a container that several people share they can name whoever started it, so they are used only when they match the workspace. The workspace directory name breaks ties, ambiguous setups are never guessed, `--email` still overrides and `hearth whoami` shows what was found. This only selects the account — the master password is still what unlocks the vault.
- With a platform identity broker configured on the server (`--identity-broker`, `--identity-broker-key`), being logged in is enough: `hearth login`, `hearth setup` and the daemon start check once and automatically upload the vault key wrapped by a key derived from the account's platform credential. `hearth login` on a new machine then proves possession to the server, which resolves the same credential through the broker, so no password is asked. The platform secret is never sent; the system key stays on the server. Without a matching credential it falls back to the password, and `hearth identity disable` opts out (the automatic check honours it).
- Incremental, append-only upload of session `.jsonl` files, each agent in its own namespace:
  - Codex: `$CODEX_HOME/sessions/**`, `archived_sessions/**`
  - Claude Code: `$CLAUDE_CONFIG_DIR/projects/**` (including subagent transcripts)
  - Comate: `~/.comate-engine/store/chat_session_*`
  - Long-term memory notes (`projects/<slug>/memory/*.md` of Claude Code and Comate, symlinked directories included) are synced too and pinned so they never expire.
- Every chunk is encrypted with XChaCha20-Poly1305, bound by AAD to its session, generation and byte range. Session IDs are HMACs, so the server sees neither paths nor agent names.
- Crash safety: chunk uploads are idempotent; after a crash or lost local state the client resumes from the server's committed size after verifying the prefix.
- Rewritten or truncated files start a new generation.
- Single-writer leases per session; `session take` moves a session to a new machine after the old one died.
- Expiry: sessions not accessed for 30 days become `expired`, are deleted after a 7-day grace period, and leave a tombstone so old machines do not re-upload them (`session revive` to undo). Pinned and leased sessions never expire.
- `daemon` uploads new session data every 60 s (`--interval`), syncs the library every 20 min (`--lib-interval`); it flushes everything on SIGTERM, so a graceful stop loses nothing and a kill loses at most the last minute; `daemon start` runs it in the background with a single-instance lock; `push` (alias `flush`) is suitable for a k8s preStop hook.
- One-line install on a new machine: the server hands out `/install.sh` and client builds for Linux and macOS on amd64 and arm64 (`--public-url`, builds from `scripts/build-dist.sh` in `<data>/dist` or `--dist-dir`; the Docker image includes them), with a self-signed certificate made by `server gencert` and pinned by the one-liner. The installer picks the build for `uname`, verifies its SHA-256 and runs `setup`. `hearth self-update` later replaces the binary with the server's build (checksum verified) and restarts the daemon.
- macOS: env steps hook into `~/.zshrc` (zsh `precmd`) when the login shell is zsh, `~/.bashrc` (`PROMPT_COMMAND`) otherwise.
- Verified: `codex exec resume <id>` works from a restored rollout `.jsonl` alone in a fresh `CODEX_HOME`; Codex rebuilds its sqlite state.

## Cross-agent library (skills, agents, instructions)

One canonical library in `~/.hearth/library` is rendered into every agent:

| Library | Codex | Claude Code |
|---|---|---|
| `skills/<name>/SKILL.md` | symlink `~/.codex/skills/<name>` | symlink `~/.claude/skills/<name>` |
| `agents/<name>.md` (YAML front matter + instructions, optional `x-codex` / `x-claude` fields) | generated `agents/<name>.toml` | generated `agents/<name>.md` |
| `instructions/AGENTS.md` | symlink `~/.codex/AGENTS.md` | generated `CLAUDE.md` with `@<library>/instructions/AGENTS.md` |

```bash
hearth lib import codex [--from <project>/.codex]   # or: lib import claude
hearth lib sync --adopt     # render; existing unmanaged copies are moved to ~/.hearth/backup
hearth lib push             # upload to your private space (blocked if the secret scan finds credentials)
hearth lib pull             # on another machine; `setup` and the daemon do this automatically
```

- Rendered paths are tracked in `~/.hearth/render.json`: later syncs update or remove only those, and a generated file edited by hand is left alone and reported.
- The daemon pushes local library edits and pulls/renders remote ones every 20 minutes (`hearth daemon --lib-interval`). If both sides changed, it merges them file by file against the last synced version: a file changed on one side takes that side (deletions too); a file changed on both keeps this machine's copy, and the server's goes to `<home>/backup/<time>/merge-server-copies`. `lib pull --force` takes the server copy instead and backs up the local one. Each pass also renders the library again, so agent homes that moved (`hearth agents isolate`) and new residents are picked up.
- The library is stored on the server in plaintext (owner-only), as the design intends for shareable content. Credential files inside skills (by content: private keys, common token formats, `key = "<literal>"`, `ak`/`sk` lines; by name: `credentials`, `*.pem`, `*.key`, …; or a file only its owner may read) are not in that bundle: they sync as end-to-end encrypted secrets (`lib-<skill>-…`, `hearth secret list`) and are written back into the skill (mode 0600) on the user's other machines, so a skill that ships its own CLI credentials still works everywhere while its shared copy carries none. Elsewhere in the library (config, recipes, agents) a likely secret still blocks the upload; mark documented examples with `hearth:allow-secret`.
- Skills an agent sees in its own skill list are the resident ones (`[skills] resident`, plus every skill `hearth lib import` or automatic import added since); the others are found on demand through the `hearth` MCP server's skill search.
- Not yet: companion directories next to agent files (e.g. `agents/notes/`), MCP config rendering, Comate.

## Secrets, tools and account recovery

```bash
echo -n "$TOKEN" | hearth secret set GIT_TOKEN       # env-style (UPPER_CASE)
hearth secret set gh-hosts --file ~/.config/gh/hosts.yml   # file secret, restored to the same path (0600)
hearth secret list | get NAME | rm NAME
hearth secret exec -- git push                          # env-style secrets injected for one command
hearth secret restore [--force]                         # setup does this automatically

hearth tools add codex --current        # pin the installed version; or --version x.y.z; claude is built in too
hearth tools add dumate --check 'dumate --version' --install 'curl ... | sh'
hearth tools install [--yes]            # install the pinned CLIs yourself (setup does not; they are in hearth recipe plan)

hearth passwd                           # needs the current password; other devices keep working
hearth recover --server URL --email me@example.com   # forgot it: recovery key + new password, signs out all devices
```

- Secret names and values are encrypted with the vault key; the server stores only an HMAC id and ciphertext. Private keys get a warning: prefer SSH agent forwarding.
- The tool manifest is ``config.toml` `[[tools.pin]]``, synced like the rest of the library. Built-in tools are installed with `npm install -g <package>@<validated version>`. Custom install commands run only after you approve them on that machine, and a changed command needs approval again, so a tampered manifest cannot run code by itself.
- A recovery key that was shown to someone (pasted into a chat, a ticket, a screenshot) is replaced with `hearth recovery-key rotate` (asks for the master password; the old key stops working, devices and data are unaffected).
- Registration also stores an HMAC of a key derived from the recovery key, so the server can verify recovery without learning the key.

## Sharing: teams and remote sources

```bash
# alice (owner) sets up a team and publishes from her library
hearth team create sglang-team
hearth team add sglang-team bob@example.com --role member     # or maintainer (can publish), owner
hearth lib publish skill benchmark --team sglang-team              # immutable v1, v2, ... (secret scan first)
hearth lib publish agent sglang-expert --team sglang-team

# bob uses it
hearth lib source add team sglang-team team      # source name "team"
hearth lib source add git community https://github.com/<owner>/skills --subdir . # untrusted by default
hearth lib source list            # lookup order: local library, then sources in this order
hearth lib find 压测 benchmark     # search every source
hearth lib link benchmark         # first source that has it; or team:benchmark; --agent for agents
hearth lib link community:pdf     # untrusted: shows files and SKILL.md, then needs --yes
hearth lib update                 # shows newer versions with a file diff; --yes applies
hearth lib links | unlink <name>
hearth team share-usage sglang-team [--auto]   # opt-in: daily counts of the team's published skills
hearth team stats sglang-team                  # most used skills in the team, last 30 days
```

- A link pins an exact version (team) or commit (git) plus a content hash. Links and sources are stored in `library/links.json` and `library/sources.json`, so they sync with the library; a new machine downloads the pinned content on its first render.
- Name resolution: an item in your own library always wins; `source:name` picks a source explicitly. Remote copies never shadow local ones.
- Roles: members read and link, maintainers also publish, owners manage members (a team always keeps at least one owner). Non-members get 404 for a team. Any registered user can create a team.
- Packages are stored in plaintext on the server, readable by members only.
- `hearth mcp` also searches linked skills and, from trusted sources only, skills that are not linked yet; `get_skill` downloads those on demand. Untrusted sources never reach an agent without `link --yes`.

## On-demand skills over MCP

Instead of loading every skill description into each agent's context, agents can search and load skills when needed:

```bash
hearth lib mcp-install          # runs `codex mcp add` / `claude mcp add --scope user` for `hearth mcp`
hearth lib search "压测 吞吐 benchmark throughput"   # same ranking as the MCP tool
echo bench > the `[skills] resident` list in ~/.hearth/library/config.toml && hearth lib sync   # keep only these skills resident
hearth lib stats                # skills most loaded through get_skill (local only)
```

- Tools: `search_skills(query, limit)`, `get_skill(name)` (SKILL.md, directory and file list), `list_skills()`. All are marked read-only, so Codex runs them without an approval prompt.
- Sources, most local first: the current project's `.agents/skills`, `.codex/skills`, `.claude/skills`, any `--skills-dir`, then the library. The index is rebuilt when a SKILL.md changes.
- Ranking is BM25 over name, description and the start of the body, with CJK bigrams and light English stemming. On the 62 skills in this workspace, 25 hand-written tasks gave recall@3 of 19/25 for Chinese-only queries and 25/25 (recall@1 24/25) for queries with English keywords added, which the tool description asks agents to include. There are no embeddings yet.
- Verified end to end: `codex exec` called `search_skills` then `get_skill` through this server and answered from the loaded skill.

## Codex Desktop thread list

Codex rebuilds its thread list (`state_*.sqlite`, `thread_history_*.sqlite`) from the rollout files, so those databases are not synced. What the rollouts do not contain travels in each session's encrypted metadata:

- Thread names (renames in Desktop) and sidebar sections, including Pinned. The client reads them from Codex's state database read-only (falling back to `session_index.jsonl`) and re-sends a session's metadata when they change, without re-uploading content.
- Recency: restored rollouts get their last-activity time as mtime (from the last `timestamp` line), because Codex orders threads by it.
- Codex Desktop's sidebar reads Codex's state database, which only learns about rollouts written by another program when a thread is read. After `setup` / `restore`, every restored or rewritten thread is read once through `codex app-server`, so it shows up without opening it by id; `hearth codex-threads` does the same for all local rollouts. A Desktop window that is already open caches its sidebar list and will not show restored threads until it restarts; Search (thread name or id) finds and opens them right away.
- `hearth session restore <id>` with Codex Desktop running forks the restored thread through Desktop's own backend (its control socket), which announces it to the open window, so it shows up without a restart. The fork references the restored history instead of copying it; the restored original is archived so a restart does not list it twice. Fork parents are restored with the thread. Codex archive/unarchive moves are tracked as the same session.
- Agent integrations are modular: the sync/restore/index core is agent-neutral; each agent supplies a file adapter plus an optional `AgentIntegration` (conversation grouping, post-restore UI/index hook, moved/deleted detection, cross-agent import). Codex is the reference implementation; see `docs/agent_integration.md`. `hearth agent list` shows the capability matrix.
- `hearth agent import codex --from claude [file | session-id ...]` (alias `hearth codex-import`) turns Claude Code sessions into Codex threads with Codex's own importer (`externalAgentConfig/import`), through the Desktop backend when it runs, so they appear in the open window. Codex records what it imported and skips unchanged sessions.
- Threads are grouped by `cwd`: sessions get this machine's workspace path (see "Paths on machines with a different layout"), including sessions uploaded before their machine had a workspace: every workspace any of the user's machines recorded is mapped to the local one, and machines record their workspace for older sessions on the next pass. Copies restored earlier with the old paths are rewritten on the next `hearth restore` (`remapped`).
- After `setup` / `restore`, names and sections are applied through `codex app-server` (the API Codex Desktop uses): missing custom sections are created, already-correct threads are left alone. `hearth codex-threads` re-applies them, e.g. if Codex was installed after the restore.
- Archived threads stay archived (`archived_sessions/` is synced).
- A thread deleted in Codex (Desktop or `codex delete`: rollout and state row gone, unlike archiving) is marked expired on the server by the next sync (`POST /v1/sessions/{id}/expire`); deleting the Desktop copy of a restored thread expires its archived original too. Expired sessions are not restored and are removed after the grace period (`server --grace-days`, default 7); `hearth session restore <id>` still brings one back. If most synced sessions vanish at once (moved or wiped `CODEX_HOME`), nothing is expired.
- The server lists sessions by last activity (last appended content), newest first, with the total count (`GET /v1/sessions?limit=&offset=`, header `X-Hearth-Total-Count`). `setup` and `restore` bring back only the 10 most recently active conversations (`restore --last N`, `--all`, `setup --sessions N`); memory notes and other non-conversation entries do not count against the limit.
- **Backup selection.** What an account uploads is the user's choice, recorded in `config.toml` `[backup] include/exclude` (synced, so every machine follows the same habits). Everything supported is uploaded by default: per agent and kind (`@codex`, `@codex.sessions`, `@claude.memory`, ...), `@workspace` and `@credentials`; agents added later too. `exclude` turns categories or paths off (`@claude.sessions`, `**/*.tmp`, `~/.ssh/id_rsa`); `include` adds files (`${WORKSPACE}/notes/`). Chosen files restore under the next machine's home or workspace. Commands: `hearth backup status|show|edit|enable <cat>|disable <cat>|add <path>|exclude <pattern>`.
- **Session index.** Content chunks live in the blob store; the small searchable index lives separately in the `sessions` row (size, status, last activity, last writing device) and its encrypted metadata: title, conversation rounds (user requests), last write time, last writing machine (`Writer`), path, cwd, agent. The daemon keeps the index up to date incrementally from appended bytes, without re-reading old history. Titles and counts are encrypted end to end, so search decrypts metadata on the client; content is never downloaded for listing. `hearth session list` shows newest 20 (`-n 0` for all) with title, turns, updated, machine, size, status; `hearth session find <text>` searches title/path/cwd/machine; `hearth session show <id>` prints one full index entry; `-s/--search`, `--host`, `--status`, `--json` filter. Restore chosen entries with `hearth session restore <id> [<id> ...]` (or `hearth restore <id> ...`); any unique id prefix works, and a Codex thread's related rollout files are restored together.

Verified with Codex 0.163 in `scripts/codex-e2e.sh`: a thread renamed and pinned on machine A shows up on machine B with the same name, section and position.

## Paths on machines with a different layout

Transcripts are full of absolute paths (`cwd`, files read and edited, commands). When a session moves to a machine whose home or workspace is elsewhere (`/root/workspace/me` on an H800 box, `/Users/me/work` on a Mac), Hearth rewrites them:

- Each session records its origin machine's home and workspace. On restore, the transcript is rewritten to this machine's: `/root/workspace/me/repo/a.go` becomes `/Users/me/work/repo/a.go`, other paths under the old home move to the new home. Only whole roots on path boundaries are replaced (`/root/ws` never matches `/root/ws2` or `/data/root/ws`).
- Claude Code files its transcripts by the encoded cwd (`~/.claude/projects/-root-workspace-me-repo/`); they are restored into the directory of the local cwd (`-Users-me-work-repo/`), so `claude --resume` finds them. Codex rollouts get the local `cwd`, so Codex Desktop groups the thread under the local project and `codex resume` works from the local workspace.
- The server keeps the origin's form. Lines this machine appends are translated back before upload, whole lines at a time (an incomplete last line waits for its newline), so every machine sees its own paths, including turns written by other machines, and the origin gets its exact bytes back.
- Only Codex and Claude Code JSONL transcripts are translated; memory notes and other files are restored as they are. `HEARTH_NO_PATH_MAP=1` turns translation off. Sessions uploaded before this feature are not translated (they carry no roots).

Verified in `scripts/codex-e2e.sh`: three machines with different workspaces; B resumes A's session with B's paths, and B's new turns reach C with C's paths.

## Continuing in another agent

`hearth session handoff <id>` turns any synced Codex, Claude Code or Comate session into an agent-neutral Markdown hand-off (original request, recent turns, instructions), for when the original agent cannot resume it or you want to switch agents.

## Restore recipes

Recipes (`library/recipes/*.toml`, synced) replace hand-written `save.sh` / `restore.sh` scripts. Step types: `tool` (download with per-arch sha256, optional proxy and mirrors, or npm with a pinned Node and a wrapper), `file` (secret or library file to one or more paths, mode, policy `if-absent` / `warn` / `overwrite`), `env` (variables and aliases, optionally only inside given directories), `run`, `service`, `check`, `capture` (save side, e.g. `codex-mixin config export` into a secret) and `manual` (QR-code logins, registering public keys). See `examples/recipes/` for the two cases this was designed from.

```bash
hearth recipe add examples/recipes/ssh-identities.toml
hearth recipe plan [--json]        # this machine's restore flow: software and commands with a check each (the hearth skill goes through it)
hearth recipe run <key> ...        # run chosen steps now and mark the ones that worked done
hearth recipe done <key> ...       # a step done by hand on this machine
hearth recipe apply --dry-run      # everything at once instead (setup only restores files and env)
hearth recipe apply                # in a terminal: lists the unapproved commands and asks once
hearth recipe apply --yes          # approve without asking; approvals are shared by your machines (vault secret hearth.recipe-approvals)
hearth doctor                      # checks + pending manual steps, changes nothing
hearth save                        # run capture steps now (the daemon runs due ones)
hearth up                          # after a container restart: daemon + services
hearth env status | uninstall      # the ~/.bashrc hook written by env steps
```

- Steps are idempotent and ordered by phase (tool, file, env, run, service, check, manual) and `depends_on`; a failed step skips its dependants.
- Commands from synced recipes never run on a machine before you approve them there; a changed command needs approval again. Downloads must carry a sha256.
- Shared files (`~/.ssh/config`, `~/.gitconfig`, `~/.npmrc`, shell rc files, `/etc/`) cannot be written by `file` steps unless `shared_ok = true`; use `env` redirects (`GIT_SSH_COMMAND`, `GIT_CONFIG_GLOBAL`, ...). The only rc change is one marked block in `~/.bashrc` that sources `~/.hearth/env/activate.sh`.

## Per-user workspaces

Several people often share one Unix account on GPU machines. `hearth init <dir>` makes `<dir>` that person's workspace: state lives in `<dir>/.hearth`, every hearth command run inside `<dir>` (or with `--workspace`) uses it, and recipes can refer to `${WORKSPACE}` so the same recipe works wherever the workspace is on the next machine.

```bash
hearth init /root/workspace/alice
hearth agents home codex '${WORKSPACE}/.codex'   # keep this user's Codex config in the workspace
```

The `~/.bashrc` hook is one block per workspace and only activates inside that workspace.

## Shared accounts: only your own sessions

On GPU machines several people often share `root`, so `~/.codex` and `~/.claude` hold everybody's conversations. As soon as a workspace is set (`hearth init`, `--workspace`, or a machine profile), only sessions whose working directory is inside it are uploaded; Claude transcripts and memory notes without a `cwd` are judged by their project directory. Everything else stays local.

```bash
hearth session scope                 # what is uploaded, and the local sessions that are not (by directory)
hearth session scope add ~/scratch   # also yours: upload sessions run there
hearth session scope all             # nobody else uses this account: upload everything
hearth session scope prune [--keep <dir>] [--yes]   # delete sessions uploaded outside the uploading machine's workspace
```

Sessions restored from your account are always yours, wherever they ran. Without any workspace the home directory is assumed to be yours alone (`hearth onboard status` warns on `/root`), except sessions run inside a workspace made with `hearth init`: that workspace has its own login, usually another person's (`hearth session scope all` takes them too). The scope is per machine; each session records its `cwd`, so `prune` can judge sessions uploaded by any machine.

## Personal files inside the workspace

Many people keep their agent state in the workspace rather than in `$HOME`: the workspace is the Codex / Claude Code / Comate project directory, so it holds `.codex/skills`, `.codex/agents`, `.comate/memory`, `.claude/settings.local.json`, `.comate/mcp.json`, the memory MCP graph (`.memory/`), and a workspace `.ssh/`. `hearth learn` looks there too:

- Skills and agents in `${WORKSPACE}/.codex`, `.claude`, `.comate` that the library lacks: imported into the library (then rendered for every agent, on every machine).
- Personal files and directories (memory notes, agent settings, MCP configs, known_hosts, tool configs such as `.popo.json`): added to the synced list `library/workspace-files.json`. They sync end-to-end encrypted like sessions, never expire, and are restored into the workspace of the next machine wherever it is (`setup` pulls the library first, then restores). `.hearth/` and `.git/` are never synced.
- MCP configs of the workspace are also offered for `mcp-config import` (tokens become secrets).
- SSH keys and other credential files found both in the workspace and in `$HOME` share one secret when identical and get separate ones (`.ws` / `.home`) when they differ.

## Different kinds of machines

The same user restores differently on an H800 box, a GB200 (aarch64) node, an A30 dev machine or a Mac, and keeps work in different places, but most habits (git identity, SSH keys, agent config, aliases) are the same everywhere. hearth keeps one synced library and scopes only what differs:

```bash
hearth machine                      # detected facts (os, arch, GPU via nvidia-smi, hostname, labels) and active profiles
hearth machine add h800 --match gpu=h800 --workspace '/mnt/ssd/$USER/work'
hearth machine add gb --match gpu='gb*,b*' --match arch=arm64 --workspace /data/$USER
hearth machine add mac --match os=darwin --workspace '~/work'
hearth machine add pdc --match label=pdc   # labels come from $HEARTH_LABELS
hearth machine use special                 # apply a profile here even if it does not match
```

- Profiles live in `library/machines.toml` (synced); the facts and `machine use` choices are local. Match values are comma-separated alternatives with globs and `!` negation; `gpu=none` means no GPU; `arch` accepts `amd64`/`arm64`, `os` accepts `mac`.
- Recipe steps without `when` run on every machine. `when = { profile = "h800" }` (or `gpu`, `arch`, `os`, `hostname`, `label`) on a step or a whole recipe limits it; skipped steps say why (`for profile h800 (this machine: mac)`).
- A profile's `workspace` becomes `${WORKSPACE}` on matching machines and is created by `setup`, unless `hearth init` or `--workspace` set one explicitly. Recipes written with `${WORKSPACE}` therefore land in the right place on each kind of machine.
- `learn` asks per item: `[a]ll machines` or `[m] only machines like this`. Items that look machine-specific (CUDA/NCCL/HF paths, proxies, absolute paths outside home and workspace) default to the latter. The first `m` creates a profile for this machine (named after the GPU model, else `mac` / `linux-aarch64`) holding the current workspace. Machine-only steps are named `<step>@<profile>` and machine-only files get their own secret, so an H800 and a Mac can each keep their own version. Free-form steps prefixed with `here:` are machine-only too.
- Agents see the profiles in `list_setup_recipes` and are asked to add `when` to steps only some machines need.
- `HEARTH_MACHINE='{"gpu":"h800","arch":"arm64"}'` overrides detected facts, to try recipes for other machines.

## Learning each user's setup

Every user restores a different environment, so the client learns it instead of assuming one:

```bash
hearth learn --list      # what was found: new / known / changed
hearth learn             # one checklist with proposals (a / m / n / v per item), then free-form steps; --one-by-one asks per item
hearth recipe show learned
```

- Discovery looks at the workspace, home and environment: the workspace's private SSH identity (its `ssh/config`, every private key that config references, and the `GIT_SSH_COMMAND` / `sshx` / `scpx` wiring — one item, because a key ssh never points at is not an identity), Codex `auth.json`/`config.toml`, Claude Code login and settings, gh, git credentials, netrc, workspace gitconfig, Comate login tokens and the token files listed in `library/learn.json` (`{"token_files": ["~/.config/sso/token_*"]}`), env conventions (`GIT_CONFIG_GLOBAL`, `GIT_SSH_COMMAND`, proxies, npm registry), agent CLI versions, codex-mixin (capture its config so new machines skip the QR login), MCP servers not yet in `[mcp]`,.
- Files become encrypted secrets; everything else becomes steps of the synced recipe `learned`. The answers live on the server, so `hearth setup` on any new machine replays them.
- "never" is remembered (`[learn] ignore` in config.toml, synced). Later runs only ask about new items and credentials whose content changed (`--all` to review everything).
- Free-form answers: `$ command` becomes a `run` step (approved per machine like any other), plain text a `manual` reminder.
- Agents learn too: the MCP server offers `list_setup_recipes` and `propose_setup_step`. When an agent helps you install or log in to something, it can propose the step; nothing runs until you accept it with `hearth recipe proposals [accept|reject <id>]`. `doctor` and `setup` mention pending proposals.

## MCP servers for every agent

```bash
hearth mcp-config import comate     # ~/.comate/mcp.json -> `config.toml` `[mcp]`; tokens become secrets
hearth mcp-config sync [--adopt]    # Comate mcp.json, Claude ~/.claude.json, Codex via `codex mcp add`
```

``config.toml` `[mcp]`` references credentials as `${secret:NAME}`; they are resolved at render time. Existing servers that hearth did not create are left alone unless `--adopt`.

## Quick start

### Server host (once)

```bash
go build -o bin/hearth ./cmd/hearth
D=/var/lib/hearth

# Self-signed TLS for the addresses clients use (skip if you terminate TLS in a proxy)
./bin/hearth server gencert --data $D --host hearth.example.com
# Client builds for linux/darwin x amd64/arm64 (the server serves its own platform without this)
scripts/build-dist.sh $D/dist
# Serves the API plus /install.sh and the builds in $D/dist; prints the install one-liner
./bin/hearth server --data $D --addr 0.0.0.0:8787 --public-url https://hearth.example.com:8787
# Invite mode (default): one single-use invite per new user (default 7 days).
# Trusted network: start the server with --registration open and skip invites.
./bin/hearth server invite --data $D
```

### Any machine: one line installs, logs in, restores and starts sync

```bash
# First machine of a new user: also creates the account (prints the recovery key once)
curl -fsSL -k https://hearth.example.com:8787/install.sh | sh -s -- --invite inv-...
# ... or on a server with --registration open:
curl -fsSL -k https://hearth.example.com:8787/install.sh | sh -s -- --new

# Every later / replacement machine
curl -fsSL -k https://hearth.example.com:8787/install.sh | sh
```

The server prints the exact command at startup. `-k` only covers the first download; the installer then trusts exactly the embedded certificate. `server --pin-install` adds `--pinnedpubkey` to verify the first download too. The script embeds the server certificate as `~/.hearth/ca.pem`, downloads the binary over that trusted channel, checks its SHA-256, installs it to `~/.local/bin/hearth` and runs `hearth setup`, which:

1. logs in (asks for the master password; creates the account first with `--new`, or `--invite <code>` on invite-only servers),
2. restores every session into `~/.codex` / `~/.claude`,
3. starts the background daemon (`hearth daemon status|stop|start`).

### Other commands

```bash
hearth session list
hearth session take <id>     # take over a session whose old machine was killed
hearth session pin|unpin|touch|rm|revive <id>
hearth device list|revoke <id>
hearth flush                 # upload everything now (k8s preStop hook)
```

`HEARTH_PASSWORD` can supply the password non-interactively; `HEARTH_HOME` (default `~/.hearth`) holds `config.json`, the cached `vault.key` and `state.json`, all mode 0600.

## Security notes

- Registration defaults to `--registration invite`: each account needs a single-use invite code that only someone with access to the server's data directory can create (`hearth server invite`). Only the SHA-256 of the code is stored. `open` (anyone who can reach the server may sign up with `--new`; fine on an internal network) and `closed` are also available. `GET /v1/info` tells clients and the setup skill which mode applies, so nobody is asked for an invite code that is not needed.
- Plain HTTP is refused except to loopback; use `server gencert`, `--tls-cert/--tls-key` or a reverse proxy. `--ca-cert` on `login`/`register`/`setup` trusts a private CA. All session and device endpoints require a bearer token; auth endpoints are rate limited per IP.
- Tokens expire after 30 days without use (sliding).
- The vault key is cached in `vault.key` so the daemon runs unattended. Root on that machine can read it; this matches the design's threat model (a compromised machine is not defended against).
- By design any device that logs in with the master password can download and decrypt every secret; there is no approval step by an existing device. The master password is therefore the only protection of the secrets: use a strong one, and after a suspected leak run `device revoke`, `passwd` and rotate the secrets. MFA for login is not implemented yet.
- Email addresses are not verified: an invite holder can register any address. Planned options: bind invites to an email, restrict allowed domains, or email verification codes.

## Known gaps

- Comate's agent and instruction formats are not rendered (skills and MCP are).
- Windows is not supported (use WSL). The darwin and linux-arm64 builds are cross-compiled and served by tests, but were not executed on those platforms here.
- Client side: no systemd unit for the daemon; run `hearth up` after a reboot or container restart.
- Path translation covers home and workspace only; other absolute paths (e.g. /mnt/data on one machine, /data on another) are kept as they are.
- Polling instead of fsnotify. The only agent hooks are the memory hooks of Codex and Claude Code.

Planned work for these and other items: [development roadmap](https://github.com/hearth-project/hearth-cli/issues/1).

## Development

```bash
go test -race ./...
# the same suite against PostgreSQL and S3 (MinIO, or an in-process fake):
HEARTH_TEST_POSTGRES=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable \
HEARTH_TEST_S3=fake go test -race ./...
```

The end-to-end tests in `internal/client` run a real server in-process and cover restore, leases, rewrites, crash recovery, tombstones, expiry, tampering and the absence of plaintext on the server.
