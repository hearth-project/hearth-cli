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

skill 或脚本需要 token 时，不要把真实值写进 skill：放在本地认证文件里（如 `${WORKSPACE}/.config/<工具>/…`），用上面的 `secret set --file` 加密备份，skill 从该文件读取。skill 自带的凭证文件（云 CLI 的 `credentials`、密钥、只有本人可读的文件）不会进明文 library：它们端到端加密同步，在你的其他机器上写回 skill 里。library 其他位置（配置、recipes、agents）发现疑似凭证仍会拒绝上传；确认是文档示例时，在该行加注释 `hearth:allow-secret`。

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
hearth team share <团队> <skill|agent> ...     # 把自己 library 里的 skill / agent 共享给团队
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
