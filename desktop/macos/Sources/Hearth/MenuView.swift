import SwiftUI

/// The menu bar dropdown. Machines are grouped into cards (online, then
/// recently offline); a row opens in place to show details, problems and
/// what can be done about them.
struct MenuView: View {
    @ObservedObject var store: MachineStore
    var openConsole: () -> Void
    var snapshot = false
    @State var expanded: String? = nil
    @State var note: String? = nil
    @State private var copying = false
    /// Height of the machine list. A MenuBarExtra window sizes itself to its
    /// content's ideal size and a ScrollView has none, so the list gets this
    /// height (at most 460) explicitly; without it the list collapses to zero.
    @State var listHeight: CGFloat = 0

    var body: some View {
        VStack(spacing: 0) {
            header
            content.overlay(alignment: .bottom) { noteView }
            Divider().opacity(0.5)
            footer
        }
        .frame(width: 360)
        .background(snapshot ? AnyView(Color(nsColor: .windowBackgroundColor)) : AnyView(Color.clear))
        .background(snapshot ? nil : MenuWindowAnchor())
    }

    /// Makes a one-time install command and copies it; the command is never shown.
    private func copyInstallCommand() {
        copying = true
        Task {
            let s = await store.copyInstallCommand()
            copying = false
            withAnimation { note = s }
        }
    }

    /// The list never makes the dropdown taller than the screen below the
    /// menu bar: header and footer take about 230 points.
    private var maxListHeight: CGFloat {
        let screen = NSScreen.main?.visibleFrame.height ?? 800
        return max(160, min(460, screen - 260))
    }

    private var header: some View {
        HStack(spacing: 10) {
            AppTile(size: 30)
            VStack(alignment: .leading, spacing: 1) {
                Text("Hearth").font(.system(size: 13, weight: .semibold))
                Text(summary).font(.system(size: 11)).foregroundStyle(.secondary)
            }
            Spacer()
            if store.loading && store.list != nil {
                ProgressView().controlSize(.small).scaleEffect(0.8)
            }
        }
        .padding(.horizontal, 14)
        .padding(.top, 12)
        .padding(.bottom, 8)
        .help(store.list.map { "\($0.email) · \($0.server)" } ?? "")
    }

    private var summary: String {
        guard store.list != nil else { return store.error == nil ? "正在读取机器…" : "读取失败" }
        if store.machines.isEmpty { return "没有在线的机器" }
        var s = "\(store.onlineCount) 台在线"
        let bad = store.machines.filter { $0.env == "error" }.count
        if bad > 0 { s += " · \(bad) 台有问题" } else if store.attentionCount == 0 { s += " · 环境都正常" }
        return s
    }

    private var groups: [(title: String, machines: [Machine])] {
        let on = store.machines.filter(\.online)
        let off = store.machines.filter { !$0.online }
        return [("在线", on), ("最近离线", off)].filter { !$0.1.isEmpty }
    }

