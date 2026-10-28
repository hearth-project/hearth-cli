# Agent 接入规范（Agent Integration）

[English](../en/agent-integration.md) · [返回 README](../../README.zh-CN.md)

本文档定义 Hearth 如何接入一个 agent。目标是：**核心同步、加密、索引、恢复逻辑只有一套；每个 agent 的差异被收敛到可替换的适配层。** 新增 agent 时不改核心流程，只新增适配代码和测试。

代码位置：

| 层 | 路径 | 职责 |
|---|---|---|
| 核心引擎 | `internal/client/engine.go`、`restore.go`、`deleted.go` | 发现文件、增量上传、租约、过期、恢复循环、状态文件 |
| 文件适配器 | `internal/adapter/adapter.go` | agent 的目录布局、文件分类（session/memory/config）、相对路径安全校验 |
| Agent 集成接口 | `internal/client/agentintegration.go` | 会话分组、恢复后钩子、移动文件识别、删除识别、跨 agent 导入 |
| Codex 集成 | `internal/client/codexintegr.go`、`codexstate.go`、`codexdesktop.go`、`codeximport.go` | Codex 专属实现 |
| CLI | `cmd/hearth/` | `hearth agent list/import`、`hearth session list/find/show/restore` |

## 1. 分层原则

核心代码**不允许**出现 `if agent == "codex"` 这类分支。agent 差异必须通过以下两个接口表达：

1. `adapter.Adapter`：这个 agent 的文件在哪里、哪些文件需要同步。
2. `client.AgentIntegration`：这个 agent 的逻辑会话如何划分、恢复后还要做什么、如何识别删除、如何导入其他 agent。

新增 agent 的最小改动：

```text
internal/adapter/xxx.go          // 目录与文件分类
internal/client/xxxintegr.go     // AgentIntegration 实现
internal/client/agentintegration.go // 注册一行
internal/client/xxx_test.go      // 单元 + 端到端测试
docs/zh/agent-integration.md, docs/en/agent-integration.md  // 能力矩阵更新
```

## 2. 标准适配流程

每个 agent 接入必须回答下面这些问题，缺一项就说明接入不完整。

### 2.1 文件发现与分类

- agent 的配置目录是什么？是否支持环境变量覆盖（例如 `CODEX_HOME`、`CLAUDE_CONFIG_DIR`）？
- 会话文件在哪个子目录？是否按日期/项目/cwd 分目录？
- 哪些是会话（需要增量上传、可恢复）？
- 哪些是长期记忆（memory，必须固定、不过期）？
- 哪些是配置、skills、agents、hooks、MCP（走 library/secrets，不走 session）？
- 是否有子 agent、续写分段、临时文件、锁文件需要排除？
- 符号链接、路径穿越、超长路径如何处理？

实现位置：`internal/adapter`，返回 `SessionFile{Agent, Root, RelPath, AbsPath, Kind, Thread...}`。

### 2.1.1 用户备份选择

agent 适配器只负责“发现”。是否上传由核心统一按用户的 `config.toml` `[backup]` 判断。agent 文件按 `<agent>.<kind>` → `<agent>` → `<kind>` 的顺序取第一条用户写过的规则：例如 Claude Code 会话依次查 `@claude.sessions`、`@claude`、`@sessions`。kind 来自适配器分类（sessions / memory）。工作区个人文件归 `@workspace`，凭证归 `@credentials`；`!` 路径排除对所有文件生效。

新 agent 只要注册 integration 并声明 `Capabilities().Sessions/Memory`，`hearth backup enable/disable <agent>[.kind]` 就自动可用；不需要、也不应该自己实现上传开关。

### 2.2 会话身份与分组

一个“会话”在磁盘上可能不是单个文件：

- Codex：一个 thread 可以有主 rollout、续写分段和子 agent rollout，必须归为同一会话。
- 其他 agent 可能一个 session 一个 JSONL，也可能一个目录。

集成必须提供：

```go
ConversationKey(relPath string) string // 空字符串表示一个文件一个会话
IsPart(relPath string) bool            // 是否是主文件的附属文件
```

