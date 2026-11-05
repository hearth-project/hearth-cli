# Usage

[中文](../zh/usage.md) · [Back to README](../../README.md)

Every command takes `--help`; the full list is in the [CLI reference](../cli.md). You can also just tell your agent "use hearth" and the `hearth` skill runs these for you.

## 1. Status

```bash
hearth status                 # one screen: login, background sync (last check / upload / error), storage, agents, library, next steps
hearth status --json          # the same for scripts and agents
hearth onboard status         # full checklist and next steps (--json for agents)
```

`hearth setup` starts background sync; `hearth daemon start | stop` by hand. Intervals: sessions 60 s, library (skills, agents, settings) 20 min. After a container restart run `hearth up`; before a graceful stop run `hearth flush` (a good k8s preStop hook).

## 2. Conversations: find and restore

Content and index are stored separately. The index holds title, rounds (user requests), last update, last writing machine, size and status; listing and searching read only the index and download no content.

```bash
hearth session list                         # newest 20 and the total, newest activity first; -n 0 = all
hearth session list --agent codex --host gpu-01 --status active
hearth session list --files                 # also backed-up files and memory notes
hearth session find benchmark               # title / path / working directory / machine
hearth session show <id>                    # one entry in full
hearth session list --json                  # for scripts
```

Restore:

```bash
hearth session restore <id> [<id> …]        # one or several; any unique id prefix
hearth restore                              # in a terminal: numbered list, pick 1 3 5-7, Enter = newest 10
hearth restore -y                           # no question: the newest 10 (the default when not in a terminal)
hearth restore --include-online             # also conversations of machines that are still online
hearth restore --last 30                    # the newest 30
hearth restore --all                        # everything (use with care)
```

- The default restore takes the 10 most recently active conversations of machines that are **offline**: conversations last written on a machine that is still online (its background sync heartbeats every minute; about 15 minutes of silence, or a graceful stop, makes it offline) stay there, since that machine is working on them; lists mark such machines with `●`. Naming the id, picking it by number in the list, or `--include-online` restores them anyway. `hearth setup` in a terminal lists them and asks once whether to bring them too (Enter = yes).
- A Codex conversation can span several files (continuations, subagents); they are restored together, and a forked thread brings its parent along.
- When home or workspace differ between machines, absolute paths inside the session are rewritten for this machine.
- With Codex Desktop running, restored threads appear in the open window at once (as a copy; the original is archived so a restart does not show it twice). Without Desktop running, Codex lists them on its next start. If you cannot find one, search for its title in Desktop.
- `expired`: deleted in the agent, or not accessed for 30 days (server setting). Bulk restore skips these; restoring by id revives them.
- Restored sessions keep syncing from this machine. `session is leased by another device` means another machine is writing it; if that machine is gone, `hearth session take <id>`.

More: `session pin | unpin` (never expire), `session touch` (refresh access time), `session rm` (delete the server copy), `session handoff <id>` (an agent-neutral Markdown hand-off to continue in any agent).

## 3. Settings: one file

Everything configurable lives in `<hearth home>/library/config.toml`, synced with the library to every machine. The default home is `~/.hearth`; with a workspace it is `<workspace>/.hearth`. Show or edit it:

```bash
hearth config                # print path and contents
hearth config --edit         # open it in $EDITOR
```

Every section is optional:

| Section | Purpose |
|---|---|
| `[skills]` | `dir` — where the one copy of the skills lives; `resident` — skills rendered into every agent (the rest are found on demand) |
| `[agents]` | `enabled` agents; `[agents.homes]` per-agent config dirs |
| `[backup]` | `include` / `exclude` — what is uploaded |
| `[learn]` | `ignore` — findings never to ask about again |
| `[tools]` | `[[tools.pin]]` agent CLI versions; `[[tools.custom]]` check/install commands |
| `[mcp]` | `[mcp.servers.<name>]` MCP servers shared by the agents |
| `[usage]` | `share_with` teams that get this machine's skill usage counts (opt-in) |

