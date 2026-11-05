# 使用指南

[English](../en/usage.md) · [返回 README](../../README.zh-CN.md)

所有命令都可以加 `--help`，完整列表见 [命令参考](../cli.md)。也可以直接对 agent 说「用 hearth」，由 `hearth` skill 代为执行下面的操作。

## 1. 状态

```bash
hearth status                 # 一屏看全：登录、后台同步（上次检查/上传/错误）、存储、agent、library、下一步
hearth status --json          # 同上，给脚本 / agent 用
hearth onboard status         # 本机完整检查清单和下一步（--json 供 agent 使用）
```

后台同步由 `hearth setup` 启动，也可手动 `hearth daemon start | stop`。同步间隔：会话 60 秒，library（skills、agents、配置）20 分钟。容器重启后执行 `hearth up`；容器优雅退出前执行 `hearth flush`（适合作为 k8s preStop hook）。

## 2. 会话：查找与恢复

会话内容和索引分开存储。索引包含标题、轮数（用户请求数）、最后更新时间、最后写入的机器、大小、状态，列表和检索只读索引，不下载内容。

```bash
hearth session list                         # 最新 20 条和总数，最近活动在前；-n 0 列出全部
hearth session list --agent codex --host gpu-01 --status active
hearth session list --files                 # 连同备份文件、记忆一起列出
hearth session find 压测                     # 按标题 / 路径 / 工作目录 / 机器检索
hearth session show <id>                    # 一条的完整索引
hearth session list --json                  # 给脚本用
```

恢复：

```bash
hearth session restore <id> [<id> …]        # 指定一条或几条，ID 写唯一前缀即可
hearth restore                              # 终端里：先列出编号，选 1 3 5-7，回车 = 最近 10 条
hearth restore -y                           # 不询问，直接恢复最近 10 条（脚本里默认如此）
hearth restore --include-online             # 连同仍在线机器上的会话一起恢复
hearth restore --last 30                    # 最近 30 条
hearth restore --all                        # 全部（谨慎使用）
```

- 默认恢复只取**已下线机器**上最近活跃的 10 条：最后由仍在线机器（后台同步每分钟上报一次心跳，约 15 分钟没心跳算下线，正常停止时立即下线）写入的会话，那台机器还在用，默认不搬过来；列表里这类机器名后带 `●`。按 id 指定、在选择列表里按编号选，或加 `--include-online`，都可以照样恢复。新机器第一次 `hearth setup` 时如果在终端里，会列出这些会话并问一次是否一起恢复（回车 = 恢复）。
- Codex 的一段对话可能分成多个文件（续写、子 agent），会作为一个整体恢复；fork 出来的会话会连同父会话一起恢复。
- 不同机器 home / 工作目录不同时，会话中的绝对路径会改写成本机路径。
- Codex Desktop 正在运行时，恢复的会话会直接出现在打开的窗口里（以副本形式，原会话归档，避免重启后重复出现）；Desktop 没运行时，下次启动自动列出。找不到时在 Desktop 里搜索标题。
- `expired` 状态：在 agent 中删除，或超过 30 天（服务器可配置）未访问。批量恢复会跳过；按 ID 恢复会重新激活。
- 恢复后由本机继续同步。提示 `session is leased by another device` 表示另一台机器正在写；那台机器已不存在时执行 `hearth session take <id>`。

其他会话命令：`session pin | unpin`（固定后不会过期）、`session touch`（刷新访问时间）、`session rm`（删除服务器副本）、`session handoff <id>`（导出为任意 agent 可读的 Markdown 交接文档）。

## 3. 配置文件（只改一个文件）

所有可配置项都在 `<hearth home>/library/config.toml` 里，随个人库同步到所有机器。默认 home 是 `~/.hearth`，设了工作目录则是 `<工作目录>/.hearth`。查看或编辑：

```bash
hearth config                # 打印路径和内容
hearth config --edit         # 用 $EDITOR 打开
```

文件分节，每一节都可以省略：

