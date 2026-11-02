import AppKit
import SwiftUI

/// Renders the menu with sample data to PNG files (light and dark), for CI
/// and for reviewing the design without a menu bar.
enum Snapshot {
    @MainActor static func render(fixture: String, outDir: String) {
        guard let data = FileManager.default.contents(atPath: fixture),
              let list = try? JSONDecoder().decode(MachineList.self, from: data) else {
            FileHandle.standardError.write("cannot read \(fixture)\n".data(using: .utf8)!)
            exit(1)
        }
        try? FileManager.default.createDirectory(atPath: outDir, withIntermediateDirectories: true)
        let store = MachineStore(fixture: list)
        let first = store.machines.first?.id
        for (name, scheme) in [("light", ColorScheme.light), ("dark", ColorScheme.dark)] {
            for (suffix, open) in [("", nil as String?), ("-open", first)] {
                let view = MenuView(store: store, openConsole: {}, snapshot: true, expanded: open)
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
}
