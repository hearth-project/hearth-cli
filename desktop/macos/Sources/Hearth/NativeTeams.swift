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
    @State private var memberInvite: Team?
    @State private var binding: Team?
    @State private var bindingPrefix = false
    @State private var allMembers: Set<String> = []

    var body: some View {
        PageScroll(title: "团队") {
            if let e = portal.error { Text(e).font(.system(size: 12)).foregroundStyle(Theme.bad) }
            if portal.teams.isEmpty {
                Panel { Text(portal.me == nil ? "加载中…" : "还没有加入团队。创建：hearth team create <名称>；加入：hearth team join <邀请码>").font(.system(size: 12)).foregroundStyle(.secondary) }
            }
            ForEach(portal.teams) { t in teamCard(t) }
        }
        .sheet(item: $open) { o in PackageSheet(portal: portal, team: o.team, pkg: o.pkg) { open = nil }.toastHost() }
        .sheet(item: $memberInvite) { t in
            MemberInviteSheet(portal: portal, team: t, close: { memberInvite = nil }).toastHost()
        }
        .sheet(item: $binding) { t in RepoSheet(portal: portal, team: t, focusPrefix: bindingPrefix) { binding = nil }.toastHost() }
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
                            Button(t.repo == nil ? "绑定 git 仓库…" : "修改仓库绑定…") { bindingPrefix = false; binding = t }
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
                members(t)
            }
            let links = (t.shared_with ?? []) + (t.shared_from ?? [])
            if !links.isEmpty {
                Text("与其他团队共享：" + links.joined(separator: "、")).font(.system(size: 11)).foregroundStyle(.secondary)
            }
            if let r = t.repo {
                TeamRepoRow(portal: portal, team: t, repo: r) { bindingPrefix = true; binding = t }
            }
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

    /// Members as chips, owners first; a long team shows the first 12 until asked.
    private func members(_ t: Team) -> some View {
        let rank = ["owner": 0, "maintainer": 1, "member": 2, "reader": 3]
        let sorted = t.members.sorted { (rank[$0.role] ?? 4, $0.email) < (rank[$1.role] ?? 4, $1.email) }
        let shown = allMembers.contains(t.name) ? sorted : Array(sorted.prefix(12))
        return Flow(spacing: 6) {
            ForEach(shown, id: \.email) { m in MemberChip(member: m, me: m.email == portal.me?.email) }
            if shown.count < sorted.count {
                Button("还有 \(sorted.count - shown.count) 位…") { allMembers.insert(t.name) }
                    .buttonStyle(.link).font(.system(size: 11.5))
                    .padding(.horizontal, 4).padding(.vertical, 4)
            }
        }
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

/// One team member: a small monogram, the email, and the role unless it is
/// a plain member (the header already counts members).
struct MemberChip: View {
    var member: TeamMember
    var me = false

    private var tint: Color {
        switch member.role {
        case "owner": return Theme.accent
        case "maintainer": return Theme.blue
        default: return Theme.slate
        }
    }

    var body: some View {
        HStack(spacing: 6) {
            Text(String(member.email.first.map(String.init) ?? "?").uppercased())
                .font(.system(size: 9.5, weight: .semibold)).foregroundStyle(tint)
                .frame(width: 18, height: 18)
                .background(Circle().fill(tint.opacity(0.16)))
            Text(member.email).font(.system(size: 11.5)).lineLimit(1)
            if me { Text("你").font(.system(size: 10.5)).foregroundStyle(.secondary) }
            if member.role != "member" {
                Text(TeamsPage.role(member.role)).font(.system(size: 10.5, weight: .medium)).foregroundStyle(tint)
            }
        }
        .padding(.leading, 3).padding(.trailing, 9).padding(.vertical, 3)
        .background(Capsule().fill(Color.primary.opacity(0.05)))
        .help("\(member.email) · \(TeamsPage.role(member.role))")
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
    /// Opened from a refused review that wants an iCafe card: start in 提交前缀.
    var focusPrefix = false
    var close: () -> Void
    @State private var url = ""
    @State private var ref = ""
    @State private var device = ""
    @State private var push = "cr"
    @State private var commitPrefix = ""
    @State private var exclude = ""
    @State private var saving = false
    @FocusState private var prefixFocused: Bool

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
                    .focused($prefixFocused)
                TextField("不同步", text: $exclude, prompt: Text("可选，skill / agent 名字，逗号分隔：两边都不动"))
            }
            if focusPrefix {
                Label("仓库的提交检查要求提交信息里有一个已存在的 iCafe 卡片号。把卡片号（如 proj-123）填进「提交前缀」，同步时会加在每条提交信息前面。",
                      systemImage: "lightbulb")
                    .font(.system(size: 11)).foregroundStyle(Theme.warn).fixedSize(horizontal: false, vertical: true)
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
            if focusPrefix { DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { prefixFocused = true } }
        }
    }
}