| 节 | 作用 |
|---|---|
| `[skills]` | `dir` 指定 skills 唯一副本的存放位置；`resident` 指定常驻给所有 agent 的 skills（其余按需检索） |
| `[agents]` | `enabled` 启用的 agent；`[agents.homes]` 每个 agent 的配置目录 |
| `[backup]` | `include` / `exclude` 选择上传什么 |
| `[learn]` | `ignore` 不再询问的 learn 条目 |
| `[tools]` | `[[tools.pin]]` 固定 agent CLI 版本；`[[tools.custom]]` 自定义工具的检查/安装命令 |
| `[mcp]` | `[mcp.servers.<名字>]` 各 agent 共用的 MCP server |
| `[usage]` | `share_with` 把本机 skills 使用量分享给哪些团队（可选） |

旧版本分散的 `agents.json`、`backup.conf`、`tools.json`、`resident.txt`、`learn-ignore.json`、`workspaces.json`、`mcp.toml` 会在第一次运行时自动合并进 `config.toml` 并删除。登录信息（`config.json`）、本机自动更新开关、本机恢复了哪些 agent 属于机器本地状态，不在这个文件里。

### 上传范围

文件里列出了**所有能上传的东西**，每一类都能单独开关：

```toml
[backup.categories]
"codex" = true            # Codex 会话和记忆
"codex.sessions" = true
"codex.memory" = true
"claude" = true
"claude.sessions" = false  # 不上传 Claude Code 会话
"claude.memory" = true
"comate" = true
"comate.sessions" = true
"comate.memory" = true
"workspace" = true         # 工作区里的个人文件
"credentials" = true       # SSH key、git / 云登录凭证

[backup]
include = ["${WORKSPACE}/notes/", "~/.gitconfig"]   # 额外文件/目录
exclude = ["**/*.tmp", "~/.ssh/id_rsa"]             # 路径排除，优先级最高
```

- 分类之外的额外路径用 `include`；要排除具体文件用 `exclude`。
- 路径支持 `~/`、`${WORKSPACE}/`、`*`、`**`、`?`，以 `/` 结尾表示整个目录。
- 删掉某一行表示用默认值（上传）；以后新增的 agent 默认上传。
- 单个凭证文件（某个 SSH key、某个 token）通过 `hearth secret list` 查看和 `hearth secret add/rm` 管理，受 `credentials` 开关控制。

```bash
hearth backup status                         # 本机按分类实际上传多少文件、多大
hearth backup disable claude.sessions        # 等价于往 exclude 加 @claude.sessions
hearth backup enable codex
hearth backup add '${WORKSPACE}/notes/'
hearth backup exclude '**/*.tmp'
```

## 4. 凭证与认证文件

凭证作为端到端加密的 secret 保存，新机器在 `setup` 时写回原位置（权限 0600）。

```bash
hearth secret list                                     # 只列名字
hearth secret set GITHUB_TOKEN                         # 隐藏输入
hearth secret set file-ku --file .config/ku/config.yaml --restore-to '${WORKSPACE}/.config/ku/config.yaml'
hearth secret restore                                  # 立即写回所有文件类 secret
hearth secret exec -- <命令>                           # 以环境变量形式注入大写名字的 secret
```

skill 或脚本需要 token 时，不要把真实值写进 skill：放在本地认证文件里（如 `${WORKSPACE}/.config/<工具>/…`），用上面的 `secret set --file` 加密备份，skill 从该文件读取。按需恢复的机器（所有新机器）上，凭证在用到时才取：agent 运行某个 skill 前调用 `hearth` MCP 工具 `prepare_credentials`，或者你执行 `hearth secret use <skill|名字>`（文件权限 0600，`--ttl` 后删除，默认 12 小时，也可以 `hearth secret clean` 立刻删；这台机器不会再上传它们）；`hearth secret exec [--for <skill>] -- <命令>` 只为这一条命令注入环境变量（加 `--for` 时连文件一起，命令结束即删）。凭证值不经过 agent。skill 自带的凭证文件（云 CLI 的 `credentials`、密钥、只有本人可读的文件）不会进明文 library：它们端到端加密同步，在你的其他机器上写回 skill 里。library 其他位置（配置、recipes、agents）发现疑似凭证仍会拒绝上传；确认是文档示例时，在该行加注释 `hearth:allow-secret`。

