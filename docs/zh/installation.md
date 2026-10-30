# 安装指南

[English](../en/installation.md) · [返回 README](../../README.zh-CN.md)

Hearth 客户端是一个静态二进制，支持 Linux（x86_64、ARM64）和 macOS（Apple Silicon、Intel）。Windows 暂不支持，可在 WSL 中按 Linux 方式安装。

下文的 `<server>` 是你们部署的 Hearth 服务器地址，例如 `https://hearth.example.com:8787`。服务器首页 `https://<server>/` 会列出该服务器的完整安装命令和下载链接。

## 一键安装（推荐）

```bash
# 已有账号：安装、登录、恢复这台机器、启动后台同步
curl -fsSL https://<server>/install.sh | sh

# 新账号：开放注册的服务器
curl -fsSL https://<server>/install.sh | sh -s -- --new

# 新账号：邀请制服务器（邀请码由管理员 `hearth server invite` 生成）
curl -fsSL https://<server>/install.sh | sh -s -- --invite inv-xxxx

# 只安装，不登录（之后在 agent 里说「用 hearth」，或手动执行 hearth setup）
curl -fsSL https://<server>/install.sh | sh -s -- --install-only
```

服务器注册方式可用 `curl -fsS https://<server>/v1/info` 查看：`open`（任何能访问服务器的人可注册）、`invite`（需要邀请码）、`closed`（不接受新账号）。

### 自签证书的服务器

内网服务器常用自签证书，安装命令带公钥 pin：

```bash
curl -fsSL -k https://<server>/install.sh | sh
```

`-k` 只用于第一次下载安装脚本：脚本里带着服务器证书，之后 hearth 与服务器的所有通信都只信任这一份证书（首次使用即信任）。需要第一次下载也校验时，服务器加 `--pin-install`，安装命令会带上 `--pinnedpubkey`。安装脚本会把服务器证书保存为 `~/.hearth/ca.pem`，之后 hearth 与服务器的通信都用它校验。

### 安装脚本做了什么

1. 用 `uname` 判断系统和架构，下载对应安装包并校验 SHA-256。
2. 安装到 `~/.local/bin/hearth`（`HEARTH_BIN_DIR` 可改）。
3. 把该目录写入 `~/.bashrc`、`~/.profile`（以及已存在的 `~/.zshrc`、`~/.bash_profile`），只写一次；PATH 中已有可写目录（如以 root 运行时的 `/usr/local/bin`）时，再建一个软链接，当前终端也能直接用。
4. 从服务器获取组织默认配置（站点配方、登录 token 位置等，不需要登录），给本机已有的 Codex、Claude Code、Comate 安装 `hearth` skill。
5. 除非指定 `--install-only`，运行 `hearth setup`：在终端读取主密码（不回显），登录或注册，询问这台机器的工作目录（推荐你同类型机器用的目录，没有就推荐用户目录），恢复最近 10 条会话（`--sessions N` 改条数，0 为全部）、skills、凭证和安装步骤，启动后台同步。最后打印一句话（「恢复 <机器> 的 Codex 环境」），在另一台已登录 Hearth 的机器上告诉 agent，它会通过 `hearth ssh` 给这台机器装好软件。

### 安装选项

| 变量 / 参数 | 作用 |
| --- | --- |
| `--install-only` | 只安装，不运行 `setup` |
| `--email`、`--new`、`--invite <码>` | 传给 `hearth setup` |
| `HEARTH_BIN_DIR=<目录>` | 安装目录，默认 `~/.local/bin` |
| `HEARTH_HOME=<目录>` | 客户端状态目录，默认 `~/.hearth`（或工作区下的 `.hearth`） |
| `HEARTH_NO_SKILLS=1` | 不安装 `hearth` skill |
| `HEARTH_NO_PATH=1` | 不修改 shell 启动文件、不建软链接 |

变量写在 `sh` 前面，例如 `curl … | HEARTH_NO_SKILLS=1 sh -s -- --install-only`。

## 多人共用一个 Linux 账号

共享机器上（多人都用 `root` 等同一账号），每个人必须有自己的工作目录，Hearth 的状态和会话都放在里面：

```bash
mkdir -p /root/workspace/<你的名字> && cd /root/workspace/<你的名字>
curl -fsSL https://<server>/install.sh | sh -s -- --install-only
hearth init .
hearth setup --server https://<server> --email you@example.com
```

之后在该目录下运行的 hearth 命令都使用 `<工作目录>/.hearth`，只上传你自己的会话。

如果之前已经在没有工作目录的情况下登录过（`~/.hearth`），请把这份登录移进工作目录，而不是再登录一次（那样会多出一台设备，两边的后台同步互相抢同一批会话）：`hearth init --move-login <目录> && cd <目录> && hearth daemon start`。`~/.hearth` 的账号与工作目录的身份一致时，`hearth onboard status` 会给出这一步；`hearth setup` 不会在同一台机器上把同一账号再登录一次。

