import SwiftUI

/// Per-account settings the user's machines apply: resident skills,
/// how many skills to share, kept team packages, pending changes.
struct MySettingsPage: View {
    @ObservedObject var portal: PortalStore
    @State private var picked: Set<String> = []
    @State private var edited = false
    @State private var shareTop = 0
    @State private var shareEdited = false
    @State private var query = ""
    @State private var filter = Filter.all
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    enum Filter: Hashable { case all, picked, unpicked }

    private var skills: [LibraryItem] {
        (portal.library?.items ?? []).filter { $0.type == "skill" }.sorted { ($0.uses) > ($1.uses) }
    }
    private var current: Set<String> { Set(portal.residentNames) }

    var body: some View {
        PageScroll(title: "我的设置") {
            if !portal.intents.isEmpty { pending }
            resident
            CardRow(spans: [1, 2]) {
                sharing
                links
            }
        }
        .onAppear(perform: sync)
        .onChange(of: portal.updated) { _ in sync() }
    }

    private func sync() {
        if !edited { picked = current }
        if !shareEdited { shareTop = portal.settings?.share_top ?? 0 }
    }

    private var pending: some View {
        Panel(title: "等待机器应用的改动") {
            ForEach(Array(portal.intents.enumerated()), id: \.element.id) { i, it in
                if i > 0 { Divider().opacity(0.35) }
                HStack(spacing: 8) {
                    Image(systemName: "clock.arrow.circlepath").foregroundStyle(Theme.warn)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(Self.kind(it.kind)).font(.system(size: 12.5, weight: .medium))
                        if let d = it.data?.text, !d.isEmpty { Text(d).font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(2) }
                    }
                    Spacer()
                    if let t = it.created_at { Text(Fmt.ago(t)).font(.system(size: 10)).foregroundStyle(.tertiary) }
                    Button("取消") { Task { await portal.call("DELETE", "/v1/me/intents/\(it.id)", done: "已取消") } }
                        .controlSize(.small)
                }
                .padding(.vertical, 3)
            }
            Text("机器联网后一分钟左右内会应用这些改动。").font(.system(size: 11)).foregroundStyle(.tertiary)
        }
    }

    private var shown: [LibraryItem] {
        skills.filter { s in
            let hit = query.isEmpty || s.name.localizedCaseInsensitiveContains(query)
                || s.description.localizedCaseInsensitiveContains(query)
            switch filter {
            case .all: return hit
            case .picked: return hit && picked.contains(s.name)
            case .unpicked: return hit && !picked.contains(s.name)
            }
        }
    }
    private var maxUses: Int64 { max(1, skills.first?.uses ?? 1) }
    private var changes: Int { picked.symmetricDifference(current).count }

    private func set(_ names: Set<String>) {
        withAnimation(Theme.motion(Theme.spring, reduce: reduceMotion)) { picked = names; edited = true }
    }

    private func toggle(_ name: String) {
        var p = picked
        if p.contains(name) { p.remove(name) } else { p.insert(name) }
        set(p)
    }

    private var resident: some View {
        Panel {
            HStack(alignment: .center, spacing: 12) {
                VStack(alignment: .leading, spacing: 3) {
                    Text("常驻 Skills").font(.system(size: 15, weight: .semibold))
                    Text("常驻的 skill 会加载到每个 agent；其余的按需加载。").font(.system(size: 11.5)).foregroundStyle(.secondary)
                }
                Spacer()
                ResidentRing(count: picked.count, total: skills.count)
            }
            if skills.isEmpty {
                Text("库里还没有 skill。").font(.system(size: 12)).foregroundStyle(.tertiary)
            } else {
                if !picked.isEmpty { chips }
                toolbar
                list
                if edited && changes > 0 { saveBar.transition(.move(edge: .bottom).combined(with: .opacity)) }
            }
        }
    }

    private var chips: some View {
        Flow(spacing: 6) {
            ForEach(skills.filter { picked.contains($0.name) }) { s in
                HStack(spacing: 4) {
                    Text(s.name).font(.system(size: 11.5, weight: .medium)).lineLimit(1)
                    Button { toggle(s.name) } label: {
                        Image(systemName: "xmark").font(.system(size: 8, weight: .bold))
                    }
                    .buttonStyle(.plain).foregroundStyle(Theme.accent.opacity(0.8)).help("不再常驻")
                }
                .padding(.leading, 9).padding(.trailing, 7).padding(.vertical, 4)
                .background(Capsule().fill(Theme.accent.opacity(0.13)))
                .foregroundStyle(Theme.accent)
                .transition(.scale(scale: 0.85).combined(with: .opacity))
            }
        }
    }

    private var toolbar: some View {
        HStack(spacing: 8) {
            HStack(spacing: 5) {
                Image(systemName: "magnifyingglass").foregroundStyle(.tertiary)
                TextField("搜索 skill", text: $query).textFieldStyle(.plain)
                if !query.isEmpty {
                    Button { query = "" } label: { Image(systemName: "xmark.circle.fill") }
                        .buttonStyle(.plain).foregroundStyle(.tertiary)
                }
            }
            .font(.system(size: 12))
            .padding(.horizontal, 8).padding(.vertical, 5)
            .background(RoundedRectangle(cornerRadius: 7, style: .continuous).fill(Color.primary.opacity(0.05)))
            .frame(maxWidth: 260)
            Picker("", selection: $filter) {
                Text("全部").tag(Filter.all)
                Text("已选 \(picked.count)").tag(Filter.picked)
                Text("未选").tag(Filter.unpicked)
            }
            .pickerStyle(.segmented).labelsHidden().fixedSize()
            Spacer()
            Menu {
                Button("最常用 5 个") { set(Set(skills.prefix(5).map(\.name))) }
                Button("最常用 10 个") { set(Set(skills.prefix(10).map(\.name))) }
                Divider()
                Button("全部常驻") { set(Set(skills.map(\.name))) }
                Button("全部取消") { set([]) }
            } label: {
                Label("快速选择", systemImage: "wand.and.stars")
            }
            .menuStyle(.borderlessButton).fixedSize()
        }
        .controlSize(.small)
    }

    private var list: some View {
        let items = shown
        return Group {
            if items.isEmpty {
                Text(query.isEmpty ? "这里没有 skill。" : "没有匹配「\(query)」的 skill。")
                    .font(.system(size: 12)).foregroundStyle(.tertiary)
                    .frame(maxWidth: .infinity, minHeight: 60)
            } else {
                ScrollView {
                    LazyVGrid(columns: [GridItem(.flexible(), spacing: 14), GridItem(.flexible(), spacing: 14)], spacing: 2) {
                        ForEach(items) { s in skillRow(s) }
                    }
                    .padding(.vertical, 2)
                }
                .scrollIndicators(.never)
                .frame(height: min(CGFloat((items.count + 1) / 2) * 40 + 4, 420))
            }
        }
    }

    private func skillRow(_ s: LibraryItem) -> some View {
        let on = picked.contains(s.name)
        return Button { toggle(s.name) } label: {
            HStack(spacing: 9) {
                Image(systemName: on ? "checkmark.circle.fill" : "circle")
                    .font(.system(size: 15))
                    .foregroundStyle(on ? Theme.accent : Color.secondary.opacity(0.5))
                VStack(alignment: .leading, spacing: 1) {
                    Text(s.name).font(.system(size: 12.5, weight: on ? .semibold : .regular)).lineLimit(1).truncationMode(.middle)
                    Text(s.description.isEmpty ? "没有描述" : s.description)
                        .font(.system(size: 10.5)).foregroundStyle(.tertiary).lineLimit(1)
                }
                Spacer(minLength: 6)
                if s.uses > 0 {
                    UsageMeter(value: s.uses, peak: maxUses)
                }
            }
            .frame(height: 38)
            .contentShape(Rectangle())
        }
        .buttonStyle(ListRowStyle())
        .help(s.description.isEmpty ? s.name : s.description)
        .accessibilityValue(on ? "常驻" : "按需")
    }

    private var saveBar: some View {
        HStack(spacing: 10) {
            Image(systemName: "pencil.circle.fill").foregroundStyle(Theme.accent)
            Text("有 \(changes) 处改动未保存").font(.system(size: 12, weight: .medium))
            Spacer()
            Button("还原") { set(current); edited = false }
            Button("保存") {
                let all = picked.count == skills.count
                let names = all ? [] : skills.map(\.name).filter { picked.contains($0) }
                Task { await portal.queue("resident", ["names": names]); edited = false }
            }
            .modifier(PrimaryButton())
            .keyboardShortcut("s", modifiers: .command)
        }
        .controlSize(.small)
        .padding(.horizontal, 12).padding(.vertical, 8)
        .background(RoundedRectangle(cornerRadius: 9, style: .continuous).fill(Theme.accent.opacity(0.09)))
    }

    private var sharing: some View {
        Panel(title: "自动共享") {
            Text("把最常用的 skills 自动共享给你能发布的团队。").font(.system(size: 11.5)).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            HStack(alignment: .firstTextBaseline, spacing: 4) {
                Text(shareTop == 0 ? "关闭" : "\(shareTop)")
                    .font(.system(size: 26, weight: .semibold, design: .rounded)).monospacedDigit()
                    .contentTransition(.numericText())
                    .foregroundStyle(shareTop == 0 ? Color.secondary : Theme.accent)
                if shareTop > 0 { Text("个最常用的").font(.system(size: 11.5)).foregroundStyle(.secondary) }
                Spacer()
                Stepper("", value: Binding(get: { shareTop }, set: { v in
                    withAnimation(Theme.motion(Theme.spring, reduce: reduceMotion)) { shareTop = v; shareEdited = true }
                }), in: 0...50).labelsHidden()
            }
            Spacer(minLength: 0)
            HStack {
                Spacer()
                Button("保存") { Task { await portal.queue("share-top", ["n": shareTop]); shareEdited = false } }
                    .modifier(PrimaryButton()).controlSize(.small)
                    .disabled(!shareEdited || shareTop == (portal.settings?.share_top ?? 0))
            }
        }
    }

    private var links: some View {
        Panel(title: "保留的团队内容") {
            let ls = portal.settings?.links ?? []
            if ls.isEmpty {
                Text("还没有保留团队共享的 skill 或 agent。在「团队」页打开一个共享项即可保留。").font(.system(size: 12)).foregroundStyle(.tertiary)
            }
            ForEach(Array(ls.enumerated()), id: \.element.id) { i, l in
                if i > 0 { Divider().opacity(0.35) }
                HStack(spacing: 8) {
                    Image(systemName: l.type == "agent" ? "sparkles" : "books.vertical").foregroundStyle(Theme.accent).frame(width: 18)
                    Text(l.name).font(.system(size: 12.5, weight: .medium))
                    if let s = l.source { Badge(text: s, color: Theme.slate) }
                    if let v = l.version { Badge(text: "v\(v)", color: .secondary) }
                    if l.auto == true { Badge(text: "自动", color: Theme.blue) }
                    Spacer()
                    Button("取消保留") { Task { await portal.queue("unlink", ["type": l.type, "name": l.name]) } }
                        .controlSize(.small)
                }
                .padding(.vertical, 3)
            }
        }
    }

    static func kind(_ k: String) -> String {
        switch k {
        case "resident": return "修改常驻 skills"
        case "share-top": return "修改自动共享"
        case "link": return "保留团队内容"
        case "unlink": return "取消保留"
        default: return k
        }
    }
}

