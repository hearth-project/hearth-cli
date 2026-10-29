# Client software setup guide

On a new machine `hearth setup` restores data and credentials only: the login, the hearth skill, conversations, skills / agents, credential files (SSH keys, agent logins, tokens) and your shell lines. **Hearth does not install software.** Once the client is installed, ask your agent "use hearth to restore this machine's environment": it goes through this machine's restore flow (`hearth recipe plan`) step by step with you, using this guide to install and check each tool, and marks each step with `hearth recipe done <key>`.

This is the generic guide. Your organisation's mirrors, proxies and gateway settings live in the server's site supplement, which `hearth docs env` prints as well.

## 0. Look at the machine first

```bash
uname -sm                  # OS and architecture: Linux x86_64 / Linux aarch64 / Darwin arm64 ...
id -un; echo $HOME         # user and home (on a shared account, go to your own workspace)
hearth status              # logged in? what was restored?
hearth recipe plan         # what this machine still needs (from your recorded restore flow)
for c in node npm codex claude codex-mixin; do printf '%-12s ' $c; command -v $c >/dev/null && $c --version 2>&1 | head -1 || echo missing; done
type -a codex              # another command of the same name may come first on some images
```

Download names per architecture: `x86_64` → `x64` / `x86_64`, `aarch64` (ARM servers, GB-series) → `arm64` / `aarch64`, Apple silicon → `darwin-arm64`. **Pick the package from `uname -m`; do not copy another machine's command.**

## 1. Hearth client

- Install / restore a new machine: the one-line command on the server's download page (`curl -fsSL <server>/install.sh | sh`). It installs `hearth`, the hearth skill for each agent, logs in and restores your data.
- Check: `hearth status` (version, account, background sync).
- Latest version: the server's download page (`<server>/`) or <https://github.com/hearth-project/hearth-cli/releases>; installed clients: `hearth self-update` (checked every 30 minutes by default).

## 2. Node.js (for the npm installs of Codex CLI and Claude Code)

