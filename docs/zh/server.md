# 服务器部署与管理

[English](../en/server.md) · [返回 README](../../README.zh-CN.md)

服务器和客户端是同一个 `hearth` 二进制。服务器只保存密文（会话、凭证）和用户自己的 library，没有解密能力。

## 存储选择

| | 默认 | 可选（多实例、大数据量） |
| --- | --- | --- |
| 元数据（`--db`、`$HEARTH_DB`） | `--data` 目录下的 SQLite | `postgres://user:pass@host/db` |
| 对象（`--blobs`、`$HEARTH_BLOBS`） | `<data>/objects` | `s3://bucket/prefix?endpoint=host:9000&path_style=1`（AWS S3、MinIO、BOS、OSS、COS、Ceph），密钥用 `$HEARTH_S3_ACCESS_KEY` / `$HEARTH_S3_SECRET_KEY` 或 `AWS_*` |

SQLite + 本地目录不缺任何功能，限制在运维层面：单实例、单机磁盘；容器中数据卷必须持久化。需要多实例、负载均衡时换成 PostgreSQL + S3，可以随时用 `hearth server migrate` 迁移。

## 单机部署（推荐起步）

```bash
D=/var/lib/hearth
go build -o /usr/local/bin/hearth ./cmd/hearth            # 或从已有服务器下载对应平台的二进制

# 自签 TLS 证书，--host 写客户端访问用的 IP / 域名（可多次）；用反向代理终结 TLS 时跳过
hearth server gencert --data $D --host hearth.example.com

# 供下载的客户端安装包（linux/darwin × amd64/arm64）；不做这一步只提供服务器本平台的包
scripts/build-dist.sh $D/dist

# 后台运行（自带守护进程：退出或 /healthz 连续 3 次无响应时重启，日志轮转）
hearth server start --data $D --addr 0.0.0.0:8787 \
  --public-url https://hearth.example.com:8787 --registration open
hearth server status --data $D
```

启动时打印带公钥 pin 的安装命令。把服务器首页 `https://hearth.example.com:8787/` 发给用户即可，上面有安装命令、下载链接和 SHA-256（中英文）。

- `--registration`：`invite`（默认，每个新用户需要邀请码）、`open`（内网，任何能访问的人可注册）、`closed`。
- `--public-url`：客户端访问服务器用的地址，开启 `/install.sh`、`/download/*`、下载页。
- 所有参数都有对应环境变量（`HEARTH_DATA`、`HEARTH_ADDR`、`HEARTH_REGISTRATION`、`HEARTH_PUBLIC_URL`、`HEARTH_QUOTA` …），密码不必出现在命令行。
- 除回环地址外拒绝明文 HTTP：用 `server gencert`、`--tls-cert/--tls-key` 或反向代理。

### 开机自启

- **有 systemd**：`hearth server install-service <同样的参数>` 打印 unit 文件（`Restart=always`），保存到 `/etc/systemd/system/hearth.service` 后 `systemctl enable --now hearth`。仓库里也有 `deploy/hearth.service`。
- **容器 / 无 systemd**：把 `hearth server start …` 放进容器入口脚本或 `@reboot` cron；重复执行不会启动第二份。
- `hearth server stop | restart | status --data <目录>`。

### 日志

不记录每个请求。启动打印几行，GC 只在有变化时打印一行；重复错误折叠为每分钟最多 5 条加计数，TLS 握手噪音丢弃。守护进程下日志为 `<data>/server.log`，10 MiB 轮转、保留 5 个（最多 60 MiB）。

## Docker

```bash
docker compose -f deploy/docker-compose.yml up -d                            # 单容器，SQLite + 数据卷
docker compose -f deploy/docker-compose.yml exec hearth hearth server invite  # 邀请码
docker compose -f deploy/docker-compose.scale.yml up -d --scale hearth=2      # PostgreSQL + MinIO + 多实例
```

每次发布都会推送镜像 `ghcr.io/hearth-project/hearth`（标签 `latest`、`<版本号>`、`<主版本>.<次版本>`；linux/amd64 和 linux/arm64），compose 文件默认使用它。加 `--build` 则用本地代码构建镜像。Docker 相关文件有改动时，CI 会构建镜像并启动服务器检查。

设置 `HEARTH_PUBLIC_URL` 并提供 TLS（`hearth server gencert` 写入数据卷，或前置 HTTPS 反向代理）后才提供安装脚本。

## 站点默认配置（组织内部的配方和文件）

