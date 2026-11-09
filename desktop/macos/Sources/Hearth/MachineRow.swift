import SwiftUI

/// One machine in a group card: an icon for the kind of machine with its
/// online dot, the name, and the environment state as a small glyph. Opening
/// it shows the facts, the problems and the actions. The copy button that
/// appears on hover, or the right-click menu, copies the machine name
/// without opening it; a plain click only opens and closes the row, so the
/// two never get in each other's way.
struct MachineRow: View {
    var m: Machine
    var open: Bool
    var toggle: () -> Void
    var say: (String) -> Void
    var rename: (String) async throws -> Void
    /// Server-side details, shown in the native window only.
    var info: Device? = nil
    /// Remove the machine from the account (native window only).
    var remove: (() -> Void)? = nil
    @State private var hover = false
    @State private var draft = ""
    @State private var saving = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 10) {
                    MachineIcon(m: m)
                    HStack(spacing: 6) {
                        Text(m.title)
                            .font(.system(size: 13, weight: m.env == "error" ? .semibold : .regular))
                            .lineLimit(1)
                            .truncationMode(.middle)
                        if m.isThis { Badge(text: "本机", color: .secondary) }
                    }
                    .layoutPriority(1)
                    Spacer(minLength: 6)
                    if !m.online {
                        Text(Words.ago(m.last_seen)).font(.system(size: 11)).foregroundStyle(.tertiary).lineLimit(1).fixedSize()
                    }
                    // Always laid out, so showing it on hover does not shift the row.
                    Button(action: copyName) {
                        Image(systemName: "doc.on.doc").font(.system(size: 11))
                    }
                    .buttonStyle(.borderless)
                    .foregroundStyle(.secondary)
                    .opacity(hover ? 1 : 0)
                    .allowsHitTesting(hover)
                    .accessibilityHidden(!hover)
                    .help("复制 \(m.host)")
                    EnvGlyph(m: m)
                    Image(systemName: "chevron.right")
                        .font(.system(size: 9, weight: .semibold))
                        .foregroundStyle(.tertiary)
                        .rotationEffect(.degrees(open ? 90 : 0))
                        .animation(Theme.motion(Theme.spring, reduce: reduceMotion), value: open)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 7)
            .contentShape(Rectangle())
            .onTapGesture(perform: toggle)
            .accessibilityElement(children: .contain)
            .accessibilityAddTraits(.isButton)
            .accessibilityAction(named: open ? "收起" : "展开", toggle)
            .contextMenu {
                Button("复制机器名", action: copyName)
                Button(open ? "收起" : "展开", action: toggle)
                if let remove {
                    Divider()
                    Button("移除这台机器…", role: .destructive, action: remove)
                }
            }
            .help("\(m.host)\n单击展开，右键或悬停时点复制按钮复制机器名")
            if open {
                detail.transition(.opacity.combined(with: .scale(scale: 0.98, anchor: .top)))
            }
        }
        .background(Color.primary.opacity(open ? 0.035 : (hover ? 0.03 : 0)))
        .animation(Theme.motion(Theme.hover, reduce: reduceMotion), value: hover)
        .onHover { hover = $0 }
    }

    private func copyName() {
        Actions.copy(m.host)
        say("已复制 \(m.host)")
    }

    private var detail: some View {
        VStack(alignment: .leading, spacing: 10) {
            VStack(spacing: 5) {
                if m.online { nameField }
                Fact(label: "主机", value: m.name, selectable: true)
                let f = Words.facts(m)
                if !f.isEmpty { Fact(label: "系统", value: f) }
                Fact(label: "状态", value: m.online ? "在线" : "离线 · " + Words.ago(m.last_seen))
                if !m.isThis, let s = Words.ssh(m.ssh) { Fact(label: "SSH", value: s) }
                if let d = info {
                    if let ip = d.ip, !ip.isEmpty { Fact(label: "IP", value: ip, selectable: true) }
                    if let v = d.version, !v.isEmpty { Fact(label: "版本", value: v) }
                    if let s = d.sessions { Fact(label: "会话", value: "\(s) 个" + (d.last_active.map { $0 > 0 ? " · 最近活动 " + Fmt.ago($0) : "" } ?? "")) }
                    if let c = d.created_at, c > 0 { Fact(label: "加入于", value: Date(timeIntervalSince1970: TimeInterval(c)).formatted(date: .abbreviated, time: .omitted)) }
                }
            }
            if !m.problems.isEmpty {
                VStack(alignment: .leading, spacing: 5) {
                    ForEach(m.problems.prefix(5), id: \.self) { p in
                        HStack(alignment: .firstTextBaseline, spacing: 7) {
                            Image(systemName: p.level == "error" ? "xmark.circle.fill" : "exclamationmark.circle.fill")
                                .font(.system(size: 10))
                                .foregroundStyle(p.level == "error" ? Theme.bad : Theme.warn)
                            Text(Words.problem(p)).font(.system(size: 12)).fixedSize(horizontal: false, vertical: true)
                        }
                    }
                    if m.problems.count > 5 {
                        Text("还有 \(m.problems.count - 5) 项，在控制台查看").font(.system(size: 11)).foregroundStyle(.secondary)
                    }
                }
                .padding(9)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(RoundedRectangle(cornerRadius: 7, style: .continuous).fill(Color.primary.opacity(0.04)))
            }
            HStack(spacing: 6) {
                if m.env != "ok" {
                    Button { say(Actions.restore(m)) } label: { Text("恢复环境") }
                        .modifier(PrimaryButton())
                }
                Button { say(Actions.fix(m)) } label: { Text("检查") }
                if m.sshUsable {
                    Button { say(Actions.ssh(m)) } label: { Text("SSH 连接") }
                }
                if let remove {
                    Spacer()
                    Button(role: .destructive, action: remove) { Text("移除…") }
                }
            }
            .controlSize(.small)
        }
        .padding(.leading, 48)
        .padding(.trailing, 12)
        .padding(.top, 2)
        .padding(.bottom, 12)
        .onAppear { draft = m.alias ?? "" }
        .onChange(of: m.alias) { draft = $0 ?? "" }
    }

    /// The machine's name, editable in place. Empty goes back to the host name.
    private var nameField: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text("名称").font(.system(size: 11)).foregroundStyle(.secondary).frame(width: 34, alignment: .leading)
            TextField(m.name, text: $draft)
                .textFieldStyle(.roundedBorder)
                .font(.system(size: 11))
                .controlSize(.small)
                .disabled(saving)
                .onSubmit(save)
            if saving {
                ProgressView().controlSize(.small).scaleEffect(0.7)
            } else if changed {
                Button("保存", action: save).controlSize(.small)
            }
        }
    }

    private var changed: Bool { draft.trimmingCharacters(in: .whitespaces) != (m.alias ?? "") }

    private func save() {
        let name = draft.trimmingCharacters(in: .whitespaces)
        guard changed, !saving else { return }
        if !name.isEmpty, name.range(of: #"^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$"#, options: .regularExpression) == nil {
            say("名称只能用字母、数字、. _ -，不超过 63 个字符")
            return
        }
        saving = true
        Task {
            do {
                try await rename(name)
                say(name.isEmpty ? "已恢复为 \(m.name)" : "已改名为 \(name)")
            } catch {
                let s = error.localizedDescription
                say(s.contains("has that name") ? "已有机器叫 \(name)" : s)
            }
            saving = false
        }
    }
}