- Requirement: Codex CLI needs a recent Node (see `engines` on <https://www.npmjs.com/package/@openai/codex>, currently ≥ 22); Claude Code: <https://www.npmjs.com/package/@anthropic-ai/claude-code>.
- Check: `node --version; npm --version`.
- Latest: <https://nodejs.org/en/download>; LTS binaries under `https://nodejs.org/dist/latest-v24.x/` (`SHASUMS256.txt` holds the checksums). Mirrors help when it is slow, e.g. `https://npmmirror.com/mirrors/node/`.
- When the system Node is too old (v18 on many container images), **leave it alone** and install into your own directory:

```bash
V=v24.4.1                                   # the latest LTS
case $(uname -m) in aarch64|arm64) A=arm64;; *) A=x64;; esac
OS=$(uname -s | tr A-Z a-z)                 # linux / darwin
F=node-$V-$OS-$A.tar.xz
cd /tmp && curl -fsSLO https://nodejs.org/dist/$V/$F
curl -fsSL https://nodejs.org/dist/$V/SHASUMS256.txt | grep " $F\$" | sha256sum -c -   # macOS: shasum -a 256 -c -
mkdir -p ~/.hearth/opt && tar -xJf $F -C ~/.hearth/opt && rm $F
~/.hearth/opt/node-$V-$OS-$A/bin/node --version
```

## 3. Codex CLI

- Latest: `npm view @openai/codex version`, or <https://github.com/openai/codex/releases> (standalone binaries without Node too).
- Install: with a recent system Node, `npm install -g @openai/codex@<version>`; add `--registry=<mirror>` when the npm registry is slow.
- With the Node of step 2, add a wrapper that pins it instead of changing PATH:

```bash
NODE_HOME=~/.hearth/opt/node-v24.4.1-linux-x64      # the directory of step 2
PATH=$NODE_HOME/bin:$PATH npm install -g @openai/codex@<version>
mkdir -p ~/.local/bin && cat > ~/.local/bin/codex <<EOS
#!/bin/sh
export PATH="$NODE_HOME/bin:\$PATH"
exec "$NODE_HOME/bin/codex" "\$@"
EOS
chmod +x ~/.local/bin/codex
```

- Check: `codex --version`; `type -a codex` should list yours first. Login: not needed when Hearth restored `~/.codex/auth.json` and `config.toml`; otherwise `codex login`.
- Versions: the latest by default (`npm view @openai/codex version`). `[[tools.pin]]` in config.toml lists the CLIs to install; only a `version` there holds one back.

## 4. Claude Code

- Latest: `npm view @anthropic-ai/claude-code version`; docs: <https://docs.anthropic.com/en/docs/claude-code/setup>.
- Install: `curl -fsSL https://claude.ai/install.sh | bash` (no Node needed) or `npm install -g @anthropic-ai/claude-code`.
- Check: `claude --version`. Login: not needed when Hearth restored `~/.claude/.credentials.json`; otherwise run `claude` and follow the prompt.

## 5. codex-mixin (optional: a local gateway that connects Codex to other model services)

- Latest: <https://github.com/Edward-lyz/codex-mixin/releases>, or `curl -s https://api.github.com/repos/Edward-lyz/codex-mixin/releases/latest | grep tag_name`.
- Package per architecture (Linux: the musl build): `codex-mixin-cli-<version>-$(uname -m)-unknown-linux-musl.tar.gz`; macOS: the `apple-darwin` asset of the release.
- Download and check the sha256 GitHub publishes:

```bash
VER=0.7.3                                   # the latest version
F=codex-mixin-cli-$VER-$(uname -m)-unknown-linux-musl.tar.gz
D=$(mktemp -d) && cd $D
curl -fsSLO https://github.com/Edward-lyz/codex-mixin/releases/download/v$VER/$F
curl -fsS https://api.github.com/repos/Edward-lyz/codex-mixin/releases/tags/v$VER \
  | python3 -c "import sys,json;[print(a['digest'].split(':')[1]+'  '+a['name']) for a in json.load(sys.stdin)['assets'] if a['name']=='$F']" \
  | sha256sum -c -
tar -xzf $F && mkdir -p ~/.local/bin && install -m 0755 */codex-mixin ~/.local/bin/codex-mixin
cd / && rm -r $D && codex-mixin --version
```

- **First machine**: `codex-mixin setup --preset <service> --key <key> --codex-mode custom`, run by the user in an interactive terminal (the service login may show a QR code). Then `hearth save --yes` keeps its configuration, encrypted, in Hearth.
- **Other machines**: with a backup, no login; import it (the import step of `hearth recipe plan` is these lines):

```bash
t=$(mktemp) && hearth secret get codex-mixin-config > "$t" \
  && codex-mixin config import --no-tui "$t" && codex-mixin config apply --no-tui \
  && codex-mixin service restart --no-tui; rm "$t"
```

- Codex installed after codex-mixin: `codex-mixin agent connect codex --mode custom-only`.
- **An import that succeeds is not yet a working setup**: afterwards `codex-mixin doctor --no-tui` must say `doctor: ok`. With a degraded provider (`codex exec` answers "high demand") this machine needs the user's own login again, in a terminal: `codex-mixin setup ...` (from another machine: `hearth ssh -t <machine> -- ...`), then `hearth save --yes`.
- `api.github.com` rate-limited (403) behind a shared proxy: read the version from `https://github.com/Edward-lyz/codex-mixin/releases.atom` and the checksum from the release page, or use a logged-in `gh api`.
- Check: `codex-mixin doctor --no-tui` (`doctor: ok`), `codex-mixin provider list --no-tui` (STATUS healthy), `codex-mixin service status --no-tui` (gateway running).

## 6. Comate and other agents

Install them as their docs say. Hearth restores their logins, settings and skills (`~/.comate` ...), so they need no setup afterwards.

## 7. Final check

| Check | Command | Expect |
|---|---|---|
| Hearth | `hearth status` | logged in, background sync running |
| Restore flow | `hearth recipe plan` | `nothing left to do` |
| Codex | `codex --version`; `codex exec --skip-git-repo-check 'reply with ok'` | a version; ok |
| Claude Code | `claude --version` | a version |
| codex-mixin | `codex-mixin doctor --no-tui` | `doctor: ok` |
| Credentials | `ssh -T git@github.com` (or your git server); `gh auth status` | authenticated |

## 8. When this machine needs something else

If this machine needed another way (another proxy, another directory, no package for ARM), record it once it works, so the next machine of this kind follows it:

```bash
hearth learn --here-step '$ <command only machines like this need>'   # machines like this one (profile by GPU / OS)
hearth learn --step '$ <command every machine needs>'
hearth learn --step '<something a person does, e.g. a QR-code login>'
```

In an agent the hearth skill records it with `propose_setup_step`; the user confirms in `hearth recipe proposals`.
