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

When a skill or script needs a token, do not put the value in the skill: keep it in a local auth file (e.g. `${WORKSPACE}/.config/<tool>/…`), back that file up with `secret set --file`, and have the skill read the file. The library is scanned for likely secrets before upload and refused if any are found; mark documented examples with a `hearth:allow-secret` comment.

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

On a new machine `hearth setup` restores only their files (credentials, configs) and environment variables; installing software, running commands and QR-code logins make up this machine's **restore flow** (`hearth recipe plan`), which the agent goes through with you using the hearth skill (ask "use hearth to restore this machine's environment"): it runs each step's check, installs following the [client software setup guide](client-env.md) (`hearth docs env`), checks again and marks it with `hearth recipe done <key>`. When this machine needed another way, the agent records it back into the flow (`--here-step` for machines like this one). In a terminal: `hearth recipe run <key>`, or all at once `hearth recipe apply` (lists the commands, asks once). A command approved on any of your machines runs on the others without asking; the approvals are end-to-end encrypted, so the server cannot forge them, and a changed command is asked about again. `[recipes] share_approvals = false` in config.toml approves per machine instead. Machine profiles: `hearth machine` (show), `hearth machine add <name> --here`, `hearth machine use <name>`.

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
hearth team share <team> <skill|agent> ...    # share from your library
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
