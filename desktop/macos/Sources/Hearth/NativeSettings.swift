import SwiftUI

/// Per-account settings the user's machines apply: resident skills,
/// how many skills to share, kept team packages, pending changes.
struct MySettingsPage: View {
    @ObservedObject var portal: PortalStore
    @State private var picked: Set<String> = []
    @State private var edited = false
    @State private var shareTop = 0
    @State private var shareEdited = false

    private var skills: [LibraryItem] {
        (portal.library?.items ?? []).filter { $0.type == "skill" }.sorted { ($0.uses) > ($1.uses) }
    }
    private var current: Set<String> { Set(portal.residentNames) }

    var body: some View {
        PageScroll(title: "我的设置") {
            PortalNoticeBar(store: portal)
            if !portal.intents.isEmpty { pending }
            resident
            sharing
            links
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

    private var resident: some View {
        Panel(title: "常驻 Skills") {
            Text("常驻的 skill 会加载到每个 agent；其余的按需加载。").font(.system(size: 11.5)).foregroundStyle(.secondary)
            HStack(spacing: 6) {
                Button("全部") { picked = Set(skills.map(\.name)); edited = true }
                Button("全不选") { picked = []; edited = true }
                Button("最常用 5 个") { picked = Set(skills.prefix(5).map(\.name)); edited = true }
                Spacer()
                Text("已选 \(picked.count) / \(skills.count)").font(.system(size: 11)).foregroundStyle(.tertiary)
                Button("保存") {
                    let all = picked.count == skills.count
                    let names = all ? [] : skills.map(\.name).filter { picked.contains($0) }
                    Task { await portal.queue("resident", ["names": names]); edited = false }
                }
                .modifier(PrimaryButton()).disabled(!edited || picked == current)
            }
            .controlSize(.small)
            if skills.isEmpty {
                Text("库里还没有 skill。").font(.system(size: 12)).foregroundStyle(.tertiary)
            } else {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 210), spacing: 8, alignment: .leading)], alignment: .leading, spacing: 4) {
                    ForEach(skills) { s in
                        Toggle(isOn: Binding(get: { picked.contains(s.name) }, set: { on in
                            if on { picked.insert(s.name) } else { picked.remove(s.name) }
                            edited = true
                        })) {
                            HStack(spacing: 4) {
                                Text(s.name).font(.system(size: 12)).lineLimit(1).truncationMode(.middle)
                                if s.uses > 0 { Text("\(s.uses)").font(.system(size: 10)).foregroundStyle(.tertiary) }
                            }
                        }
                        .toggleStyle(.checkbox)
                    }
                }
            }
        }
    }

    private var sharing: some View {
        Panel(title: "自动共享") {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("向团队自动共享最常用的 skills").font(.system(size: 12.5))
                    Text("0 表示关闭。只共享到你能发布的团队。").font(.system(size: 11)).foregroundStyle(.secondary)
                }
                Spacer()
                Stepper(value: Binding(get: { shareTop }, set: { shareTop = $0; shareEdited = true }), in: 0...50) {
                    Text(shareTop == 0 ? "关闭" : "前 \(shareTop) 个").font(.system(size: 12, weight: .medium)).monospacedDigit()
                }
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
