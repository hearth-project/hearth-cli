# Quick start: server and clients in 10 minutes

[中文](../zh/quickstart.md) · [Back to README](../../README.md)

From scratch: deploy a Hearth server on one machine, install the client on a dev machine and create an account, then restore a second machine with one command. Addresses below are placeholders; use your own:

| Placeholder | Meaning | Example |
| --- | --- | --- |
| `<server-host>` | IP or name clients use to reach the server | `hearth.example.com` |
| `<you@example.com>` | your account email | `alice@example.com` |

Server and client are the same `hearth` binary, for Linux / macOS (x86_64, ARM64).

---

## Step 1: deploy the server (once)

Pick a machine every dev machine can reach (Linux, with a persistent disk).

### One command

```bash
curl -fsSL https://raw.githubusercontent.com/hearth-project/hearth-cli/main/scripts/deploy-server.sh \
  | sh -s -- --host <server-host> --registration open        # open on an internal network; keep the default invite otherwise
```

The script downloads the latest release (or builds from source when there is none and Go 1.26+ is installed), puts the server and the client builds for 4 platforms into `/var/lib/hearth` (`~/hearth-server` when not root), creates a self-signed certificate, starts the supervised server and prints the client install command. Running it again upgrades and restarts. Options: `--port`, `--data`, `--tls-cert/--tls-key` (your own certificate), `--version`, `--source` (always build from source). From a clone: `scripts/deploy-server.sh --host <server-host>`.

Steps 1.1 and 1.2 below are what the script does, for a manual deployment.

### 1.1 Get the binary

```bash
git clone https://github.com/hearth-project/hearth-cli hearth && cd hearth
go build -o /usr/local/bin/hearth ./cmd/hearth        # Go 1.26+
scripts/build-dist.sh /var/lib/hearth/dist             # builds for the 4 client platforms
```

Without Go on that machine, build elsewhere and copy `hearth` and `dist/` over.

### 1.2 Certificate and start

```bash
D=/var/lib/hearth
hearth server gencert --data $D --host <server-host>          # self-signed; skip with a real certificate or a TLS proxy
hearth server start --data $D --addr 0.0.0.0:8787 \
  --public-url https://<server-host>:8787 \
  --registration open                                          # fine on an internal network; use the default invite otherwise
```

`hearth server start` runs the server in the background under a supervisor (restarted if it exits or hangs; log in `$D/server.log`, rotated). Its output contains the **install command**, like:

```text
curl -fsSL -k https://<server-host>:8787/install.sh | sh
```

### 1.3 Check it

```bash
hearth server status --data $D
curl -fsSk https://<server-host>:8787/healthz           # prints ok
```

Open `https://<server-host>:8787/` in a browser: the download page shows this server's install command, download links and SHA-256 checksums. **Share that address with your colleagues.**

