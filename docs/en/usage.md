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

### QR-code logins

A login that shows a QR code in the terminal (for example `codex-mixin setup` through DUCX) is a `manual` step with `tty = true`. Give it the command in `run` and `hearth recipe login <key>` runs it in the foreground of your terminal, so the program draws the code itself; when the command succeeds the step's check runs and the step is marked done. Environment comes from `set`, where `${secret:NAME}` is one of your Hearth secrets, filled in on the machine that runs the step (never on a command line).

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

When an agent restores another machine and reaches such a step, it does not show a code in the chat: it runs `hearth recipe login <key> --on <machine>`, which checks that the machine can run the step and prints one command, and gives it to you:

```bash
hearth ssh -t gpu-a -- 'PATH="$HOME/.local/bin:$PATH"; exec hearth recipe login learned/5dc36cca78'
```

Run it in your own terminal, scan the code drawn there, and tell the agent; it checks the result. `hearth recipe login <key> --print` prints the command even in a terminal; without `--on` the step runs on this machine. Only plan steps of this kind can run (the target needs a Hearth that has `recipe login`: `hearth self-update`).

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

### Versions and updates of shared skills

A team package has versions: every `hearth lib publish` (or an automatic share of your most used items) that changes the content makes a new immutable version, v1, v2, …; publishing identical content makes none. The server keeps them all. Your own library has no per-skill history (it syncs as a whole between your machines).

```bash
hearth lib link bench                 # follows the team's newest version from now on
hearth lib link bench --pin           # keep this version
hearth lib link bench --version 2     # link version 2 and keep it (also how to go back to an older one)
hearth lib links [--check]            # version, mode (follows / pinned / automatic) and, with --check, the newest
hearth lib update [name] [--yes]      # newer versions with a file diff; --yes applies (also moves a pinned link)
hearth status                         # "updated in the last 7 days: bench v1 → v3 (2h ago)"
```

- **Following.** A link to a trusted team's package moves to the newest version by itself: the daemon checks every 20 minutes, switches the link, renders the new content into the agents, and records what changed (`~ SKILL.md`, `+ run.sh`) in its log and in `hearth status`. The team's most used items (see below) follow the same way. `[teams] follow_updates = false` turns it off; run `hearth lib link <name>` again to follow again after a pin.
- **Pinned.** `--pin` and `--version N` keep a link where it is until you move it with `hearth lib update --yes` or link again.
- **Git sources** are always pinned to a commit; a newer commit shows up in `hearth lib update`, and `--yes` applies it. Untrusted sources never move without a review.
- **Publishing.** A maintainer publishes a new version with `hearth lib publish skill <name> --team <team>`; your five most used skills and agents are republished automatically when they change (`share_top`). Others are published by hand.

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

### A name stands for a machine

A machine whose disk was reset (its container image restarted) logs in again as a new device. When it keeps its host name, `hearth setup` continues the old, offline entry without asking. A new machine continues an old one when it is given that machine's name: at setup's name prompt, with `--alias`, or later with `hearth device rename` (also the Mac app). Continuing means: the new machine has the old one's name (its SSH host name); the conversations the old one uploaded count as the new one's (`hearth restore --last 10` brings them back); when the old workspace is on this machine (cluster storage that survived), setup proposes it again and keeps the old Hearth home there (agent homes, library, state), only the new login moves in; the old entry is logged out and leaves the list. A machine that is online keeps its name.

### Copying files between machines

```bash
hearth cp ./ckpt gpu-a:/data/            # push: /data/ckpt on gpu-a
hearth cp gpu-a:/data/ckpt .              # pull
hearth cp gpu-a:/data/ckpt gpu-b:/data/   # straight from gpu-a to gpu-b
```

The control channel goes over Hearth's SSH; the data goes directly between the two machines over parallel TCP connections, encrypted with AES-256-GCM under a key that only went through SSH, never through the Hearth server. Large files travel in 4 MiB chunks over every connection at once (16 nearby, up to 64 across regions, `-j` to set) and are written in place; chunks of several files go at once, so writers do not queue on one file. An interrupted copy resumes when the same command runs again; files whose size and time match are skipped (`--force` copies them anyway, `-n` keeps any existing file). Across regions chunks are compressed with zstd where it helps (`--compress auto|on|off`). When neither machine can open a connection to the other, the data goes through SSH port forwards; when the other machine's path is on a disk this one mounts too, it is copied there directly. Between two other machines the copy runs on the source, or, when the source cannot reach the target, on the target.

Measured (memory to memory, 16 files of 2 GiB): 9.7 GiB/s between two bzz2 machines (400 Gb/s), 1.4 GiB/s from yq01 to johor (76 ms, 64 connections); one large file to a local disk goes at about the disk's speed (O_DIRECT, 3 GiB/s here).

