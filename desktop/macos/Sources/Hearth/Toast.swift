import SwiftUI

/// The one action notice of the Hearth window. Pages and sheets don't draw
/// their own banners: the topmost layer (the open sheet, else the window)
/// shows it as a floating toast. Success hides itself after two seconds;
/// errors stay until closed.
@MainActor
final class Toasts: ObservableObject {
    static let shared = Toasts()
    @Published private(set) var text: String?
    @Published private(set) var isError = false
    /// Number of sheets on screen; the toast is drawn by the layer at this depth.
    @Published var depth = 0
    private var generation = 0

    func show(_ text: String, error: Bool) {
        generation += 1
        let g = generation
        self.text = text
        isError = error
        guard !error else { return }
        Task { [weak self] in
            try? await Task.sleep(nanoseconds: 2_000_000_000)
            guard let self, self.generation == g else { return }
            self.text = nil
        }
    }

    func hide() {
        generation += 1
        text = nil
    }
}

struct ToastView: View {
    var text: String
    var isError: Bool
    var close: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: isError ? "exclamationmark.triangle.fill" : "checkmark.circle.fill")
                .foregroundStyle(isError ? Theme.bad : Theme.ok)
            Text(text).font(.system(size: 12, weight: .medium)).lineLimit(3)
                .fixedSize(horizontal: false, vertical: true)
            if isError {
                Button(action: close) {
                    Image(systemName: "xmark").font(.system(size: 9, weight: .bold))
                }
                .buttonStyle(.plain).foregroundStyle(.secondary).focusable(false)
                .help("关闭")
            }
        }
        .padding(.horizontal, 14).padding(.vertical, 9)
        .background(.regularMaterial, in: Capsule())
        .overlay(Capsule().strokeBorder(Color.primary.opacity(0.08)))
        .shadow(color: .black.opacity(0.16), radius: 10, y: 4)
        .frame(maxWidth: 460)
        .padding(.top, 10)
        .transition(.move(edge: .top).combined(with: .opacity))
    }
}

@MainActor
struct ToastHost: ViewModifier {
    var root: Bool
    @ObservedObject private var center = Toasts.shared
    @State private var level: Int?

    func body(content: Content) -> some View {
        content
            .overlay(alignment: .top) {
                if (root ? 0 : level) == center.depth, let t = center.text {
                    ToastView(text: t, isError: center.isError) { center.hide() }
                }
            }
            .animation(.easeOut(duration: 0.2), value: center.text)
            .onAppear {
                guard !root, level == nil else { return }
                center.depth += 1
                level = center.depth
            }
            .onDisappear {
                guard !root, level != nil else { return }
                center.depth = max(0, center.depth - 1)
                level = nil
            }
    }
}

extension View {
    /// Draw the window's action notice here when this is the topmost layer.
    /// Use `root: true` on the window content and the default on each sheet.
    func toastHost(root: Bool = false) -> some View { modifier(ToastHost(root: root)) }
}
