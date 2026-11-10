<h1 align="center">Hearth</h1>

<p align="center">
  <b>Coding Agent 的随身工作现场。</b><br>
  Codex / Claude Code / Comate 的会话、skills、凭证和环境，换到哪台机器都跟着你。<br>
  <sub>自托管 · 端到端加密 · 服务端和客户端是同一个 Go 程序</sub>
</p>

<p align="center">
  <b>简体中文</b> · <a href="README.en.md">English</a>
</p>

<p align="center">
  <a href="https://github.com/hearth-project/hearth-cli/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/hearth-project/hearth-cli/actions/workflows/ci.yml/badge.svg"></a>
  <img alt="Platforms" src="https://img.shields.io/badge/platform-Linux%20%7C%20macOS-lightgrey">
  <img alt="Arch" src="https://img.shields.io/badge/arch-x86__64%20%7C%20arm64-blue">
  <img alt="E2EE" src="https://img.shields.io/badge/encryption-end--to--end-green">
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
</p>

<p align="center">
  <a href="#为什么需要-hearth">为什么</a> ·
  <a href="#快速上手">快速上手</a> ·
  <a href="#路线图长期记忆">路线图</a> ·
  <a href="#功能">功能</a> ·
  <a href="#agent-支持情况">Agent 支持</a> ·
  <a href="#文档">文档</a> ·
  <a href="#开发">开发</a>
</p>

---

## 为什么需要 Hearth

AI coding agent 常常分散在好几台机器上：GPU 机器、随时会被回收的容器、笔记本。每换一台机器，都要重装 agent、拷 skills、重新登录 git 和 SSH，上一台机器上的对话也找不回来了。

Hearth 就是 agent 的随身工作现场：这些状态放在团队自己的服务器上，换到哪台机器都能原样恢复。

- **每台机器一条命令。** 装好客户端就自动登录，恢复最近的会话、skills、agents、凭证和 shell 配置，之后在后台持续同步。
- **剩下的交给 agent。** 安装时会给每个 agent 装上 `hearth` skill。说一句「用 hearth」，就能恢复更早的会话、选择上传哪些内容，或者一步步装好这台机器的软件环境。
- **服务器看不到你的数据。** 会话和凭证在客户端加密，服务器只存密文。
- **共用一份，不重复。** 一套 skills 和 agents 同时供 Codex、Claude Code、Comate 使用；团队里用得最多的会自动共享。

```text
  机器 A ─────┐                                 ┌── 机器 B（新）
  Codex       │    加密同步        ┌──────────┐  │   curl …/install.sh | sh
  Claude Code ├──────────────────▶ │  Hearth  │ ─┤   → 会话、skills、登录全部回来
  Comate      │                    │  服务器  │  │
  skills、ssh ┘                    └──────────┘  └── 机器 C …
```

---

## 快速上手

服务端和客户端是同一个 `hearth` 程序（Linux、macOS，x86_64、ARM64）。服务器部署一次，之后每台机器装一次客户端。

### 1. 部署服务器（一条命令）

在所有开发机都能访问到的一台 Linux 机器上执行：

```bash
curl -fsSL https://raw.githubusercontent.com/hearth-project/hearth-cli/main/scripts/deploy-server.sh \
  | sh -s -- --host <服务器 IP 或域名>
```

