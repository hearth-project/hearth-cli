# Hearth command reference

Generated from `hearth --help` by `hearth docs cli > docs/cli.md`; every command also has `--help`.

| Command | Description |
|---|---|
| [`hearth setup`](#hearth-setup) | Log in, restore the newest conversations, skills and credentials, and start background sync (new machine) |
| [`hearth status`](#hearth-status) | Everything at a glance: login, background sync, storage, agents, library and what to do next |
| [`hearth restore`](#hearth-restore) | Bring conversations back from other machines (pick from a list, or the newest N) |
| [`hearth config`](#hearth-config) | Show the single settings file (skills, agents, backup, tools, MCP, workspaces) |
| [`hearth session`](#hearth-session) | Manage synced sessions |
| [`hearth session list`](#hearth-session-list) | List the session index, most recently active first |
| [`hearth session list find`](#hearth-session-list-find) | Search the session index by title, path or working directory |
| [`hearth session find`](#hearth-session-find) | Search stored sessions by title, path or working directory |
| [`hearth session show`](#hearth-session-show) | Show one session's index entry (title, turns, machine, path, size, status) |
| [`hearth session restore`](#hearth-session-restore) | Restore chosen sessions (any unique id prefix) and take their lease |
| [`hearth session scope`](#hearth-session-scope) | Which local sessions are yours (shared accounts: only those run inside your workspace are uploaded) |
| [`hearth session scope add`](#hearth-session-scope-add) | Also upload sessions run in these directories |
| [`hearth session scope rm`](#hearth-session-scope-rm) | Stop uploading sessions of these directories |
| [`hearth session scope all`](#hearth-session-scope-all) | Nobody else uses this account: upload every session |
| [`hearth session scope mine-only`](#hearth-session-scope-mine-only) | Only upload sessions inside the workspace and added directories (default) |
| [`hearth session scope prune`](#hearth-session-scope-prune) | Delete sessions on the server that ran outside the uploading machine's workspace (other people's, uploaded before scoping) |
| [`hearth session touch`](#hearth-session-touch) | Mark a session as accessed (resets its expiry clock) |
| [`hearth session pin`](#hearth-session-pin) | Never expire this session |
| [`hearth session unpin`](#hearth-session-unpin) | Allow this session to expire again |
| [`hearth session rm`](#hearth-session-rm) | Delete a session on the server (local files are kept) |
| [`hearth session take`](#hearth-session-take) | Force-acquire the write lease (e.g. old machine was killed) |
| [`hearth session handoff`](#hearth-session-handoff) | Print an agent-neutral Markdown hand-off of a session (continue it in any agent) |
| [`hearth session revive`](#hearth-session-revive) | Allow a deleted session to be uploaded again from local files |
| [`hearth push`](#hearth-push) | Upload all new session data once (use as k8s preStop hook) |
| [`hearth agents`](#hearth-agents) | Which coding agents hearth integrates with (default: codex, claude) |
| [`hearth agents list`](#hearth-agents-list) | Show supported and enabled agents |
| [`hearth agents enable`](#hearth-agents-enable) | Enable an agent (synced setting) |
| [`hearth agents home`](#hearth-agents-home) | Use a per-user config dir for an agent, e.g. '${WORKSPACE}/.codex' (synced) |
| [`hearth agents pull`](#hearth-agents-pull) | Restore an agent on this machine: skills/agents, newest conversations, its CLI (setup restores only recently used agents) |
| [`hearth agents isolate`](#hearth-agents-isolate) | Shared account: move your agent config and conversations into your workspace, so nobody else on the account uses your gateway login |
| [`hearth agents where`](#hearth-agents-where) | Print the config dir an agent uses for you here (e.g. CODEX_HOME=$(hearth agents where codex)) |
| [`hearth agents restart-app-server`](#hearth-agents-restart-app-server) | Stop your Codex app-servers that still use the CODEX_HOME from before Hearth switched it (Codex Desktop starts new ones) |
| [`hearth agents disable`](#hearth-agents-disable) | Disable an agent |
| [`hearth agents import`](#hearth-agents-import) | Import sessions of another agent into this agent (via its own converter) |
| [`hearth backup`](#hearth-backup) | Choose what Hearth uploads (synced, .gitignore-like selection) |
| [`hearth backup show`](#hearth-backup-show) | Print the selection file |
| [`hearth backup edit`](#hearth-backup-edit) | Open the selection in $EDITOR (synced on the next library sync) |
| [`hearth backup status`](#hearth-backup-status) | Show what the selection uploads from this machine |
| [`hearth backup enable`](#hearth-backup-enable) | Upload a category: an agent (codex), agent.kind (claude.sessions), workspace, credentials |
| [`hearth backup disable`](#hearth-backup-disable) | Stop uploading a category: an agent (codex), agent.kind (claude.sessions), workspace, credentials |
| [`hearth backup add`](#hearth-backup-add) | Back up a file, directory or pattern |
| [`hearth backup exclude`](#hearth-backup-exclude) | Never upload a file, directory or pattern |
| [`hearth lib`](#hearth-lib) | Shared skills/agents/instructions library for all agents |
| [`hearth lib import`](#hearth-lib-import) | Copy an agent's existing skills, agents and instructions into the library |
| [`hearth lib sync`](#hearth-lib-sync) | Render the library into every agent (skills symlinked, agents converted) |
| [`hearth lib push`](#hearth-lib-push) | Upload the library to your private space on the server |
| [`hearth lib pull`](#hearth-lib-pull) | Download the library from the server and render it |
| [`hearth lib status`](#hearth-lib-status) | List library contents |
| [`hearth lib config`](#hearth-lib-config) | Show the single settings file (skills, agents, backup, tools, MCP, workspaces) |
| [`hearth lib skills-dir`](#hearth-lib-skills-dir) | Where the one copy of the skills is stored (default <library>/skills); saved in <library>/config.toml |
| [`hearth lib search`](#hearth-lib-search) | Search skills the way the MCP tool does |
| [`hearth lib stats`](#hearth-lib-stats) | Most used skills, last 30 days (MCP get_skill + session logs) |
| [`hearth lib usage`](#hearth-lib-usage) | Skill usage counts on the server (ranking: hearth lib stats --server) |
| [`hearth lib usage prune`](#hearth-lib-usage-prune) | Delete the usage counts of deleted, renamed or test skills (default: every skill that no longer exists here) |
| [`hearth lib resident`](#hearth-lib-resident) | Make the most used skills resident (server ranking across your machines) |
| [`hearth lib mcp-install`](#hearth-lib-mcp-install) | Register the skill-search MCP server with Codex and Claude Code |
| [`hearth lib find`](#hearth-lib-find) | Search every source for shared skills and agents |
| [`hearth lib link`](#hearth-lib-link) | Use a shared skill (or --agent) in every agent; from a team it follows the newest version (--pin / --version N freeze it) |
| [`hearth lib unlink`](#hearth-lib-unlink) | Stop using a shared skill (or --agent) |
| [`hearth lib links`](#hearth-lib-links) | List linked shared skills and agents: version, whether it follows newer ones (--check: and the newest) |
| [`hearth lib update`](#hearth-lib-update) | Show (and with --yes apply) newer versions of linked items |
| [`hearth lib publish`](#hearth-lib-publish) | Publish a library skill or agent as a new immutable version (maintainers) |
| [`hearth lib source`](#hearth-lib-source) | Where shared skills/agents come from (searched after the local library, in order) |
| [`hearth lib source add`](#hearth-lib-source-add) | Add a source |
| [`hearth lib source add team`](#hearth-lib-source-add-team) | Use a team's packages |
| [`hearth lib source add git`](#hearth-lib-source-add-git) | Use a git repository of skills (skills/<name>/SKILL.md or a flat list) |
| [`hearth lib source list`](#hearth-lib-source-list) | List sources in lookup order |
| [`hearth lib source rm`](#hearth-lib-source-rm) | Remove a source |
| [`hearth team`](#hearth-team) | Teams: share skills and agents with colleagues (create, invite, join, share) |
| [`hearth team create`](#hearth-team-create) | Create a team (you become owner); then invite people with hearth team invite |
| [`hearth team list`](#hearth-team-list) | Teams you belong to or can read, with how many skills and agents each shares |
| [`hearth team invite`](#hearth-team-invite) | Make an invite code; whoever has it (and an account) joins with hearth team join <code> |
| [`hearth team join`](#hearth-team-join) | Join a team with an invite code from one of its members |
| [`hearth team leave`](#hearth-team-leave) | Leave a team |
| [`hearth team share`](#hearth-team-share) | Share skills or agents of your library with a team (a new version each time they change) |
| [`hearth team unshare`](#hearth-team-unshare) | Remove skills or agents from a team, with every version shared (maintainers) |
| [`hearth team share-top`](#hearth-team-share-top) | Share your most used skills and agents with your teams now (automatic every 20 min; [teams] share_top in config.toml) |
| [`hearth team use-top`](#hearth-team-use-top) | Keep your teams' most used skills and agents in all your agents now (automatic every 20 min; [teams] use_top) |
| [`hearth team share-with`](#hearth-team-share-with) | Let another team's members use this team's skills and agents (owners; --off to stop); no other team: list |
| [`hearth team members`](#hearth-team-members) | List members |
| [`hearth team add`](#hearth-team-add) | Add a member or change their role (owner only) |
| [`hearth team remove`](#hearth-team-remove) | Remove a member (owners), or leave (yourself) |
| [`hearth team share-usage`](#hearth-team-share-usage) | Upload your usage counts of this team's published skills (opt-in, last 30 days) |
| [`hearth team stats`](#hearth-team-stats) | Most used skills in a team (last 30 days, from members who share usage) |
| [`hearth team repo`](#hearth-team-repo) | The team's git repository: its skills/ and agents/ sync into the team every day; the team's new items go back as a review |
| [`hearth team repo set`](#hearth-team-repo-set) | Bind a repository to the team (owners); --machine: one of your machines that syncs it |
| [`hearth team repo sync`](#hearth-team-repo-sync) | Sync now: on the syncing machine it runs here; elsewhere it asks that machine to |
| [`hearth team repo unset`](#hearth-team-repo-unset) | Unbind the repository (owners); the team keeps its items |
| [`hearth team packages`](#hearth-team-packages) | Skills and agents published in a team |
| [`hearth mcp-config`](#hearth-mcp-config) | MCP servers for every agent from [mcp.servers] in config.toml (secrets via ${secret:NAME}) |
| [`hearth mcp-config import`](#hearth-mcp-config-import) | Import an agent's mcpServers; credential env values become secrets |
| [`hearth mcp-config sync`](#hearth-mcp-config-sync) | Register the config.toml [mcp.servers] in Codex, Claude Code and Comate |
| [`hearth mcp-config list`](#hearth-mcp-config-list) | List the MCP servers of config.toml |
| [`hearth learn`](#hearth-learn) | Discover this user's credentials, configs and setup steps and record them in the synced recipe "learned" |
| [`hearth recipe`](#hearth-recipe) | Restore recipes: files, env, commands, checks and manual steps (library/recipes) |
| [`hearth recipe apply`](#hearth-recipe-apply) | Run the recipes' commands yourself (setup only restores their files; agents go through hearth recipe plan) |
| [`hearth recipe plan`](#hearth-recipe-plan) | This machine's restore plan: software and commands to set up, with checks (the hearth skill works through it) |
| [`hearth recipe run`](#hearth-recipe-run) | Run steps of this machine's plan now (the user has agreed to them) and mark the ones that worked done |
| [`hearth recipe done`](#hearth-recipe-done) | Mark restore steps as done on this machine (keys from hearth recipe plan; none: pick) |
| [`hearth recipe proposals`](#hearth-recipe-proposals) | Setup steps proposed by agents (MCP propose_setup_step), waiting for review |
| [`hearth recipe proposals accept`](#hearth-recipe-proposals-accept) | Merge proposals into their recipes |
| [`hearth recipe proposals reject`](#hearth-recipe-proposals-reject) | Drop proposals |
| [`hearth recipe login`](#hearth-recipe-login) | Run a QR-code login step in this terminal (--on <machine>: over hearth ssh); without a terminal, print the command for the user |
| [`hearth recipe list`](#hearth-recipe-list) | List recipes |
| [`hearth recipe show`](#hearth-recipe-show) | Print a recipe |
| [`hearth recipe add`](#hearth-recipe-add) | Validate a recipe and copy it into the library |
| [`hearth recipe rm`](#hearth-recipe-rm) | Remove a recipe |
| [`hearth doctor`](#hearth-doctor) | Run recipe checks and show pending manual steps (changes nothing) |
| [`hearth tools`](#hearth-tools) | Pin and install the agent CLIs themselves (codex, claude, custom) |
| [`hearth tools add`](#hearth-tools-add) | Add a tool to the synced manifest (built in: claude, codex) |
| [`hearth tools install`](#hearth-tools-install) | Install missing or mismatched tools from the manifest |
| [`hearth tools list`](#hearth-tools-list) | Manifest versus this machine |
| [`hearth tools rm`](#hearth-tools-rm) | Remove a tool from the manifest |
| [`hearth machine`](#hearth-machine) | Kinds of machines (H800, GB200, Mac, ...) with their own setup steps and working directory |
| [`hearth machine list`](#hearth-machine-list) | List your machines, architecture and agent readiness |
| [`hearth machine inspect`](#hearth-machine-inspect) | Show a machine's public health report |
| [`hearth machine check`](#hearth-machine-check) | Check this machine's environment now and report it (the daemon does it every 5 minutes) |
| [`hearth machine add`](#hearth-machine-add) | Add or update a profile (--here: match machines like this one) |
| [`hearth machine install-command`](#hearth-machine-install-command) | Print a one-time command that installs hearth on a new machine and logs it in (30 minutes, once) |
| [`hearth machine rm`](#hearth-machine-rm) | Remove a profile |
| [`hearth machine use`](#hearth-machine-use) | Apply profiles to this machine even if they do not match (no names: clear) |
| [`hearth machine facts`](#hearth-machine-facts) | Print the detected facts as JSON |
| [`hearth secret`](#hearth-secret) | End-to-end encrypted credentials (tokens, config files, keys) |
| [`hearth secret set`](#hearth-secret-set) | Store a secret (value from --file, stdin, or a hidden prompt) |
| [`hearth secret restore`](#hearth-secret-restore) | Write file secrets back to their paths (mode 0600); names: only those |
| [`hearth secret get`](#hearth-secret-get) | Print a secret's value |
| [`hearth secret list`](#hearth-secret-list) | List secret names (not values) |
| [`hearth secret rm`](#hearth-secret-rm) | Delete a secret |
| [`hearth secret exec`](#hearth-secret-exec) | Run a command with env-style secrets (UPPER_CASE names) in its environment; --for: credential files too, removed after |
| [`hearth secret use`](#hearth-secret-use) | Write the credentials a skill or file secret needs (0600) for a while; removed after --ttl |
| [`hearth secret clean`](#hearth-secret-clean) | Remove the credential files written on demand (hearth secret use, prepare_credentials) now |
| [`hearth up`](#hearth-up) | After a container restart: start the sync daemon and recipe services |
| [`hearth init`](#hearth-init) | Make a directory your hearth workspace (state in <dir>/.hearth; --move-login brings the login of ~/.hearth) |
| [`hearth daemon`](#hearth-daemon) | Continuously sync session files in the foreground (flushes on SIGTERM) |
| [`hearth daemon start`](#hearth-daemon-start) | Start the daemon in the background |
| [`hearth daemon stop`](#hearth-daemon-stop) | Stop the background daemon (it flushes first) |
| [`hearth daemon status`](#hearth-daemon-status) | Show whether the daemon is running |
| [`hearth self-update`](#hearth-self-update) | Replace this binary with the server's build for this platform (checksum verified) |
| [`hearth login`](#hearth-login) | Log in and register this machine as a device |
| [`hearth logout`](#hearth-logout) | Remove local credentials and cached vault key |
| [`hearth whoami`](#hearth-whoami) | Show the local identity and this workspace's hearth login |
| [`hearth device`](#hearth-device) | Manage devices |
| [`hearth device list`](#hearth-device-list) | List devices |
| [`hearth device rename`](#hearth-device-rename) | Name a machine (shown as name (host), its SSH host name; an offline machine's name continues that machine); one argument: this machine; "" clears it |
| [`hearth device revoke`](#hearth-device-revoke) | Revoke a device's access (rotate any credentials it had) |
| [`hearth passwd`](#hearth-passwd) | Change the master password (other devices keep working) |
| [`hearth recover`](#hearth-recover) | Forgot the password: set a new one with the recovery key (signs out all devices) |
| [`hearth recovery-key`](#hearth-recovery-key) | Manage the account recovery key |
| [`hearth recovery-key rotate`](#hearth-recovery-key-rotate) | Issue a new recovery key and invalidate the old one (e.g. it was shown to someone) |
| [`hearth server`](#hearth-server) | Run the sync server (SQLite + local directory by default; PostgreSQL and S3 optional) |
| [`hearth server invite`](#hearth-server-invite) | Create single-use registration invite codes (run on the server host) |
| [`hearth server gencert`](#hearth-server-gencert) | Create a self-signed TLS certificate in the data directory |
| [`hearth server migrate`](#hearth-server-migrate) | Copy all data to other storage backends (e.g. SQLite + directory -> PostgreSQL + S3) |
| [`hearth server backup`](#hearth-server-backup) | Write a consistent backup (SQLite + blob files) into a new directory while the server runs |
| [`hearth server admin`](#hearth-server-admin) | Administer accounts, quotas, invites and storage (run with access to the server's storage) |
| [`hearth server admin stats`](#hearth-server-admin-stats) | Totals for the whole server |
| [`hearth server admin users`](#hearth-server-admin-users) | List accounts with their usage (largest first) |
| [`hearth server admin user`](#hearth-server-admin-user) | Show one account |
| [`hearth server admin disable`](#hearth-server-admin-disable) | Block login and every token of an account (data is kept) |
| [`hearth server admin enable`](#hearth-server-admin-enable) | Re-enable a disabled account, or approve one waiting (registration approval) |
| [`hearth server admin quota`](#hearth-server-admin-quota) | Set an account's storage quota (e.g. 20GiB) |
| [`hearth server admin revoke-devices`](#hearth-server-admin-revoke-devices) | Log out every device of an account (e.g. after a leaked password) |
| [`hearth server admin delete-user`](#hearth-server-admin-delete-user) | Delete an account and all its sessions, library and secrets |
| [`hearth server admin grant`](#hearth-server-admin-grant) | Make an account an admin of this server (it gets the administration pages in the Hearth app and can use hearth app admin-call) |
| [`hearth server admin ungrant`](#hearth-server-admin-ungrant) | Remove an admin (not the last one) |
| [`hearth server admin admins`](#hearth-server-admin-admins) | List admins |
| [`hearth server admin invites`](#hearth-server-admin-invites) | List invites (create with `hearth server invite`) |
| [`hearth server admin invites revoke`](#hearth-server-admin-invites-revoke) | Revoke an unused invite |
| [`hearth server admin gc`](#hearth-server-admin-gc) | Run session expiry now and delete orphaned blobs |
| [`hearth server start`](#hearth-server-start) | Run the server in the background under a supervisor (restarts it if it exits or hangs; rotated log) |
| [`hearth server stop`](#hearth-server-stop) | Stop the supervised server (graceful) |
| [`hearth server restart`](#hearth-server-restart) | Restart the supervised server with the flags it was started with |
| [`hearth server status`](#hearth-server-status) | Show the supervised server: pids, uptime, restarts, health |
| [`hearth server install-service`](#hearth-server-install-service) | Print a systemd unit for these server flags (when systemd runs; containers: use `hearth server start`) |
| [`hearth site`](#hearth-site) | Organisation defaults published on the server (recipes, files, learn settings) |
| [`hearth site disable`](#hearth-site-disable) | Do not apply a site recipe for you (synced choice) |
| [`hearth site enable`](#hearth-site-enable) | Apply a site recipe you disabled |
| [`hearth site pull`](#hearth-site-pull) | Fetch the site defaults now (works before login with --server) |
| [`hearth site status`](#hearth-site-status) | List the site defaults on this machine |
| [`hearth app`](#hearth-app) | The macOS menu bar app: your machines at a glance, and the console in a window |
| [`hearth app data`](#hearth-app-data) | Print the account's overview, library and teams as JSON (the macOS app's own pages read it) |
| [`hearth app admin-data`](#hearth-app-admin-data) | Print the administration pages' documents as JSON (admin accounts only; the macOS app reads it) |
| [`hearth app admin-call`](#hearth-app-admin-call) | Send one request to /v1/admin/... as the signed-in admin and print the JSON reply (the macOS app's admin actions use it) |
| [`hearth app call`](#hearth-app-call) | Send one request to /v1/me... or /v1/admin/... as the signed-in user and print the JSON reply (the macOS app's actions use it) |
| [`hearth app install`](#hearth-app-install) | Install (or update) the menu bar app from your server into ~/Applications and start it |
| [`hearth app open`](#hearth-app-open) | Start the menu bar app |
| [`hearth app uninstall`](#hearth-app-uninstall) | Quit and remove the menu bar app |
| [`hearth codex-import`](#hearth-codex-import) | Import Claude Code sessions into Codex (alias for `agent import codex --from claude`) |
| [`hearth codex-threads`](#hearth-codex-threads) | Make restored sessions show up in Codex Desktop (index threads, names, sidebar sections) |
| [`hearth cp`](#hearth-cp) | Copy files or directories between your machines, directly (parallel connections, resumable) |
| [`hearth docs`](#hearth-docs) | Documentation: how to install the agents' software (env), the command reference (cli) |
| [`hearth docs env`](#hearth-docs-env) | How to install and check Node, Codex CLI, Claude Code and codex-mixin, plus your organisation's notes from the server |
| [`hearth docs cli`](#hearth-docs-cli) | Print the command reference as Markdown |
| [`hearth env`](#hearth-env) | The shell activation installed by env steps |
| [`hearth env status`](#hearth-env-status) | Show the generated script and rc hook |
| [`hearth env uninstall`](#hearth-env-uninstall) | Remove the managed block from ~/.bashrc and ~/.zshrc |
| [`hearth identity`](#hearth-identity) | Passwordless login through the platform identity broker |
| [`hearth identity enable`](#hearth-identity-enable) | Upload the platform unlock now (normally automatic) |
| [`hearth identity disable`](#hearth-identity-disable) | Stop allowing passwordless login for this account |
| [`hearth identity status`](#hearth-identity-status) | Show whether a platform credential is available on this machine |
| [`hearth mcp`](#hearth-mcp) | Serve skill search over MCP on stdio (search_skills, get_skill, list_skills) |
| [`hearth memory`](#hearth-memory) | Long-term memory shared by your agents and machines: remember, forget, list, index, hooks |
| [`hearth memory remember`](#hearth-memory-remember) | Remember a fact or habit for all your agents (end-to-end encrypted) |
| [`hearth memory forget`](#hearth-memory-forget) | Forget a remembered fact (id f:… from hearth memory list) |
| [`hearth memory list`](#hearth-memory-list) | Remembered facts and habits |
| [`hearth memory index`](#hearth-memory-index) | Bring the local memory index up to date now (the background sync does it every 5 min) |
| [`hearth memory status`](#hearth-memory-status) | What the memory holds here, and the agent hooks |
| [`hearth memory hooks`](#hearth-memory-hooks) | Hooks that add relevant memories to every prompt of Codex and Claude Code |
| [`hearth memory hooks on`](#hearth-memory-hooks-on) | Install the hooks (default) |
| [`hearth memory hooks off`](#hearth-memory-hooks-off) | Remove the hooks (recall stays available as a command and MCP tool) |
| [`hearth onboard`](#hearth-onboard) | Machine setup status and next steps (used by the hearth agent skill) |
| [`hearth onboard status`](#hearth-onboard-status) | What is set up on this machine and what to do next |
| [`hearth onboard install-skill`](#hearth-onboard-install-skill) | Install the hearth skill into every agent on this machine (--print shows it) |
| [`hearth recall`](#hearth-recall) | Search long-term memory: your conversations on every machine and agent, remembered facts, agents' notes (local, milliseconds) |
| [`hearth register`](#hearth-register) | Create an account (prints a one-time recovery key) |
| [`hearth save`](#hearth-save) | Run every capture step now (the daemon also runs them on schedule) |
| [`hearth shell`](#hearth-shell) | Your own lines of ~/.bashrc / ~/.zshrc / ~/.bash_aliases, kept encrypted for new machines ([shell] in config.toml) |
| [`hearth shell show`](#hearth-shell-show) | Print what Hearth keeps of each rc file on this machine (your lines, without the distribution default and Hearth's blocks) |
| [`hearth shell save`](#hearth-shell-save) | Upload the changed rc files now (the background sync does it every 20 min) |
| [`hearth shell restore`](#hearth-shell-restore) | Write the stored lines as snippets in <hearth home>/shell and source them from the rc files (setup does this; --dry-run shows them) |
| [`hearth shell uninstall`](#hearth-shell-uninstall) | Take Hearth's lines out of this machine's rc files (the stored copy stays; stop syncing: [shell] sync = false) |
| [`hearth ssh`](#hearth-ssh) | SSH to your machines with Hearth managing the keys (no machine: list them) |
| [`hearth ssh enable`](#hearth-ssh-enable) | Accept SSH on this machine (default on Linux) |
| [`hearth ssh disable`](#hearth-ssh-disable) | Stop accepting SSH on this machine (--all: on every machine of the account) |
| [`hearth ssh authorize`](#hearth-ssh-authorize) | Also accept a key that is not a Hearth device (e.g. an existing laptop key) |
| [`hearth ssh status`](#hearth-ssh-status) | This machine's sshd: running, address, accepted keys |
| [`hearth ssh keys`](#hearth-ssh-keys) | Keys your machines accept |
| [`hearth ssh unauthorize`](#hearth-ssh-unauthorize) | Remove a key added with authorize |
| [`hearth ssh config`](#hearth-ssh-config) | Write Host entries for your machines into ~/.ssh/config (kept up to date by the daemon) |
| [`hearth transfer`](#hearth-transfer) | Copy large directories between storage systems with a DataTool-compatible service (asynchronous, server-side) |
| [`hearth transfer meta`](#hearth-transfer-meta) | What the service can copy: storages, allowed directions, path hints, defaults and limits |
| [`hearth transfer submit`](#hearth-transfer-submit) | Submit a copy task (shows the plan and asks first) |
| [`hearth transfer list`](#hearth-transfer-list) | Tasks submitted from this machine (--all: every task on the service) |
| [`hearth transfer status`](#hearth-transfer-status) | State, progress and error of one task |
| [`hearth transfer cancel`](#hearth-transfer-cancel) | Cancel a task (only your own, unless --force) |

## hearth setup

Log in, restore the newest conversations, skills and credentials, and start background sync (new machine)

```text
hearth setup [flags]

Flags:
      --alias string               a name for this machine (default: asked in a terminal, proposing e.g. gb-node-e05-13); an offline machine's name continues that machine; hearth device rename changes it
      --ca-cert string             PEM certificate to trust for a self-signed server
      --device string              device name (default: hostname)
      --email string               account email (default: this machine's local identity, as hearth login)
      --force[=true|false]         overwrite local session files that differ from the server
      --full[=true|false]          restore everything now (credentials, conversations, shell lines, agent setup) instead of on demand
      --insecure[=true|false]      allow plain HTTP to a non-loopback server
      --invite string              invite code: create the account first (first machine only)
      --new[=true|false]           create the account first (first machine only; servers in invite mode also need --invite)
      --no-app[=true|false]        on a Mac, do not install the menu bar app [$HEARTH_NO_APP=1]
      --no-daemon[=true|false]     do not start background sync
      --no-import[=true|false]     do not record this machine for new ones (its skills and agents, new setup items, shell lines)
      --no-register[=true|false]   never create the account; fail when the login does not exist
      --server string              server URL (default: this login's, $HEARTH_SERVER, or the server hearth was installed from)
      --sessions int               restore this many most recently active conversations (0 = all; a new machine restores none unless given) (default 10)
      --switch[=true|false]        replace an existing login to another account or server
      --team string                join a team with this invite code, or create a team of this name (asked in a terminal when you have none)
      --token string               one-time install token from an install command (the Hearth app, hearth machine install-command): log in without the password (works once, 30 minutes) [$HEARTH_INSTALL_TOKEN]
```

## hearth status

Everything at a glance: login, background sync, storage, agents, library and what to do next

```text
hearth status [flags]

Flags:
      --json[=true|false]   machine-readable (onboard status plus the daemon heartbeat)
```

## hearth restore

Restores the newest conversations (by last activity, newest first; default 10) and every memory note.
Use --last N for more, --all for everything, `hearth session list` to see them all,
or pass one or more ids (any unique prefix) to restore just those.

```text
hearth restore [session-id ...] [flags]

Flags:
      --agent string                  only restore sessions of this agent (codex, claude)
      --all[=true|false]              restore every conversation
      --force[=true|false]            overwrite local files that differ from the server copy
      --include-online[=true|false]   also restore conversations last written on machines that are still online (left out by default)
  -n, --last int                      restore the N most recently active conversations (default 10)
  -i, --pick[=true|false]             choose the conversations from a numbered list (default in a terminal)
  -y, --yes[=true|false]              do not ask: restore the newest N right away
```

## hearth config

Show the single settings file (skills, agents, backup, tools, MCP, workspaces)

```text
hearth config [flags]

Flags:
      --edit[=true|false]   open the file in $EDITOR
```

## hearth session

Manage synced sessions

```text
hearth session
```

## hearth session list

Lists the searchable index of stored sessions: title, turns, last write, machine, size, status. Filter with --search (title, path, cwd) and --host. Restore chosen ones with `hearth restore <id>...`.

```text
hearth session list [flags]

Flags:
      --agent string         filter by agent
      --files[=true|false]   also list backed-up files and memory notes (default: conversations only)
      --host string          filter by machine or device
      --json[=true|false]    print the index as JSON
  -n, --limit int            show the N most recently active sessions (0 = all) (default 20)
  -s, --search string        match title, path or working directory
      --status string        filter by status (active, expired)
  -w, --wide[=true|false]    show full titles (no cut to the terminal width)
```

## hearth session list find

Search the session index by title, path or working directory

```text
hearth session list find <query>
```

## hearth session find

Search stored sessions by title, path or working directory

```text
hearth session find <query> [flags]

Flags:
      --agent string         filter by agent
      --files[=true|false]   also list backed-up files and memory notes (default: conversations only)
      --host string          filter by machine or device
      --json[=true|false]    print the index as JSON
  -n, --limit int            show the N most recently active matches (0 = all) (default 20)
      --status string        filter by status (active, expired)
  -w, --wide[=true|false]    show full titles (no cut to the terminal width)
```

## hearth session show

Show one session's index entry (title, turns, machine, path, size, status)

```text
hearth session show <session-id>
```

## hearth session restore

Restore chosen sessions (any unique id prefix) and take their lease

```text
hearth session restore <session-id> [session-id ...] [flags]

Flags:
      --force[=true|false]   overwrite local files that differ from the server copy
```

## hearth session scope

Which local sessions are yours (shared accounts: only those run inside your workspace are uploaded)

```text
hearth session scope
```

## hearth session scope add

Also upload sessions run in these directories

```text
hearth session scope add <dir>...
```

## hearth session scope rm

Stop uploading sessions of these directories

```text
hearth session scope rm <dir>...
```

## hearth session scope all

Nobody else uses this account: upload every session

```text
hearth session scope all
```

## hearth session scope mine-only

Only upload sessions inside the workspace and added directories (default)

```text
hearth session scope mine-only
```

## hearth session scope prune

Delete sessions on the server that ran outside the uploading machine's workspace (other people's, uploaded before scoping)

```text
hearth session scope prune [flags]

Flags:
      --keep strings       directories whose sessions are yours after all (repeatable)
      --yes[=true|false]   delete them
```

## hearth session touch

Mark a session as accessed (resets its expiry clock)

```text
hearth session touch <id>
```

## hearth session pin

Never expire this session

```text
hearth session pin <id>
```

## hearth session unpin

Allow this session to expire again

```text
hearth session unpin <id>
```

## hearth session rm

Delete a session on the server (local files are kept)

```text
hearth session rm <id>
```

## hearth session take

Force-acquire the write lease (e.g. old machine was killed)

```text
hearth session take <id>
```

## hearth session handoff

Print an agent-neutral Markdown hand-off of a session (continue it in any agent)

```text
hearth session handoff <id>
```

## hearth session revive

Allow a deleted session to be uploaded again from local files

```text
hearth session revive <full-id>
```

## hearth push

Upload all new session data once (use as k8s preStop hook)

```text
hearth push
```

## hearth agents

Which coding agents hearth integrates with (default: codex, claude)

```text
hearth agents
```

## hearth agents list

Show supported and enabled agents

```text
hearth agents list
```

## hearth agents enable

Enable an agent (synced setting)

```text
hearth agents enable <agent>
```

## hearth agents home

Use a per-user config dir for an agent, e.g. '${WORKSPACE}/.codex' (synced)

```text
hearth agents home <agent> <dir>
```

## hearth agents pull

Restore an agent on this machine: skills/agents, newest conversations, its CLI (setup restores only recently used agents)

```text
hearth agents pull <agent> [flags]

Flags:
      --last int   restore this many newest conversations (0 = all) (default 10)
```

## hearth agents isolate

On an account several people share, ~/.codex is the config every Codex
session falls back to. If your codex-mixin gateway and client key are there,
everyone whose shell does not set CODEX_HOME uses your login.

isolate copies your config (skills, rules, plugins, MCP servers, model
settings) and your conversations (the session scope decides which are yours)
to <home>/agent-homes/codex (inside your Hearth home: per machine, and apart
from the workspace's project .codex), has codex-mixin connect the new home with a client key
of its own, makes this machine's Hearth sessions use it, then hands the shared
default back to codex-mixin (uninstall-codex; else the gateway provider and
key are removed). A backup of the shared default's config goes to
<home>/backup. Other people's conversations stay where they are.

```text
hearth agents isolate <codex|claude> [flags]

Flags:
      --dir string         new home (default <hearth home>/agent-homes/<agent>)
  -y, --yes[=true|false]   do not ask
```

## hearth agents where

Print the config dir an agent uses for you here (e.g. CODEX_HOME=$(hearth agents where codex))

```text
hearth agents where <agent>
```

## hearth agents restart-app-server

Codex Desktop starts a long-running codex app-server on a remote machine and
it keeps the CODEX_HOME of the session that started it. After Hearth changes
your agent home (hearth agents isolate, a new identity block), those servers
still use the old home until they restart. This stops the ones of your
sessions (HEARTH_USER) whose CODEX_HOME is the account-wide ~/.codex or a home
Hearth set before; other people's processes are not touched.

```text
hearth agents restart-app-server [flags]

Flags:
  -y, --yes[=true|false]   do not ask
```

## hearth agents disable

Disable an agent

```text
hearth agents disable <agent>
```

## hearth agents import

Uses the target agent's own importer (e.g. Codex's externalAgentConfig/import)
so format details stay owned by that agent. Arguments are transcript files or hearth
session ids (restored first). With none, imports what the target agent detects for
the current directory.

```text
hearth agents import <agent> [session-file | hearth-session-id ...] [flags]

Flags:
      --dry-run[=true|false]   only show what would be imported
      --from string            source agent to import from (default "claude")
```

## hearth backup

The selection lives in your synced config.toml ([backup] include/exclude), so every machine
follows the same choices. Lines select: @category, paths (~/ = home,
${WORKSPACE}/ = workspace, * ** ? wildcards, trailing / for directories);
"!" lines exclude and win over everything.

Categories: @<agent> and @<agent>.sessions / @<agent>.memory (e.g. @codex,
!@claude.sessions; the most specific line wins), @workspace @credentials,
and @sessions / @memory as fallbacks for agents without a line.

```text
hearth backup
```

## hearth backup show

Print the selection file

```text
hearth backup show
```

## hearth backup edit

Open the selection in $EDITOR (synced on the next library sync)

```text
hearth backup edit
```

## hearth backup status

Show what the selection uploads from this machine

```text
hearth backup status [flags]

Flags:
      --all[=true|false]   list every file, selected or not
```

## hearth backup enable

Upload a category: an agent (codex), agent.kind (claude.sessions), workspace, credentials

```text
hearth backup enable <category>
```

## hearth backup disable

Stop uploading a category: an agent (codex), agent.kind (claude.sessions), workspace, credentials

```text
hearth backup disable <category>
```

## hearth backup add

Back up a file, directory or pattern

```text
hearth backup add <path-or-pattern> [...]
```

## hearth backup exclude

Never upload a file, directory or pattern

```text
hearth backup exclude <path-or-pattern> [...]
```

## hearth lib

Shared skills/agents/instructions library for all agents

```text
hearth lib
```

## hearth lib import

Copy an agent's existing skills, agents and instructions into the library

```text
hearth lib import codex|claude|comate [flags]

Flags:
      --from string              agent directory to import from (default: $CODEX_HOME / ~/.claude); a project's .codex or .claude also works
      --overwrite[=true|false]   replace items already in the library
```

## hearth lib sync

Render the library into every agent (skills symlinked, agents converted)

```text
hearth lib sync [flags]

Flags:
      --adopt[=true|false]     back up and replace existing unmanaged skills/agents with the library version
      --dry-run[=true|false]   only show what would change
```

## hearth lib push

Upload the library to your private space on the server

```text
hearth lib push [flags]

Flags:
      --allow-secrets[=true|false]   upload even if the secret scan finds something
```

## hearth lib pull

Download the library from the server and render it

```text
hearth lib pull [flags]

Flags:
      --force[=true|false]   discard unsynced local changes (they are backed up)
```

## hearth lib status

List library contents

```text
hearth lib status
```

## hearth lib config

Show the single settings file (skills, agents, backup, tools, MCP, workspaces)

```text
hearth lib config [flags]

Flags:
      --edit[=true|false]   open the file in $EDITOR
```

## hearth lib skills-dir

Sets skills.dir in <library>/config.toml and moves the existing skills there.
Portable paths (${WORKSPACE}/.agents/skills, ~/.agents/skills, an absolute path) are
resolved on every machine, so the setting follows the library to your other machines.

```text
hearth lib skills-dir [path]
```

## hearth lib search

Search skills the way the MCP tool does

```text
hearth lib search <query> [flags]

Flags:
      --limit int            results (default 5)
      --skills-dir strings   additional skill directories to index
```

## hearth lib stats

Most used skills, last 30 days (MCP get_skill + session logs)

```text
hearth lib stats [flags]

Flags:
      --days int              window in days (default 30)
      --server[=true|false]   rank across all machines of this account (usage is uploaded first)
```

## hearth lib usage

Skill usage counts on the server (ranking: hearth lib stats --server)

```text
hearth lib usage
```

## hearth lib usage prune

Without names, lists the skills with usage counts on the server that are not in the library, not linked and
not installed in any agent on this machine, and deletes their counts after a yes. With names, deletes those.
Machines no longer upload counts of skills they do not have, so pruned names do not come back.

```text
hearth lib usage prune [skill ...] [flags]

Flags:
      --dry-run[=true|false]   only list what would be deleted
  -y, --yes[=true|false]       do not ask
```

## hearth lib resident

Make the most used skills resident (server ranking across your machines)

```text
hearth lib resident [flags]

Flags:
      --days int   usage window in days (default 30)
      --top int    how many skills to keep resident (default 5)
```

## hearth lib mcp-install

Register the skill-search MCP server with Codex and Claude Code

```text
hearth lib mcp-install
```

## hearth lib find

Search every source for shared skills and agents

```text
hearth lib find <query> [flags]

Flags:
      --limit int              results (default 10)
      --refresh[=true|false]   fetch git sources even if fetched recently
```

## hearth lib link

Links a shared skill or agent into every agent on all your machines. A package of a trusted team source
follows the team's newest version: the daemon moves the link when a new version is published (every 20
minutes, [teams] follow_updates = false turns that off) and `hearth status` shows what changed.
--pin keeps the current version, --version N links version N and keeps it (also how to go back to an
older one); run the command again without them to follow the newest version again. A git source is
always pinned to a commit: `hearth lib update` shows the newer ones, --yes applies.

```text
hearth lib link <name|source:name> [flags]

Flags:
      --agent[=true|false]   link an agent instead of a skill
      --pin[=true|false]     keep this version: do not follow the team's newer ones
      --version int          link this version of a team's package and keep it
  -y, --yes[=true|false]     confirm linking from an untrusted source
```

## hearth lib unlink

Stop using a shared skill (or --agent)

```text
hearth lib unlink <name> [flags]

Flags:
      --agent[=true|false]   unlink an agent
```

## hearth lib links

List linked shared skills and agents: version, whether it follows newer ones (--check: and the newest)

```text
hearth lib links [flags]

Flags:
      --check[=true|false]   ask the sources for the newest version of each link
```

## hearth lib update

Show (and with --yes apply) newer versions of linked items

```text
hearth lib update [name] [flags]

Flags:
  -y, --yes[=true|false]   apply the updates
```

## hearth lib publish

Publish a library skill or agent as a new immutable version (maintainers)

```text
hearth lib publish skill|agent <name> --team <team> [flags]

Flags:
      --allow-secrets[=true|false]   publish even if the secret scan finds something
      --team string                  team
```

## hearth lib source

Where shared skills/agents come from (searched after the local library, in order)

```text
hearth lib source
```

## hearth lib source add

Add a source

```text
hearth lib source add
```

## hearth lib source add team

Use a team's packages

```text
hearth lib source add team <team> [name]
```

## hearth lib source add git

Use a git repository of skills (skills/<name>/SKILL.md or a flat list)

```text
hearth lib source add git <name> <url> [flags]

Flags:
      --ref string             branch or tag (default: the remote's default branch)
      --subdir string          directory in the repo that contains skills/ (or the skill directories)
      --trusted[=true|false]   skip the review step when linking from this source
```

## hearth lib source list

List sources in lookup order

```text
hearth lib source list
```

## hearth lib source rm

Remove a source

```text
hearth lib source rm <name>
```

## hearth team

A team shares skills and agents among its members; a team can also let other teams use them.

  hearth team create <name>                 you become owner
  hearth team invite <name>                 a code for colleagues: hearth team join <code>
  hearth team share <name> <skill> ...      share from your library
  hearth team share-with <name> <other>     let another team use them too

Your teams' skills are found by your agents through skill search; hearth lib link <team>:<skill> keeps one in every agent.

```text
hearth team
```

## hearth team create

Create a team (you become owner); then invite people with hearth team invite

```text
hearth team create <name>
```

## hearth team list

Teams you belong to or can read, with how many skills and agents each shares

```text
hearth team list
```

## hearth team invite

Make an invite code; whoever has it (and an account) joins with hearth team join <code>

```text
hearth team invite <team> [flags]

Flags:
      --days int      valid for this many days (default 7)
      --role string   role of who joins: member (use shared items), maintainer (also share), owner (default "maintainer")
      --uses int      how many people can use it (0 = unlimited until it expires)
```

## hearth team join

Join a team with an invite code from one of its members

```text
hearth team join <invite code>
```

## hearth team leave

Leave a team

```text
hearth team leave <team>
```

## hearth team share

Share skills or agents of your library with a team (a new version each time they change)

```text
hearth team share <team> <skill|agent> ... [flags]

Flags:
      --allow-secrets[=true|false]   share even if the secret scan finds something
```

## hearth team unshare

Remove skills or agents from a team, with every version shared (maintainers)

```text
hearth team unshare <team> <skill|agent> ... [flags]

Flags:
      --type string   skill or agent (default: whichever is shared under that name)
```

## hearth team share-top

Share your most used skills and agents with your teams now (automatic every 20 min; [teams] share_top in config.toml)

```text
hearth team share-top [flags]

Flags:
      --dry-run[=true|false]   only show what would be shared
```

## hearth team use-top

Keep your teams' most used skills and agents in all your agents now (automatic every 20 min; [teams] use_top)

```text
hearth team use-top [flags]

Flags:
      --dry-run[=true|false]   only show what would change
```

## hearth team share-with

Let another team's members use this team's skills and agents (owners; --off to stop); no other team: list

```text
hearth team share-with <team> [other-team] [flags]

Flags:
      --off[=true|false]   stop sharing with that team
```

## hearth team members

List members

```text
hearth team members <team>
```

## hearth team add

Add a member or change their role (owner only)

```text
hearth team add <team> <email> [flags]

Flags:
      --role string   member, maintainer (can publish) or owner (default "member")
```

## hearth team remove

Remove a member (owners), or leave (yourself)

```text
hearth team remove <team> <email>
```

## hearth team share-usage

Upload your usage counts of this team's published skills (opt-in, last 30 days)

```text
hearth team share-usage <team> [flags]

Flags:
      --auto[=true|false]   keep sharing from the daemon (hourly, synced setting)
      --off[=true|false]    stop automatic sharing
```

## hearth team stats

Most used skills in a team (last 30 days, from members who share usage)

```text
hearth team stats <team>
```

## hearth team repo

A team owner binds a git repository and picks one of their own machines to sync it. That machine, with
the owner's git credentials (the server never gets them), fetches the repository every day or when a
maintainer asks, publishes the skills (skills/**/SKILL.md) and agents (agents/*.md) that changed only there
into the team (the team wins: its changed items are never replaced), and proposes the team's new or changed items back as one review:
refs/for/<branch> (icode / Gerrit, one Change-Id until it is merged), or a branch hearth/team-<team>.

  hearth team repo <team>                                show the binding and the last sync
  hearth team repo set <team> <url> --machine <machine>  bind (owners)
  hearth team repo sync <team> [--dry-run]               sync now (on the syncing machine: runs here)
  hearth team repo unset <team>

```text
hearth team repo <team> [flags]

Flags:
      --json[=true|false]   machine-readable
```

## hearth team repo set

Bind a repository to the team (owners); --machine: one of your machines that syncs it

```text
hearth team repo set <team> <url> [flags]

Flags:
      --machine string   the machine that syncs (one of yours, with git access; default: this one)
      --new-dir string   where new skills from the team go in the repository (skills or a directory under it) (default "skills")
      --push string      how the team's items go back: cr (refs/for/<branch>, a review), branch (hearth/team-<team>), off (default "cr")
      --ref string       branch (default: the repository's default branch)
```

## hearth team repo sync

Sync now: on the syncing machine it runs here; elsewhere it asks that machine to

```text
hearth team repo sync <team> [flags]

Flags:
      --dry-run[=true|false]   only say what it would publish and propose
```

## hearth team repo unset

Unbind the repository (owners); the team keeps its items

```text
hearth team repo unset <team>
```

## hearth team packages

Skills and agents published in a team

```text
hearth team packages <team>
```

## hearth mcp-config

MCP servers for every agent from [mcp.servers] in config.toml (secrets via ${secret:NAME})

```text
hearth mcp-config
```

## hearth mcp-config import

Import an agent's mcpServers; credential env values become secrets

```text
hearth mcp-config import comate|claude [flags]

Flags:
      --file string              path to an mcpServers JSON file
      --overwrite[=true|false]   replace servers already in config.toml
```

## hearth mcp-config sync

Register the config.toml [mcp.servers] in Codex, Claude Code and Comate

```text
hearth mcp-config sync [flags]

Flags:
      --adopt[=true|false]   replace servers with the same name that hearth did not create
```

## hearth mcp-config list

List the MCP servers of config.toml

```text
hearth mcp-config list
```

## hearth learn

Inspects the workspace, home and environment (SSH keys, agent logins, git/gh credentials, tokens, env vars,
agent CLIs, codex-mixin, MCP servers, repos) and asks what a new machine should get back. Answers are kept
permanently on the server: files become encrypted secrets, the rest steps of library/recipes/learned.toml.
"never" stops asking about an item on every machine. Run it again whenever your setup changes.

```text
hearth learn [flags]

Flags:
      --accept strings            keep these finding IDs for all machines ("all": every new or changed one); no prompts
      --all[=true|false]          also ask about items already learned and unchanged
      --auto-credentials string   true / false: let the background sync record new credentials by itself (encrypted)
      --here strings              keep these finding IDs only for machines like this one (its profile)
      --here-step stringArray     add a step only for machines like this one (repeatable)
      --json[=true|false]         with --list: print JSON (id, kind, title, detail, status, per_machine)
      --later[=true|false]        do not remind me in hearth status for 30 days
      --list[=true|false]         only list findings
      --never strings             never ask about these finding IDs again
      --no-push[=true|false]      do not push the library afterwards
      --one-by-one[=true|false]   ask about each finding in turn instead of one checklist
      --step stringArray          add a step for all machines: "$ command" runs a command, other text is a reminder (repeatable)
      --yes-all[=true|false]      accept every finding without asking
```

## hearth recipe

Restore recipes: files, env, commands, checks and manual steps (library/recipes)

```text
hearth recipe
```

## hearth recipe apply

Run the recipes' commands yourself (setup only restores their files; agents go through hearth recipe plan)

```text
hearth recipe apply [flags]

Flags:
      --dry-run[=true|false]   only show what would change
      --only strings           recipe names
  -y, --yes[=true|false]       approve the commands shown as needs-approval
```

## hearth recipe plan

Lists the steps of your recipes (and the server's site recipes) that install software or run commands on this
machine, in order, with a check command for each. Hearth does not run them on setup: ask your agent "用 hearth 恢复这台
机器的环境" and it goes through them with you (how to install each tool: hearth docs env). Mark a step done here with
hearth recipe done <key>.

```text
hearth recipe plan [flags]

Flags:
      --all[=true|false]    also steps done or skipped on this machine
      --json[=true|false]   JSON for agents
```

## hearth recipe run

Run steps of this machine's plan now (the user has agreed to them) and mark the ones that worked done

```text
hearth recipe run <step-key> ...
```

## hearth recipe done

Mark restore steps as done on this machine (keys from hearth recipe plan; none: pick)

```text
hearth recipe done [step-key ...] [flags]

Flags:
      --all[=true|false]   mark every step still to do
```

## hearth recipe proposals

Setup steps proposed by agents (MCP propose_setup_step), waiting for review

```text
hearth recipe proposals
```

## hearth recipe proposals accept

Merge proposals into their recipes

```text
hearth recipe proposals accept <id>...
```

## hearth recipe proposals reject

Drop proposals

```text
hearth recipe proposals reject <id>...
```

## hearth recipe login

Runs an interactive plan step (manual, tty = true, with run) in the foreground of this terminal, so the
program draws its QR code itself; when the command succeeds the step's check runs and the step is marked
done. With --on <machine> it goes over hearth ssh -t. Without a terminal (an agent runs it), or with
--print, it prints the command the user runs in their own terminal instead:

  hearth ssh -t <machine> -- 'PATH="$HOME/.local/bin:$PATH"; exec hearth recipe login <key>'

```text
hearth recipe login <step-key> [flags]

Flags:
      --on string            the machine (default: this one)
      --print[=true|false]   only print the command for the user's terminal
```

## hearth recipe list

List recipes

```text
hearth recipe list
```

## hearth recipe show

Print a recipe

```text
hearth recipe show <name>
```

## hearth recipe add

Validate a recipe and copy it into the library

```text
hearth recipe add <file.toml>
```

## hearth recipe rm

Remove a recipe

```text
hearth recipe rm <name>
```

## hearth doctor

Run recipe checks and show pending manual steps (changes nothing)

```text
hearth doctor [flags]

Flags:
      --all[=true|false]   also run optional checks
```

## hearth tools

Pin and install the agent CLIs themselves (codex, claude, custom)

```text
hearth tools
```

## hearth tools add

Add a tool to the synced manifest (built in: claude, codex)

```text
hearth tools add <name> [flags]

Flags:
      --check string           custom tool: shell command printing its version
      --current[=true|false]   pin the version installed on this machine
      --install string         custom tool: shell command installing it ($VERSION is set)
      --version string         pin this version (x.y.z)
```

## hearth tools install

Install missing or mismatched tools from the manifest

```text
hearth tools install [flags]

Flags:
      --dry-run[=true|false]   only show what would be installed
  -y, --yes[=true|false]       approve custom install commands shown in the output
```

## hearth tools list

Manifest versus this machine

```text
hearth tools list
```

## hearth tools rm

Remove a tool from the manifest

```text
hearth tools rm <name>
```

## hearth machine

Profiles live in the synced library (library/machines.toml) and match machines by os, arch, gpu,
hostname or label ($HEARTH_LABELS). Recipe steps without conditions run everywhere (common habits);
steps with when = { profile = "h800" } (or gpu / arch / os / hostname / label) only where they match.
A profile's workspace becomes ${WORKSPACE} on matching machines unless `hearth init` set one.

```text
hearth machine
```

## hearth machine list

List your machines, architecture and agent readiness

```text
hearth machine list [flags]

Flags:
      --all[=true|false]    also revoked machines
      --json[=true|false]   machine-readable (the macOS menu bar app reads it)
```

## hearth machine inspect

Show a machine's public health report

```text
hearth machine inspect <machine>
```

## hearth machine check

Check this machine's environment now and report it (the daemon does it every 5 minutes)

```text
hearth machine check
```

## hearth machine add

Add or update a profile (--here: match machines like this one)

```text
hearth machine add <name> [flags]

Flags:
      --description string     description
      --here[=true|false]      match machines like this one (by GPU model, else OS and architecture)
      --match stringToString   conditions, e.g. --match gpu=h800,h100 --match arch=x86_64 (keys: os, arch, gpu, hostname, label) (default [])
      --workspace string       working directory on these machines, e.g. /mnt/ssd/$USER/work
```

## hearth machine install-command

Print a one-time command that installs hearth on a new machine and logs it in (30 minutes, once)

```text
hearth machine install-command [flags]

Flags:
      --json[=true|false]   machine-readable (the macOS menu bar app reads it)
```

## hearth machine rm

Remove a profile

```text
hearth machine rm <name>
```

## hearth machine use

Apply profiles to this machine even if they do not match (no names: clear)

```text
hearth machine use [name...]
```

## hearth machine facts

Print the detected facts as JSON

```text
hearth machine facts
```

## hearth secret

End-to-end encrypted credentials (tokens, config files, keys)

```text
hearth secret
```

## hearth secret set

Store a secret (value from --file, stdin, or a hidden prompt)

```text
hearth secret set <NAME> [flags]

Flags:
      --file string         read the value from a file (e.g. ~/.config/gh/hosts.yml); restored to the same path by default
      --restore-to string   path to write the secret to on restore (~/... or absolute)
```

## hearth secret restore

Write file secrets back to their paths (mode 0600); names: only those

```text
hearth secret restore [NAME...] [flags]

Flags:
      --force[=true|false]   overwrite existing files that differ
```

## hearth secret get

Print a secret's value

```text
hearth secret get <NAME>
```

## hearth secret list

List secret names (not values)

```text
hearth secret list
```

## hearth secret rm

Delete a secret

```text
hearth secret rm <NAME>
```

## hearth secret exec

Run a command with env-style secrets (UPPER_CASE names) in its environment; --for: credential files too, removed after

```text
hearth secret exec [--for <skill|NAME>]... -- <command> [args...] [flags]

Flags:
      --for stringArray   also put this skill's (or file secret's) credential files in place while the command runs
```

## hearth secret use

Writes the credential files of the named skills (kept encrypted by the machine that had them) or the named file
secrets to their places, mode 0600, and prints the paths, never the values. They are removed after --ttl
(default 12h; 0 = keep) by the daemon, or now with hearth secret clean, and are not uploaded from here.

```text
hearth secret use <skill|NAME>... [flags]

Flags:
      --ttl duration   remove the files after this long (0 = keep) (default 12h0m0s)
```

## hearth secret clean

Remove the credential files written on demand (hearth secret use, prepare_credentials) now

```text
hearth secret clean
```

## hearth up

After a container restart: start the sync daemon and recipe services

```text
hearth up [flags]

Flags:
  -y, --yes[=true|false]   approve service start commands
```

## hearth init

On machines where several people share one Unix account, each person keeps their state, agent config dirs and credentials in their own workspace instead of the shared $HOME.

A workspace has its own login. If this machine is already logged in without one (~/.hearth) and the
workspace is yours, --move-login moves that login and its state there (the daemon restarts); otherwise
log in inside the workspace with hearth setup.

```text
hearth init [dir] [flags]

Flags:
      --move-login[=true|false]   move the login and state of ~/.hearth into this workspace
```

## hearth daemon

Continuously sync session files in the foreground (flushes on SIGTERM)

```text
hearth daemon [flags]

Flags:
      --interval duration          session scan interval (default 1m0s)
      --lib-interval duration      library sync interval (default 20m0s)
      --update-interval duration   how often to install a new hearth build from the server (0 = never; hearth self-update --auto off) (default 30m0s)
```

## hearth daemon start

Start the daemon in the background

```text
hearth daemon start
```

## hearth daemon stop

Stop the background daemon (it flushes first)

```text
hearth daemon stop
```

## hearth daemon status

Show whether the daemon is running

```text
hearth daemon status
```

## hearth self-update

The background daemon also does this by itself every few hours (after the server publishes a new build).
Turn that off on this machine with --auto off; a server admin can turn it off for everyone with
`hearth server --no-client-auto-update`.

```text
hearth self-update [flags]

Flags:
      --auto string     on | off | status: whether the daemon installs new builds by itself on this machine
      --server string   server URL (default: the one you are logged in to)
```

## hearth login

Logs in with the account this machine already knows: an existing login, else the local identity
(company SSO token cache, Comate login, platform user name) for --email. Only the password is asked for;
pass --switch to replace another account.

```text
hearth login [flags]

Flags:
      --ca-cert string          PEM certificate to trust for a self-signed server
      --device string           device name (default: hostname)
      --email string            account email (default: the local identity)
      --insecure[=true|false]   allow plain HTTP to a non-loopback server
      --server string           server URL (default: the existing login's, else $HEARTH_SERVER)
      --switch[=true|false]     replace an existing login to another account or server
```

## hearth logout

Remove local credentials and cached vault key

```text
hearth logout
```

## hearth whoami

Show the local identity and this workspace's hearth login

```text
hearth whoami
```

## hearth device

Manage devices

```text
hearth device
```

## hearth device list

List devices

```text
hearth device list
```

## hearth device rename

Name a machine (shown as name (host), its SSH host name; an offline machine's name continues that machine); one argument: this machine; "" clears it

```text
hearth device rename [<machine>] <name>
```

## hearth device revoke

Revoke a device's access (rotate any credentials it had)

```text
hearth device revoke <id>
```

## hearth passwd

Change the master password (other devices keep working)

```text
hearth passwd
```

## hearth recover

Forgot the password: set a new one with the recovery key (signs out all devices)

```text
hearth recover [flags]

Flags:
      --ca-cert string          PEM certificate to trust for a self-signed server
      --device string           device name (default: hostname)
      --email string            account email
      --insecure[=true|false]   allow plain HTTP to a non-loopback server
      --server string           server URL
```

## hearth recovery-key

Manage the account recovery key

```text
hearth recovery-key
```

## hearth recovery-key rotate

Issue a new recovery key and invalidate the old one (e.g. it was shown to someone)

```text
hearth recovery-key rotate
```

## hearth server

Run the sync server (SQLite + local directory by default; PostgreSQL and S3 optional)

```text
hearth server [flags]

Flags:
      --addr string                          listen address [$HEARTH_ADDR] (default "127.0.0.1:8787")
      --blobs string                         blob store: empty = <data>/objects, file:///path, or s3://bucket/prefix?endpoint=host:9000 [$HEARTH_BLOBS]
      --data string                          data directory (default database, blobs, TLS files) [$HEARTH_DATA] (default "./hearth-data")
      --db string                            metadata database: empty = SQLite in --data, sqlite:///path, or postgres://user:pass@host/db [$HEARTH_DB]
      --device-days int                      forget machines not seen for N days (offline, reclaimed or revoked: their entry, health report and login; 0 = keep) [$HEARTH_DEVICE_DAYS] (default 7)
      --dist-dir string                      directory with client builds hearth-<os>-<arch> for other platforms (scripts/build-dist.sh; default <data>/dist) [$HEARTH_DIST_DIR]
      --expire-days int                      mark sessions expired after N days without access (default 30)
      --gc-interval duration                 how often to run expiry (default 1h0m0s)
      --grace-days int                       delete expired sessions after N more days (default 7)
      --identity-broker string               platform identity broker for passwordless login, e.g. https://identity.example.com [$HEARTH_IDENTITY_BROKER_URL]
      --identity-broker-key string           system key used to sign identity-broker requests [$HEARTH_IDENTITY_BROKER_KEY]
      --no-client-auto-update[=true|false]   client daemons do not install new builds by themselves (hearth self-update still works) [$HEARTH_NO_CLIENT_AUTO_UPDATE=1]
      --no-version-check[=true|false]        do not look up the latest Codex CLI / Claude Code / codex-mixin releases (npm registry, GitHub; every 6 hours, through $HTTPS_PROXY; $HEARTH_NPM_REGISTRY for a mirror) for the machines' environment check [$HEARTH_NO_VERSION_CHECK=1]
      --pin-install[=true|false]             self-signed certificate: put --pinnedpubkey in the install one-liner, so even the first download is verified (default: plain -k; the installer then pins the certificate) [$HEARTH_PIN_INSTALL=1]
      --public-url string                    URL clients use to reach this server; enables /install.sh [$HEARTH_PUBLIC_URL]
      --quota string                         default storage quota per user, e.g. 10GiB (per-user: hearth server admin quota) [$HEARTH_QUOTA] (default "unlimited")
      --registration string                  who may register: invite, open, approval (admin approves each account) or closed; an admin can change it in the Hearth app [$HEARTH_REGISTRATION] (default "invite")
      --site string                          organisation defaults for every user: recipes/, files/, learn.json (default <data>/site) [$HEARTH_SITE]
      --site-auth[=true|false]               serve the site defaults only to logged-in users (default: anyone who can reach the server, so the installer gets them before login) [$HEARTH_SITE_AUTH=1]
      --tls-cert string                      TLS certificate file
      --tls-key string                       TLS key file
```

## hearth server invite

Create single-use registration invite codes (run on the server host)

```text
hearth server invite [flags]

Flags:
      --blobs string   blob store: empty = <data>/objects, file:///path, or s3://bucket/prefix?endpoint=host:9000 [$HEARTH_BLOBS]
      --count int      number of invites (default 1)
      --data string    data directory (default database, blobs, TLS files) [$HEARTH_DATA] (default "./hearth-data")
      --db string      metadata database: empty = SQLite in --data, sqlite:///path, or postgres://user:pass@host/db [$HEARTH_DB]
      --ttl duration   how long the invite stays valid (default 168h0m0s)
```

## hearth server gencert

Create a self-signed TLS certificate in the data directory

```text
hearth server gencert [flags]

Flags:
      --data string    server data directory (default "./hearth-data")
      --days int       validity in days (default 825)
      --host strings   IP address or DNS name clients connect to (repeatable)
```

## hearth server migrate

Copies every table and blob from the source (--data/--db/--blobs, default ./hearth-data) to an empty
destination (--to-data/--to-db/--to-blobs). The source is read in one snapshot; stop the server first
for a final cut-over so no writes are lost, then start it with the destination flags.

```text
hearth server migrate [flags]

Flags:
      --blobs string      blob store: empty = <data>/objects, file:///path, or s3://bucket/prefix?endpoint=host:9000 [$HEARTH_BLOBS]
      --data string       data directory (default database, blobs, TLS files) [$HEARTH_DATA] (default "./hearth-data")
      --db string         metadata database: empty = SQLite in --data, sqlite:///path, or postgres://user:pass@host/db [$HEARTH_DB]
      --to-blobs string   destination blob store URL [$HEARTH_TO_BLOBS]
      --to-data string    destination data directory
      --to-db string      destination database URL [$HEARTH_TO_DB]
```

## hearth server backup

Write a consistent backup (SQLite + blob files) into a new directory while the server runs

```text
hearth server backup <new-dir> [flags]

Flags:
      --blobs string   blob store: empty = <data>/objects, file:///path, or s3://bucket/prefix?endpoint=host:9000 [$HEARTH_BLOBS]
      --data string    data directory (default database, blobs, TLS files) [$HEARTH_DATA] (default "./hearth-data")
      --db string      metadata database: empty = SQLite in --data, sqlite:///path, or postgres://user:pass@host/db [$HEARTH_DB]
```

## hearth server admin

Opens the server's database and blob store directly (same --data/--db/--blobs as `hearth server`),
so it works while the server runs and needs no admin password. Changes reach running servers within 30 s.
Pass the same --quota as the server (or set $HEARTH_QUOTA) so default quotas are shown correctly.

```text
hearth server admin

Flags:
      --blobs string        blob store URL [$HEARTH_BLOBS]
      --data string         server data directory [$HEARTH_DATA] (default "./hearth-data")
      --db string           metadata database URL [$HEARTH_DB]
      --json[=true|false]   print JSON
      --quota string        server default quota, as given to hearth server --quota [$HEARTH_QUOTA]
```

## hearth server admin stats

Totals for the whole server

```text
hearth server admin stats
```

## hearth server admin users

List accounts with their usage (largest first)

```text
hearth server admin users
```

## hearth server admin user

Show one account

```text
hearth server admin user <email>
```

## hearth server admin disable

Block login and every token of an account (data is kept)

```text
hearth server admin disable <email>
```

## hearth server admin enable

Re-enable a disabled account, or approve one waiting (registration approval)

```text
hearth server admin enable <email>
```

## hearth server admin quota

Set an account's storage quota (e.g. 20GiB)

```text
hearth server admin quota <email> <size|unlimited|default>
```

## hearth server admin revoke-devices

Log out every device of an account (e.g. after a leaked password)

```text
hearth server admin revoke-devices <email>
```

## hearth server admin delete-user

Delete an account and all its sessions, library and secrets

```text
hearth server admin delete-user <email> [flags]

Flags:
      --yes[=true|false]   confirm
```

## hearth server admin grant

Make an account an admin of this server (it gets the administration pages in the Hearth app and can use hearth app admin-call)

```text
hearth server admin grant <email>
```

## hearth server admin ungrant

Remove an admin (not the last one)

```text
hearth server admin ungrant <email>
```

## hearth server admin admins

List admins

```text
hearth server admin admins
```

## hearth server admin invites

List invites (create with `hearth server invite`)

```text
hearth server admin invites
```

## hearth server admin invites revoke

Revoke an unused invite

```text
hearth server admin invites revoke <id>
```

## hearth server admin gc

Runs one expiry pass (as the server does hourly, with the server's default expiry settings), finishes queued
blob deletes, then removes blobs no database row refers to (older than --min-age).

```text
hearth server admin gc [flags]

Flags:
      --dry-run[=true|false]   only report
      --min-age duration       keep orphans younger than this (uploads in flight) (default 1h0m0s)
```

## hearth server start

Takes the same flags as `hearth server` (e.g. --data, --addr, --public-url, --db). The supervisor holds
<data>/supervisor.lock, logs to <data>/server.log (10 MiB x 5) and restarts the server with backoff when it
exits, or when /healthz fails 3 times in a row. Running it again is a no-op, so it is safe in cron or a
container entrypoint. Without systemd, add it to the entrypoint or `@reboot` cron to survive reboots.

```text
hearth server start [server flags]
```

## hearth server stop

Stop the supervised server (graceful)

```text
hearth server stop [flags]

Flags:
      --data string   server data directory (default "./hearth-data")
```

## hearth server restart

Restart the supervised server with the flags it was started with

```text
hearth server restart [flags]

Flags:
      --data string   server data directory (default "./hearth-data")
```

## hearth server status

Show the supervised server: pids, uptime, restarts, health

```text
hearth server status [flags]

Flags:
      --data string   server data directory (default "./hearth-data")
```

## hearth server install-service

Print a systemd unit for these server flags (when systemd runs; containers: use `hearth server start`)

```text
hearth server install-service [server flags]
```

## hearth site

The server admin can publish defaults for every user in the server's site directory
(hearth server --site, default <data>/site): recipes/*.toml, files/..., learn.json.
Clients fetch them at setup and with the library sync; a recipe or file of the same
name in your own library wins.

```text
hearth site
```

## hearth site disable

Do not apply a site recipe for you (synced choice)

```text
hearth site disable <recipe>
```

## hearth site enable

Apply a site recipe you disabled

```text
hearth site enable <recipe>
```

## hearth site pull

Fetch the site defaults now (works before login with --server)

```text
hearth site pull [flags]

Flags:
      --quiet[=true|false]   print nothing unless it fails
      --server string        server URL (default: the one you are logged in to)
```

## hearth site status

List the site defaults on this machine

```text
hearth site status
```

## hearth app

The macOS menu bar app: your machines at a glance, and the console in a window

```text
hearth app
```

## hearth app data

Print the account's overview, library and teams as JSON (the macOS app's own pages read it)

```text
hearth app data [flags]

Flags:
      --json[=true|false]   machine-readable
```

## hearth app admin-data

Print the administration pages' documents as JSON (admin accounts only; the macOS app reads it)

```text
hearth app admin-data [flags]

Flags:
      --json[=true|false]   machine-readable
```

## hearth app admin-call

Send one request to /v1/admin/... as the signed-in admin and print the JSON reply (the macOS app's admin actions use it)

```text
hearth app admin-call METHOD PATH [JSON-BODY]
```

## hearth app call

Send one request to /v1/me... or /v1/admin/... as the signed-in user and print the JSON reply (the macOS app's actions use it)

```text
hearth app call METHOD PATH [JSON-BODY]
```

## hearth app install

Install (or update) the menu bar app from your server into ~/Applications and start it

```text
hearth app install
```

## hearth app open

Start the menu bar app

```text
hearth app open
```

## hearth app uninstall

Quit and remove the menu bar app

```text
hearth app uninstall
```

## hearth codex-import

Import Claude Code sessions into Codex (alias for `agent import codex --from claude`)

```text
hearth codex-import [claude-session-file | hearth-session-id ...] [flags]

Flags:
      --dry-run[=true|false]   only show what would be imported
```

## hearth codex-threads

Codex rebuilds its thread list from restored rollouts but not names or sidebar sections; setup and restore apply them through `codex app-server`. Run this if Codex was installed later.

```text
hearth codex-threads
```

## hearth cp

Copies a file or a directory to, from or between your machines. The control channel goes over
Hearth's SSH; the data goes straight between the two machines over parallel TCP connections,
encrypted with a key that only went through SSH (never through the Hearth server). Large files
travel in chunks over every connection at once; an interrupted copy resumes when run again; files
whose size and time already match are skipped. When the machines cannot connect directly in either
direction, the data goes through SSH port forwards. When both see the target on a shared disk,
it is copied there directly.

  hearth cp ./ckpt gpu-a:/data/            push into /data/ckpt on gpu-a
  hearth cp gpu-a:/data/ckpt .              pull
  hearth cp gpu-a:/data/ckpt gpu-b:/data/   between two machines (straight from gpu-a to gpu-b)

A target that is an existing directory or ends in / receives the source inside it (like cp -r).

```text
hearth cp <[machine:]source> <[machine:]target> [flags]

Flags:
      --compress string           zstd per chunk, kept where it helps: auto (across regions), on, off (default "auto")
      --force[=true|false]        copy files even when their size and time match
  -n, --no-clobber[=true|false]   keep files that already exist at the target
  -q, --quiet[=true|false]        no progress
  -j, --streams int               parallel data connections (default: 16 nearby, up to 64 across regions)
```

## hearth docs

Documentation: how to install the agents' software (env), the command reference (cli)

```text
hearth docs [flags]
```

## hearth docs env

How to install and check Node, Codex CLI, Claude Code and codex-mixin, plus your organisation's notes from the server

```text
hearth docs env [flags]

Flags:
      --en[=true|false]   English
```

## hearth docs cli

Print the command reference as Markdown

```text
hearth docs cli [flags]

Flags:
  -h, --help[=true|false]   help for cli
```

## hearth env

The shell activation installed by env steps

```text
hearth env
```

## hearth env status

Show the generated script and rc hook

```text
hearth env status
```

## hearth env uninstall

Remove the managed block from ~/.bashrc and ~/.zshrc

```text
hearth env uninstall
```

## hearth identity

Passwordless login through the platform identity broker

```text
hearth identity
```

## hearth identity enable

Upload the platform unlock now (normally automatic)

```text
hearth identity enable
```

## hearth identity disable

Stop allowing passwordless login for this account

```text
hearth identity disable
```

## hearth identity status

Show whether a platform credential is available on this machine

```text
hearth identity status
```

## hearth mcp

Serve skill search over MCP on stdio (search_skills, get_skill, list_skills)

```text
hearth mcp [flags]

Flags:
      --skills-dir strings   additional skill directories to index (repeatable)
```

## hearth memory

Long-term memory shared by your agents and machines: remember, forget, list, index, hooks

```text
hearth memory
```

## hearth memory remember

Remember a fact or habit for all your agents (end-to-end encrypted)

```text
hearth memory remember <text> [flags]

Flags:
      --here[=true|false]   only for the current directory tree
      --scope string        only for this directory tree (e.g. a repository)
```

## hearth memory forget

Forget a remembered fact (id f:… from hearth memory list)

```text
hearth memory forget <id>
```

## hearth memory list

Remembered facts and habits

```text
hearth memory list
```

## hearth memory index

Bring the local memory index up to date now (the background sync does it every 5 min)

```text
hearth memory index
```

## hearth memory status

What the memory holds here, and the agent hooks

```text
hearth memory status
```

## hearth memory hooks

Hooks that add relevant memories to every prompt of Codex and Claude Code

```text
hearth memory hooks
```

## hearth memory hooks on

Install the hooks (default)

```text
hearth memory hooks on
```

## hearth memory hooks off

Remove the hooks (recall stays available as a command and MCP tool)

```text
hearth memory hooks off
```

## hearth onboard

Machine setup status and next steps (used by the hearth agent skill)

```text
hearth onboard
```

## hearth onboard status

What is set up on this machine and what to do next

```text
hearth onboard status [flags]

Flags:
      --json[=true|false]   JSON for agents
```

## hearth onboard install-skill

Install the hearth skill into every agent on this machine (--print shows it)

```text
hearth onboard install-skill [flags]

Flags:
      --print[=true|false]   print the skill instead of installing it
      --quiet[=true|false]   print nothing (setup tells the user about the skill)
```

## hearth recall

Search long-term memory: your conversations on every machine and agent, remembered facts, agents' notes (local, milliseconds)

```text
hearth recall [words ...] [flags]

Flags:
      --cwd string          rank memories of this directory higher (default: the current one)
      --json[=true|false]   JSON
  -n, --limit int           results (default 5)
      --show string         print one memory in full (id from the results)
```

## hearth register

Create an account (prints a one-time recovery key)

```text
hearth register [flags]

Flags:
      --ca-cert string          PEM certificate to trust for a self-signed server
      --email string            account email
      --insecure[=true|false]   allow plain HTTP to a non-loopback server
      --invite string           single-use invite code from the server admin
      --server string           server URL
```

## hearth save

Run every capture step now (the daemon also runs them on schedule)

```text
hearth save [flags]

Flags:
  -y, --yes[=true|false]   approve capture commands
```

## hearth shell

Your own lines of ~/.bashrc / ~/.zshrc / ~/.bash_aliases, kept encrypted for new machines ([shell] in config.toml)

```text
hearth shell
```

## hearth shell show

Print what Hearth keeps of each rc file on this machine (your lines, without the distribution default and Hearth's blocks)

```text
hearth shell show
```

## hearth shell save

Upload the changed rc files now (the background sync does it every 20 min)

```text
hearth shell save
```

## hearth shell restore

Write the stored lines as snippets in <hearth home>/shell and source them from the rc files (setup does this; --dry-run shows them)

```text
hearth shell restore [flags]

Flags:
      --dry-run[=true|false]   print what would be written, change nothing
```

## hearth shell uninstall

Take Hearth's lines out of this machine's rc files (the stored copy stays; stop syncing: [shell] sync = false)

```text
hearth shell uninstall
```

## hearth ssh

Every Linux machine of your account accepts SSH from your own devices: Hearth gives each device a key,
keeps the public keys (plus each device's ~/.ssh/id_*.pub) end-to-end encrypted, writes the keys of your
active devices into a marked block of ~/.ssh/authorized_keys, and runs the system sshd the way it is set
up by hand on GPU containers (public keys only, the machine's own addresses only, port 8022; an sshd already listening
there is used as it is). Revoking a device removes its keys everywhere within a minute.

  hearth ssh                 machines you can reach
  hearth ssh <machine>       log in (part of the name is enough)
  hearth ssh -t <machine> -- <command>   run a command that needs a terminal (QR-code login)
  hearth ssh config          Host entries in ~/.ssh/config (VS Code / Cursor Remote-SSH, scp, rsync)

```text
hearth ssh [machine] [-- command ...] [flags]

Flags:
  -t, --tty[=true|false]   give the command a terminal (logins that show a QR code or ask questions): hearth ssh -t <machine> -- <command>
```

## hearth ssh enable

Accept SSH on this machine (default on Linux)

```text
hearth ssh enable [flags]

Flags:
      --all[=true|false]   every machine of the account (synced setting)
```

## hearth ssh disable

Stop accepting SSH on this machine (--all: on every machine of the account)

```text
hearth ssh disable [flags]

Flags:
      --all[=true|false]   every machine of the account (synced setting)
```

## hearth ssh authorize

Also accept a key that is not a Hearth device (e.g. an existing laptop key)

```text
hearth ssh authorize <public key | file> [flags]

Flags:
      --name string   a name for the key (default: its comment)
```

## hearth ssh status

This machine's sshd: running, address, accepted keys

```text
hearth ssh status
```

## hearth ssh keys

Keys your machines accept

```text
hearth ssh keys
```

## hearth ssh unauthorize

Remove a key added with authorize

```text
hearth ssh unauthorize <id|name>
```

## hearth ssh config

Write Host entries for your machines into ~/.ssh/config (kept up to date by the daemon)

```text
hearth ssh config
```

## hearth transfer

Submits copy tasks to a transfer service that moves a directory from one storage system to another
on its own side (e.g. between two clusters' shared file systems), tracks them, and cancels them.
The data does not pass through this machine or the Hearth server, and it is not encrypted or versioned
by Hearth: use `hearth cp` or the normal sync for personal state, small files and anything that must
stay end-to-end encrypted. The server admin provides the service address, token and path rules
(transfer.json in the server's data directory); [transfer] in config.toml overrides them
(see docs/en/usage.md).

  hearth transfer meta                      storages, allowed directions, limits
  hearth transfer submit --from a@one:/data/ckpt --to b@two:/data/ckpt
  hearth transfer list | status <id> | cancel <id>

```text
hearth transfer
```

## hearth transfer meta

What the service can copy: storages, allowed directions, path hints, defaults and limits

```text
hearth transfer meta
```

## hearth transfer submit

Copies the source directory to the destination on the service's side. STORAGE is a name from
`hearth transfer meta`; PATH is the path on that storage. A path under a prefix listed in
[transfer.paths."STORAGE"] of config.toml is translated first (so a path as seen on this machine
works); a URL such as afs://... and any other absolute path are sent as given.

The destination directory must be empty. The plan is printed and you confirm it; without a terminal
pass --yes. --dry-run asks the service for a dry run. AFS needs a UGI: [transfer] afs_ugi_secret.

```text
hearth transfer submit --from STORAGE:PATH --to STORAGE:PATH [flags]

Flags:
      --checkers int           parallel checkers (default: the service's, at most its limit)
      --dry-run[=true|false]   ask the service for a dry run
      --from string            source, STORAGE:PATH
      --name string            task name (default: hearth-<destination directory>-<date>)
      --to string              destination, STORAGE:PATH (an empty directory)
      --transfers int          parallel file transfers (default: the service's, at most its limit)
  -y, --yes[=true|false]       do not ask; you confirm the destination directory is empty
```

## hearth transfer list

The service does not tell users apart, so by default only the tasks this machine submitted are shown
(their current state is fetched). --all lists the service's tasks, whoever created them.

```text
hearth transfer list [flags]

Flags:
      --all[=true|false]    every task on the service, not only this machine's
      --json[=true|false]   JSON output
      --keyword string      only tasks whose name or id contains this
  -n, --limit int           at most this many tasks (default 20)
      --status string       only this state (pending, serving, running, cleaning, succeeded, failed, canceling, canceled)
```

## hearth transfer status

State, progress and error of one task

```text
hearth transfer status <task-id> [flags]

Flags:
      --json[=true|false]   JSON output
```

## hearth transfer cancel

Asks the service to stop the task. The service does not tell users apart, so Hearth only cancels tasks
this machine submitted; --force cancels any task id, which can stop somebody else's copy.

```text
hearth transfer cancel <task-id> [flags]

Flags:
      --force[=true|false]   cancel a task this machine did not submit
```