### Copying large data between storages

`hearth cp` moves data between your machines. For a large directory (a model, a dataset) that lives on a shared file system and has to reach another cluster's file system, an organisation may run a DataTool-compatible transfer service: the storages copy to each other on their own side, the data never passes through this machine, and the task keeps running after you log out. `hearth transfer` talks to that service.

**The server admin sets it up once**; users configure nothing. Put `transfer.json` in the server's data directory (`<data>/transfer.json`, mode 0600; edits apply at once):

```json
{
  "url": "http://datatool.example.com",
  "token": "shared-service-token",
  "afs_ugi_secret": "afs_ugi",
  "paths": {
    "a@one": [
      {"from": "/mnt/personal", "to": "/data/{user}"},
      {"from": "/mnt",          "to": "/data"}
    ]
  }
}
```

Every logged-in client fetches it (`GET /v1/transfer/config`, login required, never part of the public site bundle), so the address, the token and the path rules are not in the open-source code or on the users' machines. `{user}` is the account name (the email without the domain); the first rule whose `from` is a prefix of the path wins. Optional keys: `api_prefix` (default `/v1/datatool/api`), `timeout`. The token is visible to every user who can log in to this server; it is the service's shared token, not a personal credential.

What stays personal is the AFS UGI, only needed for `afs` storages: `hearth secret set afs_ugi` stores it end-to-end encrypted (the name comes from `afs_ugi_secret`).

A user can override any of it under `[transfer]` in config.toml (`hearth config --edit`; nothing there is a secret, and `url` may not carry a password): `url`, `token_secret` (a Hearth secret) or `token_env` (an environment variable), `afs_ugi_secret`, `api_prefix`, `timeout`, `use_proxy = true` (requests go direct by default, the service is usually internal) and `[[transfer.paths."STORAGE"]]` rules, which replace the server's rules for that storage. The server's token is only ever sent to the server's own address: a user's `url` that differs gets no token unless the user sets one.

```bash
hearth transfer meta                                    # storages, allowed directions, limits
hearth transfer submit --from a@one:/mnt/personal/ckpt --to b@two:/data/alice/ckpt
hearth transfer list                                    # tasks submitted from this machine (--all: every task)
hearth transfer status dt-20261105-3dbb92fd             # state, bytes, speed, ETA, error
hearth transfer cancel dt-20261105-3dbb92fd
```

- **The path is the storage's, not this machine's.** `STORAGE:PATH` names a storage from `hearth transfer meta`. A path under a rule's `from` prefix is translated and the plan shows it; a URL (`afs://...`) and any other absolute path go as given. A relative path is accepted only when a rule translates it, and `..` is refused.
- **Plan first.** `submit` checks the request against the service's directions and limits, prints the plan, and asks; without a terminal it needs `--yes`. **The destination directory must be empty**; the service cannot check that for you. `--dry-run` asks the service for a dry run, `--transfers` and `--checkers` set the parallelism up to its limits.
- **Credentials never appear in output.** The AFS UGI goes only into the create request: not in the plan, the task record or errors.
- **Whose tasks.** The service does not tell users apart (everyone is the same creator), so `list` shows the tasks this machine submitted, recorded in `transfers.json` next to the Hearth state without any credential, and `cancel` refuses other ids unless `--force`: it could stop a colleague's copy.
- **Not encrypted or versioned by Hearth.** Use it for bulk data between storages. Personal state, skills, small files and anything that must stay end-to-end encrypted go through the normal sync or `hearth cp`.

### SSH between your machines

Logged-in Linux machines accept SSH from your own devices only: public keys only (no passwords), the keys of your active devices, the host keys checked from the end-to-end encrypted endpoints (no "trust this host?"). `hearth ssh <machine>` and the `~/.ssh/config` entries Hearth writes on a Mac connect with this device's Hearth key.