The files older versions split this across (`agents.json`, `backup.conf`, `tools.json`, `resident.txt`, `learn-ignore.json`, `workspaces.json`, `mcp.toml`) are folded into `config.toml` and removed on first run. Machine-local state — the login (`config.json`), the auto-update switch and which agents this machine restored — is deliberately not in it.

### What is uploaded

The file lists **everything Hearth can upload**, each switchable on its own:

```toml
[backup.categories]
"codex" = true             # Codex sessions and memory
"codex.sessions" = true
"codex.memory" = true
"claude" = true
"claude.sessions" = false  # no Claude Code conversations
"claude.memory" = true
"comate" = true
"comate.sessions" = true
"comate.memory" = true
"workspace" = true         # personal files inside the workspace
"credentials" = true       # SSH keys, git / cloud logins

[backup]
include = ["${WORKSPACE}/notes/", "~/.gitconfig"]   # extra files/dirs
exclude = ["**/*.tmp", "~/.ssh/id_rsa"]             # path exclusions win
```

- Extra paths go in `include`; concrete files to skip go in `exclude`.
- Paths accept `~/`, `${WORKSPACE}/`, `*`, `**`, `?`; a trailing `/` means a directory.
- Removing a line means the default (uploaded); agents added in future versions are uploaded by default.
- Individual credential files (one SSH key, one token) are listed by `hearth secret list` and managed with `hearth secret add/rm`; they follow the `credentials` switch.

```bash
hearth backup status                         # files and size uploaded from this machine, per category
hearth backup disable claude.sessions        # adds @claude.sessions to exclude
hearth backup enable codex
hearth backup add '${WORKSPACE}/notes/'
hearth backup exclude '**/*.tmp'
```

## 4. Credentials and auth files

Credentials are end-to-end encrypted secrets; `setup` writes them back to their place on new machines (mode 0600).

```bash
hearth secret list                                     # names only
hearth secret set GITHUB_TOKEN                         # hidden prompt
hearth secret set file-ku --file .config/ku/config.yaml --restore-to '${WORKSPACE}/.config/ku/config.yaml'
hearth secret restore                                  # write every file secret back now
hearth secret exec -- <command>                        # UPPER_CASE secrets as environment variables
```