核心按 `ConversationKey` 做“最近 N 条”计数和整组恢复，绝不按文件数误算。

### 2.3 索引字段

每个会话必须有可检索索引，内容与实际聊天文件分开存储：

| 字段 | 说明 |
|---|---|
| `title` | agent 原生标题优先（如 Codex thread 名），否则首条用户请求摘要 |
| `turns` | 用户请求数（不是消息数） |
| `last_active` | 内容中最后一次写入的时间 |
| `writer` | 最后写入机器（hostname） |
| `path` / `cwd` | 原始相对路径、工作目录 |
| `agent` | agent 名称 |
| `size` / `status` | 服务端行提供 |

索引要求：

- **增量计算**：只解析上次扫描位置之后的新字节，不能每轮重读整段历史。
- 半行（文件正在写）留到下一轮，不得计入。
- 索引版本变化时允许全量重算一次（`sessionIndexVersion`）。
- 索引元数据加密保存；标题/内容不落服务端明文。
- 索引更新不能改变会话活动时间，也不能复活已过期会话。

### 2.3.1 工作目录与路径映射

- `SessionCwd(absPath)` 由集成解析该 agent 的会话格式，核心不写 agent 专属 JSON 规则。
- `PathMap=true` 的 agent 在跨机器恢复时改写会话内的绝对路径；Claude Code 额外设置 `ClaudePaths=true`，因为其项目目录名由 cwd 编码而来。
- 无 cwd 的格式（如 Comate）返回空字符串，路径映射关闭。

### 2.4 上传与恢复语义

- 上传为追加 chunk，必须支持 generation、offset、幂等重试和租约。
- 本机文件被重写/截断时开新 generation。
- 恢复写入必须原子（临时文件 + rename），失败不能留下半个文件。
- 恢复后 mtime 使用内容最后活动时间，保证 agent 自己的排序正确。
- 不同机器工作目录不同：恢复时按路径映射改写会话中的绝对路径；追加的新行上传前反向翻译。
- 恢复后要记录“服务器已有全部字节”，避免 daemon 重复上传。
- 恢复一条会话时，如果它依赖父会话/附属文件，必须一起恢复。

### 2.5 恢复后钩子（AfterRestore）

每个 agent 的 UI/index 更新方式不同，统一放在 `AfterRestore`：

```go
AfterRestore(e *Engine, all []RemoteSession, restored []RestoredSession, opts RestoreOptions) error
```

典型动作：

1. 恢复依赖（Codex fork 的父线程）。
2. 让 agent 重建自己的索引（Codex：`codex app-server` 的 `thread/read`）。
3. 应用标题、置顶、分组等 UI 元数据。
4. 如果 agent 有常驻 UI，把恢复结果显示在**当前窗口**，而不是要求用户重启。
5. 记录显示副本，避免重复 fork/复制。

Codex 当前实现：

- 通过 `~/.codex/app-server-control/app-server-control.sock` 连接正在运行的 Desktop；
- `thread/fork` 会让打开的窗口立即出现会话（fork 引用父历史，不复制内容）；
- 原线程归档，避免重启后重复；
- 删掉 Desktop 副本时，归档的原会话也标为过期。

### 2.6 删除识别

“归档/移动”不等于删除。集成必须能区分：

- agent 自己移动文件（Codex `sessions/` ↔ `archived_sessions/`）：仍是同一会话，不能当新会话上传。
- 用户删除会话（Codex：rollout 和状态库行同时消失）：调用
  `POST /v1/sessions/{id}/expire`，之后 `restore/setup` 跳过。
- 大批文件同时消失（换/清空 agent home）：不得批量过期，只记录一次警告。
- 只处理“本机确实出现过”的文件，避免把恢复到其他目录的会话误判为删除。

### 2.7 跨 agent 导入

跨 agent 导入应由目标 agent 自己的转换器完成，Hearth 只负责搬运和编排：

```go
Import(e *Engine, sourceAgent string, paths []string) (results []ImportResult, live bool, err error)
```

- 目标 agent 负责格式转换、去重、生成新会话 ID。
- Hearth 负责：把源会话从服务器恢复到本机、调用导入、报告结果。
- 有常驻 UI 时优先走其官方接口，使窗口立即显示。
- 源格式和工具调用细节由目标 agent 自己维护，Hearth 不复制解析器。