## 5. 记录这台机器的环境（learn）

大部分不用手动做：后台同步每 20 分钟（以及 `hearth setup` 结束时）自动记录新发现的项目，用的是 `hearth learn` 会给的建议（看起来和机器有关的只记给同类机器）：

- skills / agents：本机 agent 里新加的复制进个人库（疑似含凭证的不复制）；
- 设置：环境变量、CLI 版本、MCP 配置、工作目录里的个人文件等；
- 凭证（SSH key、agent / git 登录、token 文件）：setup 时问一次"是否自动备份"，同意后同样自动记录（端到端加密）；之后可用 `hearth learn --auto-credentials=true|false` 修改；
- shell：`~/.bashrc` / `~/.zshrc` 里你自己加的部分（去掉发行版默认内容、conda 等工具管理的块和 Hearth 自己的块）以及整个 `~/.bash_aliases`，加密保存。新机器上写到 `<hearth home>/shell/`，在 rc 文件里加一个受管块去 source，不覆盖新机器原有的 rc；新机器 rc 里已有的段落不重复，source 不存在的文件会被跳过。多台机器的修改会合并，在某台机器上删掉的段落会从备份里删掉。`hearth shell show` 查看会保存什么，`hearth shell save` / `restore` 立即执行。

只有两类需要你决定：已经记录过、但本机的副本和备份不同的（可能另一台机器的才是对的），以及没开自动备份时的凭证。`hearth status` 会提示数量，`hearth learn` 处理。关闭自动记录：`config.toml` 里 `[learn] auto = false`；关闭 shell：`[shell] sync = false`。

```bash
hearth learn                      # 交互式：一张清单列出全部发现，每项带建议（所有机器 / 同类机器），回车保存；--one-by-one 逐项问
hearth learn --list --json        # 列出发现的凭证、配置、工具、环境变量
hearth learn --accept id1,id2 --here id3 --never id4 \
  --step '$ pip install -U uv' --here-step '$ export NCCL_IB_HCA=mlx5' --step '登录 VPN'
```

- `accept`：所有机器都恢复（SSH key、git 身份、agent 登录、CLI 工具）。
- `here`：只在同类机器上恢复（按 GPU / 系统自动建机器档案，如 `h800`、`mac`）。
- `never`：不再询问。
- `--step` 写命令（新机器上经确认后执行）或文字提醒。

新机器上 `hearth setup` 只恢复其中的文件（凭证、配置）和环境变量；装软件、执行命令、扫码登录这些步骤组成这台机器的**恢复流程**（`hearth recipe plan`），由 agent 用 hearth skill 带着你一步步完成：推荐在另一台已登录 Hearth 的机器上说「恢复 <机器> 的 Codex 环境」（通过 `hearth ssh` 远程执行；`hearth machine list` 能看到每台机器的环境检查），也可以在这台机器上说「用 hearth 恢复这台机器的环境」：先跑每步的检查命令，按 [客户端软件环境安装指南](client-env.md)（`hearth docs env`）安装，再检查，完成后 `hearth recipe done <key>`。这台机器需要不同做法时，agent 会把新做法记回流程（`--here-step` 只给同类机器）。自己在终端执行也可以：`hearth recipe run <key>`，或整体 `hearth recipe apply`（列出全部命令、只问一次）。在任意一台机器上批准过的命令，在你的其他机器上直接执行，不再询问；批准记录端到端加密，服务器无法伪造，命令内容一改就会重新询问。不想共享批准时在 config.toml 设置 `[recipes] share_approvals = false`。机器档案：`hearth machine`（查看）、`hearth machine add <名字> --here`、`hearth machine use <名字>`。

