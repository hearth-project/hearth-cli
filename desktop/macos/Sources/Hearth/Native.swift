import AppKit
import SwiftUI

/// The native window: overview, machines, skills and agents, teams. It reads
/// `hearth app data --json` and `hearth machine list --json`, so it uses the
/// same device login as the menu (no second sign-in). The server's admin pages
/// stay on the web; the sidebar opens them in the console window.
@MainActor final class NativeController {
    static let shared = NativeController()
    private var window: NSWindow?
    let portal = PortalStore()
    let admin = AdminStore()

    func show(machines: MachineStore, page: NativePage? = nil) {
        if let w = window {
            w.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            Task { await portal.refresh(); if portal.me?.admin == true { await admin.refresh() } }
            return
        }
        let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1080, height: 740),
                         styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                         backing: .buffered, defer: false)
        w.title = "Hearth"
        w.titlebarAppearsTransparent = true
        w.isReleasedWhenClosed = false
        w.minSize = NSSize(width: 820, height: 540)
        w.contentView = NSHostingView(rootView: NativeRoot(machines: machines, portal: portal, admin: admin))
        w.center()
        w.setFrameAutosaveName("HearthNative")
        window = w
        w.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        Task { await portal.refresh(); if portal.me?.admin == true { await admin.refresh() } }
    }
}

enum AdminPage: String, CaseIterable, Identifiable {
    case dashboard, users, invites, teams, settings, maintenance, audit
    var id: String { rawValue }
    var title: String {
        switch self {
        case .dashboard: return "管理概览"
        case .users: return "用户"
        case .invites: return "邀请码"
        case .teams: return "团队管理"
        case .settings: return "服务器设置"
        case .maintenance: return "维护"
        case .audit: return "审计日志"
        }
    }
    var icon: String {
        switch self {
        case .dashboard: return "chart.bar.xaxis"
        case .users: return "person.crop.circle"
        case .invites: return "ticket"
        case .teams: return "person.3"
        case .settings: return "gearshape"
        case .maintenance: return "wrench.and.screwdriver"
        case .audit: return "list.bullet.rectangle"
        }
    }
    var tint: Color {
        switch self {
        case .dashboard: return Theme.blue
        case .users: return Color(red: 0.55, green: 0.40, blue: 0.85)
        case .invites: return Theme.accent
        case .teams: return Theme.ok
        case .settings: return Theme.slate
        case .maintenance: return Theme.warn
        case .audit: return Theme.idle
        }
    }
}

enum Dest: Hashable {
    case user(NativePage)
    case admin(AdminPage)
}

enum NativePage: String, CaseIterable, Identifiable {
    case overview, machines, library, teams, settings
    var id: String { rawValue }
    var title: String {
        switch self {
        case .overview: return "概览"
        case .machines: return "机器"
        case .library: return "技能与 Agent"
        case .teams: return "团队"
        case .settings: return "我的设置"
        }
    }
    var icon: String {
        switch self {
        case .overview: return "square.grid.2x2"
        case .machines: return "desktopcomputer"
        case .library: return "books.vertical"
        case .teams: return "person.2"
        case .settings: return "gearshape"
        }
    }
    var tint: Color {
        switch self {
        case .overview: return Theme.blue
        case .machines: return Theme.ok
        case .library: return Theme.accent
        case .teams: return Color(red: 0.55, green: 0.40, blue: 0.85)
        case .settings: return .gray
        }
    }
}

struct NativeRoot: View {
    @ObservedObject var machines: MachineStore
    @ObservedObject var portal: PortalStore
    @ObservedObject var admin: AdminStore
    @State private var dest: Dest = .user(.overview)

    private var isAdmin: Bool { portal.me?.admin == true }

