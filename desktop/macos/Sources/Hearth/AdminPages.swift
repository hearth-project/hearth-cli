import SwiftUI

func adminPath(_ s: String) -> String {
    s.addingPercentEncoding(withAllowedCharacters: CharacterSet.urlPathAllowed.subtracting(CharacterSet(charactersIn: "/?#"))) ?? s
}

struct AdminDashboardPage: View {
    @ObservedObject var store: AdminStore
    var body: some View {
        PageScroll(title: "管理概览", trailing: store.updated.map { "更新于 " + $0.formatted(date: .omitted, time: .shortened) }) {
            if let e = store.error { Text(e).font(.system(size: 12)).foregroundStyle(Theme.bad) }
            if let o = store.overview {
                loaded(o)
            } else if store.error == nil {
                HStack { Spacer(); ProgressView(); Spacer() }.padding(.top, 60)
            }
        }
    }

    @ViewBuilder private func loaded(_ o: AdminOverview) -> some View {
        let s = o.stats
        let cols = [GridItem(.adaptive(minimum: 200), spacing: 12)]
        if let p = o.pending_users, p > 0 {
            Label("有 \(p) 位用户等待审批", systemImage: "person.badge.clock").font(.system(size: 12)).foregroundStyle(Theme.warn)
        }
        LazyVGrid(columns: cols, spacing: 12) {
            KPI(title: "用户", value: "\(s.Users ?? 0)", icon: "person.2", tint: Theme.blue)
            KPI(title: "30 天活跃", value: "\(s.ActiveUsers30d ?? 0)", icon: "waveform.path.ecg", tint: Theme.ok)
            KPI(title: "在线设备", value: "\(o.online_devices ?? 0) / \(s.Devices ?? 0)", icon: "desktopcomputer", tint: Theme.ok)
            KPI(title: "会话", value: "\(s.Sessions ?? 0)", icon: "bubble.left.and.bubble.right", tint: Theme.blue)
            KPI(title: "团队包", value: "\(s.Packages ?? 0)", icon: "shippingbox", tint: Theme.accent)
            KPI(title: "占用空间", value: Fmt.bytes((s.SessionBytes ?? 0) + (s.LibraryBytes ?? 0) + (s.PackageBytes ?? 0)), icon: "externaldrive", tint: Theme.slate)
        }
        HStack(alignment: .top, spacing: 12) {
            Panel(title: "每日上传") {
                UploadChart(series: (o.series ?? []).map { DaySeries(day: $0.day, bytes: $0.bytes ?? 0) })
            }
            Panel(title: "客户端版本") {
                let vs = o.client_versions ?? []
                if vs.isEmpty { Text("没有设备").font(.system(size: 12)).foregroundStyle(.tertiary) }
                RankBars(items: vs.map { NameCount(name: $0.name, count: $0.count) }, tint: Theme.blue)
            }
        }
        HStack(alignment: .top, spacing: 12) {
            Panel(title: "每日活跃用户") {
                CountChart(series: (o.series ?? []).map { ($0.day, $0.active_users ?? 0) }, tint: Theme.ok, unit: "人")
            }
            Panel(title: "每日注册") {
                CountChart(series: (o.series ?? []).map { ($0.day, $0.signups ?? 0) }, tint: Theme.accent, unit: "人")
            }
        }
        HStack(alignment: .top, spacing: 12) {
            Panel(title: "常用 Skills（30 天）") { RankBars(items: (o.top_skills ?? []).map { NameCount(name: $0.name, count: $0.count) }, tint: Theme.accent) }
            Panel(title: "常用 Agents（30 天）") { RankBars(items: (o.top_agents ?? []).map { NameCount(name: $0.name, count: $0.count) }, tint: Color(red: 0.55, green: 0.40, blue: 0.85)) }
        }
        if let v = o.served_version {
            Text("服务器版本 \(v)" + (o.started_at.map { " · 启动于 " + AdminFmt.when($0) } ?? ""))
                .font(.system(size: 11)).foregroundStyle(.tertiary)
        }
    }
}

struct AdminUsersPage: View {
    @ObservedObject var store: AdminStore
    @State private var query = ""
    @State private var pendingDelete: AdminUser?
    @State private var pendingRevoke: AdminUser?
    @State private var devicesOf: AdminUser?

