import SwiftUI

/// Result of the last action on a user page.
struct PortalNoticeBar: View {
    @ObservedObject var store: PortalStore
    var body: some View {
        if let n = store.notice {
            HStack(spacing: 8) {
                Image(systemName: store.noticeIsError ? "exclamationmark.triangle.fill" : "checkmark.circle.fill")
                    .foregroundStyle(store.noticeIsError ? Theme.bad : Theme.ok)
                Text(n).font(.system(size: 12)).lineLimit(3).textSelection(.enabled)
                Spacer()
                Button { store.notice = nil } label: { Image(systemName: "xmark") }.buttonStyle(.plain).foregroundStyle(.tertiary)
            }
            .padding(10)
            .background((store.noticeIsError ? Theme.bad : Theme.ok).opacity(0.10), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        }
    }
}

struct ItemFile: Decodable, Hashable { var path: String; var size: Int64? }
struct ItemBody: Decodable { var content: String?; var files: [ItemFile]? }

/// Sheet header with title, subtitle and a close button.
struct SheetHeader: View {
    var icon: String
    var title: String
    var subtitle: String? = nil
    var close: () -> Void
    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: icon).font(.system(size: 16, weight: .semibold)).foregroundStyle(Theme.accent)
                .frame(width: 30, height: 30)
                .background(Theme.accent.opacity(0.12), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.system(size: 15, weight: .semibold))
                if let s = subtitle, !s.isEmpty { Text(s).font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(2) }
            }
            Spacer()
            Button(action: close) { Image(systemName: "xmark.circle.fill").font(.system(size: 16)) }
                .buttonStyle(.plain).foregroundStyle(.tertiary).keyboardShortcut(.cancelAction)
        }
    }
}

/// Content of one skill or agent, with resident toggle and share.
struct LibraryDetailSheet: View {
    @ObservedObject var portal: PortalStore
    var item: LibraryItem
    var close: () -> Void
    @State private var body_: ItemBody?
    @State private var failed: String?
    @State private var sharing = false

    private var resident: Bool { portal.residentNames.contains(item.name) }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            SheetHeader(icon: item.type == "agent" ? "sparkles" : "books.vertical", title: item.name,
                        subtitle: item.description, close: close)
            HStack(spacing: 8) {
                Badge(text: item.type == "agent" ? "Agent" : "Skill", color: .secondary)
                Text("\(item.uses) 次使用 · \(Fmt.bytes(item.size))").font(.system(size: 11)).foregroundStyle(.secondary)
                Spacer()
                if item.type == "skill" {
                    Toggle("常驻", isOn: Binding(get: { resident }, set: { on in
                        var names = portal.residentNames.filter { $0 != item.name }
                        if on { names.append(item.name) }
                        Task { await portal.queue("resident", ["names": names]) }
                    }))
                    .toggleStyle(.switch).controlSize(.small)
                    .help("常驻的 skill 会加载进每个 agent")
                }
                if portal.teams.contains(where: \.canPublish) {
                    Button { sharing = true } label: { Label("共享到团队…", systemImage: "square.and.arrow.up") }
                }
            }
            Divider()
            Group {
                if let b = body_ {
                    ScrollView {
                        VStack(alignment: .leading, spacing: 10) {
                            if let files = b.files, !files.isEmpty {
                                VStack(alignment: .leading, spacing: 3) {
                                    ForEach(files, id: \.self) { f in
                                        HStack {
                                            Image(systemName: "doc").foregroundStyle(.tertiary)
                                            Text(f.path).font(.system(size: 11, design: .monospaced))
                                            Spacer()
                                            if let s = f.size { Text(Fmt.bytes(s)).font(.system(size: 10)).foregroundStyle(.tertiary) }
                                        }
                                    }
                                }
                                .padding(8).background(Theme.card, in: RoundedRectangle(cornerRadius: 8))
                            }
                            Text(b.content ?? "").font(.system(size: 11.5, design: .monospaced))
                                .textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }
                } else if let f = failed {
                    Text(f).font(.system(size: 12)).foregroundStyle(Theme.bad)
                } else {
                    HStack { Spacer(); ProgressView(); Spacer() }.padding(.top, 40)
                }
            }
            .frame(maxHeight: .infinity, alignment: .top)
            PortalNoticeBar(store: portal)
        }
        .padding(18)
        .frame(width: 620, height: 560)
        .task { await load() }
        .sheet(isPresented: $sharing) { ShareSheet(portal: portal, item: item) { sharing = false } }
    }

    private func load() async {
        let path = "/v1/me/library/\(adminPath(item.type))/\(adminPath(item.name))"
        guard let d = await portal.call("GET", path, reload: false) else { failed = portal.notice ?? "读取失败"; portal.notice = nil; return }
        do { body_ = try JSONDecoder().decode(ItemBody.self, from: d) } catch { failed = "读取失败：\(error.localizedDescription)" }
    }
}

struct ShareResult: Decodable { var team: String; var version: Int?; var created: Bool?; var error: String? }

/// Publish a skill or agent to teams where the user can publish.
struct ShareSheet: View {
    @ObservedObject var portal: PortalStore
    var item: LibraryItem
    var close: () -> Void
    @State private var picked: Set<String> = []
    @State private var busy = false
    @State private var results: [ShareResult] = []

    private var teams: [Team] { portal.teams.filter(\.canPublish) }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            SheetHeader(icon: "square.and.arrow.up", title: "共享 \(item.name)", subtitle: "选择要发布到的团队，成员的机器会收到这个版本。", close: close)
            VStack(alignment: .leading, spacing: 6) {
                ForEach(teams) { t in
                    let shared = t.packages.contains { $0.type == item.type && $0.name == item.name }
                    Toggle(isOn: Binding(get: { picked.contains(t.name) }, set: { on in
                        if on { picked.insert(t.name) } else { picked.remove(t.name) }
                    })) {
                        HStack(spacing: 6) {
                            Text(t.name).font(.system(size: 13))
                            if shared { Badge(text: "已共享", color: Theme.blue) }
                        }
                    }
                    .toggleStyle(.checkbox)
                }
            }
            .padding(10).frame(maxWidth: .infinity, alignment: .leading)
            .background(Theme.card, in: RoundedRectangle(cornerRadius: 10))
            ForEach(results, id: \.team) { r in
                Text(r.error.map { "\(r.team)：失败，\($0)" }
                     ?? "\(r.team)：" + ((r.created ?? false) ? "已发布为 v\(r.version ?? 0)" : "内容没有变化（v\(r.version ?? 0)）"))
                    .font(.system(size: 12)).foregroundStyle(r.error == nil ? Theme.ok : Theme.bad)
            }
            HStack {
                Spacer()
                Button("取消", action: close)
                Button { Task { await share() } } label: { busy ? AnyView(ProgressView().controlSize(.small)) : AnyView(Text("共享")) }
                    .keyboardShortcut(.defaultAction).disabled(picked.isEmpty || busy)
            }
        }
        .padding(18).frame(width: 420)
        .onAppear {
            picked = Set(teams.filter { t in !t.packages.contains { $0.type == item.type && $0.name == item.name } }.map(\.name))
        }
    }

    private func share() async {
        busy = true; defer { busy = false }
        let body: [String: Any] = ["type": item.type, "name": item.name, "teams": Array(picked).sorted()]
        guard let d = await portal.call("POST", "/v1/me/share", body, reload: true) else { return }
        results = (try? JSONDecoder().decode([ShareResult].self, from: d)) ?? []
        if results.isEmpty { close() }
    }
}
