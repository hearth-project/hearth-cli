# Installation

[中文](../zh/installation.md) · [Back to README](../../README.md)

The Hearth client is one static binary for Linux (x86_64, ARM64) and macOS (Apple Silicon, Intel). Windows is not supported yet; install the Linux build inside WSL.

`<server>` below is your Hearth server, e.g. `https://hearth.example.com:8787`. Its home page `https://<server>/` shows that server's exact install command and download links.

## One-line install (recommended)

```bash
# existing account: install, log in, restore this machine, start background sync
curl -fsSL https://<server>/install.sh | sh

# new account on an open server
curl -fsSL https://<server>/install.sh | sh -s -- --new

# new account on an invite-only server (codes come from `hearth server invite`)
curl -fsSL https://<server>/install.sh | sh -s -- --invite inv-xxxx

# install only (then tell your agent "use hearth", or run hearth setup yourself)
curl -fsSL https://<server>/install.sh | sh -s -- --install-only
```

`curl -fsS https://<server>/v1/info` tells the registration mode: `open` (anyone who can reach the server may sign up), `invite` (an invite code is needed) or `closed`.

### Self-signed servers

Internal servers often use a self-signed certificate; the install command then carries a key pin:

```bash
curl -fsSL -k https://<server>/install.sh | sh
```

`-k` is used for the first download only: the script carries the server certificate, and every later connection trusts exactly that certificate (trust on first use). To verify the first download as well, start the server with `--pin-install`; the command then includes `--pinnedpubkey`. The script stores the server certificate as `~/.hearth/ca.pem`; hearth verifies the server with it from then on.

### What the installer does

1. Detects OS and CPU with `uname`, downloads the matching build and checks its SHA-256.
2. Installs it to `~/.local/bin/hearth` (`HEARTH_BIN_DIR` to change).
3. Adds that directory to PATH in `~/.bashrc` and `~/.profile` (and an existing `~/.zshrc` / `~/.bash_profile`), once. If a writable directory is already on PATH (e.g. `/usr/local/bin` as root) it also links `hearth` there so the current terminal finds it.
4. Fetches the server's organisation defaults (site recipes, login-token locations; no login needed) and installs the `hearth` skill for the Codex, Claude Code and Comate found on the machine.
5. Unless `--install-only` is given, runs `hearth setup`: reads the master password from the terminal (no echo), logs in or registers, restores the newest 10 conversations (`--sessions N`, 0 = all), skills, credentials and setup steps, and starts background sync.

### Options

| Variable / flag | Effect |
| --- | --- |
| `--install-only` | install without running `setup` |
| `--email`, `--new`, `--invite <code>` | passed to `hearth setup` |
| `HEARTH_BIN_DIR=<dir>` | install directory, default `~/.local/bin` |
| `HEARTH_HOME=<dir>` | client state directory, default `~/.hearth` (or `.hearth` in the workspace) |
| `HEARTH_NO_SKILLS=1` | do not install the `hearth` skill |
| `HEARTH_NO_PATH=1` | do not touch shell startup files or create links |

Variables go before `sh`, e.g. `curl … | HEARTH_NO_SKILLS=1 sh -s -- --install-only`.

## Several people on one Unix account

On shared machines (everyone logs in as `root`, say) each person needs their own working directory; Hearth keeps its state and decides which sessions are yours from it:

```bash
mkdir -p /root/workspace/<you> && cd /root/workspace/<you>
curl -fsSL https://<server>/install.sh | sh -s -- --install-only
hearth init .
hearth setup --server https://<server> --email you@example.com
```

Hearth commands run inside that directory use `<dir>/.hearth` and only upload your own sessions.

Already logged in without a workspace (`~/.hearth`)? Move that login into the workspace instead of logging in again, which would make a second device whose sync competes with the first: `hearth init --move-login <dir> && cd <dir> && hearth daemon start`. `hearth onboard status` proposes it when `~/.hearth`'s account is the workspace's identity, and `hearth setup` refuses to log in the same account a second time.

## Manual install

When piping a script is not an option:

```bash
mkdir -p ~/.local/bin ~/.hearth
BIN=hearth-$(uname -s | tr A-Z a-z)-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -fsSL -o ~/.local/bin/hearth https://<server>/download/$BIN && chmod +x ~/.local/bin/hearth
curl -fsSL -o ~/.hearth/ca.pem https://<server>/ca.pem      # self-signed servers only (add -k as above)
curl -fsSL https://<server>/v1/dist                         # compare with: sha256sum ~/.local/bin/hearth
export PATH="$HOME/.local/bin:$PATH"                        # and add it to ~/.bashrc
hearth onboard install-skill                                # the hearth skill for this machine's agents
hearth setup --server https://<server> --email you@example.com
```

| Platform | Build |
| --- | --- |
| Linux x86_64 | `https://<server>/download/hearth-linux-amd64` |
| Linux ARM64 | `https://<server>/download/hearth-linux-arm64` |
| macOS Apple Silicon | `https://<server>/download/hearth-darwin-arm64` |
| macOS Intel | `https://<server>/download/hearth-darwin-amd64` |

Machines without direct access: download the build and `ca.pem` elsewhere and copy them over. Syncing still needs the server to be reachable.

## Building from source

```bash
git clone https://github.com/hearth-project/hearth-cli hearth && cd hearth
go build -o ~/.local/bin/hearth ./cmd/hearth          # Go 1.26+
scripts/build-dist.sh ./dist                           # builds for the 4 platforms
```

## Upgrading

```bash
hearth self-update
```

Usually you need not: the background daemon checks the server every 30 minutes (first 5 minutes after it starts). When there is a new build it downloads it, verifies the SHA-256, replaces the binary, restarts itself on it and refreshes the `hearth` skill in this machine's agents. After the admin publishes new builds, online clients follow within half an hour.

`hearth self-update` does the same right away. Other controls:

```bash
hearth self-update --auto status   # version, automatic updates on or off
hearth self-update --auto off      # turn automatic updates off on this machine (on: back on)
```

`hearth server --no-client-auto-update` stops every client from updating by itself (manual `self-update` still works).

## Uninstalling

```bash
hearth daemon stop                      # uploads pending data first
rm ~/.local/bin/hearth                  # and /usr/local/bin/hearth if the installer linked it
rm -r ~/.codex/skills/hearth ~/.claude/skills/hearth ~/.comate/skills/hearth
rm -r ~/.hearth                         # local state and cached key; <workspace>/.hearth in workspace mode
```

Then remove the line commented `added by the hearth installer` from `~/.bashrc`, `~/.profile` etc. Server data is not deleted; ask the admin to delete the account (`hearth server admin delete-user`).