    @ViewBuilder private var content: some View {
        if let err = store.error, store.list == nil {
            VStack(spacing: 8) {
                Image(systemName: "exclamationmark.triangle").font(.system(size: 20)).foregroundStyle(Theme.warn)
                Text(err).font(.system(size: 12)).foregroundStyle(.secondary).multilineTextAlignment(.center)
            }
            .frame(maxWidth: .infinity)
            .padding(24)
        } else if store.list == nil {
            ProgressView().controlSize(.small).frame(maxWidth: .infinity).padding(28)
        } else if store.machines.isEmpty {
            Text(store.hiddenCount > 0 ? "最近 12 小时没有在线的机器" : "还没有机器")
                .font(.system(size: 12)).foregroundStyle(.secondary)
                .frame(maxWidth: .infinity).padding(28)
        } else {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    ForEach(groups, id: \.title) { g in
                        section(g.title, g.machines)
                    }
                    if store.hiddenCount > 0 {
                        Text("另有 \(store.hiddenCount) 台离线超过 12 小时，在控制台查看")
                            .font(.system(size: 11))
                            .foregroundStyle(.tertiary)
                            .frame(maxWidth: .infinity)
                    }
                }
                .padding(.horizontal, 10)
                .padding(.top, 2)
                .padding(.bottom, 12)
                .background(GeometryReader { g in
                    Color.clear.preference(key: ListHeight.self, value: g.size.height)
                })
            }
            .onPreferenceChange(ListHeight.self) { listHeight = $0 }
            .scrollIndicators(.never) // .hidden still shows them when a mouse is connected
            .frame(height: snapshot ? nil : min(max(listHeight, 44), maxListHeight))
            .fixedSize(horizontal: false, vertical: snapshot)
        }
    }

    /// A short message after an action ("已复制 …"), over the bottom of the list.
    @ViewBuilder private var noteView: some View {
        if let n = note {
            Text(n).font(.system(size: 11, weight: .medium)).lineLimit(2).multilineTextAlignment(.center)
                .padding(.horizontal, 10).padding(.vertical, 5)
                .background(RoundedRectangle(cornerRadius: 12, style: .continuous).fill(.regularMaterial))
                .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Color.primary.opacity(0.08)))
                .shadow(color: .black.opacity(0.12), radius: 4, y: 1)
                .padding(.horizontal, 16)
                .padding(.bottom, 8)
                .transition(.opacity)
        }
    }

    private func section(_ title: String, _ ms: [Machine]) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 5) {
                Text(title).font(.system(size: 11, weight: .semibold)).foregroundStyle(.secondary)
                Text("\(ms.count)").font(.system(size: 11)).foregroundStyle(.tertiary)
                Spacer()
                let bad = ms.filter { $0.env == "error" }.count
                if bad > 0 {
                    Text("\(bad) 台需要处理").font(.system(size: 11, weight: .medium)).foregroundStyle(Theme.bad)
                }
            }
            .padding(.horizontal, 6)
            VStack(spacing: 0) {
                ForEach(Array(ms.enumerated()), id: \.element.id) { i, m in
                    if i > 0 { Divider().padding(.leading, 46) }
                    MachineRow(m: m, open: expanded == m.id, toggle: {
                        withAnimation(.easeInOut(duration: 0.18)) { expanded = expanded == m.id ? nil : m.id }
                    }, say: { note = $0 }, rename: { name in try await store.rename(m, to: name) })
                }
            }
            .background(Theme.card)
            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Color.primary.opacity(0.07)))
        }
    }

    /// Menu-style rows at the bottom, like a native status menu.
    private var footer: some View {
        VStack(spacing: 0) {
            MenuRow(icon: "macwindow", title: "打开控制台…", action: openConsole)
            MenuRow(icon: "plus.rectangle.on.rectangle", title: copying ? "正在生成命令…" : "复制新增机器的命令", action: copyInstallCommand) {
                if copying {
                    ProgressView().controlSize(.small).scaleEffect(0.7)
                } else {
                    Image(systemName: "doc.on.doc").font(.system(size: 11)).foregroundStyle(.tertiary)
                }
            }
            .disabled(copying)
            .help("一次性命令：在新机器上运行即安装 Hearth 并登录，30 分钟内可用一次。命令不显示，只复制到剪贴板。")
            MenuRow(icon: "arrow.clockwise", title: "刷新", action: { Task { await store.refresh() } }) {
                if let err = store.error, store.list != nil {
                    Label("刷新失败", systemImage: "exclamationmark.triangle.fill")
                        .font(.system(size: 11)).foregroundStyle(Theme.warn).help(err)
                } else if let u = store.updated {
                    Text("更新于 " + u.formatted(.dateTime.hour(.twoDigits(amPM: .omitted)).minute(.twoDigits).locale(Locale(identifier: "zh_CN"))))
                        .font(.system(size: 11)).foregroundStyle(.tertiary)
                }
            }
            LoginItemRow()
            Divider().opacity(0.5).padding(.horizontal, 8).padding(.vertical, 4)
            MenuRow(icon: "power", title: "退出 Hearth", action: { NSApp.terminate(nil) })
        }
        .padding(.horizontal, 6)
        .padding(.vertical, 6)
        .task(id: note) {
            if note != nil {
                try? await Task.sleep(nanoseconds: 3_000_000_000)
                withAnimation { note = nil }
            }
        }
    }
}

/// One row of the bottom menu: an icon, a title, optional trailing content,
/// highlighted under the pointer.
struct MenuRow<Trailing: View>: View {
    var icon: String
    var title: String
    var action: () -> Void
    @ViewBuilder var trailing: () -> Trailing
    @State private var hover = false