> Company-specific setup (internal git, proxies, install recipes for internal tools, the SSO token location) goes into `$D/site/`; users get it automatically when they install. See [Site defaults](server.md#site-defaults-your-organisations-recipes-and-files).
>
> Start at boot: with systemd, `hearth server install-service --data $D --addr 0.0.0.0:8787 --public-url https://<server-host>:8787 --registration open` prints a unit; in a container put the `hearth server start …` line in the entrypoint. With `--registration invite`, create a code per new user with `hearth server invite --data $D`.

---

## Step 2: the first dev machine (create the account)

Run the install command from the download page. On an open server, setup creates the account when there is none (it asks for the password a second time); on invite-only servers append `-s -- --invite <code>`:

```bash
curl -fsSL -k https://<server-host>:8787/install.sh | sh
```

The installer:

1. downloads this machine's build, checks its SHA-256, installs it to `~/.local/bin/hearth` and adds it to PATH;
2. installs the `hearth` skill for Codex / Claude Code / Comate on this machine;
3. asks you to choose a **master password** (no echo), creates the account and prints the **recovery key** once;
4. copies the skills and agents your Codex / Claude Code / Comate already have into your library (items that look like they hold a credential are listed and left out; `--no-import` skips), so the next machine has them;
5. starts background sync: sessions every 60 s, skills / settings every 20 min. A final `done:` line sums up the conversations, skills, agents and credential files restored, followed by the next steps.

The installer installs no other software. On later machines the software is set up by your agent, preferably from another machine (Step 3): it follows your recorded restore flow (`hearth recipe plan`) and the [client software setup guide](client-env.md) to install Node, Codex CLI, Claude Code, codex-mixin and so on.

> The recovery key is shown once: store it in a password manager. It is the only way back if you forget the master password. Never send the password or the key to anyone, or to an agent.

After the login setup asks for your working directory on this machine (your home on the first machine; later machines get the one your machines of the same kind use). Several people on one Unix account: type your own directory there (or `mkdir -p ~/work/<you> && cd ~/work/<you> && hearth init .` before installing).

### Record this machine's setup

New skills, environment variables, tool versions, MCP servers and your shell lines (your own part of `~/.bashrc`) are recorded by the background sync; setup asks once whether credentials are kept automatically too. Whatever needs your choice (e.g. a copy that differs from the stored one) shows up in `hearth status`. You can also tell Codex or Claude Code: **"use hearth to record this machine's setup"**. The agent lists the SSH keys, git / gh logins, agent logins, tools and environment variables it found and lets you choose what new machines restore. Or run `hearth learn` in a terminal.

### Look around

```bash
hearth status
hearth backup status          # what this machine uploads (default: everything - every agent's sessions and memory, workspace files, credentials)
hearth session list           # synced conversations
```

---

## Step 3: a second machine (install, then restore the software from another machine)

**3.1 On the new machine**, run the same install command:

```bash
curl -fsSL -k https://<server-host>:8787/install.sh | sh
```

After the master password setup proposes the working directory: the one your machines of the same kind use (same GPU model, else the same GPU family such as B or GB series, else the same OS and CPU), or your home when none is alike. Press Enter or type another; you always confirm it. Then it restores:

- the 10 most recently active conversations (`hearth restore --last 30` for more, `hearth session restore <id>` for specific ones);
- skills, agents, MCP configuration;
- SSH keys, git logins and other credentials and auth files;
- the setup steps you recorded (commands are shown and need your yes first);
- background sync.

**3.2 From any other machine with Hearth**, restore the software. Setup ends with the sentence to say, e.g.:

```text
On any other machine with Hearth, tell its agent:
      "恢复 b-node-gpu-0126 的 Codex 环境"      (restore the Codex environment of b-node-gpu-0126)
```

That agent finds the new machine (`hearth machine list`: environment check and SSH host name), picks the restore flow for its kind, and installs and checks Node, Codex CLI, codex-mixin, … there over `hearth ssh`; steps that need you (a QR-code login) it hands to you with `hearth ssh -t <machine> -- <command>`. The new machine's environment check turns green in `hearth machine list` and the web console. No other machine at hand: ask the agent on the new machine "use hearth to restore this machine's environment".

From then on, just ask your agent:

- "restore my conversations from the last machine", "find the conversation about the benchmark"
- "don't upload Claude sessions", "don't upload credentials"
- "import my Claude sessions into Codex"

---

## Cheat sheet

| Task | Command |
| --- | --- |
| sync state | `hearth status`, `hearth daemon status` |
| list / search conversations | `hearth session list`, `hearth session find <words>` |
| restore specific ones | `hearth session restore <id> [<id> …]` |
| what is uploaded | `hearth backup status`, `hearth backup disable claude.sessions` |
| keep an auth file | `hearth secret set <name> --file <file>` |
| upgrade the client | automatic (daemon); right away: `hearth self-update` |
| users / quotas (on the server) | `hearth server admin users --data $D`, `hearth server admin quota <email> 20GiB --data $D` |

## Next

- [Installation](installation.md): manual / offline installs, options, uninstalling
- [Client software setup](client-env.md): installing and checking Node, Codex CLI, Claude Code, codex-mixin; where the latest versions are
- [Usage](usage.md): conversations, upload choices, credentials, learn, teams
- [Server](server.md): Docker, systemd, PostgreSQL + S3, backups, migration
- [Troubleshooting](troubleshooting.md)
