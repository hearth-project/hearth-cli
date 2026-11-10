import SwiftUI

/// A page: large title, optional trailing text, scrolling content.
struct PageScroll<Content: View>: View {
    var title: String
    var trailing: String? = nil
    @ViewBuilder var content: () -> Content
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                HStack(alignment: .firstTextBaseline) {
                    Text(title).font(.system(size: 24, weight: .bold))
                    Spacer()
                    if let t = trailing { Text(t).font(.system(size: 11)).foregroundStyle(.tertiary) }
                }
                content()
            }
            .padding(.horizontal, 26).padding(.top, 8).padding(.bottom, 26)
            .frame(maxWidth: 1000, alignment: .leading)
            .frame(maxWidth: .infinity)
        }
        .scrollIndicators(.never)
    }
}

/// A rounded card with an optional heading.
struct Panel<Content: View>: View {
    var title: String? = nil
    @ViewBuilder var content: () -> Content
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let t = title { Text(t).font(.system(size: 12, weight: .semibold)).foregroundStyle(.secondary) }
            content()
        }
        .padding(14)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .cardSurface()
    }
}

/// The column grid shared by KPI tiles and cards, so card edges line up with the tiles above.
enum Columns {
    static let spacing: CGFloat = 12
    static let grid = Array(repeating: GridItem(.flexible(), spacing: spacing), count: 3)
}

/// A row of cards on the 3-column grid. `spans` is each card's width in columns;
/// every card gets the height of the tallest one.
struct CardRow: Layout {
    var spans: [Int]
    var spacing: CGFloat = Columns.spacing
    private func widths(_ total: CGFloat, _ n: Int) -> [CGFloat] {
        let unit = (total - spacing * 2) / 3
        return (0..<n).map { i in
            let s = CGFloat(i < spans.count ? spans[i] : 1)
            return unit * s + spacing * (s - 1)
        }
    }
    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let w = proposal.width ?? 900
        let h = zip(subviews, widths(w, subviews.count))
            .map { $0.sizeThatFits(ProposedViewSize(width: $1, height: nil)).height }.max() ?? 0
        return CGSize(width: w, height: h)
    }
    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX
        for (v, w) in zip(subviews, widths(bounds.width, subviews.count)) {
            v.place(at: CGPoint(x: x, y: bounds.minY), proposal: ProposedViewSize(width: w, height: bounds.height))
            x += w + spacing
        }
    }
}

