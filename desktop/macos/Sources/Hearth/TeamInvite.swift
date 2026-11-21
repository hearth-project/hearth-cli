import SwiftUI

/// What POST /v1/me/teams/{team}/invite answers. Older servers send only
/// code and expires_at; their codes are a maintainer's, for anyone.
struct TeamInviteCode: Decodable, Equatable {
    var code: String
    var expires_at: Int64
    var role: String?
    var max_uses: Int?
}

enum InviteMode: Hashable { case account, code }

/// The sheet's starting state, for snapshots (no network).
struct InvitePreview {
    var mode: InviteMode = .account
    var query = ""
    var users: [TeamMemberSearchResult] = []
    var selected: String?
    var code: TeamInviteCode?
}

/// Add people to a team: a registered account joins at once, anyone else
/// gets an invite code. The sheet keeps one size, so switching modes or
/// picking a result never moves the buttons.
struct MemberInviteSheet: View {
    @ObservedObject var portal: PortalStore
    var team: Team
    var close: () -> Void
    private let preview: InvitePreview?
    @State private var mode: InviteMode
    @State private var role = "member"
    @State private var selected: TeamMemberSearchResult?
    @State private var code: TeamInviteCode?
    @State private var oneUse = true
    @State private var busy = false

    init(portal: PortalStore, team: Team, close: @escaping () -> Void, preview: InvitePreview? = nil) {
        self.portal = portal
        self.team = team
        self.close = close
        self.preview = preview
        _mode = State(initialValue: preview?.mode ?? .account)
        _selected = State(initialValue: preview?.users.first { $0.email == preview?.selected })
        _code = State(initialValue: preview?.code)
    }