Codex 已支持从 Claude Code 导入（`externalAgentConfig/detect` + `import`）；其他 agent 通过实现同一方法获得同样能力。

### 2.8 能力声明

```go
AgentCapabilities{
    Sessions: true,       // 同步会话
    Memory: true,         // 同步长期记忆
    Threads: true,        // 一个会话可能多文件
    LiveUI: true,         // 能把恢复结果显示到正在运行的 UI
    DeleteSync: true,     // 能识别用户删除并标记服务端过期
    PathMap: true,        // 恢复时改写会话里的绝对路径
    ClaudePaths: false,   // 路径是否编码在 Claude 项目目录名中
    ImportFrom: []string{"claude"},
    IndexFields: []string{"title", "turns", "last_active", "writer", "thread_name", "section"},
}
```

`hearth agent list` 展示能力矩阵，便于用户和开发者确认某个 agent 支持到什么程度。

## 3. Codex 接入映射（参考实现）

| 能力 | 实现 |
|---|---|
| 会话发现 | `adapter.Codex()`，`sessions/` + `archived_sessions/` |
| 记忆 | Codex memory 文件，adapter 分类为 `memory` |
| 会话分组 | `codexIntegration.ConversationKey`（thread id） |
| 索引 | `scanStats` 增量解析 response_item，标题/turns/last_active |
| 名称/分组 | `codexstate.go` 读写 Codex 状态库，`app-server` 应用 |
| 恢复后显示 | `codexdesktop.go`，unix socket + WebSocket，`thread/fork` |
| 父会话 | `CodexForkParent` + `restoreParents` |
| 移动识别 | `codexMoved`（按 rollout 文件名） |
| 删除识别 | `codexIntegration.ExpireDeleted`（rollout + state 行消失） |
| 导入 Claude | `ImportClaudeIntoCodex` |

## 4. 当前能力矩阵

| Agent | 会话同步 | 索引 | 多文件会话 | 恢复后显示 | 删除同步 | 导入 |
|---|---|---|---|---|---|---|
| Codex | 是 | 是 | 是 | 是（Desktop 运行中） | 是 | 从 Claude 导入 |
| Claude Code | 是 | 是（通用扫描） | 单文件 | CLI 文件恢复 | 待接入（当前不会误删） | 待接入 |
| Comate | 是 | 是（通用扫描） | 单文件 | 待接入 | 待接入（当前不会误删） | 待接入 |

## 5. 测试清单

每个新 agent 至少覆盖：

1. 目录发现：session/memory 分类正确，不误收配置和临时文件。
2. 增量索引：半行不计、追加后 turns/标题/时间正确。
3. 上传：新会话、追加、重写 generation、断点重试。
4. 恢复：跨机器路径改写、原子写入、mtime、已有文件冲突。
5. 多文件会话：按组计数和整组恢复。
6. 移动：agent 归档/改名后不产生第二个会话。
7. 删除：过期标记、不会因索引更新复活、批量消失保护。
8. UI：打开窗口能显示恢复会话（如果 LiveUI=true）。
9. 跨 agent 导入：重复导入跳过、内容完整。
10. 多机器：A 上传、B 恢复、B 继续写、A 再拉到新内容。

## 6. 命令约定

```bash
hearth agent list
hearth agent import codex --from claude [file | session-id ...]
hearth session list [-s 关键词] [--host 机器] [--status active] [--json]
hearth session find 关键词
hearth session show <id>
hearth session restore <id> [<id> ...]
hearth restore [<id> ...]           # 不带 id 时恢复最近 N 条
```

## 7. 非目标

- 不把 agent 的私有数据库格式复制进 Hearth 核心；由集成层通过官方接口操作。
- 不在服务端保存明文标题、聊天内容或凭证。
- 不要求用户重启 UI 才能看到恢复结果（有 LiveUI 的 agent）。
- 不保证不同 agent 的 tool-call 细节无损互转；跨 agent 导入以目标 agent 的转换能力为准。
