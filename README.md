<h1 align="center">Hearth</h1>

<p align="center">
  <b>A portable workspace for your coding agents.</b><br>
  Conversations, skills, credentials and setup of Codex / Claude Code / Comate follow you to any machine.<br>
  <sub>Self-hosted · end-to-end encrypted · one Go binary for server and client</sub>
</p>

<p align="center">
  <b>English</b> · <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  <a href="https://github.com/hearth-project/hearth-cli/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/hearth-project/hearth-cli/actions/workflows/ci.yml/badge.svg"></a>
  <img alt="Platforms" src="https://img.shields.io/badge/platform-Linux%20%7C%20macOS-lightgrey">
  <img alt="Arch" src="https://img.shields.io/badge/arch-x86__64%20%7C%20arm64-blue">
  <img alt="E2EE" src="https://img.shields.io/badge/encryption-end--to--end-green">
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
</p>

<p align="center">
  <a href="#why-hearth">Why</a> ·
  <a href="#quick-start">Quick start</a> ·
  <a href="#roadmap-long-term-memory">Roadmap</a> ·
  <a href="#features">Features</a> ·
  <a href="#agent-support">Agent support</a> ·
  <a href="#documentation">Docs</a> ·
  <a href="#development">Development</a>
</p>

---

## Why Hearth

You work with AI coding agents on several machines: GPU boxes, containers that get reclaimed, a laptop. Every new machine means installing the agents again, copying skills, logging in to git and SSH again, and the conversations from the last machine are gone.

Hearth is your agents' portable workspace: it keeps that state on a server your team runs and puts it back on any machine.

- **One command per machine.** Install the client and it logs in, restores your newest conversations, skills, agents, credentials and shell setup, then keeps syncing in the background.
- **Your agent does the rest.** The installer adds a `hearth` skill to every agent. Say *"use hearth"* to restore older conversations, choose what is uploaded, or set up this machine's software step by step.
- **The server never sees your data.** Conversations and credentials are encrypted on the client; the server stores ciphertext only.
- **Shared, not duplicated.** One set of skills and agents serves Codex, Claude Code and Comate; teams share their most used ones automatically.

```text
  machine A ──┐                                 ┌── machine B (new)
  Codex       │  encrypted sync   ┌──────────┐  │   curl …/install.sh | sh
  Claude Code ├─────────────────▶ │  Hearth  │ ─┤   → conversations, skills, logins back
  Comate      │                   │  server  │  │
  skills, ssh ┘                   └──────────┘  └── machine C …
```

---

## Quick start

Server and client are the same `hearth` binary (Linux and macOS, x86_64 and ARM64). Deploy the server once, then install the client on each machine.

### 1. Deploy the server (one command)

On a Linux machine every dev machine can reach:

```bash
curl -fsSL https://raw.githubusercontent.com/hearth-project/hearth-cli/main/scripts/deploy-server.sh \
  | sh -s -- --host <server-ip-or-name>
```

This downloads the latest release (or builds from source when there is none and Go 1.26+ is installed), creates a self-signed certificate, and starts the server under a supervisor on port 8787. It prints the client install command; the same command is on the download page `https://<server>:8787/`. Re-run it to upgrade.

| Option | Default | |
| --- | --- | --- |
| `--host` | — (required) | IP or DNS name clients use |
| `--port` | `8787` | listen port |
| `--data` | `/var/lib/hearth` (root) / `~/hearth-server` | database, files, certificate |
| `--registration` | `invite` | `invite`, `open` (internal networks), `approval`, `closed` |
| `--tls-cert`, `--tls-key` | self-signed | your own certificate |

<details>
<summary>Other ways: Docker, systemd, PostgreSQL + S3</summary>

```bash
docker compose -f deploy/docker-compose.yml up -d                       # one container, SQLite + volume
docker compose -f deploy/docker-compose.yml exec hearth hearth server invite
hearth server install-service --data /var/lib/hearth …                  # prints a systemd unit
```

Several instances on PostgreSQL + S3: `deploy/docker-compose.scale.yml`. Everything about running the server (backups, migrations, quotas, the admin panel): [Server guide](docs/en/server.md).
</details>

Invite users with `hearth server invite --data <dir>` (not needed with `--registration open`). After signing up, make yourself an admin of the web console: `hearth server admin grant <you@example.com> --data <dir>`, then `hearth panel`.

### 2. Install the client (one command per machine)

Copy the command from the server's download page; it looks like this:

```bash
curl -fsSL -k https://<server>:8787/install.sh | sh                          # existing account, or open registration
curl -fsSL -k https://<server>:8787/install.sh | sh -s -- --invite <code>    # first machine on an invite-only server
```