    private var roles: [String] { team.role == "owner" ? ["member", "maintainer", "owner"] : ["member", "maintainer"] }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            SheetHeader(icon: "person.badge.plus", title: "邀请成员加入 \(team.name)",
                        subtitle: "已注册 Hearth 的同事可以直接加入，其他人用邀请码。", close: close)
            Picker("", selection: $mode) {
                Text("已注册用户").tag(InviteMode.account)
                Text("邀请码").tag(InviteMode.code)
            }
            .pickerStyle(.segmented).labelsHidden().fixedSize()
            ZStack(alignment: .top) {
                InviteSearchPane(portal: portal, team: team, selected: $selected, preview: preview) { mode = .code }
                    .opacity(mode == .account ? 1 : 0).disabled(mode != .account)
                    .accessibilityHidden(mode != .account)
                codePane
                    .opacity(mode == .code ? 1 : 0).disabled(mode != .code)
                    .accessibilityHidden(mode != .code)
            }
            .frame(maxWidth: .infinity, minHeight: 236, maxHeight: 236, alignment: .top)
            rolePicker
            footer
        }
        .padding(20)
        .frame(width: 520)
        .onChange(of: role) { _ in code = nil }
        .onChange(of: oneUse) { _ in code = nil }
    }

    private var rolePicker: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 10) {
                Text("加入后的角色").font(.system(size: 12)).foregroundStyle(.secondary)
                Picker("", selection: $role) {
                    ForEach(roles, id: \.self) { Text(TeamsPage.role($0)).tag($0) }
                }
                .pickerStyle(.segmented).labelsHidden().fixedSize()
            }
            Text(Self.roleHint(role)).font(.system(size: 11)).foregroundStyle(.tertiary)
        }
    }

    private var footer: some View {
        HStack(spacing: 8) {
            if mode == .account, let s = selected {
                Text("\(s.email) 将以\(TeamsPage.role(role))身份加入").font(.system(size: 11)).foregroundStyle(.secondary)
                    .lineLimit(1).truncationMode(.middle)
            }
            Spacer(minLength: 8)
            Button(code == nil ? "取消" : "完成", action: close)
            switch mode {
            case .account:
                Button(busy ? "加入中…" : "加入团队") { Task { await add() } }
                    .keyboardShortcut(.defaultAction).disabled(selected == nil || busy)
            case .code:
                if let c = code {
                    Button("复制命令") { copy(c) }.keyboardShortcut(.defaultAction)
                } else {
                    Button(busy ? "生成中…" : "生成邀请码") { Task { await make() } }
                        .keyboardShortcut(.defaultAction).disabled(busy)
                }
            }
        }
    }

    @ViewBuilder private var codePane: some View {
        if let c = code {
            VStack(alignment: .leading, spacing: 10) {
                VStack(alignment: .leading, spacing: 8) {
                    Text(c.code).font(.system(size: 20, weight: .semibold, design: .monospaced)).textSelection(.enabled)
                    HStack(spacing: 8) {
                        Text(Self.command(c)).font(.system(size: 11.5, design: .monospaced)).foregroundStyle(.secondary)
                            .textSelection(.enabled).lineLimit(1).truncationMode(.middle)
                        Spacer(minLength: 0)
                        Button { copy(c) } label: { Label("复制", systemImage: "doc.on.doc") }.controlSize(.small)
                    }
                }
                .padding(14).frame(maxWidth: .infinity, alignment: .leading)
                .background(Theme.accent.opacity(0.07), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .strokeBorder(Theme.accent.opacity(0.3), style: StrokeStyle(lineWidth: 1, dash: [4, 3])))
                Label(Self.terms(c), systemImage: "clock").font(.system(size: 11.5)).foregroundStyle(.secondary)
                Text("已在用 Hearth 的同事：在任意一台已登录的机器上运行上面这条命令。")
                    .font(.system(size: 11)).foregroundStyle(.tertiary).fixedSize(horizontal: false, vertical: true)
                if let install = portal.me?.install, !install.isEmpty {
                    HStack(alignment: .center, spacing: 10) {
                        Text(Self.newcomerHint(portal.me?.registration))
                            .font(.system(size: 11)).foregroundStyle(.tertiary).fixedSize(horizontal: false, vertical: true)
                        Spacer(minLength: 0)
                        Button { copyInstall(install, c) } label: { Label("复制安装命令", systemImage: "terminal") }
                            .controlSize(.small).fixedSize()
                            .help("服务器的安装命令，末尾带 --team \(c.code)")
                    }
                }
                if c.role == nil {
                    Label("这台服务器版本较旧：邀请码固定为维护者、7 天内不限人数。", systemImage: "exclamationmark.triangle")
                        .font(.system(size: 11)).foregroundStyle(Theme.warn)
                }
            }
        } else {
            VStack(spacing: 8) {
                Image(systemName: "ticket").font(.system(size: 22, weight: .light)).foregroundStyle(.tertiary).padding(.bottom, 2)
                Text("生成一个邀请码").font(.system(size: 12.5, weight: .medium))
                Text("适合还没注册 Hearth 的同事，或一次邀请几个人。已在用 Hearth 的运行 hearth team join <邀请码>，还没装的用安装命令，装好后自动加入。")
                    .font(.system(size: 11)).foregroundStyle(.secondary).multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
                HStack(spacing: 10) {
                    Text("可以使用").font(.system(size: 12)).foregroundStyle(.secondary)
                    Picker("", selection: $oneUse) {
                        Text("一个人").tag(true)
                        Text("不限人数").tag(false)
                    }
                    .pickerStyle(.segmented).labelsHidden().fixedSize()
                }
                .padding(.top, 6)
                Text("7 天内有效").font(.system(size: 11)).foregroundStyle(.tertiary)
            }
            .padding(.horizontal, 32)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .cardSurface(radius: 10)
        }
    }

    static func roleHint(_ r: String) -> String {
        switch r {
        case "owner": return "和你一样：还能管理成员、绑定 git 仓库、与其他团队共享。"
        case "maintainer": return "还能把 skills 和 agents 共享到团队、邀请成员。"
        default: return "可以使用团队共享的 skills 和 agents。"
        }
    }

    static func command(_ c: TeamInviteCode) -> String { "hearth team join \(c.code)" }

    /// "成员 · 一个人 · 11月28日 14:05 前有效"
    static func terms(_ c: TeamInviteCode) -> String {
        let n = c.max_uses ?? 0
        let uses = n == 0 ? "不限人数" : (n == 1 ? "一个人" : "\(n) 个人")
        let f = DateFormatter()
        f.locale = Locale(identifier: "zh_CN")
        f.dateFormat = "M月d日 HH:mm"
        let until = f.string(from: Date(timeIntervalSince1970: TimeInterval(c.expires_at)))
        return "\(TeamsPage.role(c.role ?? "maintainer")) · \(uses) · \(until) 前有效"
    }

    /// For people without Hearth, by how this server signs up new accounts.
    static func newcomerHint(_ registration: String?) -> String {
        let s = "还没装 Hearth 的同事：把安装命令发给对方，在对方的机器上运行，注册或登录后自动加入团队。"
        switch registration {
        case "invite": return s + "这台服务器注册新账号还要管理员的注册邀请码（命令末尾加 --invite <注册码>）。"
        case "approval": return s + "这台服务器的新账号要等管理员批准，批准后再运行上面的加入命令。"
        case "closed": return s + "这台服务器不接受新账号，对方得先有账号。"
        default: return s
        }
    }

    /// The server's install one-liner with `--team <code>` for `hearth setup`,
    /// added the way WithInstallToken (internal/client/install_command.go) adds its arguments.
    static func installCommand(_ cmd: String, code: String) -> String {
        var c = cmd.trimmingCharacters(in: .whitespacesAndNewlines)
        if c.hasSuffix("| sh") { c = String(c.dropLast(4)) + "| sh -s --" }
        let safe = !code.isEmpty && code.allSatisfy { $0.isASCII && ($0.isLetter || $0.isNumber || "-_.".contains($0)) }
        return c + " --team " + (safe ? code : "'" + code.replacingOccurrences(of: "'", with: "'\\''") + "'")
    }

    private func make() async {
        busy = true
        defer { busy = false }
        guard let d = await portal.call("POST", "/v1/me/teams/\(adminPath(team.name))/invite",
                                        ["role": role, "max_uses": oneUse ? 1 : 0], reload: false),
              let c = try? JSONDecoder().decode(TeamInviteCode.self, from: d) else { return }
        code = c
    }

    private func add() async {
        guard let s = selected else { return }
        busy = true
        defer { busy = false }
        guard await portal.call("POST", "/v1/me/teams/\(adminPath(team.name))/members", ["email": s.email, "role": role],
                                done: "\(s.email) 已加入 \(team.name)") != nil else { return }
        close()
    }

    private func copy(_ c: TeamInviteCode) {
        Actions.copy(Self.command(c))
        portal.noticeIsError = false
        portal.notice = "已复制邀请命令"
    }

    private func copyInstall(_ cmd: String, _ c: TeamInviteCode) {
        Actions.copy(Self.installCommand(cmd, code: c.code))
        portal.noticeIsError = false
        portal.notice = "已复制安装命令，对方运行后加入 \(team.name)"
    }
}

