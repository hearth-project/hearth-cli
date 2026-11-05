import SwiftUI

struct MachinesPage: View {
    @ObservedObject var store: MachineStore
    @State private var expanded: String?
    @State private var note: String?
    @State private var copying = false

    private var all: [Machine] {
        (store.list?.machines ?? []).sorted { a, b in
            if a.online != b.online { return a.online }
            if a.isThis != b.isThis { return a.isThis }
            return a.title.localizedStandardCompare(b.title) == .orderedAscending
        }
    }

    var body: some View {
        PageScroll(title: "机器", trailing: store.updated.map { "更新于 " + $0.formatted(date: .omitted, time: .shortened) }) {
            HStack(spacing: 10) {
                Button {
                    copying = true
                    Task { note = await store.copyInstallCommand(); copying = false }
                } label: {
                    Label("新增开发机", systemImage: "plus.rectangle.on.rectangle")
                }
                .modifier(PrimaryButton())
                .disabled(copying)
                if let n = note { Text(n).font(.system(size: 12)).foregroundStyle(.secondary) }
                Spacer()
            }
            if let e = store.error { Text(e).font(.system(size: 12)).foregroundStyle(Theme.bad) }
            if store.list == nil && store.error == nil {
                HStack { Spacer(); ProgressView(); Spacer() }.padding(.top, 60)
            }
            group("在线", all.filter(\.online))
            group("离线", all.filter { !$0.online })
        }
    }

    @ViewBuilder private func group(_ title: String, _ ms: [Machine]) -> some View {
        if !ms.isEmpty {
            Panel(title: "\(title)  \(ms.count)") {
                VStack(spacing: 0) {
                    ForEach(Array(ms.enumerated()), id: \.element.id) { i, m in
                        if i > 0 { Divider().opacity(0.5) }
                        MachineRow(m: m, open: expanded == m.id,
                                   toggle: { withAnimation(.easeOut(duration: 0.15)) { expanded = expanded == m.id ? nil : m.id } },
                                   say: { note = $0 },
                                   rename: { try await store.rename(m, to: $0) })
                    }
                }
            }
        }
    }
}

struct LibraryPage: View {
    @ObservedObject var portal: PortalStore
    @State private var kind = "skill"
    @State private var query = ""

    var body: some View {
        PageScroll(title: "技能与 Agent", trailing: portal.library.map { "资源库 v\($0.version)" }) {
            if let e = portal.error { Text(e).font(.system(size: 12)).foregroundStyle(Theme.bad) }
            if let lib = portal.library {
                let count = { (k: String) in lib.items.filter { $0.type == k }.count }
                HStack(spacing: 10) {
                    Picker("", selection: $kind) {
                        Text("Skills \(count("skill"))").tag("skill")
                        Text("Agents \(count("agent"))").tag("agent")
                    }
                    .pickerStyle(.segmented).labelsHidden().frame(width: 220)
                    TextField("搜索", text: $query).textFieldStyle(.roundedBorder).frame(maxWidth: 260)
                    Spacer()
                }
                let q = query.lowercased()
                let items = lib.items.filter { $0.type == kind && (q.isEmpty || ($0.name + " " + $0.description).lowercased().contains(q)) }
                Panel {
                    if items.isEmpty {
                        Text("这里还没有内容").font(.system(size: 12)).foregroundStyle(.tertiary)
                    } else {
                        VStack(spacing: 0) {
                            ForEach(Array(items.enumerated()), id: \.element.id) { i, x in
                                if i > 0 { Divider().opacity(0.5) }
                                itemRow(x)
                            }
                        }
                    }
                }
                Text("按最近 30 天的使用次数排序（所有机器）。常驻的 skill 会加载进每个 agent，其余的在需要时由 skill 搜索找到。修改：hearth config --edit")
                    .font(.system(size: 11)).foregroundStyle(.tertiary)
            } else if portal.error == nil {
                HStack { Spacer(); ProgressView(); Spacer() }.padding(.top, 60)
            }
        }
    }

    private func itemRow(_ x: LibraryItem) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: x.type == "agent" ? "sparkles" : "books.vertical").foregroundStyle(Theme.accent).frame(width: 20).padding(.top, 2)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(x.name).font(.system(size: 13, weight: .medium))
                    if x.resident == true { Badge(text: "常驻", color: Theme.blue) }
                }
                if !x.description.isEmpty {
                    Text(x.description).font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(2)
                }
            }
            Spacer(minLength: 8)
            VStack(alignment: .trailing, spacing: 2) {
                Text("\(x.uses) 次").font(.system(size: 11)).foregroundStyle(.secondary)
                Text(Fmt.bytes(x.size)).font(.system(size: 10)).foregroundStyle(.tertiary)
            }
        }
        .padding(.vertical, 8)
    }
}

struct TeamsPage: View {
    @ObservedObject var portal: PortalStore

    var body: some View {
        PageScroll(title: "团队") {
            if let e = portal.error { Text(e).font(.system(size: 12)).foregroundStyle(Theme.bad) }
            if portal.teams.isEmpty {
                Panel { Text(portal.me == nil ? "加载中…" : "还没有加入团队。创建或加入：hearth team create / hearth team join").font(.system(size: 12)).foregroundStyle(.secondary) }
            }
            ForEach(portal.teams) { t in
                Panel {
                    HStack(spacing: 8) {
                        Image(systemName: "person.2.fill").foregroundStyle(Theme.slate)
                        Text(t.name).font(.system(size: 15, weight: .semibold))
                        Badge(text: Self.role(t.role), color: .secondary)
                        Spacer()
                        Text("\(t.members.count) 位成员 · \(t.packages.count) 个共享").font(.system(size: 11)).foregroundStyle(.tertiary)
                    }
                    if !t.members.isEmpty {
                        Text(t.members.map { "\($0.email)（\(Self.role($0.role))）" }.joined(separator: "，"))
                            .font(.system(size: 11)).foregroundStyle(.secondary)
                    }
                    if !t.packages.isEmpty {
                        Divider().opacity(0.5)
                        ForEach(t.packages) { p in
                            HStack(alignment: .top, spacing: 8) {
                                Image(systemName: p.type == "agent" ? "sparkles" : "books.vertical").foregroundStyle(Theme.accent).frame(width: 18)
                                VStack(alignment: .leading, spacing: 2) {
                                    HStack(spacing: 6) {
                                        Text(p.name).font(.system(size: 12, weight: .medium))
                                        Badge(text: "v\(p.version)", color: .secondary)
                                    }
                                    if let d = p.description, !d.isEmpty {
                                        Text(d).font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(2)
                                    }
                                }
                                Spacer()
                                Text("\(p.published_by) · \(Fmt.ago(p.published_at))").font(.system(size: 10)).foregroundStyle(.tertiary)
                            }
                        }
                    }
                }
            }
        }
    }

    static func role(_ r: String) -> String {
        switch r {
        case "owner": return "所有者"
        case "maintainer": return "维护者"
        case "reader": return "只读"
        default: return r
        }
    }
}