公司内部的配置（内网 git 地址、代理、模型网关的安装配方、公司 SSO token 的位置）不应该写进开源代码，放在服务器的站点目录里即可。目录默认 `<data>/site`（`--site` 或 `$HEARTH_SITE` 修改），结构与个人 library 相同：

```text
<data>/site/
  recipes/*.toml     所有用户的恢复配方（安装工具、写配置、检查、手动步骤）
  files/client-env.md  客户端软件安装指南的组织补充（镜像、代理、网关配置），`hearth docs env` 打印在通用指南之后
  files/...          配方安装用的文件（如 ssh 配置模板）
  learn.json         {"token_files": ["~/.config/sso/token_*"], "identity": {"email_domain": "example.com"}}：登录 token 与本地身份配置
```

- **安装脚本在登录之前就会拉取**（`hearth site pull --server <url>`），之后 `hearth setup`、`hearth recipe apply` 和后台 library 同步（20 分钟）时刷新；接口 `GET /v1/site` 默认不需要登录（内容不变时返回 304），保存在 `<hearth home>/site`。只想给已登录用户时加 `--site-auth`（此时安装阶段拿不到，登录后才生效）。改目录无需重启服务器，10 秒内生效。
- 用户自己 library 里同名的配方或文件优先；用户可以 `hearth site disable <配方>` 退出某个站点配方（选择随账号同步）。`hearth site status` 查看本机拿到的内容。
- 站点配方里执行命令的步骤和个人配方一样，需要用户在每台机器上确认（`--yes`）后才会运行。
- 站点配方面向所有用户，不要假设某个用户的 secret 名字；依赖 secret 的步骤加 `when_secret = "<名字>"`。
- 站点内容以明文提供给所有能访问服务器的人（`--site-auth` 时为所有已登录用户），**不要放凭证**；凭证由每个用户自己用 `hearth secret` 保存。
- `learn.json` 的 `token_files` 也是 `hearth login` / `hearth whoami` 查找本地身份的地方：`identity.email_domain` 把 token 里的用户名拼成账号邮箱，`identity.username_claim` 指定读哪个 JWT claim（默认 `username`）；不配域名时用用户 git email 的域名。

### 通过平台 broker 免密登录

如果平台会往每个作业容器注入用户凭证，可以让服务端把身份校验委托给 broker：

```bash
hearth server start ... \
  --identity-broker https://identity.example.com \
  --identity-broker-key <系统密钥>          # 或 $HEARTH_IDENTITY_BROKER_URL / _KEY
```

- 服务端请求 `GET <broker>/v3/user/userAuth?userName=<erp>`，带 `Authorization: Bearer <unix-ts>/<hex HMAC-SHA256(key, ts)>`，读取 `data.sk`。系统密钥不会下发到客户端。
- 不需要用户命令：已登录时 `hearth login`、`hearth setup` 和 daemon 启动会检查一次，容器里有该账号的平台凭证就自动把 vault key 用派生密钥包裹后上传。之后新机器 `hearth login` 用同一凭证做时间戳签名，服务端通过 broker 解析出同一凭证并校验，全程不需要密码。`hearth identity enable` 只用于强制重传/排查。
- `hearth identity disable` 删除免密路径，且该选择是粘性的：自动检查不会把它重新打开。主密码登录始终可用。
- **安全边界**：拿到该平台凭证的人可以解开对应账号的 vault；broker 系统密钥可以取任意用户的凭证，必须按 root 级凭据保护。启动 broker 前创建的账号在没执行 `identity enable` 之前不受影响。

## 用户与配额

管理命令直接打开服务器的数据库和对象存储（参数同 `hearth server`），服务器运行时也可执行，不需要管理员密码；改动 30 秒内在所有实例生效。

```bash
hearth server invite --data $D                         # 生成一次性邀请码（默认 7 天有效）
hearth server admin invites | invites revoke <码>
hearth server admin users --data $D                    # 用户及用量，最大的在前
hearth server admin user <邮箱>
hearth server admin quota <邮箱> 20GiB | unlimited | default
hearth server admin disable <邮箱> | enable <邮箱>     # 禁止登录，数据保留
hearth server admin revoke-devices <邮箱>              # 所有设备下线（密码泄露时）
hearth server admin delete-user <邮箱>                 # 删除账号及其全部数据
hearth server admin stats                              # 全服务器统计
hearth server admin gc                                 # 立即执行过期清理并删除孤儿对象
```

默认配额由 `hearth server --quota 10GiB` 设置（默认不限）。超出配额的客户端暂停上传并在 `hearth status` 中提示。