    var body: some View {
        Button(action: action) {
            MenuRowLabel(icon: icon, title: title, hover: hover, trailing: trailing)
        }
        .buttonStyle(.plain)
        .onHover { hover = $0 }
    }
}

extension MenuRow where Trailing == EmptyView {
    init(icon: String, title: String, action: @escaping () -> Void) {
        self.init(icon: icon, title: title, action: action, trailing: { EmptyView() })
    }
}

struct MenuRowLabel<Trailing: View>: View {
    var icon: String
    var title: String
    var hover: Bool
    @ViewBuilder var trailing: () -> Trailing

    var body: some View {
        HStack(spacing: 9) {
            Image(systemName: icon)
                .font(.system(size: 12, weight: .medium))
                .foregroundStyle(.secondary)
                .frame(width: 18)
            Text(title).font(.system(size: 13))
            Spacer(minLength: 8)
            trailing()
        }
        .padding(.horizontal, 8)
        .frame(height: 28)
        .contentShape(Rectangle())
        .background(RoundedRectangle(cornerRadius: 6, style: .continuous).fill(Color.primary.opacity(hover ? 0.08 : 0)))
    }
}

/// Keeps the dropdown hanging from the menu bar. When its content grows (a
/// row opens, the list gets longer) AppKit keeps the window's bottom edge and
/// grows it upwards, under the menu bar; this puts the top edge back.
struct MenuWindowAnchor: NSViewRepresentable {
    final class Tracker: NSView {
        private var top: CGFloat?
        private var tokens: [NSObjectProtocol] = []

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            tokens.forEach(NotificationCenter.default.removeObserver)
            tokens = []
            guard let w = window else { return }
            let nc = NotificationCenter.default
            tokens.append(nc.addObserver(forName: NSWindow.didBecomeKeyNotification, object: w, queue: .main) { [weak self] _ in
                self?.top = nil
                self?.pin(w)
            })
            tokens.append(nc.addObserver(forName: NSWindow.didResizeNotification, object: w, queue: .main) { [weak self] _ in
                self?.pin(w)
            })
            pin(w)
        }

        private func pin(_ w: NSWindow) {
            let limit = (w.screen ?? NSScreen.main)?.visibleFrame.maxY ?? w.frame.maxY
            let want = min(top ?? w.frame.maxY, limit)
            top = want
            if abs(w.frame.maxY - want) > 0.5 {
                w.setFrameTopLeftPoint(NSPoint(x: w.frame.minX, y: want))
            }
        }

        deinit { tokens.forEach(NotificationCenter.default.removeObserver) }
    }

    func makeNSView(context: Context) -> Tracker { Tracker() }
    func updateNSView(_ nsView: Tracker, context: Context) {}
}

/// The primary action: Liquid Glass on macOS 26, the bordered style before
/// that or when the user turned transparency down. Snapshots use the bordered
/// style: glass is not drawn when a view is rendered off screen.
struct PrimaryButton: ViewModifier {
    @Environment(\.accessibilityReduceTransparency) private var reduce
    func body(content: Content) -> some View {
        #if compiler(>=6.2)
        if #available(macOS 26.0, *), !reduce, !Snapshot.active {
            content.buttonStyle(.glassProminent).tint(Theme.accent)
        } else {
            content.buttonStyle(.borderedProminent).tint(Theme.accent)
        }
        #else
        content.buttonStyle(.borderedProminent).tint(Theme.accent)
        #endif
    }
}

/// Hearth's mark: a white flame on a warm rounded square.
struct AppTile: View {
    var size: CGFloat
    var body: some View {
        RoundedRectangle(cornerRadius: size * 0.27, style: .continuous)
            .fill(LinearGradient(colors: [Color(red: 0.98, green: 0.62, blue: 0.30), Color(red: 0.86, green: 0.33, blue: 0.16)],
                                 startPoint: .top, endPoint: .bottom))
            .frame(width: size, height: size)
            .overlay(Image(systemName: "flame.fill").font(.system(size: size * 0.52, weight: .semibold)).foregroundStyle(.white))
            .shadow(color: .black.opacity(0.12), radius: 1.5, y: 1)
    }
}

private struct ListHeight: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = max(value, nextValue()) }
}