脚本会下载最新 release（还没有 release、且装了 Go 1.26+ 时从源码构建），生成自签名证书，以守护进程方式在 8787 端口启动服务。最后会打印客户端安装命令，下载页 `https://<服务器>:8787/` 上也有同一条命令。再执行一次就是升级。

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--host` | 无（必填） | 客户端访问用的 IP 或域名 |
| `--port` | `8787` | 监听端口 |
| `--data` | `/var/lib/hearth`（root）/ `~/hearth-server` | 数据库、文件、证书 |
| `--registration` | `invite` | `invite`、`open`（内网适用）、`approval`、`closed` |
| `--tls-cert`、`--tls-key` | 自签名 | 使用自己的证书 |

<details>
<summary>其他方式：Docker、systemd、PostgreSQL + S3</summary>

```bash
docker compose -f deploy/docker-compose.yml up -d                       # 单容器，SQLite + 数据卷
docker compose -f deploy/docker-compose.yml exec hearth hearth server invite
hearth server install-service --data /var/lib/hearth …                  # 打印 systemd unit
```

多实例 + PostgreSQL + S3 见 `deploy/docker-compose.scale.yml`。备份、迁移、配额、管理后台等全部服务端内容见 [服务器指南](docs/zh/server.md)。
</details>

邀请用户：`hearth server invite --data <目录>`（`--registration open` 时不需要）。注册账号后，把自己设为管理员：`hearth server admin grant <you@example.com> --data <目录>`。管理页面在 macOS 菜单栏小程序里（`hearth app install`）；其他系统用 `hearth server admin ...`。

### 2. 安装客户端（每台机器一条命令）

从服务器下载页复制安装命令，形如：

```bash
curl -fsSL -k https://<服务器>:8787/install.sh | sh                          # 已有账号，或开放注册
curl -fsSL -k https://<服务器>:8787/install.sh | sh -s -- --invite <邀请码>  # 邀请制服务器上的第一台机器
```

`-k` 只用于第一次从自签名服务器下载；之后客户端只信任这台服务器的证书。在 Mac 上，这条命令还会装上菜单栏小程序（`--no-app` 或 `HEARTH_NO_APP=1` 跳过）。

安装脚本会把 `hearth` 装到 `~/.local/bin`，给 Codex / Claude Code / Comate 装上 `hearth` skill，询问主密码和这台机器的工作目录，然后启动 SSH 和后台同步；会话、凭证、agent 环境在需要时再取：

```text
Downloading hearth for linux-amd64 ... done
Installed hearth 0.1.0 to ~/.local/bin/hearth

Welcome to Hearth 0.1.0

This sets up gpu-node-13 (linux x86_64, 8 x NVIDIA H800) from your other machines.

  account     you@example.com (from comate login you)
  password
  login       signed in

What should this machine be called?
The name is shown in your machine list and is its SSH host name.
Give it the name of an offline machine to continue that one (its conversations and workspace).

  - Press ENTER to keep the name below
  - Or type another

[h800-gpu-node-13] >>>

Where do you work on this machine?
Hearth keeps this machine's state and your agents' homes there.
The directory below is where gpu-node-12 (GPU h800) works.

  - Press ENTER to use the directory below
  - Or type another directory

[/data/you] >>>

Setting up this machine ... done

  name        h800-gpu-node-13 (gpu-node-13)
  workspace   /data/you
  library     42 skills and 3 agents, set up for an agent when you restore its environment
  sync        on, in the background
  ssh         h800-gpu-node-13 (10.0.0.13:8022)

Hearth is ready on h800-gpu-node-13.

Next steps:

  1. Set up the software (codex, codex-mixin and 2 more).
     On any machine with Hearth, tell its agent:

       恢复 h800-gpu-node-13 的 Codex 环境