### 扫码登录

在终端里显示二维码的登录（比如通过 DUCX 的 `codex-mixin setup`）是 `tty = true` 的 `manual` 步骤。在 `run` 里写上命令后，`hearth recipe login <key>` 会在你终端的前台运行它，二维码由程序自己画出来；命令成功后运行该步骤的检查并标记完成。环境变量来自 `set`，其中 `${secret:NAME}` 是你的某个 Hearth 密钥，在运行步骤的机器上取值（不会出现在命令行里）。

```toml
[[step]]
type = "manual"
name = "codex-mixin-login"
text = "登录 codex-mixin（扫码）"
tty = true
run = 'PATH="$HOME/.local/bin:$PATH"; codex-mixin setup --no-tui --preset baidu-oneapi --key "$ONEAPI_KEY" --quota-username me --codex-mode custom'
set = { ONEAPI_KEY = "${secret:ONEAPI_KEY}", https_proxy = "http://proxy.example.com:8080" }
done_when = 'PATH="$HOME/.local/bin:$PATH"; codex-mixin doctor --no-tui | grep -q "doctor: ok"'
```

agent 恢复另一台机器、走到这样的步骤时，不会在聊天里显示二维码：它执行 `hearth recipe login <key> --on <机器>`，先确认那台机器能运行这个步骤，再打印出一条命令交给你：

```bash
hearth ssh -t gpu-a -- 'PATH="$HOME/.local/bin:$PATH"; exec hearth recipe login learned/5dc36cca78'
```

在你自己的终端里运行它，扫那里画出的二维码，然后告诉 agent，它会检查结果。`hearth recipe login <key> --print` 在终端里也只打印命令；不带 `--on` 时在本机运行。只能运行计划里这类步骤（目标机器上的 Hearth 要有 `recipe login`：`hearth self-update`）。

### 组织默认配置

管理员可以在服务器上发布全员通用的配方（如内部工具安装、代理、SSO token 位置），`setup` 时自动拿到并与你自己的配方一起执行：

```bash
hearth site status                   # 本机拿到的站点配置
hearth site pull                     # 立即刷新
hearth site disable <配方>           # 不对我执行某个站点配方（随账号同步）
```

## 6. skills、agents 与团队

```bash
hearth lib import codex               # 把本机 agent（codex / claude / comate）已有的 skills / agents 收进 library
hearth lib status | push | pull | sync
hearth lib skills-dir                 # 查看 skills 唯一副本放在哪里（默认 <library>/skills）
hearth lib skills-dir '${WORKSPACE}/.agents/skills'   # 改位置：移动已有 skills、更新 agent 软链
hearth lib mcp-install                # 补注册 skill 检索 MCP（setup 已自动注册；按需加载 skill）
hearth lib search <关键词>
hearth lib stats --server             # 本账号所有机器的 skills 调用排行（先上传本机增量）
hearth lib resident --top 5           # 把服务端排名前 5 的 skills 设为常驻
hearth lib resident --top 5 --days 7  # 只看最近 7 天
hearth lib usage prune                # 清理已删除 / 改名 / 测试 skill 的调用统计（先列出再确认；--dry-run 只看）
hearth team create <团队>                     # 创建团队（你是 owner）
hearth team invite <团队>                     # 生成邀请码（默认 maintainer 可共享，--role member 只能用；--days 7，--uses N）
hearth team join <邀请码>                      # 加入；新机器上也可以 hearth setup --team <邀请码>
hearth team share <团队> <skill|agent> ...     # 把自己 library 里的 skill / agent 共享给团队（skill 里的凭证文件不会共享出去）
hearth team unshare <团队> <名字> ...          # 从团队里删掉，连同所有版本
hearth team share-with <团队> <另一个团队>      # owner：让另一个团队也能用（只读，--off 取消）
hearth team list                              # 我的团队、角色、各自共享了多少 skills / agents
hearth team share-top [--dry-run]             # 立即把使用最多的 5 个 skills 和 5 个 agents 共享给团队（后台每 20 分钟自动做）
hearth team use-top [--dry-run]               # 立即把每个团队成员用得最多的 20 个 skills 和 20 个 agents 常驻到你的所有 agent（后台自动做）
hearth lib publish skill <名字> --team <团队>
hearth lib source add git <名字> <git 地址>      # 加入 / 可读的团队会自动成为来源，无需手动添加
hearth lib find <关键词>; hearth lib link <skill>
```