/// Registered accounts by email prefix; one of them can be picked.
struct InviteSearchPane: View {
    @ObservedObject var portal: PortalStore
    var team: Team
    @Binding var selected: TeamMemberSearchResult?
    var useCode: () -> Void
    @State private var query: String
    @State private var users: [TeamMemberSearchResult]
    @State private var answered: String?
    @State private var failedFor: String?
    @State private var inFlight = 0

    init(portal: PortalStore, team: Team, selected: Binding<TeamMemberSearchResult?>, preview: InvitePreview?, useCode: @escaping () -> Void) {
        self.portal = portal
        self.team = team
        _selected = selected
        self.useCode = useCode
        _query = State(initialValue: preview?.query ?? "")
        _users = State(initialValue: preview?.users ?? [])
        _answered = State(initialValue: preview.map { $0.query.trimmingCharacters(in: .whitespacesAndNewlines) })
    }

    private var text: String { query.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        VStack(spacing: 8) {
            field
            list.frame(maxWidth: .infinity, maxHeight: .infinity).cardSurface(radius: 10)
        }
        .onChange(of: query) { _ in selected = nil }
        .task(id: text) {
            guard !Snapshot.active, text.count >= 2, text != answered else { return }
            try? await Task.sleep(nanoseconds: 250_000_000)
            guard !Task.isCancelled else { return }
            await search(text)
        }
    }

