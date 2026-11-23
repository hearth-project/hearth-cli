import AppKit
import SwiftUI

/// A menu that opens beside the dropdown, the way a submenu opens beside its
/// menu: right of the window, or left of it where the screen ends, with its
/// first item level with the row that opened it. A native NSMenu, so the
/// keyboard, VoiceOver and the system's look come with it.
@MainActor
final class SideMenu: NSObject {
    struct Item {
        var title: String
        var icon: String
        var enabled = true
        var separatorBefore = false
        var action: @MainActor () -> Void
    }

    /// The row the menu opens from (set by SideMenuAnchor).
    weak var row: NSView?
    private var actions: [@MainActor () -> Void] = []
    private var chosen: Int?
    private var tracking = false

    /// Space between the dropdown and the menu.
    private static let gap: CGFloat = 4
    /// The menu's padding above its first item: the menu starts this much
    /// higher, so the first item is level with the row.
    private static let topInset: CGFloat = 5

    /// Shows the menu and returns once it has closed. The chosen item's
    /// action runs after that, outside the menu's event loop, so an alert it
    /// opens is an ordinary modal.
    func show(_ items: [Item]) {
        guard let row, let window = row.window else { return }
        let menu = NSMenu()
        menu.autoenablesItems = false
        for (i, item) in items.enumerated() {
            if item.separatorBefore { menu.addItem(.separator()) }
            let m = NSMenuItem(title: item.title, action: #selector(choose(_:)), keyEquivalent: "")
            m.target = self
            m.tag = i
            m.isEnabled = item.enabled
            m.image = NSImage(systemSymbolName: item.icon, accessibilityDescription: nil)
            menu.addItem(m)
        }
        actions = items.map(\.action)
        chosen = nil
        let r = window.convertToScreen(row.convert(row.bounds, to: nil))
        let screen = (window.screen ?? NSScreen.main)?.visibleFrame ?? window.frame
        var x = window.frame.maxX + Self.gap
        if x + menu.size.width > screen.maxX {
            x = window.frame.minX - Self.gap - menu.size.width
        }
        tracking = true
        _ = menu.popUp(positioning: nil, at: NSPoint(x: x, y: r.maxY + Self.topInset), in: nil)
        tracking = false
        runChosen()
    }

    /// AppKit sends the item's action as the menu closes, before popUp
    /// returns; should it come after, the action runs from here instead.
    @objc private func choose(_ sender: NSMenuItem) {
        chosen = sender.tag
        if !tracking { runChosen() }
    }

    private func runChosen() {
        guard let i = chosen, actions.indices.contains(i) else { return }
        chosen = nil
        let action = actions[i]
        Task { @MainActor in action() }
    }
}

/// Hands the row's NSView to its SideMenu, which places the menu from it.
struct SideMenuAnchor: NSViewRepresentable {
    let menu: SideMenu

    final class Anchor: NSView {
        override func hitTest(_ point: NSPoint) -> NSView? { nil } // clicks go to the row
    }

    func makeNSView(context: Context) -> Anchor {
        let v = Anchor()
        menu.row = v
        return v
    }

    func updateNSView(_ v: Anchor, context: Context) { menu.row = v }
}