library 在服务器上明文保存（仅本人可见，团队共享的内容团队成员和被授权的团队可见），因此不能包含凭证。setup 时如果你还没有团队，会问一次邀请码或新团队名，回车跳过。默认会把你自己 library 里使用最多的 5 个 skills 和 5 个 agents（所有机器、最近 30 天）共享给你能发布的团队（maintainer / owner），内容变了自动发新版本；会先做凭证扫描，命中的不共享。`config.toml` 里 `[teams] share_top = 0` 关闭，`share_exclude` 排除个别。agent 用量来自按类型启动子 agent 的记录（Codex `spawn_agent`、Claude Code `Task`），内置角色（explorer、worker 等）不计。

团队共享的内容怎么被调用：每个团队里成员用得最多的 20 个 skills 和 20 个 agents 会自动链接到你每台机器的所有 agent 目录，和自己的 skill / agent 一样出现在 agent 的列表里、被自动调用（`[teams] use_top`，`use_exclude` 排除；在控制台或用 `hearth lib unlink` 取消某个后不会再自动加回）。同名时你自己的版本优先。其余团队 skill 通过 `hearth mcp` 的 `search_skills` / `list_skills` 检索、`get_skill` 按需下载，用到的次数也计入团队排行，用得多了就会进入常驻。

## 7. Agent

```bash
hearth agent list                                    # 各 agent 支持哪些能力
hearth agents enable claude | disable comate         # 本账号接入哪些 agent
hearth agents pull claude                           # 新机器只恢复最近在用的 agent；其他的需要时手动拉取
hearth agent import codex --from claude [文件|ID]   # 把 Claude Code 会话导入 Codex（Codex 自带转换器）
hearth codex-threads                                 # 重新应用 Codex 线程名和分组
hearth onboard install-skill                         # 给本机 agent 安装 / 更新 hearth skill
```

## 8. 账号与设备

```bash
hearth device list | revoke <id>       # 设备丢失时撤销
hearth passwd                          # 修改主密码
hearth recover                         # 忘记密码：用恢复密钥重设（所有设备需重新登录）
hearth recovery-key rotate             # 恢复密钥泄露时换新
```

任何设备只要用主密码登录就能解密全部凭证，没有旧设备审批，所以主密码是凭证的唯一保护：请使用强密码，疑似泄露时立即 `device revoke`、`passwd` 并轮换相关凭证。

### 名字即机器

机器的磁盘被重置（比如容器镜像重启）后，再登录会成为一台新设备。主机名没变时，`hearth setup` 会直接接续那条离线的旧记录，不用确认。新机器用了某台离线机器的名字，也会接续它：可以在 setup 起名时输入，也可以用 `--alias`，或之后执行 `hearth device rename`（控制台和 Mac 菜单栏 App 里改名也一样）。接续之后：新机器用旧机器的名字（也就是它的 SSH 主机名）；旧机器上传过的会话都归到新机器名下，`hearth restore --last 10` 可以恢复回来；旧的工作目录在这台机器上也能访问时（集群共享盘没丢），setup 会再次推荐它，并沿用那里旧的 Hearth 目录（agent 目录、库、状态），只换上新的登录信息；旧记录退出登录，并从列表中移除。在线的机器名字不会被占用。

### 在机器之间传文件

```bash
hearth cp ./ckpt gpu-a:/data/            # 推送：gpu-a 上的 /data/ckpt
hearth cp gpu-a:/data/ckpt .              # 拉取
hearth cp gpu-a:/data/ckpt gpu-b:/data/   # 从 gpu-a 直接传到 gpu-b
```

