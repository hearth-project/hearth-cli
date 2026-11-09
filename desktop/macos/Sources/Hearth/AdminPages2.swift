import SwiftUI

struct AdminInvitesPage: View {
    @ObservedObject var store: AdminStore
    @State private var days = 7
    @State private var fresh: String?

    var body: some View {
        PageScroll(title: "邀请码", trailing: "\(store.invites.count) 个") {
            Panel(title: "生成邀请码") {
                HStack(spacing: 10) {
                    Picker("有效期", selection: $days) {
                        Text("1 天").tag(1); Text("7 天").tag(7); Text("30 天").tag(30)
                    }
                    .pickerStyle(.segmented).frame(width: 220).labelsHidden()
                    Button("生成") { create() }.modifier(PrimaryButton())
                    Spacer()
                }
                if let c = fresh {
                    HStack {
                        Text(c).font(.system(size: 13, design: .monospaced)).textSelection(.enabled)
                        Spacer()
                        Button("复制") { Actions.copy(c) }.buttonStyle(.bordered)
                    }
                    .padding(10).background(Theme.accent.opacity(0.08), in: RoundedRectangle(cornerRadius: 9, style: .continuous))
                    Text("邀请码只显示这一次。").font(.system(size: 11)).foregroundStyle(.secondary)
                }
            }
            Panel(title: "未使用和已使用") {
                if store.invites.isEmpty { Text("还没有邀请码").font(.system(size: 12)).foregroundStyle(.tertiary) }
                ForEach(Array(store.invites.enumerated()), id: \.element.id) { i, v in
                    if i > 0 { Divider().opacity(0.5) }
                    HStack(spacing: 10) {
                        Image(systemName: v.used_by == nil ? "ticket" : "checkmark.seal").foregroundStyle(v.used_by == nil ? Theme.accent : Theme.ok)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(v.used_by.map { "已被 \($0) 使用" } ?? "未使用").font(.system(size: 13, weight: .medium))
                            Text("创建于 \(AdminFmt.day(v.created_at)) · 到期 \(AdminFmt.day(v.expires_at))")
                                .font(.system(size: 11)).foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button { Task { await store.call("DELETE", "/v1/admin/invites/\(adminPath(v.id))", done: "已删除邀请码") } } label: {
                            Image(systemName: "trash").foregroundStyle(.secondary)
                        }.buttonStyle(.plain)
                    }
                    .padding(.vertical, 3)
                }
            }
        }
    }

    private func create() {
        Task {
            if let d = await store.call("POST", "/v1/admin/invites", ["days": days]),
               let c = try? JSONDecoder().decode(InviteCode.self, from: d) {
                fresh = c.code
                store.notice = nil
            }
        }
    }
}

struct AdminTeamsPage: View {
    @ObservedObject var store: AdminStore
    @State private var name = ""
    @State private var owner = ""

    var body: some View {
        PageScroll(title: "团队管理", trailing: "\(store.teams.count) 个") {
            Panel(title: "新建团队") {
                HStack(spacing: 8) {
                    TextField("团队名", text: $name).textFieldStyle(.roundedBorder)
                    TextField("所有者邮箱", text: $owner).textFieldStyle(.roundedBorder)
                    Button("创建") {
                        let n = name.trimmingCharacters(in: .whitespaces), o = owner.trimmingCharacters(in: .whitespaces)
                        Task {
                            if await store.call("POST", "/v1/admin/teams", ["name": n, "owner": o], done: "已创建团队 \(n)") != nil { name = ""; owner = "" }
                        }
                    }
                    .modifier(PrimaryButton()).disabled(name.isEmpty || owner.isEmpty)
                }
            }
            ForEach(store.teams) { t in TeamCard(store: store, team: t) }
        }
    }
}

private struct TeamCard: View {
    @ObservedObject var store: AdminStore
    var team: AdminTeam
    @State private var open = false
    @State private var members: [TeamMember] = []
    @State private var newEmail = ""
    @State private var confirmDelete = false
    private var base: String { "/v1/admin/teams/\(adminPath(team.name))" }

    var body: some View {
        Panel {
            Button { open.toggle(); if open { Task { await load() } } } label: {
                HStack(spacing: 10) {
                    Image(systemName: "person.3").foregroundStyle(Theme.blue)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(team.name).font(.system(size: 14, weight: .semibold))
                        Text("\(team.members ?? 0) 位成员 · \(team.skills ?? 0) 个 Skill · \(team.agents ?? 0) 个 Agent · \(Fmt.bytes(team.bytes ?? 0))")
                            .font(.system(size: 11)).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Image(systemName: "chevron.right").rotationEffect(.degrees(open ? 90 : 0)).foregroundStyle(.tertiary)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            if open {
                Divider().opacity(0.5)
                ForEach(members, id: \.email) { m in
                    HStack {
                        Avatar(email: m.email, tint: Theme.blue)
                        Text(m.email).font(.system(size: 13))
                        Spacer()
                        Menu(roleName(m.role)) {
                            ForEach(["owner", "maintainer", "member"], id: \.self) { r in
                                Button(roleName(r)) { Task { await setRole(m.email, r) } }
                            }
                            Divider()
                            Button("移出团队", role: .destructive) {
                                Task { await store.call("DELETE", base + "/members/\(adminPath(m.email))", done: "已移出 \(m.email)"); await load() }
                            }
                        }
                        .menuStyle(.borderlessButton).fixedSize()
                    }
                }
                HStack(spacing: 8) {
                    TextField("添加成员邮箱", text: $newEmail).textFieldStyle(.roundedBorder)
                    Button("添加") { let e = newEmail; Task { await setRole(e, "member"); newEmail = "" } }
                        .buttonStyle(.bordered).disabled(newEmail.isEmpty)
                    Button("删除团队…", role: .destructive) { confirmDelete = true }.buttonStyle(.bordered)
                }
            }
        }
        .confirmationDialog("删除团队 \(team.name)？", isPresented: $confirmDelete, titleVisibility: .visible) {
            Button("删除团队", role: .destructive) { Task { await store.call("DELETE", base, done: "已删除团队 \(team.name)") } }
        } message: { Text("团队里的共享包会一起删除。") }
    }

    private func roleName(_ r: String) -> String { r == "owner" ? "所有者" : r == "maintainer" ? "维护者" : "成员" }

    private func load() async {
        if let d = await store.call("GET", base, reload: false), let t = try? JSONDecoder().decode(TeamDetail.self, from: d) { members = t.members }
    }

    private func setRole(_ email: String, _ role: String) async {
        await store.call("PUT", base + "/members/\(adminPath(email))", ["role": role], done: "已更新 \(email)")
        await load()
    }
}