Your agents' skills, skill search and memory, credentials and shell lines are set up when
you restore this machine's environment; conversations with hearth restore --last 10;
everything now: hearth setup --full.
Anything else: ask Codex "用 hearth", or run hearth status.
Log: ~/.cache/hearth/setup-….log
```

推荐的工作目录来自你同类型的机器：先看同 GPU 型号，再看同 GPU 系列，再看同系统同架构；没有同类机器就推荐用户目录；这台机器上没挂载的盘上的目录不会被推荐。直接回车采用，也可以输入其他路径。接续某台离线机器时（主机名相同，或用了它的名字），直接进入那台机器的工作目录，不再询问。

### 3. 在另一台机器上恢复软件环境

新机器上已经有你的文件；软件环境（Node、Codex CLI、网关……）由 agent 来装。在**任意一台已登录 Hearth 的机器**上，把 setup 最后打印的那句话告诉它的 agent，例如「恢复 h800-gpu-node-13 的 Codex 环境」。它会查看新机器（`hearth machine list`），按这类机器的恢复流程，通过 `hearth ssh` 在新机器上逐步安装并检查；需要你本人操作的步骤（扫码登录）用 `hearth ssh -t` 交给你。手边没有别的机器时，也可以在新机器上直接说「用 hearth 恢复这台机器的环境」。Hearth 小程序的「机器」页（和 `hearth machine list`）能看到每台机器的环境检查结果。

新账号会打印一次**恢复密钥**，请离线保存（例如密码管理器），忘记主密码时只能靠它找回。不要把主密码或恢复密钥贴到任何对话里。

### 4. 开始使用

直接用自己的话跟 agent 说：

| 你说 | 会发生什么 |
| --- | --- |
| 「恢复 <机器> 的 Codex 环境」 | 按那类机器的恢复流程，通过 `hearth ssh` 给另一台机器装软件，每步都检查 |
| 「用 hearth 恢复这台机器的环境」 | 对当前这台机器做同样的事 |
| 「恢复上一台机器的会话」 | 按时间从新到旧列出，恢复你选的那几条（也能直接出现在打开的 Codex Desktop 窗口里） |
| 「不要上传 Claude Code 的会话」 | 修改上传选择，你所有机器都生效 |

也可以用命令行：

```bash
hearth status                        # 登录、同步、存储、还有什么没做
hearth session list                  # 所有机器的会话，从新到旧
hearth session restore <id> …        # 恢复选中的会话
hearth machine list                  # 你的机器：环境检查、SSH 主机名
hearth recipe plan                   # 这台机器的软件环境恢复步骤
hearth config --edit                 # 所有设置都在这一个文件里
```

每一步的详细说明见 [快速上手](docs/zh/quickstart.md)。

---

## 路线图：长期记忆

Hearth 搬运工作现场，现在也开始带上记忆：任何 agent 都能想起你在任何机器上做过的事（历史对话、决定、踩过的坑）和你让它记住的习惯；Codex 和 Claude Code 通过提问前 hook 自动带上相关记忆，毫秒级检索，依然端到端加密。试试 `hearth recall <关键词>`。设计和后续计划见 [记忆功能路线图](docs/zh/memory-roadmap.md)。本季度和下季度的全部计划见 [开发路线图](https://github.com/hearth-project/hearth-cli/issues/1)。

---

## 功能

| | |
| --- | --- |
| **会话** | 每 60 秒增量加密同步，机器被 kill 最多丢最后一分钟。可搜索的索引（标题、轮数、机器、最后活跃时间）让你只恢复需要的会话。home 或工作目录不同时自动改写会话里的路径。在 Codex 里删掉的会话在服务器上标为过期，长期不用的会被清理。 |
| **Skills 和 agents** | 一个个人库渲染到所有 agent（只存一份，软链接过去）。最常用的 skill 常驻，其余通过 `hearth` MCP 的 skill 检索按需加载，节省 agent 上下文。团队共享的包有版本：链接到受信任团队的包会自动跟随最新版本，也可以用 `--pin` 固定（`hearth lib links` 查看）。 |
| **凭证和 shell** | SSH key、git / gh、agent 登录和 token 文件作为端到端加密的 secret 保存，恢复时以 0600 权限写回。`~/.bashrc` / `~/.zshrc` 里你自己加的配置跟着你走，不会覆盖新机器原有的 rc 文件。 |
| **学习你的环境** | 后台同步会自动记录新的 skills、工具、环境变量和 MCP 配置；凭证（包括 skill 声明的凭证文件）默认也自动记录。不同类型的机器（H800、GB200、Mac……）可以有各自的步骤。 |
| **由 agent 恢复软件环境** | Hearth 自己不装软件。`hearth recipe plan` 列出这台机器的步骤和每步的检查命令，`hearth` skill 按 [客户端软件环境指南](docs/zh/client-env.md) 带你逐步完成。 |
| **上传内容自己选** | 按 agent 和类别（`@codex.sessions`、`!@claude.sessions`）、凭证、额外路径和排除规则，都写在一个跟随账号的 `config.toml` 里。 |
| **远程机器就绪检查** | 每台在线 Hearth 客户端只上报非敏感状态：系统、架构、GPU/机器 profile、工作目录、Hearth 版本、agent 版本、MCP 状态和待完成的恢复步骤。在另一台已登录机器上运行 `hearth machine list` 就能看到目标是否正常；用户说“恢复 Codex 环境”后，`hearth` skill 会按目标架构选择 profile，通过 SSH 在目标机执行对应恢复流程。 |
| **机器之间 SSH** | 登录过的 Linux 机器默认接受你自己设备的 SSH：设备公钥写进 `~/.ssh/authorized_keys` 里的一个标记块，系统 sshd 只开公钥认证、只监听本机自己的地址（bond0、主机名解析到的、路由用的；端口 8022；已有 sshd 在这个端口就直接用）。`hearth ssh <机器>`、用你平时的 key `ssh -p 8022`，或 `hearth ssh config` 给 VS Code / Cursor Remote-SSH 用。吊销设备后它的 key 在所有机器上失效。容器或防火墙后面、别人连不进去的机器也能连：它的 daemon 保持一条到服务器的出站连接，由服务器中转 SSH（服务器只看到密文，机器上不需要对外开放端口），详见[使用指南](docs/zh/usage.md#机器之间的-ssh)。 |
| **传文件和大数据** | `hearth cp` 在机器之间直连传输：并行 TCP、AES-GCM 加密、可断点续传，数据不经过服务器。`hearth transfer` 把大目录（模型、数据集）交给组织的 DataTool 兼容传输服务，在存储之间异步拷贝，服务端配置，用户无需额外设置，详见[使用指南](docs/zh/usage.md#在存储系统之间传大数据)。 |
| **团队** | 邀请码、角色、团队之间共享。每个成员最常用的 skills 和 agents 自动共享，每个团队最常用的自动常驻到成员的 agent 里。 |
| **服务端** | 默认 SQLite + 本地目录，多实例可用 PostgreSQL + S3。自带守护进程、日志轮转、配额、备份、迁移。macOS 菜单栏小程序：普通用户看自己的机器、资源库、团队、设置；管理员有数据面板、用户、注册审批、邀请码、审计日志。 |

---

## Agent 支持情况

| 能力 | Codex | Claude Code | Comate |
| --- | :---: | :---: | :---: |
| 同步、恢复会话和记忆 | ✅ | ✅ | ✅ |
| 跨机器改写路径 | ✅ | ✅ | — |
| 渲染 skills | ✅ | ✅ | ✅ |
| 渲染 agents 和指令文件 | ✅ | ✅ | — |
| MCP 配置和 skill 检索 | ✅ | ✅ | MCP 配置 |
| 恢复的会话显示在打开的应用里 | ✅ Desktop | — | — |
| 应用内删除同步到服务器 | ✅ | — | — |
| 导入其他 agent 的会话 | 从 Claude Code | — | — |
| 自动安装 `hearth` skill | ✅ | ✅ | ✅ |

`hearth agent list` 查看本机的情况。接入新的 agent 见 [Agent 接入](docs/zh/agent-integration.md)。

---

## 文档

| 主题 | 中文 | English |
| --- | --- | --- |
| 10 分钟部署服务器和客户端 | [快速上手](docs/zh/quickstart.md) | [Quick start](docs/en/quickstart.md) |
| 客户端安装、升级、卸载 | [安装指南](docs/zh/installation.md) | [Installation](docs/en/installation.md) |
| 新机器上装 Node / Codex CLI / Claude Code / codex-mixin | [客户端软件环境](docs/zh/client-env.md) | [Client software setup](docs/en/client-env.md) |
| 会话、上传选择、凭证、学习、团队 | [使用指南](docs/zh/usage.md) | [Usage](docs/en/usage.md) |
| 服务器运行与管理 | [服务器](docs/zh/server.md) | [Server](docs/en/server.md) |
| 常见问题与排查 | [故障排查](docs/zh/troubleshooting.md) | [Troubleshooting](docs/en/troubleshooting.md) |
| 接入新的 agent | [Agent 接入](docs/zh/agent-integration.md) | [Agent integration](docs/en/agent-integration.md) |
| 全部命令 | — | [CLI reference](docs/cli.md) |
| 内部实现 | — | [Technical reference](docs/en/reference.md) |
| 长期记忆：设计与进展 | [记忆功能路线图](docs/zh/memory-roadmap.md) | [Memory roadmap](docs/en/memory-roadmap.md) |
| agent skill | [skills/hearth/SKILL.md](skills/hearth/SKILL.md) | — |

---

## 开发

```bash
go test -race ./...                                                          # SQLite + 本地文件
HEARTH_TEST_POSTGRES=postgres://… HEARTH_TEST_S3=fake go test -race ./...   # PostgreSQL + S3
scripts/deploy-server.sh --host 127.0.0.1 --source --data /tmp/hearth       # 用当前代码起一个本地服务器
hearth docs cli > docs/cli.md                                                # 重新生成 CLI 参考
```

欢迎贡献，见 [CONTRIBUTING](CONTRIBUTING.md) 和 [行为准则](CODE_OF_CONDUCT.md)。获取帮助见 [SUPPORT](SUPPORT.md)，安全问题见 [SECURITY](SECURITY.md)，更新记录见 [CHANGELOG](CHANGELOG.md)。

## 许可证

[Apache License 2.0](LICENSE)
