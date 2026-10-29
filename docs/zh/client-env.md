# 客户端软件环境安装指南

新机器上 `hearth setup` 只恢复数据和鉴权：登录、hearth skill、会话、skills / agents、凭证文件（SSH key、agent 登录、token）、shell 配置。**软件不由 Hearth 安装**：装好客户端后，在 agent 里说「用 hearth 恢复这台机器的环境」，agent 按 `hearth recipe plan` 列出的这台机器的恢复流程，参照本指南一步步装、一步步检查，每步完成后 `hearth recipe done <key>`。

本指南是通用版本。组织自己的镜像、代理、网关配置写在服务器的站点补充里，`hearth docs env` 会一起打印。

## 0. 先看清楚这台机器

```bash
uname -sm                  # 系统和架构：Linux x86_64 / Linux aarch64 / Darwin arm64 ...
id -un; echo $HOME         # 当前用户和 home（多人共用账号时进入自己的工作目录）
hearth status              # Hearth 是否登录、恢复了什么
hearth recipe plan         # 这台机器还要装 / 配置什么（来自你记录的恢复流程）
for c in node npm codex claude codex-mixin; do printf '%-12s ' $c; command -v $c >/dev/null && $c --version 2>&1 | head -1 || echo missing; done
type -a codex              # 同名命令（有的镜像自带别的 codex）排在前面时要区分
```

架构对应的下载名：`x86_64` → `x64` / `x86_64`，`aarch64`（ARM 服务器、GB 系列）→ `arm64` / `aarch64`，Mac Apple 芯片 → `darwin-arm64`。**按 `uname -m` 选包，不要照抄别的机器的命令。**

## 1. Hearth 客户端

- 安装 / 新机器恢复：服务器下载页上的一行命令（`curl -fsSL <server>/install.sh | sh`），会装好 `hearth`、给各 agent 装 hearth skill、登录并恢复数据。
- 检查：`hearth status`（版本、账号、后台同步）。
- 最新版本：服务器下载页（`<server>/`），或 GitHub Releases <https://github.com/hearth-project/hearth-cli/releases>；已安装的用 `hearth self-update`（默认每 30 分钟自动检查）。

## 2. Node.js（Codex CLI、Claude Code 的 npm 安装需要）

- 版本要求：Codex CLI 需要较新的 Node（以 <https://www.npmjs.com/package/@openai/codex> 的 `engines` 为准，目前 ≥ 22）；Claude Code 的要求见 <https://www.npmjs.com/package/@anthropic-ai/claude-code>。
- 检查：`node --version; npm --version`。
- 最新版本：<https://nodejs.org/en/download>，长期支持版的二进制在 `https://nodejs.org/dist/latest-v24.x/`（`SHASUMS256.txt` 是校验值）。下载慢时可用镜像，如 `https://npmmirror.com/mirrors/node/`。
- 系统自带的 Node 太旧（很多容器镜像是 v18）时，**不要替换系统 Node**，装到自己的目录：

```bash
V=v24.4.1                                   # 用最新的 LTS 版本号
case $(uname -m) in aarch64|arm64) A=arm64;; *) A=x64;; esac
OS=$(uname -s | tr A-Z a-z)                 # linux / darwin
F=node-$V-$OS-$A.tar.xz
cd /tmp && curl -fsSLO https://nodejs.org/dist/$V/$F
curl -fsSL https://nodejs.org/dist/$V/SHASUMS256.txt | grep " $F\$" | sha256sum -c -   # Mac: shasum -a 256 -c -
mkdir -p ~/.hearth/opt && tar -xJf $F -C ~/.hearth/opt && rm $F
~/.hearth/opt/node-$V-$OS-$A/bin/node --version
```

## 3. Codex CLI

- 最新版本：`npm view @openai/codex version`，或 <https://github.com/openai/codex/releases>（也有不依赖 Node 的独立二进制）。
- 安装：系统 Node 够新时 `npm install -g @openai/codex@<版本>`；npm 官方源慢时加 `--registry=<镜像>`。
- 用第 2 步自己目录里的 Node 时，装完写一个包装脚本固定 Node，不改 PATH：

```bash
NODE_HOME=~/.hearth/opt/node-v24.4.1-linux-x64      # 第 2 步的目录
PATH=$NODE_HOME/bin:$PATH npm install -g @openai/codex@<版本>
mkdir -p ~/.local/bin && cat > ~/.local/bin/codex <<EOS
#!/bin/sh
export PATH="$NODE_HOME/bin:\$PATH"
exec "$NODE_HOME/bin/codex" "\$@"
EOS
chmod +x ~/.local/bin/codex
```

