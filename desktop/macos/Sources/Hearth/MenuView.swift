import SwiftUI

/// The menu bar dropdown: one compact row per machine; a row opens to show
/// its problems and what can be done about it.
struct MenuView: View {
    @ObservedObject var store: MachineStore
    var openConsole: () -> Void
    var snapshot = false
    @State var expanded: String? = nil
    @State var note: String? = nil

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider().opacity(0.6)
            content
            Divider().opacity(0.6)
            footer
        }
        .frame(width: 340)
        .background(snapshot ? AnyView(Color(nsColor: .windowBackgroundColor)) : AnyView(Color.clear))
        .tint(Theme.accent)
    }

    private var header: some View {
        HStack(spacing: 8) {
            Image(systemName: "flame.fill")
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(Theme.accent)
            Text("Hearth").font(.system(size: 14, weight: .semibold))
            Spacer()
            if store.list != nil {
                Text("\(store.onlineCount) / \(store.machines.count) 在线")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
            }
            Button {
                Task { await store.refresh() }
            } label: {
                Image(systemName: "arrow.clockwise")
                    .font(.system(size: 12, weight: .medium))
                    .rotationEffect(.degrees(store.loading ? 180 : 0))
                    .animation(.easeInOut(duration: 0.4), value: store.loading)
            }
            .buttonStyle(.plain)
            .foregroundStyle(.secondary)
            .help("刷新")
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
    }

    @ViewBuilder private var content: some View {
        if let err = store.error, store.list == nil {
            VStack(spacing: 8) {
                Image(systemName: "exclamationmark.triangle").font(.system(size: 20)).foregroundStyle(Theme.warn)
                Text(err).font(.system(size: 12)).foregroundStyle(.secondary).multilineTextAlignment(.center)
            }
            .frame(maxWidth: .infinity)
            .padding(20)
        } else if store.list == nil {
            ProgressView().controlSize(.small).frame(maxWidth: .infinity).padding(24)
        } else if store.machines.isEmpty {
            Text("还没有机器").font(.system(size: 12)).foregroundStyle(.secondary).frame(maxWidth: .infinity).padding(24)
        } else {
            ScrollView {
                VStack(spacing: 2) {
                    ForEach(store.machines) { m in
                        MachineRow(m: m, open: expanded == m.id, toggle: {
                            withAnimation(.easeInOut(duration: 0.18)) { expanded = expanded == m.id ? nil : m.id }
                        }, say: { note = $0 })
                    }
                }
                .padding(6)
            }
            .frame(maxHeight: snapshot ? .infinity : 430)
            .fixedSize(horizontal: false, vertical: snapshot)
        }
    }

    private var footer: some View {
        HStack(spacing: 8) {
            Button(action: openConsole) {
                Label("打开控制台", systemImage: "macwindow")
                    .font(.system(size: 12, weight: .medium))
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.regular)
            if let n = note {
                Text(n).font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(1)
            } else if let u = store.updated {
                Text("更新于 " + u.formatted(date: .omitted, time: .shortened))
                    .font(.system(size: 11)).foregroundStyle(.tertiary)
            }
            Spacer()
            Menu {
                Button("刷新") { Task { await store.refresh() } }
                LoginItemToggle()
                Divider()
                Button("退出 Hearth") { NSApp.terminate(nil) }
            } label: {
                Image(systemName: "gearshape").font(.system(size: 13))
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .task(id: note) {
            if note != nil {
                try? await Task.sleep(nanoseconds: 3_000_000_000)
                note = nil
            }
        }
    }
}

struct MachineRow: View {
    var m: Machine
    var open: Bool
    var toggle: () -> Void
    var say: (String) -> Void
    @State private var hover = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button(action: toggle) {
                HStack(spacing: 10) {
                    Circle()
                        .fill(m.online ? Theme.ok : Theme.idle)
                        .frame(width: 8, height: 8)
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Text(m.title).font(.system(size: 13, weight: .medium)).lineLimit(1)
                            if m.isThis {
                                Text("本机").font(.system(size: 10, weight: .medium))
                                    .padding(.horizontal, 5).padding(.vertical, 1)
                                    .background(Capsule().fill(Color.secondary.opacity(0.15)))
                                    .foregroundStyle(.secondary)
                            }
                        }
                        Text(line).font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(1)
                    }
                    Spacer(minLength: 6)
                    Text(Words.env(m))
                        .font(.system(size: 11, weight: .medium))
                        .foregroundStyle(Theme.envColor(m.env))
                    Image(systemName: "chevron.right")
                        .font(.system(size: 9, weight: .semibold))
                        .foregroundStyle(.tertiary)
                        .rotationEffect(.degrees(open ? 90 : 0))
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 8)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            if open { detail }
        }
        .background(
            RoundedRectangle(cornerRadius: 8, style: .continuous)
                .fill(Color.primary.opacity(open ? 0.07 : (hover ? 0.05 : 0)))
        )
        .onHover { hover = $0 }
    }

    private var line: String {
        var parts = [m.online ? "在线" : "离线 · " + Words.ago(m.last_seen)]
        let f = Words.facts(m)
        if !f.isEmpty { parts.append(f) }
        if let s = Words.ssh(m.ssh), !m.isThis { parts.append(s) }
        return parts.joined(separator: " · ")
    }

    private var detail: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let h = m.subtitleHost {
                Text(h).font(.system(size: 11)).foregroundStyle(.tertiary).textSelection(.enabled)
            }
            if !m.problems.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(m.problems.prefix(5), id: \.self) { p in
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            Circle().fill(p.level == "error" ? Theme.bad : Theme.warn).frame(width: 5, height: 5)
                            Text(Words.problem(p)).font(.system(size: 12))
                        }
                    }
                    if m.problems.count > 5 {
                        Text("还有 \(m.problems.count - 5) 项").font(.system(size: 11)).foregroundStyle(.secondary)
                    }
                }
            }
            HStack(spacing: 6) {
                if m.env != "ok" {
                    Button { say(Actions.restore(m)) } label: { Text("恢复环境") }
                        .buttonStyle(.borderedProminent)
                }
                Button { say(Actions.fix(m)) } label: { Text("检查") }
                if m.sshUsable {
                    Button { say(Actions.ssh(m)) } label: { Text("SSH 连接") }
                }
                Spacer()
                Button {
                    Actions.copy(m.host)
                    say("已复制 \(m.host)")
                } label: { Image(systemName: "doc.on.doc") }
                    .buttonStyle(.borderless)
                    .help("复制机器名")
            }
            .controlSize(.small)
        }
        .padding(.leading, 28)
        .padding(.trailing, 10)
        .padding(.bottom, 10)
    }
}
