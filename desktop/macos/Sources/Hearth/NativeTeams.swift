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
    @State private var memberInvite: Team?
    @State private var binding: Team?

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
        .sheet(item: $memberInvite) { t in
            MemberInviteSheet(portal: portal, team: t, close: { memberInvite = nil }, showInviteCode: {
                memberInvite = nil
                Task { await makeInvite(t.name) }
            }).toastHost()
        }
        .sheet(item: $binding) { t in RepoSheet(portal: portal, team: t) { binding = nil }.toastHost() }
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
                    Button { memberInvite = t } label: { Label("邀请成员", systemImage: "person.badge.plus") }
                }
                if t.role != "reader" && t.via == nil {
                    Menu {
                        if t.role == "owner" {
                            Button(t.repo == nil ? "绑定 git 仓库…" : "修改仓库绑定…") { binding = t }
                            if t.repo != nil {
                                Button("解除仓库绑定", role: .destructive) {
                                    Task { await portal.call("DELETE", "/v1/me/teams/\(adminPath(t.name))/repo", done: "已解除 \(t.name) 的仓库绑定") }
                                }
                            }
                            Divider()
                        }
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
            if let r = t.repo { repoRow(t, r) }
            if !t.packages.isEmpty {
                Divider().opacity(0.5)
                ForEach(Array(t.packages.enumerated()), id: \.element.id) { i, p in
                    if i > 0 { Divider().opacity(0.35) }
                    Button { open = OpenPackage(team: t.name, pkg: p) } label: { packageRow(p) }
                        .buttonStyle(ListRowStyle())
                        .help("查看 \(p.name)")
                }
            }
        }
    }

    /// The bound repository: where, who syncs, the last result, sync now.
    private func repoRow(_ t: Team, _ r: TeamRepo) -> some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "arrow.triangle.2.circlepath").foregroundStyle(Theme.blue).frame(width: 18).padding(.top, 1)
            VStack(alignment: .leading, spacing: 2) {
                Text(r.url + (r.ref.map { $0.isEmpty ? "" : "  ·  " + $0 } ?? "")).font(.system(size: 11.5, design: .monospaced)).lineLimit(1).truncationMode(.middle).textSelection(.enabled)
                Text(repoStatus(r)).font(.system(size: 11)).foregroundStyle(r.status == "failed" ? Theme.bad : .secondary).lineLimit(2)
                if let v = r.review, !v.isEmpty, let u = URL(string: v), v.hasPrefix("http") {
                    Link("评审：\(v)", destination: u).font(.system(size: 11))
                }
            }
            Spacer()
            if t.canPublish {
                Button { Task { await portal.call("POST", "/v1/me/teams/\(adminPath(t.name))/repo/sync", [:], done: "已通知 \(r.device_name ?? "同步机器") 立即同步，几分钟内完成") } } label: { Label("立即同步", systemImage: "arrow.clockwise") }
                    .disabled((r.requested_at ?? 0) > 0)
            }
        }
        .padding(8)
        .background(RoundedRectangle(cornerRadius: 8, style: .continuous).fill(Theme.blue.opacity(0.06)))
    }

    private func repoStatus(_ r: TeamRepo) -> String {
        let who = "由 \(r.device_name ?? r.device) 同步"
        if (r.requested_at ?? 0) > 0 { return who + " · 等待同步…" }
        guard let at = r.synced_at, at > 0 else { return who + " · 还没同步过" }
        let st = r.status == "failed" ? "失败" : "成功"
        return who + " · 上次 \(Fmt.ago(at))\(st)" + (r.detail.map { $0.isEmpty ? "" : "：" + $0 } ?? "")
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
            VStack(alignment: .trailing, spacing: 2) {
                Text("\((p.uses ?? 0)) 次调用 · \((p.users ?? 0)) 人").font(.system(size: 10, weight: .medium)).foregroundStyle(Theme.blue)
                Text("\(p.published_by) · \(Fmt.ago(p.published_at))").font(.system(size: 10)).foregroundStyle(.tertiary)
            }
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

/// Search active Hearth accounts by email prefix and add one directly to a
/// team. The invite-code flow remains available as a fallback for accounts
/// that have not registered yet.
struct MemberInviteSheet: View {
    @ObservedObject var portal: PortalStore
    var team: Team
    var close: () -> Void
    var showInviteCode: () -> Void
    @State private var query = ""
    @State private var users: [TeamMemberSearchResult] = []
    @State private var selected: TeamMemberSearchResult?
    @State private var role = "member"
    @State private var searching = false
    @State private var hasSearched = false
    @State private var adding = false

    private var roles: [String] {
        team.role == "owner" ? ["member", "maintainer", "owner"] : ["member", "maintainer"]
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            SheetHeader(icon: "person.badge.plus", title: "邀请成员",
                        subtitle: "\(team.name) 的已注册 Hearth 用户可以直接加入团队。", close: close)
            TextField("搜索邮箱或邮箱前缀", text: $query, prompt: Text("例如 zhengshifeng 或 zhengshifeng@baidu.com"))
                .textFieldStyle(.roundedBorder)
            if query.trimmingCharacters(in: .whitespacesAndNewlines).count < 2 {
                Text("输入至少 2 个字符开始搜索。未注册用户无法直接加入，请先提醒对方注册 Hearth。")
                    .font(.system(size: 11)).foregroundStyle(.secondary)
            } else if searching {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("查找已注册用户…").font(.system(size: 11)).foregroundStyle(.secondary)
                }
            } else if !hasSearched {
                Text("正在准备搜索…").font(.system(size: 11)).foregroundStyle(.secondary)
            } else if users.isEmpty && hasSearched {
                VStack(alignment: .leading, spacing: 4) {
                    Text("未找到已注册的 Hearth 用户").font(.system(size: 12, weight: .medium))
                    Text("请提醒对方先注册 Hearth，注册完成后再邀请。")
                        .font(.system(size: 11)).foregroundStyle(.secondary)
                }
                .padding(10).frame(maxWidth: .infinity, alignment: .leading)
                .background(Theme.card, in: RoundedRectangle(cornerRadius: 8))
            } else {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(users) { user in
                        Button {
                            selected = user
                            if let r = user.role, roles.contains(r) {
                                role = r
                            } else {
                                role = "member"
                            }
                        } label: {
                            HStack(spacing: 8) {
                                Image(systemName: user.in_team ? "checkmark.circle.fill" : "person.circle")
                                    .foregroundStyle(user.in_team ? Theme.ok : Theme.accent)
                                Text(user.email).font(.system(size: 12))
                                Spacer()
                                if user.in_team {
                                    Badge(text: "已在团队", color: Theme.ok)
                                }
                            }
                            .padding(.horizontal, 8).padding(.vertical, 6)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .disabled(user.in_team)
                    }
                }
                .padding(6).frame(maxWidth: .infinity, alignment: .leading)
                .background(Theme.card, in: RoundedRectangle(cornerRadius: 8))
            }
            if selected != nil {
                Picker("加入后角色", selection: $role) {
                    ForEach(roles, id: \.self) { Text(TeamsPage.role($0)).tag($0) }
                }
                .pickerStyle(.menu)
            }
            HStack {
                Button("生成邀请码…", action: showInviteCode)
                    .buttonStyle(.link)
                Spacer()
                Button("取消", action: close)
                Button(adding ? "加入中…" : "直接加入") {
                    Task { await addSelected() }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(selected == nil || adding)
            }
        }
        .padding(20)
        .frame(width: 500, height: 330)
        .onChange(of: query) { _ in
            selected = nil
            users = []
            hasSearched = false
        }
        .task(id: query) {
            let text = query.trimmingCharacters(in: .whitespacesAndNewlines)
            guard text.count >= 2 else { return }
            try? await Task.sleep(nanoseconds: 250_000_000)
            guard !Task.isCancelled else { return }
            await search(text)
        }
    }

    private func search(_ text: String) async {
        searching = true
        defer {
            searching = false
            hasSearched = true
        }
        var components = URLComponents()
        components.queryItems = [URLQueryItem(name: "q", value: text)]
        let suffix = components.percentEncodedQuery.map { "?\($0)" } ?? ""
        let path = "/v1/me/teams/\(adminPath(team.name))/members/search\(suffix)"
        guard let data = await portal.call("GET", path, reload: false) else {
            users = []
            return
        }
        guard text == query.trimmingCharacters(in: .whitespacesAndNewlines) else { return }
        users = (try? JSONDecoder().decode([TeamMemberSearchResult].self, from: data)) ?? []
    }

    private func addSelected() async {
        guard let selected else { return }
        adding = true
        defer { adding = false }
        let path = "/v1/me/teams/\(adminPath(team.name))/members"
        guard await portal.call("POST", path, ["email": selected.email, "role": role],
                                done: "\(selected.email) 已加入 \(team.name)") != nil else { return }
        close()
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
    @State private var usage: [TeamMemberUsage]?
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
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    usageView
                    if let d = detail {
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
                    } else if let f = failed {
                        Text(f).font(.system(size: 12)).foregroundStyle(Theme.bad)
                    } else {
                        HStack { Spacer(); ProgressView(); Spacer() }.padding(.top, 40)
                    }
                }
            }
            .frame(maxHeight: .infinity, alignment: .top)
        }
        .padding(18)
        .frame(width: 620, height: 560)
        .task {
            let path = "/v1/me/teams/\(adminPath(team))/packages/\(adminPath(pkg.type))/\(adminPath(pkg.name))"
            if let d = await portal.call("GET", path, reload: false) {
                do { detail = try JSONDecoder().decode(PackageDetail.self, from: d) }
                catch { failed = "读取失败：\(error.localizedDescription)" }
            } else {
                failed = portal.notice ?? "读取失败"
                portal.notice = nil
            }
            let usagePath = path + "/usage"
            if let d = await portal.call("GET", usagePath, reload: false) {
                usage = try? JSONDecoder().decode([TeamMemberUsage].self, from: d)
            }
        }
    }

    @ViewBuilder
    private var usageView: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("最近 30 天调用").font(.system(size: 12, weight: .semibold)).foregroundStyle(.secondary)
            if let usage {
                if usage.isEmpty {
                    Text("暂无调用记录").font(.system(size: 11)).foregroundStyle(.tertiary)
                } else {
                    ForEach(Array(usage.enumerated()), id: \.element.id) { i, row in
                        HStack(spacing: 8) {
                            Text("\(i + 1)").font(.system(size: 10, design: .monospaced)).foregroundStyle(.tertiary).frame(width: 18, alignment: .trailing)
                            Text(row.email).font(.system(size: 11.5))
                            Spacer()
                            Text("\(row.count) 次").font(.system(size: 11, weight: .medium)).foregroundStyle(Theme.blue)
                        }
                    }
                }
            } else {
                HStack(spacing: 6) {
                    ProgressView().controlSize(.small)
                    Text("加载调用统计…").font(.system(size: 11)).foregroundStyle(.tertiary)
                }
            }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.card, in: RoundedRectangle(cornerRadius: 8))
    }
}