struct KPI: View {
    var title: String
    var value: String
    var icon: String
    var tint: Color
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    var body: some View {
        HStack(spacing: 10) {
            RoundedRectangle(cornerRadius: 8, style: .continuous).fill(tint.opacity(0.14)).frame(width: 34, height: 34)
                .overlay(Image(systemName: icon).font(.system(size: 15, weight: .medium)).foregroundStyle(tint))
            VStack(alignment: .leading, spacing: 1) {
                Text(value).font(.system(size: 18, weight: .semibold)).lineLimit(1).minimumScaleFactor(0.7)
                    .contentTransition(.numericText())
                    .animation(Theme.motion(Theme.spring, reduce: reduceMotion), value: value)
                Text(title).font(.system(size: 11)).foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardSurface()
    }
}

/// Bytes uploaded per day, as bars.
struct UploadChart: View {
    var series: [DaySeries]
    @State private var grown = Snapshot.active
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    var body: some View {
        let peak = max(series.map(\.bytes).max() ?? 0, 1)
        VStack(spacing: 4) {
            GeometryReader { g in
            let h = g.size.height
            HStack(alignment: .bottom, spacing: 3) {
                ForEach(Array(series.enumerated()), id: \.element.day) { i, d in
                    RoundedRectangle(cornerRadius: 2, style: .continuous)
                        .fill(d.bytes > 0 ? Theme.blue : Theme.blue.opacity(0.18))
                        .frame(height: grown ? max(3, h * CGFloat(d.bytes) / CGFloat(peak)) : 3)
                        .frame(maxWidth: .infinity)
                        .animation(Theme.motion(Theme.spring.delay(Double(i) * 0.012), reduce: reduceMotion), value: grown)
                        .help("\(d.day)  \(Fmt.bytes(d.bytes))")
                }
            }
            .frame(width: g.size.width, height: h, alignment: .bottom)
            }
            .frame(minHeight: 90, maxHeight: .infinity)
            .onAppear { grown = true }
            HStack {
                Text(String((series.first?.day ?? "").dropFirst(5)))
                Spacer()
                Text("最高 \(Fmt.bytes(peak))")
                Spacer()
                Text(String((series.last?.day ?? "").dropFirst(5)))
            }
            .font(.system(size: 10)).foregroundStyle(.tertiary)
        }
    }
}

/// A ranking: name, bar, count.
struct RankBars: View {
    var items: [NameCount]
    var tint: Color
    @State private var grown = Snapshot.active
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    var body: some View {
        if items.isEmpty {
            Text("最近 30 天还没有使用记录").font(.system(size: 12)).foregroundStyle(.tertiary)
        } else {
            let top = max(items.map(\.count).max() ?? 1, 1)
            Grid(alignment: .leading, horizontalSpacing: 8, verticalSpacing: 7) {
                ForEach(Array(items.prefix(6).enumerated()), id: \.element.name) { i, it in
                    GridRow {
                        Text(it.name).font(.system(size: 12)).lineLimit(1).truncationMode(.middle)
                            .frame(maxWidth: 120, alignment: .leading)
                            .help(it.name)
                        GeometryReader { g in
                            Capsule().fill(tint.opacity(0.75))
                                .frame(width: grown ? max(4, g.size.width * CGFloat(it.count) / CGFloat(top)) : 4, height: 6)
                                .frame(maxHeight: .infinity, alignment: .center)
                                .animation(Theme.motion(Theme.spring.delay(Double(i) * 0.03), reduce: reduceMotion), value: grown)
                        }
                        .frame(minWidth: 40, maxWidth: .infinity)
                        .frame(height: 12)
                        Text("\(it.count)").font(.system(size: 11).monospacedDigit()).foregroundStyle(.secondary)
                            .gridColumnAlignment(.trailing)
                    }
                }
            }
            .onAppear { grown = true }
        }
    }
}

struct OverviewPage: View {
    @ObservedObject var portal: PortalStore
    @ObservedObject var machines: MachineStore
    var go: (NativePage) -> Void

    var body: some View {
        PageScroll(title: "概览", trailing: portal.updated.map { "更新于 " + $0.formatted(date: .omitted, time: .shortened) }) {
            if let e = portal.error { Text(e).font(.system(size: 12)).foregroundStyle(Theme.bad) }
            if let me = portal.me {
                loaded(me)
            } else if portal.error == nil {
                HStack { Spacer(); ProgressView(); Spacer() }.padding(.top, 60)
            }
        }
    }

    @ViewBuilder private func loaded(_ me: Me) -> some View {
        let all = machines.list?.machines ?? []
        let online = all.filter(\.online)
        let lib = portal.library?.items ?? []
        let cols = Columns.grid
        if let n = me.pending_changes, n > 0 {
            Label("有 \(n) 项改动等待你的机器应用，装有 hearth 的机器约一分钟内生效", systemImage: "bolt.fill")
                .font(.system(size: 12)).foregroundStyle(Theme.warn)
        }
        LazyVGrid(columns: cols, spacing: Columns.spacing) {
            KPI(title: "在线机器", value: "\(online.count) / \(all.filter { $0.status == "active" }.count)", icon: "desktopcomputer", tint: Theme.ok)
            KPI(title: "会话", value: "\(me.account.sessions ?? 0)", icon: "bubble.left.and.bubble.right", tint: Theme.blue)
            KPI(title: "Skills", value: "\(lib.filter { $0.type == "skill" }.count)", icon: "books.vertical", tint: Theme.accent)
            KPI(title: "Agents", value: "\(lib.filter { $0.type == "agent" }.count)", icon: "sparkles", tint: Color(red: 0.55, green: 0.40, blue: 0.85))
            KPI(title: "团队", value: "\(portal.teams.count)", icon: "person.2", tint: Theme.slate)
            KPI(title: "已用空间", value: Fmt.bytes(me.used_bytes) + (me.account.quota_bytes.map { $0 > 0 ? " / " + Fmt.bytes($0) : "" } ?? ""), icon: "externaldrive", tint: Theme.blue)
        }
        CardRow(spans: [2, 1]) {
            Panel(title: "每日上传") { UploadChart(series: me.series) }
            Panel(title: "在线机器") {
                if online.isEmpty {
                    Text("现在没有机器在线").font(.system(size: 12)).foregroundStyle(.tertiary)
                } else {
                    ForEach(online.prefix(6)) { m in
                        HStack(spacing: 8) {
                            MachineIcon(m: m)
                            Text(m.title).font(.system(size: 12)).lineLimit(1).truncationMode(.middle)
                            Spacer()
                            EnvGlyph(m: m)
                        }
                    }
                    Button("全部机器") { go(.machines) }.buttonStyle(.link).font(.system(size: 12))
                }
            }
        }
        CardRow(spans: [2, 1]) {
            Panel(title: "常用 Skills（30 天）") { RankBars(items: me.top_skills, tint: Theme.accent) }
            Panel(title: "常用 Agents（30 天）") { RankBars(items: me.top_agents, tint: Color(red: 0.55, green: 0.40, blue: 0.85)) }
        }
        Panel(title: "团队动态（7 天）") {
            if me.feed.isEmpty {
                Text("本周团队里没有新内容").font(.system(size: 12)).foregroundStyle(.tertiary)
            } else {
                ForEach(me.feed) { f in
                    HStack(alignment: .top, spacing: 8) {
                        Image(systemName: f.type == "agent" ? "sparkles" : "books.vertical").foregroundStyle(Theme.accent).frame(width: 18)
                        VStack(alignment: .leading, spacing: 2) {
                            HStack(spacing: 6) {
                                Text(f.name).font(.system(size: 12, weight: .medium))
                                Badge(text: "v\(f.version)", color: .secondary)
                                Text("在 \(f.team)").font(.system(size: 11)).foregroundStyle(.tertiary)
                            }
                            Text("\(f.version > 1 ? "更新" : "分享")：\(f.published_by) · \(Fmt.ago(f.published_at))")
                                .font(.system(size: 11)).foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
        Text("会话和凭证是端到端加密的，这里看不到它们的标题和内容。").font(.system(size: 11)).foregroundStyle(.tertiary)
    }
}

struct CountChart: View {
    var series: [(String, Int)]
    var tint: Color
    var unit: String
    @State private var grown = Snapshot.active
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    var body: some View {
        let peak = max(series.map(\.1).max() ?? 0, 1)
        VStack(spacing: 4) {
            GeometryReader { g in
            let h = g.size.height
            HStack(alignment: .bottom, spacing: 3) {
                ForEach(Array(series.enumerated()), id: \.offset) { i, d in
                    RoundedRectangle(cornerRadius: 2, style: .continuous)
                        .fill(d.1 > 0 ? tint : tint.opacity(0.18))
                        .frame(height: grown ? max(3, h * CGFloat(d.1) / CGFloat(peak)) : 3)
                        .frame(maxWidth: .infinity)
                        .animation(Theme.motion(Theme.spring.delay(Double(i) * 0.012), reduce: reduceMotion), value: grown)
                        .help("\(d.0)  \(d.1) \(unit)")
                }
            }
            .frame(width: g.size.width, height: h, alignment: .bottom)
            }
            .frame(minHeight: 90, maxHeight: .infinity)
            .onAppear { grown = true }
            HStack {
                Text(String((series.first?.0 ?? "").dropFirst(5)))
                Spacer()
                Text("最高 \(peak) \(unit)")
                Spacer()
                Text(String((series.last?.0 ?? "").dropFirst(5)))
            }
            .font(.system(size: 10)).foregroundStyle(.tertiary)
        }
    }
}