    var body: some View {
        NavigationSplitView {
            VStack(spacing: 0) {
                List(selection: Binding<Dest?>(get: { dest }, set: { if let d = $0 { dest = d } })) {
                    ForEach(NativePage.allCases) { p in
                        Label { Text(p.title) } icon: { Image(systemName: p.icon).foregroundStyle(p.tint) }
                            .tag(Dest.user(p))
                    }
                    if isAdmin {
                        Section("管理") {
                            ForEach(AdminPage.allCases) { p in
                                Label { Text(p.title) } icon: { Image(systemName: p.icon).foregroundStyle(p.tint) }
                                    .tag(Dest.admin(p))
                            }
                        }
                    }
                }
                .listStyle(.sidebar)
                Divider().opacity(0.5)
                Button { ConsoleController.shared.show() } label: {
                    HStack(spacing: 8) {
                        Image(systemName: "globe").foregroundStyle(Theme.slate)
                        Text("管理后台（网页）").font(.system(size: 12))
                        Spacer()
                        Image(systemName: "arrow.up.right").font(.system(size: 10)).foregroundStyle(.tertiary)
                    }
                    .padding(.horizontal, 14).padding(.vertical, 10)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help("用户、团队管理、审批、设置、审计等管理页面")
            }
            .navigationSplitViewColumnWidth(min: 170, ideal: 190, max: 240)
        } detail: {
            Group {
                if portal.loginExpired || machines.loginExpired || (admin.error?.contains("登录已过期") == true) {
                    ExpiredView(retry: { Task { await portal.refresh(); await machines.refresh() } })
                } else {
                    switch dest {
                    case .user(.overview): OverviewPage(portal: portal, machines: machines, go: { dest = .user($0) })
                    case .user(.machines): MachinesPage(store: machines, portal: portal)
                    case .user(.library): LibraryPage(portal: portal)
                    case .user(.teams): TeamsPage(portal: portal)
                    case .user(.settings): MySettingsPage(portal: portal)
                    case .admin(.dashboard): AdminDashboardPage(store: admin)
                    case .admin(.users): AdminUsersPage(store: admin)
                    case .admin(.invites): AdminInvitesPage(store: admin)
                    case .admin(.teams): AdminTeamsPage(store: admin)
                    case .admin(.settings): AdminSettingsPage(store: admin)
                    case .admin(.maintenance): AdminMaintenancePage(store: admin)
                    case .admin(.audit): AdminAuditPage(store: admin)
                    }
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Color(nsColor: .windowBackgroundColor))
            .toolbar {
                ToolbarItem(placement: .primaryAction) {
                    Button {
                        Task { await portal.refresh(); await machines.refresh(); if isAdmin { await admin.refresh() } }
                    } label: {
                        if portal.loading || admin.loading { ProgressView().controlSize(.small) } else { Image(systemName: "arrow.clockwise") }
                    }
                    .help("刷新")
                }
            }
            .onChange(of: dest) { d in
                if case .admin = d { Task { await admin.refresh() } }
            }
        }
    }
}

struct ExpiredView: View {
    var retry: () -> Void
    @State private var note: String?
    @State private var waiting = false
    var body: some View {
        VStack(spacing: 12) {
            Image(systemName: "lock.trianglebadge.exclamationmark").font(.system(size: 34)).foregroundStyle(Theme.warn)
            Text("登录已过期").font(.system(size: 17, weight: .semibold))
            Text("重新登录后才能查看。主密码在终端里输入，不经过这个应用。")
                .font(.system(size: 12)).foregroundStyle(.secondary)
            Button("重新登录") {
                note = Actions.loginInTerminal()
                waiting = true
            }
            .modifier(PrimaryButton())
            if let n = note { Text(n).font(.system(size: 11)).foregroundStyle(.tertiary) }
        }
        // Cancelled automatically when this view leaves the hierarchy (login recovered).
        .task(id: waiting) {
            guard waiting else { return }
            for _ in 0..<200 {
                try? await Task.sleep(nanoseconds: 3_000_000_000)
                if Task.isCancelled { return }
                retry()
            }
        }
    }
}
