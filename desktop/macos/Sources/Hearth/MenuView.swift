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
    /// Height of the machine list. A MenuBarExtra window sizes itself to its
    /// content's ideal size and a ScrollView has none, so the list gets this
    /// height (at most 460) explicitly; without it the list collapses to zero.
    @State var listHeight: CGFloat = 0

    var body: some View {
        VStack(spacing: 0) {
            header
            content
            Divider().opacity(0.5)
            footer
        }
        .frame(width: 360)
        .background(snapshot ? AnyView(Color(nsColor: .windowBackgroundColor)) : AnyView(Color.clear))
    }

    private var header: some View {
        HStack(spacing: 10) {
            AppTile(size: 30)
            VStack(alignment: .leading, spacing: 1) {
                Text("Hearth").font(.system(size: 13, weight: .semibold))
                Text(summary).font(.system(size: 11)).foregroundStyle(.secondary)
            }
            Spacer()
            Button {
                Task { await store.refresh() }
            } label: {
                Image(systemName: "arrow.clockwise")
                    .font(.system(size: 11, weight: .semibold))
                    .rotationEffect(.degrees(store.loading ? 180 : 0))
                    .animation(.easeInOut(duration: 0.4), value: store.loading)
                    .frame(width: 26, height: 26)
                    .background(Circle().fill(Color.primary.opacity(0.06)))
            }
            .buttonStyle(.plain)
            .foregroundStyle(.secondary)
            .help("刷新")
        }
        .padding(.horizontal, 14)
        .padding(.top, 12)
        .padding(.bottom, 8)
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
            .frame(height: snapshot ? nil : min(max(listHeight, 44), 460))
            .fixedSize(horizontal: false, vertical: snapshot)
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
                    }, say: { note = $0 })
                }
            }
            .background(Theme.card)
            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Color.primary.opacity(0.07)))
        }
    }

    private var footer: some View {
        HStack(spacing: 8) {
            Button(action: openConsole) {
                Label("打开控制台", systemImage: "macwindow")
                    .font(.system(size: 12, weight: .medium))
            }
            .modifier(PrimaryButton())
            if let n = note {
                Text(n).font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(1)
            } else if let err = store.error, store.list != nil {
                Label("刷新失败", systemImage: "exclamationmark.triangle.fill")
                    .font(.system(size: 11))
                    .foregroundStyle(Theme.warn)
                    .help(err)
            } else if let u = store.updated {
                Text("更新于 " + u.formatted(.dateTime.hour(.twoDigits(amPM: .omitted)).minute(.twoDigits).locale(Locale(identifier: "zh_CN"))))
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