`-k` is only for the first download from a self-signed server; after that the client trusts exactly that server's certificate.

The installer puts `hearth` in `~/.local/bin`, adds the `hearth` skill to Codex / Claude Code / Comate, asks for your master password and your working directory on this machine, and restores your files:

```text
hearth 0.1.0 installed to ~/.local/bin/hearth
Hearth setup on gpu-node-13 (linux x86_64, 8 x NVIDIA H800)
  account      you@example.com (from comate login you)
  Master password:
  login        ok, this machine is device gpu-node-13
  workspace    /data/you, as on gpu-node-12 (GPU h800)
               Enter to use it, or type another directory:
  library      42 skills, 3 agents for codex
  files        6 credential and config files written
  sessions     10 conversations restored
  sync         on (background)
  ssh          h800-gpu-node-13 (10.0.0.13:8022)

Next
  1. Set up the software (codex, codex-mixin and 2 more): on any machine with Hearth, tell its agent
     "恢复 h800-gpu-node-13 的 Codex 环境"
  Anything else: ask Codex "用 hearth" · hearth status · details: ~/.cache/hearth/setup-….log
```

The working directory proposed is the one your machines of the same kind use (same GPU model, else the same GPU family, else the same OS and CPU); with none alike it is your home. Press Enter to take it or type another.

### 3. Restore the software from another machine

The new machine has your files; its software (Node, Codex CLI, gateways, …) is set up by your agent. On **any other machine with Hearth**, tell its agent the sentence setup printed, e.g. *"restore the Codex environment of h800-gpu-node-13"*. It looks at the new machine (`hearth machine list`), picks the restore flow for that kind of machine, and installs and checks each step there over `hearth ssh`; steps that need you (a QR-code login) it hands to you with `hearth ssh -t`. No other machine at hand: say *"use hearth to restore this machine's environment"* on the new machine itself. The web console's Machines page shows each machine's environment check.

A new account prints a **recovery key** once. Keep it offline; it is the only way back if you forget the password. Never paste the password or the key into a chat.

### 4. Use it

Ask your agent, in your own words:

| Say | What happens |
| --- | --- |
| *"restore the Codex environment of <machine>"* | sets up another machine's software over `hearth ssh`, following the restore flow for its kind, checking each step |
| *"use hearth to restore this machine's environment"* | the same for the machine you are on |
| *"restore my conversations from the last machine"* | lists them newest first and restores the ones you pick (into the open Codex Desktop window too) |
| *"don't upload Claude Code sessions"* | changes your upload selection for all your machines |

Or the CLI:

```bash
hearth status                        # login, sync, storage, what is left to do
hearth session list                  # conversations of all machines, newest first
hearth session restore <id> …        # restore chosen ones
hearth machine list                  # your machines: environment check, SSH host names
hearth recipe plan                   # this machine's software setup steps
hearth config --edit                 # every setting, in one file
```

Every step in detail: [Quick start guide](docs/en/quickstart.md).

---

## Roadmap: long-term memory

