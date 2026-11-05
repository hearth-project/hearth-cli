import SwiftUI

struct AdminSettingsPage: View {
    @ObservedObject var store: AdminStore

    private static let modes: [(String, String, String)] = [
        ("invite", "凭邀请码", "只有持邀请码的人能注册"),
        ("approval", "需审批", "任何人可申请，管理员批准后生效"),
        ("open", "开放", "任何人可直接注册"),
        ("closed", "关闭", "不接受新用户"),
    ]

    var body: some View {
        PageScroll(title: "服务器设置", trailing: nil) {
            NoticeBar(store: store)
            if let s = store.settings {
                Panel(title: "注册方式") {
                    ForEach(Self.modes, id: \.0) { m in
                        Button { save(s, registration: m.0) } label: {
                            HStack(spacing: 10) {
                                Image(systemName: s.registration == m.0 ? "largecircle.fill.circle" : "circle")
                                    .foregroundStyle(s.registration == m.0 ? Theme.accent : Color.secondary)
                                VStack(alignment: .leading, spacing: 1) {
                                    Text(m.1).font(.system(size: 13, weight: .medium))
                                    Text(m.2).font(.system(size: 11)).foregroundStyle(.secondary)
                                }
                                Spacer()
                            }
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                    }
                }
                Panel(title: "默认配额") {
                    HStack {
                        Text("新用户可用空间").font(.system(size: 13))
                        Spacer()
                        Menu(AdminFmt.quota(s.default_quota)) {
                            Button("不限") { save(s, quota: 0) }
                            ForEach([5, 10, 50, 100], id: \.self) { g in Button("\(g) GB") { save(s, quota: Int64(g) << 30) } }
                        }
                        .menuStyle(.borderlessButton).fixedSize()
                    }
                }
                Panel(title: "客户端") {
                    Toggle(isOn: Binding(get: { s.client_auto_update }, set: { save(s, auto: $0) })) {
                        VStack(alignment: .leading, spacing: 1) {
                            Text("自动更新").font(.system(size: 13, weight: .medium))
                            Text("设备在后台自动升级到服务器提供的版本").font(.system(size: 11)).foregroundStyle(.secondary)
                        }
                    }
                    .toggleStyle(.switch)
                }
            } else {
                HStack { Spacer(); ProgressView(); Spacer() }.padding(.top, 60)
            }
        }
    }

    private func save(_ s: AdminSettings, registration: String? = nil, quota: Int64? = nil, auto: Bool? = nil) {
        let body: [String: Any] = [
            "registration": registration ?? s.registration,
            "default_quota": quota ?? s.default_quota,
            "client_auto_update": auto ?? s.client_auto_update,
        ]
        Task { await store.call("PUT", "/v1/admin/settings", body, done: "设置已保存") }
    }
}

struct AdminMaintenancePage: View {
    @ObservedObject var store: AdminStore
    @State private var result: GCResult?
    @State private var wasDry = true
    @State private var running = false
    @State private var confirm = false

    var body: some View {
        PageScroll(title: "维护", trailing: nil) {
            NoticeBar(store: store)
            Panel(title: "清理") {
                Text("清理过期会话和没有引用的文件。先预览，确认后再执行。")
                    .font(.system(size: 12)).foregroundStyle(.secondary)
                HStack(spacing: 8) {
                    Button("预览") { Task { await run(dry: true) } }.buttonStyle(.bordered)
                    Button("执行清理…") { confirm = true }.modifier(PrimaryButton())
                    if running { ProgressView().controlSize(.small) }
                    Spacer()
                }
                .disabled(running)
                if let r = result {
                    Divider().opacity(0.5)
                    Text(wasDry ? "预览结果（未改动任何数据）" : "清理完成").font(.system(size: 12, weight: .medium))
                    line("过期会话", r.expired ?? 0)
                    line(wasDry ? "待删除的会话" : "已删除的会话", r.deleted ?? 0)
                    line("无引用文件", r.orphans ?? 0, extra: Fmt.bytes(r.orphan_bytes ?? 0))
                    if !wasDry { line("已删除的文件", r.orphans_deleted ?? 0) }
                }
            }
        }
        .confirmationDialog("执行清理？", isPresented: $confirm, titleVisibility: .visible) {
            Button("执行清理", role: .destructive) { Task { await run(dry: false) } }
        } message: { Text("会删除过期会话和无引用文件，不能恢复。") }
    }

    private func line(_ t: String, _ n: Int, extra: String? = nil) -> some View {
        HStack {
            Text(t).font(.system(size: 12)).foregroundStyle(.secondary)
            Spacer()
            Text("\(n)" + (extra.map { " · \($0)" } ?? "")).font(.system(size: 12))
        }
    }

    private func run(dry: Bool) async {
        running = true
        defer { running = false }
        if let d = await store.call("POST", "/v1/admin/gc", ["dry_run": dry], done: dry ? nil : "清理完成", reload: !dry),
           let r = try? JSONDecoder().decode(GCResult.self, from: d) {
            result = r
            wasDry = dry
        }
    }
}

struct AdminAuditPage: View {
    @ObservedObject var store: AdminStore
    @State private var query = ""

    private var shown: [AuditEntry] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        return store.audit.filter {
            q.isEmpty || [$0.actor, $0.action, $0.target, $0.detail].contains { ($0 ?? "").lowercased().contains(q) }
        }
    }

    var body: some View {
        PageScroll(title: "审计日志", trailing: "最近 \(store.audit.count) 条") {
            HStack {
                Image(systemName: "magnifyingglass").foregroundStyle(.tertiary)
                TextField("搜索操作人、动作、对象", text: $query).textFieldStyle(.plain)
            }
            .padding(8).background(Theme.card, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 9, style: .continuous).strokeBorder(Color.primary.opacity(0.07)))
            Panel {
                if shown.isEmpty { Text("没有记录").font(.system(size: 12)).foregroundStyle(.tertiary) }
                ForEach(Array(shown.enumerated()), id: \.element.id) { i, e in
                    if i > 0 { Divider().opacity(0.5) }
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Badge(text: e.action, color: Theme.blue)
                            if let t = e.target, !t.isEmpty { Text(t).font(.system(size: 12, weight: .medium)).lineLimit(1) }
                            Spacer()
                            Text(AdminFmt.when(e.at)).font(.system(size: 11)).foregroundStyle(.tertiary)
                        }
                        Text([e.actor, e.detail].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                            .font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(2)
                    }
                    .padding(.vertical, 3)
                }
            }
        }
    }
}