## 网页控制台与管理后台

服务器自带网页控制台 `/app`（`/admin` 同一页面）。所有已注册用户都能用邮箱 + 主密码登录，查看自己的机器（在线状态、机器名、IP、客户端版本，可让某台机器下线）、个人库里的 skills 和 agents（按使用频率排序，常驻 / 按需，可一键共享到自己是 maintainer / owner 的团队，共享前做凭证扫描）、最常用的 skills / agents、所在团队的成员和共享内容（maintainer 可生成邀请码）；会话和凭证端到端加密，网页端看不到内容。管理员额外看到管理页面：概览数据面板（账号、活跃用户、每日上传、注册趋势、热门 skills、客户端版本分布）、用户管理（每人当前在线的客户端：机器名、IP、版本；禁用 / 启用、配额、管理员、下线设备、删除，点邮箱看全部设备）、注册审批、邀请码、团队（创建团队并指定 owner、添加 / 移除成员、改角色、共享内容、团队间共享、删除）、运行时设置（注册方式、默认配额、客户端自动更新，无需重启、所有实例 10 秒内生效）、维护（过期清理、孤儿对象回收）和审计日志。界面支持中英文。

```bash
hearth server admin grant <邮箱> --data $D     # 在服务器上把账号设为管理员（ungrant 取消，admins 列出）
# 浏览器打开 https://<server>/admin，用账号（邮箱 + 主密码）登录
hearth panel                                  # 或：在以管理员账号登录的机器上打印 10 分钟有效的登录链接并打开浏览器
hearth server admin link <邮箱> --url <公网地址> --data $D   # 或直接在服务器上生成登录链接
```

账号登录时，浏览器在本地用和客户端相同的方式（Argon2id + HKDF）从主密码派生登录密钥，只把派生密钥发给服务器，主密码不离开浏览器（默认参数下约需几秒）。登录后（或打开登录链接后）得到 12 小时的签名会话 Cookie（HttpOnly、SameSite=Strict），修改操作还要求 `X-Hearth-Admin` 请求头（防 CSRF），页面有严格的 CSP。管理员被取消或账号被禁用后会话立即失效。每个修改都写入审计日志（命令行操作记为 `cli`）。

**注册审批**：把注册方式设为“需审批”（后台设置，或 `hearth server --registration approval`），任何人都能注册，账号在管理员于“审批”页通过（或 `hearth server admin approve <邮箱>`）之前不能登录；客户端会提示等待审批。

## 会话过期

- 超过 `--expire-days`（默认 30）天未访问的会话标为过期，再过 `--grace-days`（默认 7）天删除；固定（pin）的会话和长期记忆不过期。
- 用户在 Codex 中删除的会话由客户端标为过期，同样在宽限期后删除。
- 宽限期内按 ID 恢复会重新激活。

## 备份与迁移

```bash
hearth server backup /backups/hearth-$(date +%F) --data $D                   # 运行中的一致性备份（SQLite + 对象文件）
hearth server stop --data $D && hearth server migrate --data $D --to-db postgres://… --to-blobs s3://…   # 迁移（先停服务，目标需为空）
```

## 发布新版本客户端

```bash
go build -o /usr/local/bin/hearth ./cmd/hearth
scripts/build-dist.sh $D/dist <版本号>
hearth server restart --data $D
```

之后在线客户端的后台同步进程会在 30 分钟内自动更新（同时更新本机 agent 中的 `hearth` skill）；想立即更新就执行 `hearth self-update`。不希望客户端自动更新时，服务器加 `--no-client-auto-update`。

## 性能

服务端以检索和恢复速度优先：会话按最近活动建索引并分页，列表只读索引；对象按会话追加分块存储，读写都是顺序的。多实例下各实例缓存有界：撤销的 token 30 秒内在其他实例失效，团队发布的内容 2 秒内可被检索，GC 同一时间只在一个实例运行。基准数据见 [技术参考](../en/reference.md#server-performance)。

## 安全

- 默认邀请制；服务器只保存邀请码的 SHA-256。
- 所有会话和设备接口需要 token，登录接口按 IP 限流；token 30 天未使用失效。
- 新设备用主密码登录即可解密全部凭证（不需要旧设备审批）。主密码是凭证的唯一保护；尚未支持 MFA 和邮箱验证。
- 管理员能看到账号邮箱、用量和元数据大小，看不到会话内容、标题和凭证；library 以明文保存（仅本人可见，团队发布内容团队可见），上传前客户端会扫描并阻止疑似凭证。