/// "8 / 78" with a ring that fills as more skills become resident.
private struct ResidentRing: View {
    var count: Int
    var total: Int
    var body: some View {
        let f = total == 0 ? 0 : Double(count) / Double(total)
        HStack(spacing: 8) {
            ZStack {
                Circle().stroke(Color.primary.opacity(0.08), lineWidth: 4)
                Circle().trim(from: 0, to: f)
                    .stroke(Theme.accent, style: StrokeStyle(lineWidth: 4, lineCap: .round))
                    .rotationEffect(.degrees(-90))
            }
            .frame(width: 26, height: 26)
            VStack(alignment: .leading, spacing: 0) {
                Text("\(count)").font(.system(size: 16, weight: .semibold, design: .rounded)).monospacedDigit()
                    .contentTransition(.numericText())
                Text("/ \(total) 常驻").font(.system(size: 10)).foregroundStyle(.tertiary)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// How often a skill was used in the last 30 days, relative to the top one.
private struct UsageMeter: View {
    var value: Int64
    var peak: Int64
    var body: some View {
        HStack(spacing: 5) {
            GeometryReader { g in
                Capsule().fill(Color.primary.opacity(0.07))
                    .overlay(alignment: .leading) {
                        Capsule().fill(Theme.accent.opacity(0.55))
                            .frame(width: max(3, g.size.width * CGFloat(value) / CGFloat(peak)))
                    }
            }
            .frame(width: 36, height: 4)
            Text("\(value)").font(.system(size: 10)).foregroundStyle(.tertiary).monospacedDigit()
                .frame(minWidth: 16, alignment: .trailing)
        }
        .help("30 天内用了 \(value) 次")
    }
}

/// Wraps children onto new lines like text.
struct Flow: Layout {
    var spacing: CGFloat = 6
    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let w = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, line: CGFloat = 0, widest: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x > 0 && x + s.width > w { y += line + spacing; x = 0; line = 0 }
            x += s.width + spacing; line = max(line, s.height); widest = max(widest, x - spacing)
        }
        return CGSize(width: proposal.width ?? widest, height: y + line)
    }
    func placeSubviews(in b: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = b.minX, y = b.minY, line: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x > b.minX && x + s.width > b.maxX { y += line + spacing; x = b.minX; line = 0 }
            v.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(s))
            x += s.width + spacing; line = max(line, s.height)
        }
    }
}