When a skill or script needs a token, do not put the value in the skill: keep it in a local auth file (e.g. `${WORKSPACE}/.config/<tool>/…`), back that file up with `secret set --file`, and have the skill read the file. On a machine that restores on demand (every new one), credentials come when they are needed: your agent calls the `hearth` MCP tool `prepare_credentials` for the skill it is about to run, or you run `hearth secret use <skill|name>` (files written 0600, removed after `--ttl`, default 12 hours, or now with `hearth secret clean`; never uploaded from that machine), and `hearth secret exec [--for <skill>] -- <command>` puts env secrets (and, with `--for`, files) in place for one command only. The values never pass through the agent. A credential file a skill ships with (a cloud CLI's `credentials`, a key, a file only you may read) is kept out of the plaintext library: it syncs end-to-end encrypted and comes back into the skill on your other machines. Likely secrets elsewhere in the library (config, recipes, agents) still block the upload; mark documented examples with a `hearth:allow-secret` comment.

## 5. Recording this machine's setup (learn)

Most of it happens by itself: the background sync (every 20 minutes, and `hearth setup` at its end) records new findings with the proposal `hearth learn` would make (items that look machine-specific only for machines like this one):

- skills / agents added to the agents go into the library (not the ones that look like they hold a credential);
- settings: environment variables, CLI versions, MCP servers, personal files of the workspace;
- credentials (SSH keys, agent / git logins, token files): setup asks once whether to keep them automatically (end-to-end encrypted); change it with `hearth learn --auto-credentials=true|false`;
- shell: your own part of `~/.bashrc` / `~/.zshrc` (without the distribution's default, blocks managed by tools such as conda, and Hearth's own blocks) and the whole `~/.bash_aliases`, encrypted. A new machine gets them under `<hearth home>/shell/`, sourced from a managed block in its rc file; its own rc is not replaced, paragraphs it already has are not repeated, and lines sourcing files it does not have are skipped. Changes from several machines are merged; a paragraph deleted on one machine is deleted from the stored copy. `hearth shell show` prints what is kept, `hearth shell save` / `restore` run it now.

Two kinds of items need you: recorded items whose copy here differs from the stored one (the other machine's may be right), and credentials while automatic recording of them is off. `hearth status` counts them; `hearth learn` handles them. Off: `[learn] auto = false`; shell off: `[shell] sync = false` in config.toml.

```bash
hearth learn                      # interactive: one checklist with a proposal per item (all machines / machines like this), Enter saves; --one-by-one asks per item
hearth learn --list --json        # found credentials, configs, tools, environment variables
hearth learn --accept id1,id2 --here id3 --never id4 \
  --step '$ pip install -U uv' --here-step '$ export NCCL_IB_HCA=mlx5' --step 'log in to the VPN'
```

- `accept`: restore on every machine (SSH keys, git identity, agent logins, CLIs).
- `here`: only on machines like this one (a machine profile such as `h800` or `mac` is created from GPU / OS).
- `never`: do not ask again.
- `--step`: a command (run on new machines after approval) or a reminder.

On a new machine `hearth setup` restores only their files (credentials, configs) and environment variables; installing software, running commands and QR-code logins make up this machine's **restore flow** (`hearth recipe plan`), which an agent goes through with the hearth skill: preferably from another machine with Hearth ("restore the Codex environment of <machine>", over `hearth ssh`; `hearth machine list` shows each machine's environment check), or on the machine itself ("use hearth to restore this machine's environment"): it runs each step's check, installs following the [client software setup guide](client-env.md) (`hearth docs env`), checks again and marks it with `hearth recipe done <key>`. When this machine needed another way, the agent records it back into the flow (`--here-step` for machines like this one). In a terminal: `hearth recipe run <key>`, or all at once `hearth recipe apply` (lists the commands, asks once). A command approved on any of your machines runs on the others without asking; the approvals are end-to-end encrypted, so the server cannot forge them, and a changed command is asked about again. `[recipes] share_approvals = false` in config.toml approves per machine instead. Machine profiles: `hearth machine` (show), `hearth machine add <name> --here`, `hearth machine use <name>`.

### QR-code logins in the chat

A login that shows a QR code in the terminal (for example `codex-mixin setup` through DUCX) is a `manual` step with `tty = true`. Give it the command in `run` and the agent runs it for you without leaving the chat: Hearth starts it under a pseudo-terminal in the background on the target machine (over `hearth ssh`), reads the QR code from the output as a PNG, the agent shows it, you scan it, and when the command ends the step's check runs and the step is marked done. Environment comes from `set`, where `${secret:NAME}` is one of your Hearth secrets; secret values are replaced by `********` in the output.

```toml
[[step]]
type = "manual"
name = "codex-mixin-login"
text = "log in to codex-mixin (QR code)"
tty = true
run = 'PATH="$HOME/.local/bin:$PATH"; codex-mixin setup --no-tui --preset baidu-oneapi --key "$ONEAPI_KEY" --quota-username me --codex-mode custom'
set = { ONEAPI_KEY = "${secret:ONEAPI_KEY}", https_proxy = "http://proxy.example.com:8080" }
done_when = 'PATH="$HOME/.local/bin:$PATH"; codex-mixin doctor --no-tui | grep -q "doctor: ok"'
```

Agents use the `hearth` MCP tools `run_interactive_step`, `poll_interactive_step`, `answer_interactive_step` and `stop_interactive_step`; in a terminal: `hearth recipe tty start <key> --on <machine>` (prints the PNG's path), `hearth recipe tty status <session> --on <machine>`. Only plan steps of this kind can be started, the code images stay in a temporary directory of the machine you work on (mode 0600) and are removed when the step ends, and nothing of the session is uploaded. The target machine runs Linux and needs this version of Hearth.

### Organisation defaults

Admins can publish recipes for everyone on the server (internal tool installs, proxies, where the SSO token lives); `setup` fetches them and runs them with your own:

```bash
hearth site status                   # site defaults on this machine
hearth site pull                     # refresh now
hearth site disable <recipe>         # do not apply a site recipe for me (synced)
```

## 6. Skills, agents and teams

```bash
hearth lib import codex               # copy an agent's (codex / claude / comate) skills and agents into the library
hearth lib status | push | pull | sync
hearth lib skills-dir                 # where the one copy of the skills lives (default <library>/skills)
hearth lib skills-dir '${WORKSPACE}/.agents/skills'   # move it: moves the skills and rewrites the agent links
hearth lib mcp-install                # re-register the skill-search MCP (setup does it; loads skills on demand)
hearth lib search <words>
hearth lib stats --server             # skill ranking across all machines of this account (uploads this machine first)
hearth lib resident --top 5           # make the server's top 5 skills resident
hearth lib resident --top 5 --days 7  # rank the last 7 days
hearth lib usage prune                # delete counts of deleted / renamed / test skills (lists them, asks; --dry-run to look)
hearth team create <team>                     # you become owner
hearth team invite <team>                     # an invite code (default maintainer = can share; --role member = use only; --days 7, --uses N)
hearth team join <code>                       # join; on a new machine also: hearth setup --team <code>
hearth team share <team> <skill|agent> ...    # share from your library (credential files of a skill stay with you)
hearth team unshare <team> <name> ...         # remove from the team, every version
hearth team share-with <team> <other-team>    # owners: let another team use them (read-only; --off to stop)
hearth team list                              # your teams, role, how many skills / agents each shares
hearth team share-top [--dry-run]             # share your 5 most used skills and 5 most used agents now (the daemon does it every 20 min)
hearth team use-top [--dry-run]               # keep each team's 20 most used skills and 20 most used agents in all your agents now (automatic)
hearth lib publish skill <name> --team <team>
hearth lib source add git <name> <git-url>    # teams you are in or can read are sources automatically
hearth lib find <words>; hearth lib link <skill>
```

The library is stored in plaintext on the server (visible to you only; team publications to the team), so it must not contain credentials.

How team items get invoked: each team's 20 most used skills and 20 most used agents (by its members, last 30 days) are linked into every agent on all your machines, so the agents list and invoke them like your own (`[teams] use_top`, `use_exclude`; unlinking one keeps it out). Your own item of the same name wins. The other team skills are found through `hearth mcp` (`search_skills`, `list_skills`) and loaded by `get_skill`; that use counts toward the team ranking, so a skill that gets used becomes resident.

## 7. Agents

```bash
hearth agent list                                    # capabilities per agent
hearth agents enable claude | disable comate         # which agents this account integrates
hearth agents pull claude                           # a new machine restores only recently used agents; pull others when needed
hearth agent import codex --from claude [file|id]   # Claude Code sessions into Codex (Codex's own importer)
hearth codex-threads                                 # re-apply Codex thread names and sections
hearth onboard install-skill                         # install / update the hearth skill for this machine's agents
```

## 8. Account and devices

```bash
hearth device list | revoke <id>       # a lost device
hearth passwd                          # change the master password
hearth recover                         # forgot it: reset with the recovery key (all devices log in again)
hearth recovery-key rotate             # the recovery key was exposed
```

Any device that logs in with the master password can decrypt every credential (no approval by an existing device), so the master password is their only protection: use a strong one, and after a suspected leak run `device revoke`, `passwd` and rotate the affected credentials.

### Copying files between machines

```bash
hearth cp ./ckpt gpu-a:/data/            # push: /data/ckpt on gpu-a
hearth cp gpu-a:/data/ckpt .              # pull
hearth cp gpu-a:/data/ckpt gpu-b:/data/   # straight from gpu-a to gpu-b
```

The control channel goes over Hearth's SSH; the data goes directly between the two machines over parallel TCP connections, encrypted with AES-256-GCM under a key that only went through SSH, never through the Hearth server. Large files travel in 4 MiB chunks over every connection at once (16 nearby, up to 64 across regions, `-j` to set) and are written in place; chunks of several files go at once, so writers do not queue on one file. An interrupted copy resumes when the same command runs again; files whose size and time match are skipped (`--force` copies them anyway, `-n` keeps any existing file). Across regions chunks are compressed with zstd where it helps (`--compress auto|on|off`). When neither machine can open a connection to the other, the data goes through SSH port forwards; when the other machine's path is on a disk this one mounts too, it is copied there directly. Between two other machines the copy runs on the source, or, when the source cannot reach the target, on the target.

Measured (memory to memory, 16 files of 2 GiB): 9.7 GiB/s between two bzz2 machines (400 Gb/s), 1.4 GiB/s from yq01 to johor (76 ms, 64 connections); one large file to a local disk goes at about the disk's speed (O_DIRECT, 3 GiB/s here).

### SSH between your machines

Logged-in Linux machines accept SSH from your own devices only: public keys only (no passwords), the keys of your active devices, the host keys checked from the end-to-end encrypted endpoints (no "trust this host?"). `hearth ssh <machine>` and the `~/.ssh/config` entries Hearth writes on a Mac connect with this device's Hearth key.

- **Keys expire unless renewed.** Each machine accepts a device key for 7 days (`[ssh] key_days`, `0` = no expiry; OpenSSH 8.2 or newer) and its daemon renews that every day. A revoked device loses access at the next daemon pass, and at the latest after 7 days on a machine whose daemon is down.
- **Agent forwarding** (`[ssh] forward_agent`, default on): sessions forward this device's ssh-agent, so `git push` on the machine uses the keys loaded here (`ssh-add`, on a Mac `ssh-add --apple-use-keychain`) and no private key needs to be stored there. While you are connected, root on that machine can use the forwarded agent (not copy the keys); turn it off for machines you do not trust.

## 9. Mac menu bar app

```bash
hearth app install      # download the app from your server into ~/Applications and start it (again to update)
hearth app open         # start it
hearth app uninstall    # quit and remove it
```

The app puts a flame in the menu bar (filled when a machine's environment has an error). Its menu lists your machines (machines offline for more than 12 hours are left out; the console still shows them): online or offline, when they were last seen, their GPU and SSH state, and whether their environment is fine. To copy a machine's name, hover over it and click the copy button, or right-click it. Click a machine for its problems and buttons: *Restore environment* and *Check* open Codex with a prompt for that machine (the prompt is also copied), *SSH* opens a terminal on it; an online machine can be renamed there (empty goes back to its host name, like `hearth device rename`). Below the list: *Open console* opens a native window with your overview (machines, daily uploads, most used skills and agents, team activity), machines, library (skills and agents) and teams; it reads them with `hearth app data --json` and needs no password. For an admin account the sidebar has an *Admin* group as well (overview, users, invitations, teams, server settings, maintenance, audit log): the pages read with `hearth app admin-data --json` and act through `hearth app admin-call` (the same `/v1/admin` API as the web console, so the same permissions). The sidebar entry *Admin (web)* still opens the web console in a window, already signed in and without asking for the master password; *Add machine* copies a one-time install command (`hearth machine install-command`, valid once for 30 minutes) without showing it; then *Refresh*, *Start at login* and *Quit*. When the login on this Mac has expired (device logins last 30 days) the menu lists nothing and offers *Log in again*, which opens Terminal on `hearth login`; the master password is typed there. The console window signs in again by itself when its page has fallen back to the login form (its web session lasts 12 hours), and *Reload* always uses a fresh link. In the native window, an expired login shows *Log in again* as well and the pages load by themselves once it works.

The app does everything through the `hearth` command (`hearth machine list --json`, `hearth panel --json`, `hearth machine install-command --json`, `hearth app data --json`, `hearth app admin-data --json`, `hearth app admin-call`, `hearth device rename`), so this Mac must be logged in; if the app says the command is too old, run `hearth self-update`. It is only on servers whose admin publishes it ([server guide](server.md#releasing-a-new-client-version)).
