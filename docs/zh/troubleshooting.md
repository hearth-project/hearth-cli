# 常见问题与故障排查

[English](../en/troubleshooting.md) · [返回 README](../../README.zh-CN.md)

先运行 `hearth onboard status`，它会检查安装、登录、工作区、后台同步、agent、skill，并给出按顺序的下一步。

## 安装

**装完后提示 `hearth: command not found`。**
安装在 `~/.local/bin`。当前终端执行 `export PATH="$HOME/.local/bin:$PATH"`；新版安装脚本会写入 `~/.bashrc` / `~/.profile`，旧版不会，重新执行一次安装命令即可。

**`certificate signed by unknown authority` / `x509` 错误。**
服务器是自签证书，而本机没有它的证书副本。重新执行管理员给的安装命令（可加 `--install-only`），会保存 `~/.hearth/ca.pem`；工作区模式下也会自动使用这份证书。也可以 `hearth setup --ca-cert <文件>`。

**`curl: (90) SSL: public key does not match pinned public key`。**
服务器证书换过，或连到了别的服务器。向管理员要新的安装命令；不要去掉 pin。

**安装脚本提示没有对应平台的包。**
目前提供 linux / darwin × amd64 / arm64；Windows 请用 WSL。其他平台需管理员用 `scripts/build-dist.sh` 补充，或自行从源码构建。

## 登录与账号

**新用户被要求邀请码。** 服务器是邀请制（`/v1/info` 返回 `invite`），向管理员要邀请码，`--invite <码>`。

**`403 account disabled`。** 账号被管理员禁用。

**忘记主密码。** `hearth recover` 用恢复密钥重设，所有设备需要重新登录。恢复密钥也丢了则无法解密已有数据。

**恢复密钥被别人看到了。** `hearth recovery-key rotate`，旧的立即失效。

## 会话

**恢复后在 Codex Desktop 里看不到。**
Desktop 运行时 `hearth session restore` 会通过它的后端让会话立即出现；若 Desktop 当时没运行，会在下次启动时列出。仍然找不到：在 Desktop 中按标题搜索，或执行 `hearth codex-threads`。

**`session is leased by another device`。** 另一台机器正在写这个会话。那台机器已不存在时执行 `hearth session take <id>`。

**`local file differs from server copy`。** 本地文件与服务器内容冲突（两边都改过）。确认以服务器为准后 `hearth restore --force <id>`。

**某些会话没有上传。**
- `hearth backup status` 看是否被选择关闭（如 `!@claude.sessions`）；
- 多人共用账号时，工作目录之外运行的会话不会上传：`hearth session scope` 查看，`hearth session scope add <目录>` 添加你自己的目录；
- 查看后台同步日志（`hearth daemon status` 打印路径）中的 `skip` 行。

**删除的会话又出现了 / 想彻底删除。** 在 Codex 中删除会让服务器副本过期（宽限期后删除）；立即删除用 `hearth session rm <id>`。

## library 与凭证

**`library: refusing to upload: N likely secret(s) in the library`。**
library（skills、agents、配置）在服务器上明文保存。skill 里的凭证文件会自动加密同步；其他位置（配置、recipes、agent 文件）发现疑似凭证会拒绝上传，后续 library 同步（包括 `config.toml` `[backup]`）都会停住。把真实值移到本地认证文件，用 `hearth secret set <名字> --file <文件> --restore-to <路径>` 加密备份，skill 从文件读取；确实是文档示例的行加注释 `hearth:allow-secret`。

**新机器上缺少某个认证文件。** `hearth secret list` 确认已保存，`hearth secret restore` 写回；没有保存过就在原机器 `hearth learn` 或 `hearth secret set --file`。

## 服务器

**`507` / `storage quota exceeded`。** 达到配额，客户端暂停上传。管理员 `hearth server admin quota <邮箱> <大小>`，或用户删除不需要的会话。

**服务器挂了没有自动拉起。** 用 `hearth server start` 运行（自带守护进程），或 systemd（`hearth server install-service`）。容器重启后需在入口脚本里再次执行 `hearth server start`。

**客户端版本落后。** `hearth self-update`。

## 仍未解决

收集 `hearth onboard status --json`、`hearth status` 和后台同步日志的相关几行（不要包含凭证内容）再反馈。
