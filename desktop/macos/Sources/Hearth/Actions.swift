import AppKit

/// What the buttons do. Requests about a machine go to Codex: a new task
/// with the sentence in its composer (codex://new?prompt=...), and the
/// sentence is on the clipboard as well, for when Codex is not installed or
/// the link does not open it.
enum Actions {
    static let githubURL = URL(string: "https://github.com/hearth-project/hearth-cli")!

    static func askCodex(_ prompt: String) -> String {
        copy(prompt)
        var c = URLComponents()
        c.scheme = "codex"
        c.host = "new"
        c.queryItems = [URLQueryItem(name: "prompt", value: prompt)]
        if let url = c.url, NSWorkspace.shared.urlForApplication(toOpen: url) != nil {
            NSWorkspace.shared.open(url)
            return "已在 Codex 中打开（也已复制）"
        }
        return "已复制，粘贴到 Codex 发送"
    }

    static func restore(_ m: Machine) -> String { askCodex("恢复 \(m.host) 的 Codex 环境") }
    static func fix(_ m: Machine) -> String { askCodex("检查一下 \(m.host) 的环境，有问题就修好") }

    /// Opens Terminal with `ssh <host>`: the host name is in ~/.ssh/config
    /// (Hearth keeps an entry per machine on a Mac).
    static func ssh(_ m: Machine) -> String {
        if let url = URL(string: "ssh://\(m.host)") {
            NSWorkspace.shared.open(url)
            return "正在打开终端：ssh \(m.host)"
        }
        return "打不开终端"
    }

    /// Opens Terminal running `hearth login`, which asks for the master
    /// password (it must not pass through this app).
    static func loginInTerminal() -> String {
        guard let bin = HearthCLI.binary else { return "没有找到 hearth 命令" }
        let quoted = "'" + bin.replacingOccurrences(of: "'", with: "'\\''") + "'"
        let script = "#!/bin/zsh\nclear\n\(quoted) login\necho\nread -k 1 '?按任意键关闭…'\nexit\n"
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("hearth-login.command")
        do {
            try script.write(to: url, atomically: true, encoding: .utf8)
            try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: url.path)
        } catch {
            return "打不开终端：\(error.localizedDescription)"
        }
        NSWorkspace.shared.open(url)
        return "已在终端打开 hearth login"
    }

    /// Shows a short overview of Hearth's capabilities without opening the
    /// web console or asking the user to run a command.
    static func showAbout() {
        let version = (Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String) ?? "未知版本"
        let alert = NSAlert()
        alert.messageText = "关于 Hearth"
        alert.informativeText = """
        Hearth \(version)

        让你的 coding agents 在多台机器之间保持连续工作。

        • 查看所有机器的在线状态、环境和 SSH 连接
        • 恢复 Codex、Claude Code 和 Comate 的会话与记忆
        • 管理个人和团队共享的 Skills、Agents 与凭证
        • 远程恢复机器环境，直接连接其他机器
        • 在机器之间高速传输文件和文件夹
        • 在菜单栏快速打开控制台和团队功能
        """
        alert.alertStyle = .informational
        alert.addButton(withTitle: "好")
        alert.runModal()
    }

    /// Opens the public Hearth source repository in the user's default browser.
    static func openGitHub() {
        NSWorkspace.shared.open(githubURL)
    }

    static func copy(_ s: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
    }
}
