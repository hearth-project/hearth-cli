import SwiftUI

struct MachinesPage: View {
    @ObservedObject var store: MachineStore
    @ObservedObject var portal: PortalStore
    @State private var expanded: String?
    @State private var note: String?
    @State private var copying = false
    @State private var removing: Machine?

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
            PortalNoticeBar(store: portal)
            if let e = store.error { Text(e).font(.system(size: 12)).foregroundStyle(Theme.bad) }
            if store.list == nil && store.error == nil {
                HStack { Spacer(); ProgressView(); Spacer() }.padding(.top, 60)
            }
            group("在线", all.filter(\.online))
            group("离线", all.filter { !$0.online })
        }
        .confirmationDialog("移除这台机器？", isPresented: Binding(get: { removing != nil }, set: { if !$0 { removing = nil } }), presenting: removing) { m in
            Button("移除 \(m.title)", role: .destructive) {
                Task {
                    await portal.call("DELETE", "/v1/me/devices/" + adminPath(m.id), done: "已移除 \(m.title)。")
                    await store.refresh()
                }
            }
        } message: { _ in
            Text("这台机器会退出登录，不再同步。要再次使用，需要在它上面重新运行 hearth setup。")
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
                                   rename: { try await store.rename(m, to: $0) },
                                   info: portal.device(m),
                                   remove: m.isThis ? nil : { removing = m })
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
    @State private var open: LibraryItem?

    var body: some View {
        PageScroll(title: "技能与 Agent", trailing: portal.library.map { "资源库 v\($0.version)" }) {
            PortalNoticeBar(store: portal)
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
                                    .contentShape(Rectangle())
                                    .onTapGesture { open = x }
                            }
                        }
                    }
                }
                Text("按最近 30 天的使用次数排序（所有机器）。点一项查看内容、设为常驻或共享到团队。常驻的 skill 会加载进每个 agent，其余的在需要时由 skill 搜索找到。")
                    .font(.system(size: 11)).foregroundStyle(.tertiary)
            } else if portal.error == nil {
                HStack { Spacer(); ProgressView(); Spacer() }.padding(.top, 60)
            }
        }
        .sheet(item: $open) { x in LibraryDetailSheet(portal: portal, item: x) { open = nil } }
    }

    private func itemRow(_ x: LibraryItem) -> some View {
        let teams = portal.teams.filter { t in t.packages.contains { $0.type == x.type && $0.name == x.name } }.map(\.name)
        let resident = x.type == "skill" && portal.residentNames.contains(x.name)
        return HStack(alignment: .top, spacing: 10) {
            Image(systemName: x.type == "agent" ? "sparkles" : "books.vertical").foregroundStyle(Theme.accent).frame(width: 20).padding(.top, 2)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(x.name).font(.system(size: 13, weight: .medium))
                    if resident || x.resident == true { Badge(text: "常驻", color: Theme.blue) }
                    ForEach(teams, id: \.self) { Badge(text: $0, color: Theme.slate) }
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
            Image(systemName: "chevron.right").font(.system(size: 10, weight: .semibold)).foregroundStyle(.quaternary).padding(.top, 4)
        }
        .padding(.vertical, 8)
    }
}

