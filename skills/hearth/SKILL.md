---
name: hearth
description: Hearth - back up and restore coding-agent state across machines. Checks by itself whether Hearth is installed and logged in on this machine; if not, installs it and guides the user through login and restoring this machine; if it is, helps with daily use - find and restore conversations from other machines (Codex, Claude Code, Comate; into the open Codex Desktop window too), choose what is uploaded per agent (sessions, memory, workspace files, credentials), import Claude Code conversations into Codex, record this machine's setup, restore this machine's software environment step by step with the user (agent CLIs, gateways, from the user's recorded restore flow), check sync. Use when the user mentions Hearth, a new or changed machine ("换机器了", "恢复环境", "恢复这台机器的环境", "配置 hearth", "set up this machine"), old conversations ("恢复会话", "找回聊天记录", "上一台机器的对话", "拉一下远程会话"), what gets uploaded ("不要上传 claude 的会话", "备份 ssh"), or importing Claude Code sessions into Codex.
---

# Hearth

Hearth keeps this user's agent conversations, memory, skills, credentials and setup steps end-to-end encrypted on a server their team hosts, and restores them on any machine. Run the commands yourself and talk to the user in their language. Ask everything you need in one message, with your recommended answers filled in.

## Rules

- Never ask for, read, type or store the master password or the recovery key. Commands that need the password run in the user's own terminal. Do not set `HEARTH_PASSWORD` yourself.
- Never print secret contents (`auth.json`, SSH keys, tokens); refer to them by name.
- Show commands from the user's synced recipes before running them and get a yes; installs and logins follow **D. Restore this machine's environment**.
- Only restore or change what the user asked for; ask before bulk actions (`--all`, turning categories off).

## 0. Check the state first (always)

```bash
command -v hearth && hearth onboard status --json
```

- `hearth` not found → **A. Install**.
- `logged_in` is false → **B. Log in and restore this machine**.
- logged in → **C. Daily use**. If `next` lists open items (daemon not running, environment steps to do, things to learn), mention them in one line and offer to fix them; `recipes` in `next` → **D**.

Re-run `hearth onboard status --json` after each step; it is cheap and is the source of truth.

## A. Install

Ask the user for the install command their admin gave them (it looks like `curl -fsSL [-k] https://<server>/install.sh | sh -s -- …`), or only the server URL if the server has a normal HTTPS certificate. Install without logging in so you keep control:

```bash
curl -fsSL <same curl options> https://<server>/install.sh | sh -s -- --install-only
export PATH="$HOME/.local/bin:$PATH"
hearth onboard status --json
```

Keep the curl options exactly as given (a self-signed server's command has `-k`, sometimes `--pinnedpubkey`). If curl fails with a certificate error, ask the admin for the full command. Then continue with B.

## B. Log in and restore this machine

The standard flow has two parts. **(1) On the new machine**: install, log in, choose the working directory, and Hearth restores the user's files (conversations, skills, credentials, shell lines). **(2) From any other machine with Hearth**: the user tells that machine's agent "恢复 <machine> 的 Codex 环境", and it installs and checks the software over `hearth ssh` following the restore flow for this kind of machine (see "Discover and restore a newly online machine" in C). Part 2 can also run here (D) when no other machine is at hand.

1. Collect in one message (skip what `onboard status` already knows):
   - server URL only if `onboard status` has none (the installer remembers it); account email only if `onboard status` shows no `email` (it is detected from this machine's identity). Ask for an invite code only if `registration` is `invite`; open servers create a missing account during setup.
   - workspace: `hearth setup` proposes it itself after the login, from the user's machines of the same kind (same GPU model, else the same GPU family such as B / GB series, else the same OS and CPU; none alike: the home), and the user confirms or types another in the terminal. Ask here only when setup cannot ask (no terminal): then propose the same and run `hearth init <dir>` first.
   - which agents to integrate (default: the installed ones among codex, claude, comate).
   - team (optional): an invite code from a colleague to join their team, or a name to create a new team; skip if they work alone. Pass it as `--team <code-or-name>` to setup (setup also asks in the terminal when the user has no team).
2. You run:

   ```bash
   # only when setup cannot ask (no terminal), or the user already said where:
   mkdir -p <workspace> && hearth init <workspace> && cd <workspace>
   # already logged in without a workspace (~/.hearth) and it is this person's: hearth init --move-login <workspace>
   hearth agents enable codex      # / claude / comate; disable the unwanted ones
   ```

3. The user runs exactly one command in their own terminal (it prompts for the password; it must not go through the chat):

   ```bash
   cd <workspace> && hearth setup        # the `command` of the login step in onboard status; it adds --server / --email only when they are unknown
   cd <workspace> && hearth setup --invite <code>   # new account on an invite-only server
   ```

   Tell them: a new account prints a recovery key once; keep it offline (password manager), never paste it into the chat. `setup` restores the 10 most recently active conversations (`--sessions N`, 0 = all), skills and credentials, and starts background sync. It also copies the skills and agents this machine already has into the library (`--no-import` to skip; items that look like they hold a credential stay out and are listed), asks for the email when no identity is found, and offers in the terminal the conversations left on machines still online (usually the machine they are moving from). It ends with a `done:` line (conversations, library size, credential files) and the next steps. Setup installs no software: agent CLIs, gateways and services come in **D**. Wait until they say it finished.
4. Finish, following `next` in `onboard status`:
   - skill search MCP: `setup` registers it with Codex and Claude Code automatically; if it is missing (agent installed later) run `hearth lib mcp-install`;
   - skills live once in the library (`hearth lib skills-dir` shows it); move them with `hearth lib skills-dir '<path>'` (e.g. `${WORKSPACE}/.agents/skills`) — it moves the files, rewrites the agent links and saves `skills.dir` in `<library>/config.toml`, which syncs to other machines;
   - this machine's software: tell the user to say, on any other machine with Hearth, "恢复 <ssh host> 的 Codex 环境" (setup prints the sentence with this machine's name); or do **D** here;
   - `hearth daemon start` if the daemon is not running.
5. Upload choices, asked once: show `hearth backup status`. Everything is uploaded by default (every agent's sessions and memory, workspace files, credentials). Ask whether to turn anything off (see C).
6. Record this machine's setup so the next one needs nothing: see **Learn** in C.
7. Report in a few lines: account, server, workspace, agents, conversations restored, what was learned, anything pending. Then tell the user what they can ask from now on (the examples at the top of C).

## D. Restore this machine's environment

Setup restored data and credentials; the software (Node, Codex CLI, Claude Code, codex-mixin, services) is set up by you, with the user, from the restore flow the user's machines recorded on the server. Hearth does not install it by itself.

1. Read the flow and the guide:

   ```bash
   hearth recipe plan --json   # this machine's steps in order: type, do, commands, check, interactive, status, key
   hearth docs env             # how to install and check each tool, where the latest versions are, plus the organisation's notes from the server
   uname -sm                   # pick packages by architecture (x86_64 / aarch64 / Darwin)
   ```

2. Show the user the steps still `todo` as a short numbered list (what, how, how it is checked) and ask once which to do now (recommend all).
3. For each step, in order:
   - run its `check` first; if it already passes, `hearth recipe done <key>` and move on;
   - `manual` steps: do what `do` says, using the guide (download the build for this architecture, verify the checksum, install into the user's directories, never replace system packages); `interactive: true` (a QR-code or browser login) the user runs in their own terminal; wait for them;
   - `run` / `tool` / `service` / `check` steps: run the `commands` yourself after the yes (or `hearth recipe run <key>`, which runs it with Hearth's installer and marks it done);
   - run the `check` again; when it passes, `hearth recipe done <key>`; when it fails, read the error, fix it (a proxy or mirror from the organisation's notes, another architecture's package), never mark a failing step done.
4. When this machine needed something the flow does not say (another proxy, an extra package, a different directory, a step for ARM or Mac), record it so the next machine gets it: `propose_setup_step` (MCP) or `hearth learn --here-step '$ <command>'` (only machines like this) / `--step` (every machine); the user reviews it in `hearth recipe proposals`.
5. Finish with the guide's final check (section 7) and report what was installed, what was skipped and anything left for the user. `hearth recipe plan` should say `nothing left to do`.

## C. Daily use (logged in)

Settings — skills location/resident list, enabled agents, upload selection, tool versions, MCP servers, scanned workspaces — all live in one synced file: `hearth config` shows it, `hearth config --edit` opens it, `hearth lib skills-dir <path>` changes the skills location. Never hand the user a pile of separate files.

When the user only invoked the skill without a concrete request, give a short overview in their language with examples they can say, e.g. "恢复上一台机器的会话", "找一下压测相关的对话", "不要上传 Claude 的会话", "把 Claude 的会话导入 Codex", "记录这台机器的配置", "建个团队共享 skills", and show the newest few conversations (`hearth session list -n 5`).

### Find and restore conversations

Listed newest activity first; the index (title, rounds, last update, machine, size, status) is separate from the content, so listing is fast and downloads nothing.

```bash
hearth session list                       # newest 20 + total; -n 0 = all
hearth session find <words>               # title, path, working directory, machine
hearth session list --host <machine> --status active --json
hearth session list --files                # also backed-up files and memory notes
hearth session show <id>
hearth session restore <id> [<id> ...]    # any unique id prefix
hearth restore -i                         # the user picks from a numbered list in their own terminal
hearth restore --last 20                  # the newest N
```

- Show a short table (id prefix, agent, title, rounds, updated, machine) and restore what the user picks; the default bulk restore is the newest 10 of machines that are offline, ask before `--all`.
- `●` after a machine name: that machine is still online (working on it), so the default restore leaves its conversations there. Restoring one by id works; `--include-online` takes all of them. Mention it when the user expected a conversation that was left out.
- A Codex thread spread over several files is restored as one conversation, with any fork parent it needs.
- Codex Desktop running: the restored thread appears in the open window at once (as a copy; the original is archived so a restart does not show it twice). Otherwise Codex lists it when it starts; if the user cannot find it, tell them to search for the title.
- `expired`: deleted in the agent or unused for long; bulk restore skips it, restoring it by id brings it back.
- `session is leased by another device`: another machine is writing it; `hearth session take <id>` if that machine is gone.

### Choose what is uploaded

`[backup.categories]` in `library/config.toml` (Hearth home, synced) lists everything uploadable — codex/claude/comate sessions and memory, workspace, credentials — each true/false; `include` adds paths, `exclude` skips paths. All other settings (skills dir/resident, agents, tools, MCP, workspaces) are sections of the same file — `hearth config` shows it.

```bash
hearth backup status                       # what this machine uploads, per category
hearth backup disable claude               # nothing of Claude Code
hearth backup disable claude.sessions      # Claude Code memory yes, conversations no
hearth backup enable codex.memory
hearth backup disable credentials          # never offer or upload SSH keys / logins
hearth backup add ~/.gitconfig '${WORKSPACE}/notes/'
hearth backup exclude '~/.ssh/id_*_old'
hearth backup show | edit
```

Categories: `@<agent>`, `@<agent>.sessions`, `@<agent>.memory` (codex, claude, comate and agents added later), `@workspace`, `@credentials`. Turning something off stops future uploads; data already on the server stays (`hearth session rm <id>`, `hearth secret rm <name>`).

### Learn this machine's setup

The background sync records new findings by itself (every 20 min and at the end of setup): skills / agents found in the agents, env vars, CLI versions, MCP servers, workspace files, and credentials when `[learn] auto_credentials` is on (setup asks once; `hearth learn --auto-credentials=true|false`). Shell lines (the user's own part of `~/.bashrc` / `~/.zshrc`, `~/.bash_aliases`) are kept encrypted per operating system (Linux lines never reach a Mac) and sourced on new machines; lines that set `HOME` / `USER` are never applied, and lines naming paths or tools this machine lacks are guarded. `hearth shell restore --dry-run` shows what would be written, `hearth shell uninstall` takes Hearth's lines out of this machine's rc files (they stay out until `hearth shell restore`), `[shell] sync = false` stops it everywhere. What is left for the user: items whose copy here differs from the stored one, and credentials while not automatic; `hearth status` counts them (`next` step `learn`). Only those need the steps below.

```bash
hearth learn --list --json                 # (in the user's own terminal, plain `hearth learn` shows one checklist with proposals; Enter saves)
```

Show the `new` and `changed` findings as a compact table with a recommendation each: `all` (every machine: SSH keys, git identity, agent logins, CLIs), `here` (only machines like this, e.g. `per_machine: true`, GPU setup), `never`, or skip. Ask "anything else you always do on a new machine?". Then one call:

```bash
hearth learn --accept id1,id2 --here id3 --never id4 --step '$ pip install -U uv' --step 'log in to the VPN'
```

### Teams: share skills and agents

A team shares skills and agents among its members; a team's owner can also let another team use them (read-only). Teams the user is in (or can read) are skill sources automatically: the skill-search MCP finds their skills, no setup per machine.

```bash
hearth team list                                   # teams, role, how many skills/agents each shares
hearth team create <name>                          # the user becomes owner
hearth team invite <name> [--role member|maintainer] [--days 7] [--uses N]   # prints a code; give it to colleagues
hearth team join <code>                            # or on a new machine: hearth setup --team <code>
hearth team share <name> <skill|agent> ...         # publish from the user's library (secret scan first; maintainers)
hearth team packages <name>                        # what a team shares
hearth team share-with <name> <other-team> [--off] # owners: let another team use this team's items
hearth lib link <team>:<skill>                     # keep a team skill in every agent (otherwise found on demand)
hearth team use-top [--dry-run]                    # keep each team's most used items in every agent now (automatic)
hearth team leave <name>
```

- Automatic sharing: the user's 5 most used skills and 5 most used agents of their own library (all machines, last 30 days) are shared with every team where they are maintainer or owner, and republished when they change (the daemon, every 20 min; `hearth team share-top [--dry-run]` now). Tell the user when they join or create a team; `[teams] share_top = 0` turns it off, `share_exclude` keeps items private. Items blocked by the secret scan are not shared.
- Team items get invoked: each team's 20 most used skills and 20 most used agents (by its members, last 30 days) are linked automatically on every machine of a member, so they sit in the agents' own skill and agent lists like the user's own (`[teams] use_top`, `use_exclude`; unlinking one keeps it out). The user's own copy of the same name wins. The other team skills are found by the `hearth` MCP tools `search_skills` / `list_skills` and loaded by `get_skill`, and their use counts toward the team ranking. If the user asks why a team skill was not used: check `hearth team use-top --dry-run` and that skill search is registered (`hearth status`, "skill search on").
- Invite codes default to `maintainer` (can share); use `--role member` for use-only. Ask before sharing private skills; the secret scan blocks obvious credentials (`--allow-secrets` only after the user checked).

### Organisation defaults

The server may publish recipes for everyone (internal tool setup flows, proxies, SSO token locations) and notes for the guide (`hearth docs env` prints them): `hearth site status` lists them, `hearth recipe plan` merges them with the user's own recipes (you go through them in D), `hearth site disable <recipe>` opts the user out.

### Agents

```bash
hearth agent list                                   # what each integration supports
hearth agents import codex --from claude [file|id]   # Claude Code -> Codex threads (Codex's own importer)
hearth agents enable|disable <agent>                # which agents this account integrates
hearth agents pull <agent>                          # restore an agent setup skipped here (not used recently): skills, newest conversations; its CLI: D
```

| | Codex | Claude Code | Comate |
|---|---|---|---|
| sync, index, restore conversations and memory | yes | yes | yes |
| paths rewritten between machines | yes | yes | no |
| skills / agents / instructions rendered | yes | yes | skills only |
| shown in the open app after restore | Desktop | no | no |
| deletion in the app expires the server copy | yes | no | no |
| import other agents' sessions | from Claude Code | no | no |

### Long-term memory

Every conversation of the user's machines and agents has a small card (what was asked, the outcome, later requests, errors, commands, files) in a local index, with the facts they asked to remember and the agents' own notes. The `hearth` MCP server has `recall` (search, local, milliseconds), `remember` and `forget`; Codex and Claude Code also get relevant memories added to each prompt by a hook, and the remembered facts at session start.

- Use `recall` when you hit an error, start on an unfamiliar repository, or the user refers to earlier work ("上次", "like last time", "那个 NCCL 的问题"). Say where a memory came from (date, machine) and verify it before relying on it.
- When the user says "remember …" / "记住……", or agrees to a habit you noticed (they corrected you twice the same way), call `remember` with one self-contained sentence; `scope` = the repository root when it only holds there. Never store credentials.
- CLI: `hearth recall <words>` (`--show <id>`), `hearth memory remember|forget|list|index|status`, `hearth memory hooks on|off`. Codex asks once to trust a new hook: tell the user to choose "Trust all and continue". `[memory] enabled = false` turns it all off.

### Discover and restore a newly online machine

A machine becomes visible after its Hearth daemon sends its first heartbeat. The heartbeat includes only public readiness data: OS/architecture/GPU, active profile/workspace, Hearth version, installed agent versions and MCP readiness, and pending restore steps. It never includes credentials, session content or file contents.

```bash
hearth machine list                 # all logged-in machines, architecture and environment check (missing / outdated tools, failed checks, steps left)
hearth machine inspect <machine>    # full public health report
hearth ssh <machine> -- hearth onboard status --json
hearth ssh <machine> -- hearth recipe plan --json
```

Steps that need the user's own terminal (`interactive: true`: a QR-code login, a prompt) can run from here with a terminal: `hearth ssh -t <machine> -- '<command>'`; tell the user to run it in their own terminal, and wait. A step is done only when its check passes on the target (for codex-mixin: `codex-mixin doctor --no-tui` says `doctor: ok`, not just "imported").

When the user says “恢复这台机器的 Codex 环境” or “恢复 Claude Code 环境”, first use `hearth machine list` and inspect the target. Choose its matching profile from OS, architecture, GPU and labels; then use the target's SSH connection to run the restore flow there. On the target, show the pending recipe steps and follow **D. Restore this machine's environment** in order, checking each step and recording it with `hearth recipe done <key>`. Use `hearth agents enable <agent>` and `hearth agents pull <agent>` only for the requested agent. Finish with `hearth onboard status --json`, `hearth machine check` (on the target: reports its environment at once instead of within 5 minutes) and `hearth machine inspect <machine>`; the target should report `ready`, or clearly list the remaining issue.

### SSH between the user's machines

Logged-in Linux machines accept SSH from the user's own devices by default: the keys of their active devices (each device's Hearth key and its `~/.ssh/id_*.pub`) go into a marked block of `~/.ssh/authorized_keys`, and the daemon keeps the system sshd running the usual GPU-container way (public keys only, main interface, port 8022; an sshd already listening there is used as it is).

- "我能 ssh 到哪些机器" / "连上那台机器": `hearth ssh` lists them with the command; `hearth ssh <machine> [-- cmd]` logs in with host-key checking.
- From a Mac or laptop: install and log in Hearth there. On a Mac Hearth keeps a Host entry per machine in `~/.ssh/config` by itself (with host keys; elsewhere once `hearth ssh config` was run, `[ssh] config`), named `[family]-[host]-[hardware]-[node]`, e.g. `gb-johor-edge-gpu-e05-13` (`[ssh.aliases]` to rename one). `ssh <host>` works; Codex Desktop lists them under Connections > SSH, where the user adds each one once; VS Code / Cursor Remote-SSH list them too. `hearth ssh` shows the names; `onboard status` says when entries are missing.
- `hearth ssh status` (this machine), `hearth ssh keys`, `hearth ssh authorize <pubkey|file>` for a key that is not a Hearth device; `hearth ssh disable` (this machine; `--all`: every machine). A revoked device (`hearth device revoke`) loses access within a minute.

### Status and problems

```bash
hearth recipe plan        # what this machine still needs to set up (D); hearth recipe done <key> when a step is done
hearth learn --later      # stop the "not recorded yet" reminder for 30 days
hearth status        # one screen: login, background sync (last check, last upload, last error), storage, agents, library, next steps
hearth status --json # the same for you, with the daemon heartbeat
```

- Background sync: sessions every 60 s, library every 20 min; `hearth daemon start` if it is not running.
- `certificate signed by unknown authority`: self-signed server and no copy of its certificate here; re-run the admin's install command with `--install-only` or pass `--ca-cert <file>` to `hearth setup`.
- `403 account disabled` / `507 quota`: only the server admin can fix it.
- Sessions that ran outside the workspace are not uploaded on shared accounts: `hearth session scope` (add only the user's own directories).
- New version on the server: the daemon installs it by itself within 30 minutes; right away: `hearth self-update` (also refreshes this skill). `hearth self-update --auto off|on|status` controls the automatic update on this machine.