/// Bind (or change) a team's git repository: the URL, the branch and which
/// of the owner's machines syncs it.
struct RepoSheet: View {
    @ObservedObject var portal: PortalStore
    var team: Team
    var close: () -> Void
    @State private var url = ""
    @State private var ref = ""
    @State private var device = ""
    @State private var push = "cr"
    @State private var commitPrefix = ""
    @State private var exclude = ""
    @State private var saving = false

    private var machines: [Device] { portal.devices.filter { ($0.status ?? "active") == "active" } }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            SheetHeader(icon: "arrow.triangle.2.circlepath", title: "\(team.name) 的 git 仓库",
                        subtitle: "仓库里 skills/ 和 agents/ 下的内容每天同步进团队；团队里新增或修改的会以评审（CR）提交回仓库。", close: close)
            Form {
                TextField("仓库地址", text: $url, prompt: Text("ssh://you@icode.example.com:8235/group/repo"))
                TextField("分支", text: $ref, prompt: Text("留空 = 默认分支"))
                Picker("同步机器", selection: $device) {
                    ForEach(machines) { d in Text((d.alias?.isEmpty == false ? d.alias! : d.name) + ((d.online ?? false) ? "" : "（离线）")).tag(d.id) }
                }
                Picker("回推方式", selection: $push) {
                    Text("提评审（refs/for/分支）").tag("cr")
                    Text("推到分支 hearth/team-\(team.name)").tag("branch")
                    Text("不回推").tag("off")
                }
                TextField("提交前缀", text: $commitPrefix, prompt: Text("可选，如 iCafe 卡片号 proj-123（仓库要求时填）"))
                TextField("不同步", text: $exclude, prompt: Text("可选，skill / agent 名字，逗号分隔：两边都不动"))
            }
            Text("同步机器用它自己的 git 凭证 clone 和提交，服务器不保存任何凭证。选一台常在线、能访问这个仓库的机器。")
                .font(.system(size: 11)).foregroundStyle(.secondary)
            HStack {
                Spacer()
                Button("取消", action: close)
                Button(saving ? "保存中…" : "保存") {
                    saving = true
                    Task {
                        let ok = await portal.call("PUT", "/v1/me/teams/\(adminPath(team.name))/repo",
                                                   ["url": url, "ref": ref, "device": device, "push": push,
                                                    "new_dir": team.repo?.new_dir ?? "skills", "commit_prefix": commitPrefix,
                                                    "exclude": exclude.split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }],
                                                   done: "已绑定，\(team.name) 会在几分钟内第一次同步") != nil
                        saving = false
                        if ok { close() }
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(saving || url.trimmingCharacters(in: .whitespaces).isEmpty || device.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 560)
        .onAppear {
            url = team.repo?.url ?? ""
            ref = team.repo?.ref ?? ""
            push = team.repo?.push ?? "cr"
            commitPrefix = team.repo?.commit_prefix ?? ""
            exclude = (team.repo?.exclude ?? []).joined(separator: ", ")
            device = team.repo?.device ?? (machines.first { $0.online ?? false }?.id ?? machines.first?.id ?? "")
        }
    }
}