    private var field: some View {
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass").foregroundStyle(.tertiary)
            TextField("", text: $query, prompt: Text("邮箱或邮箱前缀，例如 zhangsan")).textFieldStyle(.plain)
            if inFlight > 0 {
                ProgressView().controlSize(.small).scaleEffect(0.7).frame(width: 14, height: 14)
            } else if !query.isEmpty {
                Button { query = "" } label: { Image(systemName: "xmark.circle.fill") }
                    .buttonStyle(.plain).foregroundStyle(.tertiary).help("清空")
            }
        }
        .font(.system(size: 13))
        .padding(.horizontal, 10).padding(.vertical, 7)
        .background(RoundedRectangle(cornerRadius: 8, style: .continuous).fill(Color.primary.opacity(0.05)))
    }

    @ViewBuilder private var list: some View {
        if text.count < 2 {
            empty("magnifyingglass", "输入邮箱或邮箱前缀查找同事", "只能找到已经注册 Hearth 的账号，至少输入 2 个字符。")
        } else if !users.isEmpty {
            ScrollView {
                LazyVStack(spacing: 2) { ForEach(users) { row($0) } }.padding(6)
            }
            .scrollIndicators(.never)
        } else if answered == text {
            empty("person.crop.circle.badge.questionmark", "没有找到「\(text)」开头的已注册用户",
                  "对方还没注册 Hearth 时，用邀请码邀请。", action: ("改用邀请码", useCode))
        } else if failedFor == text {
            empty("exclamationmark.triangle", "查找失败", "检查网络和登录状态后再试。",
                  action: ("重试", { Task { await search(text) } }))
        } else {
            VStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text("查找中…").font(.system(size: 11)).foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func empty(_ icon: String, _ title: String, _ hint: String, action: (String, () -> Void)? = nil) -> some View {
        VStack(spacing: 6) {
            Image(systemName: icon).font(.system(size: 22, weight: .light)).foregroundStyle(.tertiary).padding(.bottom, 2)
            Text(title).font(.system(size: 12.5, weight: .medium)).multilineTextAlignment(.center)
            Text(hint).font(.system(size: 11)).foregroundStyle(.secondary).multilineTextAlignment(.center)
            if let a = action { Button(a.0, action: a.1).controlSize(.small).padding(.top, 4) }
        }
        .padding(.horizontal, 24)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func row(_ u: TeamMemberSearchResult) -> some View {
        let on = selected?.email == u.email
        return Button { selected = on ? nil : u } label: {
            HStack(spacing: 10) {
                Avatar(email: u.email, tint: u.in_team ? Theme.slate : Theme.blue)
                Text(u.email).font(.system(size: 12.5)).lineLimit(1).truncationMode(.middle)
                Spacer(minLength: 8)
                if u.in_team {
                    Text("已在团队" + (u.role.map { " · " + TeamsPage.role($0) } ?? ""))
                        .font(.system(size: 11)).foregroundStyle(.tertiary)
                } else {
                    Image(systemName: on ? "checkmark.circle.fill" : "circle").font(.system(size: 15))
                        .foregroundStyle(on ? Theme.accent : Color.secondary.opacity(0.35))
                }
            }
            .padding(.horizontal, 8).padding(.vertical, 5)
        }
        .buttonStyle(PickRowStyle(chosen: on))
        .disabled(u.in_team)
        .help(u.in_team ? "\(u.email) 已经在团队里" : "选择 \(u.email)")
    }

    private func search(_ q: String) async {
        inFlight += 1
        defer { inFlight -= 1 }
        var c = URLComponents()
        c.queryItems = [URLQueryItem(name: "q", value: q)]
        let enc = (c.percentEncodedQuery ?? "").replacingOccurrences(of: "+", with: "%2B")
        let data = await portal.call("GET", "/v1/me/teams/\(adminPath(team.name))/members/search?\(enc)", reload: false)
        guard q == text else { return } // a newer query is on its way
        guard let data else { failedFor = q; return }
        users = (try? JSONDecoder().decode([TeamMemberSearchResult].self, from: data)) ?? []
        answered = q
        failedFor = nil
    }
}

/// A row that can be picked: a soft tint on hover, firmer on press, the
/// accent while chosen.
struct PickRowStyle: ButtonStyle {
    var chosen: Bool
    func makeBody(configuration: Configuration) -> some View { PickRowBody(configuration: configuration, chosen: chosen) }
}

private struct PickRowBody: View {
    let configuration: ButtonStyleConfiguration
    var chosen: Bool
    @State private var hover = false
    @Environment(\.isEnabled) private var enabled
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        configuration.label
            .background(RoundedRectangle(cornerRadius: 8, style: .continuous).fill(fill))
            .contentShape(Rectangle())
            .onHover { hover = $0 && enabled }
            .animation(Theme.motion(Theme.hover, reduce: reduceMotion), value: hover)
    }

    private var fill: Color {
        if chosen { return Theme.accent.opacity(configuration.isPressed ? 0.2 : 0.13) }
        if configuration.isPressed { return Color.primary.opacity(0.07) }
        return Color.primary.opacity(hover ? 0.035 : 0)
    }
}
