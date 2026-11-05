import AppKit
import SwiftUI

/// Renders the menu with sample data to PNG files (light and dark), for CI
/// and for reviewing the design without a menu bar.
enum Snapshot {
    /// True while rendering PNGs.
    @MainActor static var active = false

    @MainActor static func render(fixture: String, outDir: String) {
        guard let data = FileManager.default.contents(atPath: fixture),
              let list = try? JSONDecoder().decode(MachineList.self, from: data) else {
            FileHandle.standardError.write("cannot read \(fixture)\n".data(using: .utf8)!)
            exit(1)
        }
        try? FileManager.default.createDirectory(atPath: outDir, withIntermediateDirectories: true)
        active = true
        let store = MachineStore(fixture: list)
        let first = store.machines.first?.id
        let expired = MachineStore(fixture: list)
        expired.loginExpired = true
        expired.list = nil
        if let pf = ProcessInfo.processInfo.environment["HEARTH_PORTAL_FIXTURE"] {
            renderNative(store: store, portalFixture: pf, outDir: outDir)
        }
        if let af = ProcessInfo.processInfo.environment["HEARTH_ADMIN_FIXTURE"] {
            renderAdmin(fixture: af, outDir: outDir)
        }
        for (name, scheme) in [("light", ColorScheme.light), ("dark", ColorScheme.dark)] {
            for (suffix, open, s) in [("", nil as String?, store), ("-open", first, store), ("-expired", nil as String?, expired)] {
                let view = MenuView(store: s, openConsole: {}, snapshot: true, expanded: open)
                    .environment(\.colorScheme, scheme)
                let host = NSHostingView(rootView: view)
                host.appearance = NSAppearance(named: scheme == .dark ? .darkAqua : .aqua)
                host.frame.size = host.fittingSize
                host.layoutSubtreeIfNeeded()
                guard let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds) else { continue }
                host.cacheDisplay(in: host.bounds, to: rep)
                let path = (outDir as NSString).appendingPathComponent("menu-\(name)\(suffix).png")
                try? rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: path))
                print(path)
            }
        }
    }

    /// The native window's pages (HEARTH_PORTAL_FIXTURE = `hearth app data --json` output).
    @MainActor static func renderNative(store: MachineStore, portalFixture: String, outDir: String) {
        guard let data = FileManager.default.contents(atPath: portalFixture),
              let reply = try? JSONDecoder().decode(PortalReply.self, from: data) else {
            FileHandle.standardError.write("cannot read \(portalFixture)\n".data(using: .utf8)!)
            return
        }
        let portal = PortalStore(fixture: reply)
        let pages: [(String, AnyView)] = [
            ("overview", AnyView(OverviewPage(portal: portal, machines: store, go: { _ in }))),
            ("machines", AnyView(MachinesPage(store: store, portal: portal))),
            ("library", AnyView(LibraryPage(portal: portal))),
            ("teams", AnyView(TeamsPage(portal: portal))),
            ("settings", AnyView(MySettingsPage(portal: portal))),
        ]
        for (name, scheme) in [("light", ColorScheme.light), ("dark", ColorScheme.dark)] {
            for (page, v) in pages {
                let view = v.frame(width: 900, height: 760)
                    .background(Color(nsColor: .windowBackgroundColor))
                    .environment(\.colorScheme, scheme)
                let host = NSHostingView(rootView: view)
                host.appearance = NSAppearance(named: scheme == .dark ? .darkAqua : .aqua)
                host.frame = NSRect(x: 0, y: 0, width: 900, height: 760)
                host.layoutSubtreeIfNeeded()
                guard let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds) else { continue }
                host.cacheDisplay(in: host.bounds, to: rep)
                let path = (outDir as NSString).appendingPathComponent("native-\(page)-\(name).png")
                try? rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: path))
                print(path)
            }
        }
    }

    /// The admin pages (HEARTH_ADMIN_FIXTURE = `hearth app admin-data --json` output).
    @MainActor static func renderAdmin(fixture: String, outDir: String) {
        guard let data = FileManager.default.contents(atPath: fixture),
              let reply = try? JSONDecoder().decode(AdminReply.self, from: data) else {
            FileHandle.standardError.write("cannot read \(fixture)\n".data(using: .utf8)!)
            return
        }
        let admin = AdminStore(fixture: reply)
        let pages: [(String, AnyView)] = [
            ("dashboard", AnyView(AdminDashboardPage(store: admin))),
            ("users", AnyView(AdminUsersPage(store: admin))),
            ("invites", AnyView(AdminInvitesPage(store: admin))),
            ("teams", AnyView(AdminTeamsPage(store: admin))),
            ("settings", AnyView(AdminSettingsPage(store: admin))),
            ("maintenance", AnyView(AdminMaintenancePage(store: admin))),
            ("audit", AnyView(AdminAuditPage(store: admin))),
        ]
        for (name, scheme) in [("light", ColorScheme.light), ("dark", ColorScheme.dark)] {
            for (page, v) in pages {
                let view = v.frame(width: 900, height: 760)
                    .background(Color(nsColor: .windowBackgroundColor))
                    .environment(\.colorScheme, scheme)
                let host = NSHostingView(rootView: view)
                host.appearance = NSAppearance(named: scheme == .dark ? .darkAqua : .aqua)
                host.frame = NSRect(x: 0, y: 0, width: 900, height: 760)
                host.layoutSubtreeIfNeeded()
                guard let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds) else { continue }
                host.cacheDisplay(in: host.bounds, to: rep)
                let path = (outDir as NSString).appendingPathComponent("admin-\(page)-\(name).png")
                try? rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: path))
                print(path)
            }
        }
    }

    /// The app icon (1024 px): the flame on a warm rounded square. The build
    /// script turns it into AppIcon.icns.
    @MainActor static func icon(to path: String) {
        let view = ZStack {
            RoundedRectangle(cornerRadius: 230, style: .continuous)
                .fill(LinearGradient(colors: [Color(red: 0.98, green: 0.62, blue: 0.30), Color(red: 0.86, green: 0.33, blue: 0.16)],
                                     startPoint: .top, endPoint: .bottom))
                .frame(width: 824, height: 824)
                .shadow(color: .black.opacity(0.25), radius: 18, y: 10)
            Image(systemName: "flame.fill")
                .font(.system(size: 470, weight: .regular))
                .foregroundStyle(.white)
                .shadow(color: Color(red: 0.6, green: 0.2, blue: 0.05).opacity(0.35), radius: 10, y: 6)
        }
        .frame(width: 1024, height: 1024)
        let host = NSHostingView(rootView: view)
        host.frame = NSRect(x: 0, y: 0, width: 1024, height: 1024)
        host.layoutSubtreeIfNeeded()
        guard let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds) else { exit(1) }
        host.cacheDisplay(in: host.bounds, to: rep)
        try? rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: path))
        print(path)
    }
}
