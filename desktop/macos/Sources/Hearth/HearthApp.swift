import ServiceManagement
import SwiftUI

@main
enum Entry {
    @MainActor static func main() {
        // `Hearth --snapshot <machines.json> <dir>` renders the menu to PNG
        // files and exits (CI checks the layout without a menu bar).
        let args = CommandLine.arguments
        if let i = args.firstIndex(of: "--snapshot"), args.count > i + 2 {
            _ = NSApplication.shared
            Snapshot.render(fixture: args[i + 1], outDir: args[i + 2])
            exit(0)
        }
        if let i = args.firstIndex(of: "--icon"), args.count > i + 1 {
            _ = NSApplication.shared
            Snapshot.icon(to: args[i + 1])
            exit(0)
        }
        HearthApp.main()
    }
}

struct HearthApp: App {
    @StateObject private var store = MachineStore()

    var body: some Scene {
        MenuBarExtra {
            MenuView(store: store, openConsole: { NativeController.shared.show(machines: store) })
        } label: {
            MenuBarLabel(store: store)
        }
        .menuBarExtraStyle(.window)
    }
}

/// The flame in the menu bar; filled when a machine has a problem.
struct MenuBarLabel: View {
    @ObservedObject var store: MachineStore
    var body: some View {
        Image(systemName: store.hasError ? "flame.fill" : "flame")
    }
}

/// Start Hearth when the user logs in (macOS 13 login items), as a row of
/// the dropdown's bottom menu with a switch at the end.
struct LoginItemRow: View {
    @State private var on = SMAppService.mainApp.status == .enabled
    @State private var hover = false
    var body: some View {
        Button { set(!on) } label: {
            MenuRowLabel(icon: "sunrise", title: "登录时启动", hover: hover) {
                Toggle("", isOn: Binding(get: { on }, set: set))
                    .toggleStyle(.switch).controlSize(.mini).labelsHidden()
            }
        }
        .buttonStyle(.plain)
        .onHover { hover = $0 }
    }

    private func set(_ v: Bool) {
        do {
            if v { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
        } catch {}
        on = SMAppService.mainApp.status == .enabled
    }
}
