# 快速上手：10 分钟部署服务器和客户端

[English](../en/quickstart.md) · [返回 README](../../README.zh-CN.md)

本文从零开始：先在一台机器上部署 Hearth 服务器，再在开发机上安装客户端、创建账号，最后在第二台机器上一键恢复。示例中的地址都是占位符，请换成你自己的：

| 占位符 | 含义 | 示例 |
| --- | --- | --- |
| `<server-host>` | 客户端访问服务器用的 IP 或域名 | `hearth.example.com` |
| `<you@example.com>` | 你的账号邮箱 | `alice@example.com` |

服务器和客户端是同一个 `hearth` 二进制，支持 Linux / macOS（x86_64、ARM64）。

---

## 第一步：部署服务器（一次）

找一台开发机都能访问的机器（Linux，有一块持久磁盘）。

### 一条命令部署

```bash
curl -fsSL https://raw.githubusercontent.com/hearth-project/hearth-cli/main/scripts/deploy-server.sh \
  | sh -s -- --host <server-host> --registration open        # 内网用 open；公网保持默认 invite
```

脚本会：下载最新 release（还没有 release、且装了 Go 1.26+ 时从源码构建），把服务端和 4 个平台的客户端安装包放到 `/var/lib/hearth`（非 root 时为 `~/hearth-server`），生成自签名证书，以守护进程方式启动服务，最后打印客户端安装命令。再执行一次就是升级并重启。参数：`--port`、`--data`、`--tls-cert/--tls-key`（自己的证书）、`--version`、`--source`（强制从源码构建）。在仓库目录里也可以直接运行 `scripts/deploy-server.sh --host <server-host>`。

下面的 1.1、1.2 是脚本做的事情，想手动部署时照着做。

### 1.1 获取二进制

```bash
git clone https://github.com/hearth-project/hearth-cli hearth && cd hearth
go build -o /usr/local/bin/hearth ./cmd/hearth        # 需要 Go 1.26+
scripts/build-dist.sh /var/lib/hearth/dist             # 给客户端下载用的 4 个平台安装包
```

没有 Go 环境时，可以在任意有 Go 的机器上构建后把 `hearth` 和 `dist/` 拷过去。

### 1.2 生成证书并启动

```bash
D=/var/lib/hearth
hearth server gencert --data $D --host <server-host>          # 自签证书；有正式证书或反向代理时跳过
hearth server start --data $D --addr 0.0.0.0:8787 \
  --public-url https://<server-host>:8787 \
  --registration open                                          # 内网可用 open；对外请用默认的 invite
```

`hearth server start` 在后台运行服务器并自动守护（异常退出或无响应时重启，日志在 `$D/server.log`，自动轮转）。启动输出里有一行**安装命令**，形如：

```text
curl -fsSL -k https://<server-host>:8787/install.sh | sh
```

### 1.3 确认服务正常

```bash
hearth server status --data $D
curl -fsSk https://<server-host>:8787/healthz           # 输出 ok
```

用浏览器打开 `https://<server-host>:8787/`，下载页上有本服务器的安装命令、各平台下载链接和 SHA-256。**把这个地址发给要使用的同事即可。**