- 检查：`codex --version`；`type -a codex` 第一个应是你装的。登录状态：Hearth 已恢复 `~/.codex/auth.json` 和 `config.toml` 时不用再登录，否则 `codex login`。
- 版本：默认装最新版（`npm view @openai/codex version`）。`config.toml` 的 `[[tools.pin]]` 记录要装哪些 CLI；只有写了 `version` 才固定在那个版本。

## 4. Claude Code

- 最新版本：`npm view @anthropic-ai/claude-code version`；官方文档 <https://docs.anthropic.com/en/docs/claude-code/setup>。
- 安装：官方安装脚本 `curl -fsSL https://claude.ai/install.sh | bash`（不依赖 Node），或 `npm install -g @anthropic-ai/claude-code`。
- 检查：`claude --version`。登录：Hearth 恢复了 `~/.claude/.credentials.json` 时不用再登录，否则运行 `claude` 按提示登录。

## 5. codex-mixin（可选：给 Codex 接其他模型服务的本地网关）

- 最新版本：<https://github.com/Edward-lyz/codex-mixin/releases>，或 `curl -s https://api.github.com/repos/Edward-lyz/codex-mixin/releases/latest | grep tag_name`。
- 按架构选包（Linux 用 musl 版）：`codex-mixin-cli-<版本>-$(uname -m)-unknown-linux-musl.tar.gz`；Mac 选 Releases 页面里对应 `apple-darwin` 的包。
- 下载并用 GitHub 给出的 sha256 校验：

```bash
VER=0.7.3                                   # 用最新的版本号
F=codex-mixin-cli-$VER-$(uname -m)-unknown-linux-musl.tar.gz
D=$(mktemp -d) && cd $D
curl -fsSLO https://github.com/Edward-lyz/codex-mixin/releases/download/v$VER/$F
curl -fsS https://api.github.com/repos/Edward-lyz/codex-mixin/releases/tags/v$VER \
  | python3 -c "import sys,json;[print(a['digest'].split(':')[1]+'  '+a['name']) for a in json.load(sys.stdin)['assets'] if a['name']=='$F']" \
  | sha256sum -c -
tar -xzf $F && mkdir -p ~/.local/bin && install -m 0755 */codex-mixin ~/.local/bin/codex-mixin
cd / && rm -r $D && codex-mixin --version
```

- **第一台机器**：`codex-mixin setup --preset <服务> --key <密钥> --codex-mode custom`，必须在交互终端里由用户自己跑（服务登录可能要扫码）。完成后 `hearth save --yes` 把它的配置加密备份到 Hearth。
- **其他机器**：已有备份时不用再登录，导入即可（`hearth recipe plan` 里的 import 步骤就是这几行）：

```bash
t=$(mktemp) && hearth secret get codex-mixin-config > "$t" \
  && codex-mixin config import --no-tui "$t" && codex-mixin config apply --no-tui \
  && codex-mixin service restart --no-tui; rm "$t"
```

- Codex 晚于 codex-mixin 安装时补一次：`codex-mixin agent connect codex --mode custom-only`。
- 检查：`codex-mixin doctor --no-tui`（应为 `doctor: ok`）、`codex-mixin provider list --no-tui`（STATUS healthy）、`codex-mixin service status --no-tui`（gateway running）。

## 6. Comate 等其他 agent

按各自官方文档安装。Hearth 恢复它们在 `~/.comate` 等目录下的登录、配置和 skills，装好后不用重新配置。

## 7. 最后检查

| 检查 | 命令 | 期望 |
|---|---|---|
| Hearth | `hearth status` | 已登录，后台同步 running |
| 恢复流程 | `hearth recipe plan` | `nothing left to do` |
| Codex | `codex --version`；`codex exec --skip-git-repo-check 'reply with ok'` | 版本号；回复 ok |
| Claude Code | `claude --version` | 版本号 |
| codex-mixin | `codex-mixin doctor --no-tui` | `doctor: ok` |
| 凭证 | `ssh -T git@github.com`（或你的 git 服务器）；`gh auth status` | 认证成功 |

## 8. 这台机器的装法不一样时

装的过程中发现这台机器需要别的做法（另一个代理、另一个安装目录、ARM 上没有某个包），做完后记下来，下一台同类机器就会照着做：

```bash
hearth learn --here-step '$ <这类机器才需要的命令>'   # 只给同类机器（按 GPU / 系统自动分档）
hearth learn --step '$ <所有机器都要的命令>'
hearth learn --step '<需要人做的事，例如扫码登录>'
```

agent 里由 hearth skill 用 `propose_setup_step` 记录，用户在 `hearth recipe proposals` 里确认。