Hearth carries the workspace, and now a memory too: every agent can recall what you did on any machine (past conversations, decisions, fixes) and what you asked it to remember, automatically in Codex and Claude Code through prompt hooks, in milliseconds, still end-to-end encrypted. Try `hearth recall <words>`. Design and next steps: [Memory roadmap](docs/en/memory-roadmap.md). Everything planned for this and the next quarter: [Development roadmap](https://github.com/hearth-project/hearth-cli/issues/1).

---

## Features

| | |
| --- | --- |
| **Conversations** | Incremental, encrypted sync every 60 s; a killed machine loses at most the last minute. A searchable index (title, turns, machine, last activity) lets you restore exactly the conversations you want. Paths are rewritten when home or workspace differ. Threads deleted in Codex expire on the server; long-unused ones are cleaned up. |
| **Skills and agents** | One library rendered into every agent (single copy, symlinked). The most used skills are always loaded; the rest are found on demand through the `hearth` MCP skill search, which keeps agent context small. |
| **Credentials and shell** | SSH keys, git / gh, agent logins and token files are kept as end-to-end encrypted secrets and written back with mode 0600. Your own lines of `~/.bashrc` / `~/.zshrc` follow you without replacing a new machine's rc file. |
| **Learns your setup** | The background sync records new skills, tools, environment variables and MCP servers by itself; credentials too once you agree. Setup steps can differ per kind of machine (H800, GB200, Mac, …). |
| **Environment restore by your agent** | Hearth installs no software itself. `hearth recipe plan` lists this machine's steps with a check each; the `hearth` skill works through them with you, following the [client setup guide](docs/en/client-env.md). |
| **You choose what is uploaded** | Per agent and kind (`@codex.sessions`, `!@claude.sessions`), credentials, extra paths and exclusions, in one `config.toml` that follows your account. |
| **Remote machine readiness** | Every online Hearth client reports only non-secret readiness data: OS, architecture, GPU/profile, workspace, Hearth version, installed agent versions, MCP readiness and pending restore steps. From another logged-in machine, `hearth machine list` shows which target is ready; the `hearth` skill selects the matching profile and runs the target's recipe flow over SSH when you say “restore Codex environment”. |
| **SSH between your machines** | Logged-in Linux machines accept SSH from your own devices: their keys go into a marked block of `~/.ssh/authorized_keys` and the system sshd runs public-key only on the main interface (port 8022; an existing sshd there is reused). `hearth ssh <machine>`, plain `ssh -p 8022` with your usual key, or `hearth ssh config` for VS Code / Cursor Remote-SSH. Revoking a device removes its keys everywhere. |
| **Teams** | Invite codes, roles, sharing between teams. Each member's most used skills and agents are shared automatically; each team's most used ones are kept in every member's agents. |
| **Server** | SQLite + a directory by default, PostgreSQL + S3 for several instances. Supervisor, log rotation, quotas, backups, migrations. Web console for users (machines, skills, teams, settings) and admins (dashboard, users, approvals, invites, audit log). |

---

## Agent support

| Capability | Codex | Claude Code | Comate |
| --- | :---: | :---: | :---: |
| sync and restore conversations and memory | ✅ | ✅ | ✅ |
| paths rewritten between machines | ✅ | ✅ | — |
| skills rendered | ✅ | ✅ | ✅ |
| agents and instruction files rendered | ✅ | ✅ | — |
| MCP config and skill search | ✅ | ✅ | MCP config |
| restored threads shown in the open app | ✅ Desktop | — | — |
| deletions in the app synced to the server | ✅ | — | — |
| import other agents' conversations | from Claude Code | — | — |
| `hearth` skill installed automatically | ✅ | ✅ | ✅ |

`hearth agent list` shows this on your machine. Adding another agent: [Agent integration](docs/en/agent-integration.md).

---

## Documentation

| Topic | English | 中文 |
| --- | --- | --- |
| Server and clients in 10 minutes | [Quick start](docs/en/quickstart.md) | [快速上手](docs/zh/quickstart.md) |
| Install, upgrade, uninstall the client | [Installation](docs/en/installation.md) | [安装指南](docs/zh/installation.md) |
| Node / Codex CLI / Claude Code / codex-mixin on a new machine | [Client software setup](docs/en/client-env.md) | [客户端软件环境](docs/zh/client-env.md) |
| Conversations, uploads, credentials, learning, teams | [Usage](docs/en/usage.md) | [使用指南](docs/zh/usage.md) |
| Running and administering the server | [Server](docs/en/server.md) | [服务器](docs/zh/server.md) |
| FAQ and troubleshooting | [Troubleshooting](docs/en/troubleshooting.md) | [故障排查](docs/zh/troubleshooting.md) |
| Adding an agent | [Agent integration](docs/en/agent-integration.md) | [Agent 接入](docs/zh/agent-integration.md) |
| Every command | [CLI reference](docs/cli.md) | — |
| How it works inside | [Technical reference](docs/en/reference.md) | — |
| Long-term memory: design and status | [Memory roadmap](docs/en/memory-roadmap.md) | [记忆功能路线图](docs/zh/memory-roadmap.md) |
| The agent skill | [skills/hearth/SKILL.md](skills/hearth/SKILL.md) | — |

---

## Development

```bash
go test -race ./...                                                          # SQLite + local files
HEARTH_TEST_POSTGRES=postgres://… HEARTH_TEST_S3=fake go test -race ./...   # PostgreSQL + S3
scripts/deploy-server.sh --host 127.0.0.1 --source --data /tmp/hearth       # a local server from this checkout
hearth docs cli > docs/cli.md                                                # regenerate the CLI reference
```

Contributions are welcome: see [CONTRIBUTING](CONTRIBUTING.md) and the [Code of Conduct](CODE_OF_CONDUCT.md). Getting help: [SUPPORT](SUPPORT.md). Security issues: [SECURITY](SECURITY.md). Changes: [CHANGELOG](CHANGELOG.md).

## License

[Apache License 2.0](LICENSE)
