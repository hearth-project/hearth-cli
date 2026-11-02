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
        HearthApp.main()
    }
}

struct HearthApp: App {
    @StateObject private var store = MachineStore()

    var body: some Scene {
        MenuBarExtra {
            MenuView(store: store, openConsole: { ConsoleController.shared.show() })
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

/// Start Hearth when the user logs in (macOS 13 login items).
struct LoginItemToggle: View {
    @State private var on = SMAppService.mainApp.status == .enabled
    var body: some View {
        Toggle("登录时启动", isOn: Binding(get: { on }, set: { v in
            do {
                if v { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            } catch {}
            on = SMAppService.mainApp.status == .enabled
        }))
    }
}
