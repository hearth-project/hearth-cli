import SwiftUI

struct PackageVersion: Decodable, Hashable { var version: Int; var published_by: String?; var published_at: Int64? }
struct PackageDetail: Decodable {
    var latest: Int?
    var linked: Bool?
    var item: ItemBody?
    var versions: [PackageVersion]?
}

struct OpenPackage: Identifiable { var team: String; var pkg: TeamPackage; var id: String { team + "/" + pkg.id } }

/// Teams the user belongs to: members, shared packages, invite and leave.
struct TeamsPage: View {
    @ObservedObject var portal: PortalStore
    @State private var open: OpenPackage?
    @State private var leaving: Team?
    @State private var invite: (team: String, code: String)?

    var body: some View {
        PageScroll(title: "团队") {
            if let e = portal.error { Text(e).font(.system(size: 12)).foregroundStyle(Theme.bad) }
            if let i = invite { inviteBox(i.team, i.code) }
            if portal.teams.isEmpty {
                Panel { Text(portal.me == nil ? "加载中…" : "还没有加入团队。创建：hearth team create <名称>；加入：hearth team join <邀请码>").font(.system(size: 12)).foregroundStyle(.secondary) }
            }
            ForEach(portal.teams) { t in teamCard(t) }
        }
        .sheet(item: $open) { o in PackageSheet(portal: portal, team: o.team, pkg: o.pkg) { open = nil }.toastHost() }
        .confirmationDialog(leaving.map { "退出团队 \($0.name)？" } ?? "", isPresented: Binding(get: { leaving != nil }, set: { if !$0 { leaving = nil } })) {
            Button("退出团队", role: .destructive) {
                if let t = leaving { Task { await portal.call("DELETE", "/v1/me/teams/\(adminPath(t.name))", done: "已退出 \(t.name)") } }
                leaving = nil
            }
        } message: { Text("退出后不再收到这个团队共享的 skills 和 agents。") }
    }

    private func teamCard(_ t: Team) -> some View {
        Panel {
            HStack(spacing: 10) {
                Image(systemName: "person.2.fill").foregroundStyle(Theme.slate)
                    .frame(width: 30, height: 30).background(Theme.slate.opacity(0.12), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(t.name).font(.system(size: 15, weight: .semibold))
                        Badge(text: Self.role(t.role), color: t.canPublish ? Theme.accent : .secondary)
                        if let v = t.via { Badge(text: "通过 \(v)", color: Theme.blue) }
                    }
                    Text("\(t.members.count) 位成员 · \(t.packages.count) 个共享").font(.system(size: 11)).foregroundStyle(.tertiary)
                }
                Spacer()
                if t.canPublish {
                    Button { Task { await makeInvite(t.name) } } label: { Label("邀请成员", systemImage: "person.badge.plus") }
                }
                if t.role != "reader" && t.via == nil {
                    Menu {
                        Button("退出团队…", role: .destructive) { leaving = t }
                    } label: { Image(systemName: "ellipsis.circle") }
                    .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize()
                }
            }
            if !t.members.isEmpty {
                FlowChips(items: t.members.map { "\($0.email) · \(Self.role($0.role))" })
            }
            let links = (t.shared_with ?? []) + (t.shared_from ?? [])
            if !links.isEmpty {
                Text("与其他团队共享：" + links.joined(separator: "、")).font(.system(size: 11)).foregroundStyle(.secondary)
            }
            if !t.packages.isEmpty {
                Divider().opacity(0.5)
                ForEach(Array(t.packages.enumerated()), id: \.element.id) { i, p in
                    if i > 0 { Divider().opacity(0.35) }
                    packageRow(p).contentShape(Rectangle()).onTapGesture { open = OpenPackage(team: t.name, pkg: p) }
                }
            }
        }
    }

    private func packageRow(_ p: TeamPackage) -> some View {
        let linked = portal.settings?.links?.contains { $0.type == p.type && $0.name == p.name } ?? false
        return HStack(alignment: .top, spacing: 8) {
            Image(systemName: p.type == "agent" ? "sparkles" : "books.vertical").foregroundStyle(Theme.accent).frame(width: 18).padding(.top, 1)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(p.name).font(.system(size: 12.5, weight: .medium))
                    Badge(text: "v\(p.version)", color: .secondary)
                    if linked { Badge(text: "已保留", color: Theme.ok) }
                }
                if let d = p.description, !d.isEmpty { Text(d).font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(2) }
            }
            Spacer()
            Text("\(p.published_by) · \(Fmt.ago(p.published_at))").font(.system(size: 10)).foregroundStyle(.tertiary)
            Image(systemName: "chevron.right").font(.system(size: 10, weight: .semibold)).foregroundStyle(.quaternary).padding(.top, 2)
        }
        .padding(.vertical, 5)
    }

    private func inviteBox(_ team: String, _ code: String) -> some View {
        let cmd = "hearth team join \(code)"
        return Panel(title: "\(team) 的邀请码（7 天内有效，用一次）") {
            HStack {
                Text(cmd).font(.system(size: 12, design: .monospaced)).textSelection(.enabled)
                Spacer()
                Button { Actions.copy(cmd); portal.noticeIsError = false; portal.notice = "已复制" } label: { Label("复制", systemImage: "doc.on.doc") }
                Button { invite = nil } label: { Image(systemName: "xmark") }.buttonStyle(.plain).foregroundStyle(.tertiary)
            }
            Text("让对方在已登录 Hearth 的机器上运行这条命令。").font(.system(size: 11)).foregroundStyle(.secondary)
        }
    }

    private func makeInvite(_ team: String) async {
        struct Reply: Decodable { var code: String }
        guard let d = await portal.call("POST", "/v1/me/teams/\(adminPath(team))/invite", [:], reload: false),
              let r = try? JSONDecoder().decode(Reply.self, from: d) else { return }
        invite = (team, r.code)
    }

    static func role(_ r: String) -> String {
        switch r {
        case "owner": return "所有者"
        case "maintainer": return "维护者"
        case "reader": return "只读"
        case "member": return "成员"
        default: return r
        }
    }
}