    private var shown: [AdminUser] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        return store.users.filter { q.isEmpty || $0.email.lowercased().contains(q) }
    }

    var body: some View {
        PageScroll(title: "用户", trailing: "\(store.users.count) 位") {
            let waiting = shown.filter { $0.status == "pending" }
            if !waiting.isEmpty {
                Panel(title: "等待审批") {
                    if waiting.count > 1 {
                        HStack {
                            Text("\(waiting.count) 位用户等待审批").font(.system(size: 11.5)).foregroundStyle(.secondary)
                            Spacer()
                            Button("全部批准") {
                                Task {
                                    for u in waiting {
                                        await store.call("POST", "/v1/admin/users/\(adminPath(u.email))/status", ["status": "active"], done: "已批准 \(waiting.count) 位用户", reload: false)
                                    }
                                    await store.refresh()
                                }
                            }
                            .modifier(PrimaryButton()).controlSize(.small)
                        }
                    }
                    ForEach(waiting) { u in
                        HStack(spacing: 10) {
                            Avatar(email: u.email, tint: Theme.warn)
                            VStack(alignment: .leading, spacing: 1) {
                                Text(u.email).font(.system(size: 13, weight: .medium))
                                Text("注册于 " + AdminFmt.day(u.created_at)).font(.system(size: 11)).foregroundStyle(.secondary)
                            }
                            Spacer()
                            Button("批准") { Task { await store.call("POST", "/v1/admin/users/\(adminPath(u.email))/status", ["status": "active"], done: "已批准 \(u.email)") } }
                                .modifier(PrimaryButton())
                            Button("拒绝") { pendingDelete = u }.buttonStyle(.bordered)
                        }
                    }
                }
            }
            HStack {
                Image(systemName: "magnifyingglass").foregroundStyle(.tertiary)
                TextField("搜索邮箱", text: $query).textFieldStyle(.plain)
            }
            .padding(8).background(Theme.card, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 9, style: .continuous).strokeBorder(Color.primary.opacity(0.07)))
            Panel {
                let rest = shown.filter { $0.status != "pending" }
                if rest.isEmpty { Text("没有匹配的用户").font(.system(size: 12)).foregroundStyle(.tertiary) }
                ForEach(Array(rest.enumerated()), id: \.element.id) { i, u in
                    if i > 0 { Divider().opacity(0.5) }
                    row(u)
                }
            }
        }
        .sheet(item: $devicesOf) { u in UserDevicesSheet(store: store, user: u).toastHost() }
        .confirmationDialog("删除 \(pendingDelete?.email ?? "")？", isPresented: Binding(get: { pendingDelete != nil }, set: { if !$0 { pendingDelete = nil } }), titleVisibility: .visible) {
            Button("删除用户及其全部数据", role: .destructive) {
                if let u = pendingDelete { Task { await store.call("DELETE", "/v1/admin/users/\(adminPath(u.email))", done: "已删除 \(u.email)") } }
                pendingDelete = nil
            }
        } message: { Text("会话、凭证和设备都会被清除，不能恢复。") }
        .confirmationDialog("下线 \(pendingRevoke?.email ?? "") 的全部设备？", isPresented: Binding(get: { pendingRevoke != nil }, set: { if !$0 { pendingRevoke = nil } }), titleVisibility: .visible) {
            Button("下线全部设备", role: .destructive) {
                if let u = pendingRevoke { Task { await store.call("POST", "/v1/admin/users/\(adminPath(u.email))/revoke-devices", done: "已下线 \(u.email) 的设备") } }
                pendingRevoke = nil
            }
        } message: { Text("这些设备需要重新登录。") }
    }

    @ViewBuilder private func row(_ u: AdminUser) -> some View {
        let path = "/v1/admin/users/\(adminPath(u.email))"
        HStack(spacing: 10) {
            Avatar(email: u.email, tint: u.status == "disabled" ? Theme.idle : Theme.blue)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(u.email).font(.system(size: 13, weight: .medium)).lineLimit(1)
                    if u.admin == true { Badge(text: "管理员", color: Theme.accent) }
                    if u.status == "disabled" { Badge(text: "已停用", color: Theme.bad) }
                    if !(u.online ?? []).isEmpty { Badge(text: "在线 \(u.online!.count)", color: Theme.ok) }
                }
                Text("\(u.devices ?? 0) 台设备 · \(u.sessions ?? 0) 个会话 · 最近 \(AdminFmt.ago(u.last_seen))")
                    .font(.system(size: 11)).foregroundStyle(.secondary)
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 1) {
                Text(Fmt.bytes(u.used_bytes ?? 0)).font(.system(size: 12))
                Text("配额 " + AdminFmt.quota(u.quota_bytes) + (u.quota_source == "default" ? "（默认）" : ""))
                    .font(.system(size: 10)).foregroundStyle(.tertiary)
            }
            Menu {
                Button("查看设备…") { devicesOf = u }
                Divider()
                Menu("配额") {
                    Button("使用默认") { quota(u, -1) }
                    Button("不限") { quota(u, 0) }
                    ForEach([5, 10, 50, 100], id: \.self) { g in Button("\(g) GB") { quota(u, Int64(g) << 30) } }
                }
                Button(u.admin == true ? "取消管理员" : "设为管理员") {
                    Task { await store.call("POST", path + "/admin", ["admin": u.admin != true], done: "已更新 \(u.email)") }
                }
                Button(u.status == "disabled" ? "启用" : "停用") {
                    Task { await store.call("POST", path + "/status", ["status": u.status == "disabled" ? "active" : "disabled"], done: "已更新 \(u.email)") }
                }
                Button("下线全部设备…") { pendingRevoke = u }
                Divider()
                Button("删除用户…", role: .destructive) { pendingDelete = u }
            } label: {
                Image(systemName: "ellipsis.circle").font(.system(size: 15)).foregroundStyle(.secondary)
            }
            .menuStyle(.borderlessButton).menuIndicator(.hidden).frame(width: 24)
        }
        .padding(.vertical, 4)
    }

    private func quota(_ u: AdminUser, _ bytes: Int64) {
        Task { await store.call("POST", "/v1/admin/users/\(adminPath(u.email))/quota", ["bytes": bytes], done: "已更新 \(u.email) 的配额") }
    }
}