/// A rounded tile with a symbol for the machine (laptop, server) and a dot
/// in the corner: green online, gray offline.
struct MachineIcon: View {
    var m: Machine
    var body: some View {
        let symbol = m.os == "darwin" ? "laptopcomputer" : (m.gpu?.isEmpty == false ? "cpu" : "server.rack")
        let tint = m.os == "darwin" ? Theme.blue : Theme.slate
        RoundedRectangle(cornerRadius: 7, style: .continuous)
            .fill(tint.opacity(m.online ? 0.16 : 0.08))
            .frame(width: 26, height: 26)
            .overlay(Image(systemName: symbol).font(.system(size: 12, weight: .medium)).foregroundStyle(m.online ? tint : Color.secondary))
            .overlay(alignment: .bottomTrailing) {
                Circle()
                    .fill(m.online ? Theme.ok : Color(nsColor: .tertiaryLabelColor))
                    .frame(width: 8, height: 8)
                    .overlay(Circle().strokeBorder(Theme.card, lineWidth: 1.5))
                    .offset(x: 2, y: 2)
            }
    }
}

/// The environment state at the end of a row: a check when all is well, a
/// coloured count of problems otherwise.
struct EnvGlyph: View {
    var m: Machine
    var body: some View {
        Group {
            switch m.env {
            case "ok":
                Image(systemName: "checkmark.circle.fill").foregroundStyle(Theme.ok.opacity(0.85))
            case "error", "warn":
                let c = Theme.envColor(m.env)
                HStack(spacing: 3) {
                    Image(systemName: m.env == "error" ? "xmark.circle.fill" : "exclamationmark.circle.fill")
                    if !m.problems.isEmpty { Text("\(m.problems.count)").monospacedDigit() }
                }
                .foregroundStyle(c)
                .padding(.horizontal, 6).padding(.vertical, 2)
                .background(Capsule().fill(c.opacity(0.12)))
            default:
                Image(systemName: "minus.circle").foregroundStyle(.tertiary)
            }
        }
        .font(.system(size: 11, weight: .semibold))
        .fixedSize()
        .help(Words.env(m))
    }
}

struct Badge: View {
    var text: String
    var color: Color
    var body: some View {
        Text(text).font(.system(size: 10, weight: .medium))
            .padding(.horizontal, 5).padding(.vertical, 1)
            .background(Capsule().fill(color.opacity(0.15)))
            .foregroundStyle(color)
            .fixedSize()
    }
}

/// A label on the left, the value on the right (detail of an open row).
struct Fact: View {
    var label: String
    var value: String
    var selectable = false
    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(label).font(.system(size: 11)).foregroundStyle(.secondary).frame(width: 34, alignment: .leading)
            if selectable {
                Text(value).font(.system(size: 11)).textSelection(.enabled).lineLimit(1).truncationMode(.middle)
            } else {
                Text(value).font(.system(size: 11)).lineLimit(1).truncationMode(.middle)
            }
            Spacer(minLength: 0)
        }
    }
}
