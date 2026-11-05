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

    func show(machines: MachineStore, page: NativePage? = nil) {
        if let w = window {
            w.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            Task { await portal.refresh() }
            return
        }
        let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1080, height: 740),
                         styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                         backing: .buffered, defer: false)
        w.title = "Hearth"
        w.titlebarAppearsTransparent = true
        w.isReleasedWhenClosed = false
        w.minSize = NSSize(width: 820, height: 540)
        w.contentView = NSHostingView(rootView: NativeRoot(machines: machines, portal: portal))
        w.center()
        w.setFrameAutosaveName("HearthNative")
        window = w
        w.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        Task { await portal.refresh() }
    }
}

enum NativePage: String, CaseIterable, Identifiable {
    case overview, machines, library, teams
    var id: String { rawValue }
    var title: String {
        switch self {
        case .overview: return "概览"
        case .machines: return "机器"
        case .library: return "技能与 Agent"
        case .teams: return "团队"
        }
    }
    var icon: String {
        switch self {
        case .overview: return "square.grid.2x2"
        case .machines: return "desktopcomputer"
        case .library: return "books.vertical"
        case .teams: return "person.2"
        }
    }
    var tint: Color {
        switch self {
        case .overview: return Theme.blue
        case .machines: return Theme.ok
        case .library: return Theme.accent
        case .teams: return Color(red: 0.55, green: 0.40, blue: 0.85)
        }
    }
}

struct NativeRoot: View {
    @ObservedObject var machines: MachineStore
    @ObservedObject var portal: PortalStore
    @State private var page: NativePage = .overview

    var body: some View {
        NavigationSplitView {
            VStack(spacing: 0) {
                List(NativePage.allCases, selection: Binding<NativePage?>(get: { page }, set: { if let p = $0 { page = p } })) { p in
                    Label {
                        Text(p.title)
                    } icon: {
                        Image(systemName: p.icon).foregroundStyle(p.tint)
                    }
                    .tag(p)
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
                if portal.loginExpired || machines.loginExpired {
                    ExpiredView(retry: { Task { await portal.refresh(); await machines.refresh() } })
                } else {
                    switch page {
                    case .overview: OverviewPage(portal: portal, machines: machines, go: { page = $0 })
                    case .machines: MachinesPage(store: machines)
                    case .library: LibraryPage(portal: portal)
                    case .teams: TeamsPage(portal: portal)
                    }
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Color(nsColor: .windowBackgroundColor))
            .toolbar {
                ToolbarItem(placement: .primaryAction) {
                    Button {
                        Task { await portal.refresh(); await machines.refresh() }
                    } label: {
                        if portal.loading { ProgressView().controlSize(.small) } else { Image(systemName: "arrow.clockwise") }
                    }
                    .help("刷新")
                }
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