控制信息走 Hearth 的 SSH；数据在两台机器之间直连，用多条并行 TCP 连接传输，以 AES-256-GCM 加密，密钥只经过 SSH，不经过 Hearth 服务器。大文件切成 4 MiB 的块，同时在所有连接上传（同机房 16 条，跨地域最多 64 条，`-j` 可指定），直接写到目标位置；多个文件的块交错传输，写入不会排队等同一个文件。中断后重新执行同一条命令即可续传；大小和修改时间一致的文件会跳过（`--force` 强制传，`-n` 保留目标端已有的文件）。跨地域时，能压缩的块用 zstd 压缩（`--compress auto|on|off`）。两台机器互相都连不上时，数据走 SSH 端口转发；对方的路径在本机也挂载的共享盘上时，直接在盘上复制。在两台别的机器之间传时，命令在源机器上执行；源机器连不到目标时，改在目标机器上执行。

实测（内存到内存，16 个 2 GiB 文件）：两台 bzz2 机器之间（400 Gb/s）9.7 GiB/s，yq01 到 johor（76 ms，64 条连接）1.4 GiB/s；单个大文件写本地盘约等于盘的速度（O_DIRECT，这里 3 GiB/s）。

### 在存储系统之间传大数据

`hearth cp` 在你的机器之间传数据。大目录（模型、数据集）放在共享文件系统上、要送到另一个集群的文件系统时，组织可以运行一个兼容 DataTool 的传输服务：存储系统之间在各自一侧直接复制，数据不经过本机，你退出登录后任务也继续运行。`hearth transfer` 就是这个服务的命令行。

**服务器管理员配置一次，用户不用配置任何东西。** 在服务器数据目录放 `transfer.json`（`<data>/transfer.json`，权限 0600；修改立即生效）：

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

每个已登录的客户端会自动取这份配置（`GET /v1/transfer/config`，必须登录，不在默认公开的 site 目录里），所以服务地址、token 和路径规则既不在开源代码里，也不在用户机器上。`{user}` 是账号名（邮箱去掉域名）；取第一条 `from` 是路径前缀的规则。可选项：`api_prefix`（默认 `/v1/datatool/api`）、`timeout`。token 对所有能登录这台服务器的用户可见；它是服务的共享 token，不是个人凭证。

属于个人的只有 AFS UGI，只有 `afs` 存储才需要：`hearth secret set afs_ugi` 把它端到端加密保存（名字来自 `afs_ugi_secret`）。

用户可以在 config.toml 的 `[transfer]` 里覆盖其中任何一项（`hearth config --edit`；这里不放任何密钥，`url` 也不能带密码）：`url`、`token_secret`（Hearth secret）或 `token_env`（环境变量）、`afs_ugi_secret`、`api_prefix`、`timeout`、`use_proxy = true`（默认直连，服务通常在内网）和 `[[transfer.paths."STORAGE"]]` 规则（会替换服务器上该存储的规则）。服务器的 token 只会发给服务器配置的那个地址：用户把 `url` 改成别的地址时，除非自己设置了 token，否则不带 token。

```bash
hearth transfer meta                                    # 存储、允许的方向、限制
hearth transfer submit --from a@one:/mnt/personal/ckpt --to b@two:/data/alice/ckpt
hearth transfer list                                    # 本机提交的任务（--all：服务上的全部任务）
hearth transfer status dt-20261105-3dbb92fd             # 状态、字节数、速度、剩余时间、错误
hearth transfer cancel dt-20261105-3dbb92fd
```