/// Small wrapping chips.
struct FlowChips: View {
    var items: [String]
    var body: some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 200), spacing: 6, alignment: .leading)], alignment: .leading, spacing: 6) {
            ForEach(items, id: \.self) { s in
                Text(s).font(.system(size: 11)).lineLimit(1).truncationMode(.middle)
                    .padding(.horizontal, 8).padding(.vertical, 3)
                    .background(Capsule().fill(Color.secondary.opacity(0.10)))
            }
        }
    }
}

/// One team package: content, versions, keep on my machines.
struct PackageSheet: View {
    @ObservedObject var portal: PortalStore
    var team: String
    var pkg: TeamPackage
    var close: () -> Void
    @State private var detail: PackageDetail?
    @State private var failed: String?

    private var linked: Bool { portal.settings?.links?.contains { $0.type == pkg.type && $0.name == pkg.name } ?? (detail?.linked ?? false) }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            SheetHeader(icon: pkg.type == "agent" ? "sparkles" : "books.vertical", title: "\(team):\(pkg.name)", subtitle: pkg.description, close: close)
            HStack(spacing: 8) {
                Badge(text: "v\(detail?.latest ?? pkg.version)", color: .secondary)
                Text("\(pkg.published_by) 发布于 \(Fmt.ago(pkg.published_at))").font(.system(size: 11)).foregroundStyle(.secondary)
                Spacer()
                Toggle("保留在我的机器上", isOn: Binding(get: { linked }, set: { on in
                    Task {
                        if on { await portal.queue("link", ["type": pkg.type, "name": pkg.name, "team": team]) }
                        else { await portal.queue("unlink", ["type": pkg.type, "name": pkg.name]) }
                    }
                })).toggleStyle(.switch).controlSize(.small)
                    .help("等同于 hearth lib link \(pkg.type == "agent" ? "--agent " : "")\(team):\(pkg.name)")
            }
            Divider()
            Group {
                if let d = detail {
                    ScrollView {
                        VStack(alignment: .leading, spacing: 10) {
                            if let vs = d.versions, !vs.isEmpty {
                                VStack(alignment: .leading, spacing: 3) {
                                    ForEach(vs, id: \.self) { v in
                                        HStack {
                                            Text("v\(v.version)").font(.system(size: 11, weight: .medium, design: .monospaced))
                                            Text(v.published_by ?? "").font(.system(size: 11)).foregroundStyle(.secondary)
                                            Spacer()
                                            if let t = v.published_at { Text(Fmt.ago(t)).font(.system(size: 10)).foregroundStyle(.tertiary) }
                                        }
                                    }
                                }
                                .padding(8).background(Theme.card, in: RoundedRectangle(cornerRadius: 8))
                            }
                            Text(d.item?.content ?? "").font(.system(size: 11.5, design: .monospaced))
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
        }
        .padding(18)
        .frame(width: 620, height: 560)
        .task {
            let path = "/v1/me/teams/\(adminPath(team))/packages/\(adminPath(pkg.type))/\(adminPath(pkg.name))"
            guard let d = await portal.call("GET", path, reload: false) else { failed = portal.notice ?? "读取失败"; portal.notice = nil; return }
            do { detail = try JSONDecoder().decode(PackageDetail.self, from: d) } catch { failed = "读取失败：\(error.localizedDescription)" }
        }
    }
}