/// A round monogram for an account.
struct Avatar: View {
    var email: String
    var tint: Color
    var body: some View {
        Circle().fill(tint.opacity(0.15)).frame(width: 30, height: 30)
            .overlay(Text(String(email.first.map(String.init) ?? "?").uppercased()).font(.system(size: 13, weight: .semibold)).foregroundStyle(tint))
    }
}

/// Devices one user is logged in on, as the web Users page shows them.
struct UserDevicesSheet: View {
    @ObservedObject var store: AdminStore
    var user: AdminUser
    @Environment(\.dismiss) private var dismiss
    @State private var devices: [Device]?
    @State private var failed = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            SheetHeader(icon: "desktopcomputer", title: user.email, subtitle: "登录中的设备", close: { dismiss() })
            if let ds = devices {
                if ds.isEmpty { Text("这位用户没有登录中的设备。").font(.system(size: 12)).foregroundStyle(.tertiary) }
                ScrollView {
                    VStack(spacing: 0) {
                        ForEach(Array(ds.enumerated()), id: \.element.id) { i, d in
                            if i > 0 { Divider().opacity(0.4) }
                            row(d)
                        }
                    }
                }
                .scrollIndicators(.never)
            } else if failed {
                Text("读取设备失败。").font(.system(size: 12)).foregroundStyle(Theme.warn)
            } else {
                HStack { Spacer(); ProgressView().controlSize(.small); Spacer() }.padding(.vertical, 20)
            }
        }
        .padding(18)
        .frame(width: 520, height: 420, alignment: .top)
        .task { await load() }
    }

    private func row(_ d: Device) -> some View {
        HStack(spacing: 10) {
            Circle().fill(d.online == true ? Theme.ok : Color.secondary.opacity(0.35)).frame(width: 7, height: 7)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(d.alias?.isEmpty == false ? d.alias! : d.name).font(.system(size: 13, weight: .medium))
                    if d.alias?.isEmpty == false { Text(d.name).font(.system(size: 11)).foregroundStyle(.tertiary) }
                }
                Text([d.ip, d.version.map { "v" + $0 }, "\(d.sessions ?? 0) 个会话"].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                    .font(.system(size: 11)).foregroundStyle(.secondary)
            }
            Spacer()
            Text(d.online == true ? "在线" : (d.last_seen.map { Fmt.ago($0) } ?? "")).font(.system(size: 11)).foregroundStyle(.secondary)
        }
        .padding(.vertical, 7)
    }

    private func load() async {
        guard let data = await store.call("GET", "/v1/admin/users/\(adminPath(user.email))/devices", reload: false) else { failed = true; return }
        if let ds = try? JSONDecoder().decode([Device].self, from: data) { devices = ds }
        else if let w = try? JSONDecoder().decode([String: [Device]].self, from: data), let ds = w.values.first { devices = ds }
        else { failed = true }
    }
}