- **路径是存储端的路径，不是本机的。** `STORAGE:PATH` 里的存储名见 `hearth transfer meta`。在某条规则 `from` 前缀下的路径会被转换，计划里会显示；URL（`afs://...`）和其他绝对路径原样提交。相对路径只有在有规则能转换它时才接受，含 `..` 的路径会被拒绝。
- **先出计划。** `submit` 先按服务报告的方向和限制检查请求，打印计划并询问；没有终端时必须加 `--yes`。**目的目录必须为空**，服务无法替你检查。`--dry-run` 请求服务做试运行，`--transfers`、`--checkers` 在服务的上限内设置并行度。
- **凭证不会出现在输出里。** AFS UGI 只放进创建任务的请求，不会出现在计划、任务记录或错误信息里。
- **谁的任务。** 服务不区分用户（所有任务的创建者相同），所以 `list` 只显示本机提交的任务（记录在 Hearth 状态目录的 `transfers.json`，不含任何凭证）；`cancel` 默认拒绝不是本机提交的任务，`--force` 才能取消，这可能中断同事的复制。
- **不经过 Hearth 的加密和版本管理。** 它用于存储之间的大批量数据。个人状态、skills、小文件和必须端到端加密的内容，走普通同步或 `hearth cp`。

### 机器之间的 SSH

已登录的 Linux 机器只接受你自己设备的 SSH 连接：只认公钥、不开密码登录，只放行你未吊销设备的公钥；host key 从端到端加密发布的连接信息里校验，不需要手动确认"信任这台主机"。`hearth ssh <机器>` 和 Hearth 在 Mac 上写的 `~/.ssh/config` 条目，都用本设备的 Hearth 密钥连接。

- **公钥定期过期、自动续期。** 每台机器对一把设备公钥只放行 7 天（`[ssh] key_days`，`0` 表示不过期；需要 OpenSSH 8.2 及以上），daemon 每天续期。设备被吊销后，在下一轮同步时失效；daemon 没在运行的机器上，最晚 7 天后失效。
- **ssh-agent 转发**（`[ssh] forward_agent`，默认开启）：连接时转发本设备的 ssh-agent，机器上 `git push` 用的是本机已加载的 key（`ssh-add`，Mac 上 `ssh-add --apple-use-keychain`），私钥不用放到那台机器上。连接期间，那台机器上的 root 能借用转发过去的 agent（拿不走私钥）；不信任的机器请关掉。

## 9. Mac 菜单栏小程序

```bash
hearth app install      # 从服务器下载到 ~/Applications 并启动（再执行一次就是更新）
hearth app open         # 启动
hearth app uninstall    # 退出并删除
```

小程序在菜单栏显示一个火焰图标（有机器环境出错时变成实心）。下拉菜单列出你的机器（离线超过 12 小时的不显示，控制台里仍能看到）：是否在线、最近上线时间、GPU 和 SSH 状态、环境是否正常。鼠标移到一台机器上点复制按钮，或者右键它，可以复制机器名。单击展开能看到它的问题和操作按钮：「恢复环境」「检查」会打开 Codex 并填好针对这台机器的提示词（同时复制到剪贴板），「SSH 连接」打开终端连上它；在线的机器可以在这里改名（清空即恢复为主机名，同 `hearth device rename`）。列表下面：「打开控制台…」在窗口里打开网页控制台，已经登录好，也不用输主密码；「新增开发机」把一次性安装命令（`hearth machine install-command`，30 分钟内可用一次）复制到剪贴板，不在界面上显示；还有「刷新」「登录时启动」「退出 Hearth」。这台 Mac 的登录过期后（设备登录有效期 30 天），下拉菜单不再列出机器，只显示「重新登录」，点它会在终端里运行 `hearth login`，主密码在终端输入；控制台窗口的网页会话（12 小时）过期、页面回到登录表单时，会自动用新链接重新登录，「重新加载」也总是使用新链接。

小程序的所有操作都通过 `hearth` 命令完成（`hearth machine list --json`、`hearth panel --json`、`hearth machine install-command --json`、`hearth device rename`），所以这台 Mac 要先登录 Hearth；提示命令版本太旧时执行 `hearth self-update`。只有管理员在服务器上发布了小程序才能安装（见[服务器指南](server.md#发布新版本客户端)）。
