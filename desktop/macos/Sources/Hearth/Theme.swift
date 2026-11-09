import SwiftUI

/// A calm, mostly neutral palette: the warm accent (Hearth's flame) for the
/// main action, soft status colours that read well on light and dark menus.
enum Theme {
    static let accent = Color(red: 0.91, green: 0.45, blue: 0.22)
    static let ok = Color(red: 0.20, green: 0.68, blue: 0.42)
    static let warn = Color(red: 0.88, green: 0.60, blue: 0.13)
    static let bad = Color(red: 0.86, green: 0.30, blue: 0.27)
    static let idle = Color.secondary.opacity(0.55)
    static let blue = Color(red: 0.25, green: 0.52, blue: 0.92)
    static let slate = Color(red: 0.42, green: 0.47, blue: 0.56)
    /// The grouped cards: nearly white on a light menu, a lifted gray on dark.
    static let card = Color(nsColor: NSColor(name: nil) { a in
        a.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            ? NSColor(white: 1, alpha: 0.07)
            : NSColor(white: 1, alpha: 0.72)
    })

    static func envColor(_ env: String) -> Color {
        switch env {
        case "ok": return ok
        case "warn": return warn
        case "error": return bad
        default: return idle
        }
    }

    // Motion. Critically damped by default (no overshoot): rows and panels
    // that open or close are not thrown, so they should not bounce. Hover is
    // a short ease-out; a press shows at once (see `RowPressStyle`).
    static let spring = Animation.spring(response: 0.34, dampingFraction: 1.0)
    static let hover = Animation.easeOut(duration: 0.12)

    /// `nil` when the user asked for reduced motion.
    static func motion(_ a: Animation, reduce: Bool) -> Animation? { reduce ? nil : a }
}

extension View {
    /// The native pages' card: the grouped fill with a hairline border.
    func cardSurface(radius: CGFloat = 12) -> some View {
        self
            .background(Theme.card, in: RoundedRectangle(cornerRadius: radius, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: radius, style: .continuous).strokeBorder(Color.primary.opacity(0.07)))
    }
}

/// Menu rows and similar plain buttons: the press shows on pointer-down with
/// no delay and eases away on release, like a native menu item.
struct RowPressStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .overlay(
                RoundedRectangle(cornerRadius: 6, style: .continuous)
                    .fill(Color.primary.opacity(configuration.isPressed ? 0.06 : 0))
                    .allowsHitTesting(false)
            )
            .animation(configuration.isPressed ? nil : Theme.hover, value: configuration.isPressed)
    }
}

/// Readable Chinese for the environment check (codes from the server's
/// machineenv.go); unknown codes fall back to the CLI's English text.
enum Words {
    static func env(_ m: Machine) -> String {
        switch m.env {
        case "ok": return "环境正常"
        case "error": return m.problems.count > 0 ? "\(m.problems.count) 个问题" : "有问题"
        case "warn": return m.problems.count > 0 ? "\(m.problems.count) 项待处理" : "待处理"
        default: return "未上报"
        }
    }

    static func ssh(_ s: String?) -> String? {
        switch s {
        case "ok": return "SSH 可连接"
        case "blocked": return "SSH 服务器连不到"
        case "down": return "SSH 不通"
        case "off": return "SSH 已关闭"
        case "checking": return "SSH 检查中"
        default: return nil
        }
    }

    static func problem(_ p: Problem) -> String {
        let tool = p.tool ?? ""
        switch p.code {
        case "missing": return "\(tool) 未安装"
        case "outdated":
            if let h = p.have, let l = p.latest { return "\(tool) 可升级 \(h) → \(l)" }
            return "\(tool) 可升级"
        case "doctor": return "\(tool) 自检失败"
        case "integration": return "\(tool) 还没接入 Hearth"
        case "node": return "Node 版本太旧"
        case "steps": return "还有 \(p.behind ?? 0) 个环境恢复步骤"
        case "hearth": return "Hearth 可升级到 \(p.latest ?? "新版本")"
        case "bypass": return "Codex 没有经过 codex-mixin"
        case "unreachable": return "Codex 连不上模型服务"
        case "no_login": return "Codex 未登录"
        case "stale_app_server": return "有旧的 Codex app-server 在运行"
        case "ssh_down": return "SSH 连不上"
        case "ssh_hostkey": return "SSH 主机密钥变了"
        case "ssh_blocked": return "服务器连不到它的 SSH"
        case "shared_home": return "codex-mixin 登录在共享目录里"
        default: return p.text
        }
    }

    static func facts(_ m: Machine) -> String {
        var parts: [String] = []
        switch m.os {
        case "darwin": parts.append("macOS")
        case "linux": parts.append("Linux")
        case let s?: parts.append(s)
        default: break
        }
        if let g = m.gpu, !g.isEmpty, g != "apple" { parts.append(g.uppercased()) }
        else if let a = m.arch { parts.append(a) }
        return parts.joined(separator: " · ")
    }

    static func ago(_ ts: Int64) -> String {
        let s = Date().timeIntervalSince1970 - Double(ts)
        if s < 90 { return "刚刚" }
        if s < 3600 { return "\(Int(s / 60)) 分钟前" }
        if s < 86400 { return "\(Int(s / 3600)) 小时前" }
        return "\(Int(s / 86400)) 天前"
    }
}