- **Keys expire unless renewed.** Each machine accepts a device key for 7 days (`[ssh] key_days`, `0` = no expiry; OpenSSH 8.2 or newer) and its daemon renews that every day. A revoked device loses access at the next daemon pass, and at the latest after 7 days on a machine whose daemon is down.
- **Agent forwarding** (`[ssh] forward_agent`, default on): sessions forward this device's ssh-agent, so `git push` on the machine uses the keys loaded here (`ssh-add`, on a Mac `ssh-add --apple-use-keychain`) and no private key needs to be stored there. While you are connected, root on that machine can use the forwarded agent (not copy the keys); turn it off for machines you do not trust.
- **Machines nobody can connect to (the relay).** A machine in a container on a private network, or behind a firewall, still accepts SSH from your other machines: its daemon keeps one outbound connection to the Hearth server (TLS, like everything else it does), and when another machine asks for it the server joins the two. No port needs to be open or mapped. What travels is the SSH connection itself, so the server relays ciphertext it cannot read, and ssh still checks the host key the machine published. `hearth ssh`, the `~/.ssh/config` entries (for ssh, scp, rsync, VS Code / Cursor Remote-SSH, Codex Desktop) and `hearth cp` try the direct address first and use the relay only when it does not answer. A `~/.ssh/config` entry gets a `ProxyCommand` line only for a machine that does not answer from here but is connected to the relay; every other entry is written as before, and the block follows reachability on each sync. Port forwards (`-L`, `-R`, `-D`) and agent forwarding work through it too. `[ssh] relay = false` turns it off on a machine (it then neither joins nor uses it).
  - It is the server's bandwidth: bulk data between machines that can reach each other should go direct (`hearth cp` does), between clusters use `hearth transfer`. At most 64 relayed connections per account at a time.
  - The relay lives in the memory of one server process: with several server instances behind a load balancer, machines and clients have to reach the same one.
  - Needs the machine's own sshd (Hearth starts it, listening on any address, even `127.0.0.1` with `[ssh] address`) and a newer Hearth on both sides.

## 9. Mac menu bar app

```bash
hearth app install      # download the app from your server into ~/Applications and start it (again to update)
hearth app open         # start it
hearth app uninstall    # quit and remove it
```

On a Mac, `hearth setup` (and so the install one-liner) installs the app as well, when the server has one to hand out, so one command gives both the command line and the app (`--no-app` or `HEARTH_NO_APP=1` skips it). Where the app is already installed, `hearth app install` updates it.

The app puts a flame in the menu bar (filled when a machine's environment has an error). Its menu lists your machines (machines offline for more than 12 hours are left out; the window still shows them): online or offline, when they were last seen, their GPU and SSH state, and whether their environment is fine. To copy a machine's name, hover over it and click the copy button, or right-click it. Click a machine for its problems and buttons: *Restore environment* and *Check* open Codex with a prompt for that machine (the prompt is also copied), *SSH* opens a terminal on it; an online machine can be renamed there (empty goes back to its host name, like `hearth device rename`). Below the list: *Open console* opens a native window with your overview (machines, daily uploads, most used skills and agents, team activity, and a note when changes wait for your machines), machines (IP, version, sessions; rename or remove a machine), library (skills and agents: open one to see its content and files, make it resident or on demand, share it to teams you can publish to), teams (invitation codes, leave a team, package versions and keep/unlink) and *My settings* (resident skills, auto-share top N, links, and queued changes you can cancel); it reads them with `hearth app data --json`, changes them through `hearth app call` on the `/v1/me` API and needs no password. For an admin account the sidebar has an *Admin* group as well (overview, users with approve/approve all, their machines, quota and admin, invitations, teams with adding members and roles, server settings, maintenance, audit log): the pages read with `hearth app admin-data --json` and act through `hearth app admin-call` (the `/v1/admin` API; only admin accounts can use it). *Add machine* copies a one-time install command (`hearth machine install-command`, valid once for 30 minutes) without showing it; then *Refresh*, *Start at login* and *Quit*. When the login on this Mac has expired (device logins last 30 days) the menu lists nothing and offers *Log in again*, which opens Terminal on `hearth login`; the master password is typed there. In the window, an expired login shows *Log in again* as well and the pages load by themselves once it works.

Removing a machine: on the window's *Machines* page, right-click a machine and choose *Remove this machine…*, or expand it and click *Remove…* (this Mac itself cannot be removed there). The server logs the machine out, and its Hearth then gets "device removed" (HTTP 410, distinct from the 401 of an expired login). When the machine is online, its daemon cleans up and exits within about 30 seconds: it stops syncing, stops the sshd Hearth started, takes out the memory hooks, shell block, identity script, the Hearth section of `~/.ssh/config` and the keys Hearth manages in `authorized_keys`, removes the skills, agents and MCP entries it rendered into agent directories (files changed since are kept and listed), and deletes the login, keys, memory, library cache and state files in the Hearth directory. Only what Hearth wrote and still matches its record is removed; other tools' files are left alone, and `backup/` in the Hearth directory (the user's files from before Hearth took them over) is kept. A machine that is offline does the same the next time it comes online. To use it again, run `hearth setup` on it.

The app does everything through the `hearth` command (`hearth machine list --json`, `hearth machine install-command --json`, `hearth app data --json`, `hearth app admin-data --json`, `hearth app call`, `hearth app admin-call`, `hearth device rename`), so this Mac must be logged in; if the app says the command is too old, run `hearth self-update`. It is only on servers whose admin publishes it ([server guide](server.md#releasing-a-new-client-version)).