大家的配置都在同一个 `~/.bashrc` 里，同事的一行 `export CODEX_HOME=…` 就会决定你的会话用哪份 Codex 配置。有工作目录时，Hearth 会运行你自己的 sshd（这种情况下不接管手动启动的 sshd），它给你的会话打上标记（`SetEnv HEARTH_USER`）；rc 文件最末尾有一段由 Hearth 维护的配置，只对这些会话重新设置你的 `HEARTH_HOME`、`CODEX_HOME` 等 agent 目录，同事的会话不受影响。请通过它连接：`hearth ssh <机器>`，或 Hearth 写进 `~/.ssh/config` 的主机（Codex Desktop、VS Code、Cursor）。Codex 没走你的 codex-mixin 网关时，环境检查会报出来，并指出是哪个文件哪一行设置了 `CODEX_HOME`。

在这种账号上，你的 Codex 配置不应该放在 `~/.codex`：它是所有 Codex 会话的默认配置，你的 codex-mixin 网关和 client key 放在那里，凡是 shell 没设置 `CODEX_HOME` 的人都会用到你的账户。在工作目录里新登录时，Hearth 会给你单独的 Codex 目录 `<工作目录>/.hearth/agent-homes/codex`（按机器区分，也不和工作目录里项目自己的 `.codex`（项目 skills、agents）混在一起）。如果 `~/.codex` 里已经是你的配置和会话，运行 `hearth agents isolate codex` 迁移：它把配置（skills、rules、插件、MCP、模型设置）和你的会话（按会话范围判断哪些是你的）复制到这个目录，让 codex-mixin 给新目录单独签发一个 client key，再把 `~/.codex` 交还给 codex-mixin（`uninstall-codex`；做不了时删掉网关 provider 和 key），清理前先备份到你的 Hearth 目录。这个选择按机器生效。之后 Hearth 命令和 daemon 不管 shell 继承了什么 `CODEX_HOME`，都用你自己的目录；`hearth agents where codex` 会打印它，例如 `CODEX_HOME="$(hearth agents where codex)" codex-mixin setup …`。你的网关登录还在共用的 `~/.codex` 时，环境检查会提醒。同一个账号下的人仍然能读到你的文件：这只能防误用，防不了有意为之。要真正隔离，只能用不同的 Unix 用户或容器。

机器上能找到多个人的身份（SSO token、Comate 登录）时，`hearth setup` 会把他们列出来，让你选自己是哪一个。各 agent 的配置目录（`~/.codex`、`~/.claude`）仍是大家共用的：注册在里面的 Hearth 记忆 hook 和 MCP 服务按 agent 会话所在的工作目录取数据，每个人拿到的都是自己的记忆和 skills。

## 手动安装

不能用管道执行脚本时：

```bash
mkdir -p ~/.local/bin ~/.hearth
BIN=hearth-$(uname -s | tr A-Z a-z)-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -fsSL -o ~/.local/bin/hearth https://<server>/download/$BIN && chmod +x ~/.local/bin/hearth
curl -fsSL -o ~/.hearth/ca.pem https://<server>/ca.pem      # 仅自签证书的服务器（命令同样加 -k）
curl -fsSL https://<server>/v1/dist                         # 对照 SHA-256：sha256sum ~/.local/bin/hearth
export PATH="$HOME/.local/bin:$PATH"                        # 并写入 ~/.bashrc
hearth onboard install-skill                                # 给本机 agent 装 skill
hearth setup --server https://<server> --email you@example.com
```

| 平台 | 安装包 |
| --- | --- |
| Linux x86_64 | `https://<server>/download/hearth-linux-amd64` |
| Linux ARM64 | `https://<server>/download/hearth-linux-arm64` |
| macOS Apple Silicon | `https://<server>/download/hearth-darwin-arm64` |
| macOS Intel | `https://<server>/download/hearth-darwin-amd64` |

离线机器：在能访问服务器的机器上下载安装包和 `ca.pem`，拷贝过去后按上面的步骤放置。离线机器仍需能访问服务器才能同步。

## 从源码构建

```bash
git clone https://github.com/hearth-project/hearth-cli hearth && cd hearth
go build -o ~/.local/bin/hearth ./cmd/hearth          # Go 1.26+
scripts/build-dist.sh ./dist                           # 4 个平台的安装包
```

## 升级

```bash
hearth self-update
```

一般不需要手动执行：后台同步进程每 30 分钟（启动后 5 分钟先查一次）检查服务器上的安装包，有新版本就下载、校验 SHA-256、原地替换，用新版本重启自己，并更新本机 agent 里的 `hearth` skill。服务器管理员发布新安装包后，在线的客户端半小时内会自动跟上。

`hearth self-update` 立即执行同样的更新。其他控制：

```bash
hearth self-update --auto status   # 当前版本、是否自动更新
hearth self-update --auto off      # 本机关闭自动更新（on 重新打开）
```

服务器端用 `hearth server --no-client-auto-update` 可以让所有客户端都不自动更新（手动 `self-update` 仍可用）。

## 卸载

```bash
hearth daemon stop                      # 先停止后台同步（会先上传未同步的内容）
rm ~/.local/bin/hearth                  # 以及 /usr/local/bin/hearth（若安装脚本建了软链接）
rm -r ~/.codex/skills/hearth ~/.claude/skills/hearth ~/.comate/skills/hearth
rm -r ~/.hearth                         # 本机状态和缓存的密钥；工作区模式下是 <工作目录>/.hearth
```

最后删除 `~/.bashrc`、`~/.profile` 等文件中带 `added by the hearth installer` 注释的一行。服务器上的数据不会因此删除；需要删除账号请联系管理员（`hearth server admin delete-user`）。