> 公司内部的配置（内网 git、代理、内部工具安装配方、SSO token 位置）放进 `$D/site/`，用户安装时自动拿到，见 [站点默认配置](server.md#站点默认配置组织内部的配方和文件)。
>
> 开机自启：有 systemd 时执行 `hearth server install-service --data $D --addr 0.0.0.0:8787 --public-url https://<server-host>:8787 --registration open` 生成 unit；容器里把上面的 `hearth server start …` 写进入口脚本。邀请制（`--registration invite`）下，用 `hearth server invite --data $D` 给每个新用户生成邀请码。

---

## 第二步：第一台开发机（创建账号）

在开发机终端执行下载页上的安装命令。开放注册的服务器上，账号不存在时 setup 会让你再输一次密码并直接创建账号（邀请制服务器在末尾加 `-s -- --invite <邀请码>`）：

```bash
curl -fsSL -k https://<server-host>:8787/install.sh | sh
```

安装脚本会：

1. 下载本机对应的安装包、校验 SHA-256，装到 `~/.local/bin/hearth` 并写入 PATH；
2. 给本机的 Codex / Claude Code / Comate 装上 `hearth` skill；
3. 让你设置**主密码**（不回显），创建账号，打印一次**恢复密钥**；
4. 把本机 Codex / Claude Code / Comate 里已有的 skills 和 agents 复制进你的个人库（看起来含凭证的不会复制，会列出来；`--no-import` 跳过），下一台机器直接就有；
5. 启动后台同步：会话 60 秒、skills/配置 20 分钟上传一次。最后一行 `done:` 汇总恢复了多少会话、skills、agents 和凭证文件，并列出下一步。

安装脚本不装其他软件。之后的新机器上，软件环境由 agent 来装，推荐在另一台机器上发起（见第三步）：agent 按你记录的恢复流程（`hearth recipe plan`）和 [客户端软件环境安装指南](client-env.md) 装 Node、Codex CLI、Claude Code、codex-mixin 等。

> 恢复密钥只显示一次，请保存到密码管理器。忘记主密码时只能靠它找回。不要把主密码或恢复密钥发给任何人或 agent。

登录后 setup 会询问这台机器的工作目录（第一台机器推荐用户目录；之后的机器推荐你同类型机器用的目录）。多人共用同一个 Linux 账号时，在这里输入你自己的目录（也可以在安装前 `mkdir -p ~/work/<名字> && cd ~/work/<名字> && hearth init .`）。

### 记录这台机器的环境

新加的 skills、环境变量、工具版本、MCP 配置和 shell 配置（`~/.bashrc` 里你自己加的部分）由后台同步自动记录；凭证在 setup 时问一次是否自动备份。剩下需要你决定的（例如本机副本和备份不一致的），`hearth status` 会提示。也可以在 Codex 或 Claude Code 里说：**「用 hearth 记录这台机器的配置」**。agent 会列出发现的 SSH key、git / gh 登录、agent 登录、常用工具和环境变量，由你选择哪些要在新机器上恢复。也可以在终端执行 `hearth learn`。

### 看一下现在的状态

```bash
hearth status
hearth backup status          # 本机上传哪些内容（默认全部：各 agent 会话与记忆、工作区文件、凭证）
hearth session list           # 已同步的会话
```

---

## 第三步：第二台机器（先安装，再从另一台机器恢复软件环境）

**3.1 在新机器上**执行同一条安装命令：

```bash
curl -fsSL -k https://<server-host>:8787/install.sh | sh
```

输入主密码后，setup 会推荐工作目录：你同类型机器用的目录（先看同 GPU 型号，再看同 GPU 系列如 B 卡、GB 卡，再看同系统同架构），没有同类机器就推荐用户目录。回车采用或输入其他路径，一定由你确认。然后自动恢复：

- 最近活动的 10 条会话（`hearth restore --last 30` 恢复更多，`hearth session restore <id>` 恢复指定的）；
- skills、agents、MCP 配置；
- SSH 身份（私用 ssh config + 它引用的私钥，以及 `GIT_SSH_COMMAND` / `sshx` / `scpx` 接线）、git 登录等凭证和认证文件；
- 之前记录的安装步骤（执行命令前会先给你看、征求确认）；
- 后台同步。

**3.2 在任意另一台已登录 Hearth 的机器上**恢复软件环境。setup 最后会打印要说的那句话，例如：

```text
On any other machine with Hearth, tell its agent:
      "恢复 b-node-gpu-0126 的 Codex 环境"
```

那台机器的 agent 会找到新机器（`hearth machine list`：环境检查、SSH 主机名），按这类机器的恢复流程，通过 `hearth ssh` 在新机器上逐步安装并检查 Node、Codex CLI、codex-mixin 等；需要你本人操作的步骤（扫码登录）用 `hearth ssh -t <机器> -- <命令>` 交给你。完成后新机器的环境检查在 `hearth machine list` 和 Web 控制台里变绿。手边没有别的机器时，在新机器的 agent 里说「用 hearth 恢复这台机器的环境」。

之后在 agent 里可以直接说：

- 「恢复上一台机器的会话」「找一下压测相关的对话」
- 「不要上传 Claude 的会话」「不要上传凭证」
- 「把 Claude 的会话导入 Codex」

---

## 常用命令速查

| 想做的事 | 命令 |
| --- | --- |
| 查看同步状态 | `hearth status`、`hearth daemon status` |
| 列出 / 检索会话 | `hearth session list`、`hearth session find <关键词>` |
| 恢复指定会话 | `hearth session restore <id> [<id> …]` |
| 选择上传内容 | `hearth backup status`、`hearth backup disable claude.sessions` |
| 保存一个认证文件 | `hearth secret set <名字> --file <文件>` |
| 升级客户端 | 后台进程自动更新；立即更新：`hearth self-update` |
| 管理用户 / 配额（服务器上） | `hearth server admin users --data $D`、`hearth server admin quota <邮箱> 20GiB --data $D` |

## 下一步

- [安装指南](installation.md)：手动 / 离线安装、选项、卸载
- [客户端软件环境安装指南](client-env.md)：Node、Codex CLI、Claude Code、codex-mixin 怎么装、怎么检查、去哪下最新版
- [使用指南](usage.md)：会话、备份选择、凭证、learn、团队
- [服务器部署与管理](server.md)：Docker、systemd、PostgreSQL + S3、备份迁移
- [故障排查](troubleshooting.md)
