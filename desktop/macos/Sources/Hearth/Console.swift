import AppKit
import SwiftUI
@preconcurrency import WebKit

/// The console in a window of its own: the server's web console (machines,
/// skills and agents, teams, settings; admins also manage the server),
/// signed in with a one-time link from `hearth panel --json`, so there is no
/// password to type. A self-signed server is trusted only with the exact
/// certificate the hearth login on this Mac keeps (<home>/ca.pem).
final class ConsoleController: NSObject, WKNavigationDelegate, WKUIDelegate {
    static let shared = ConsoleController()
    private var window: NSWindow?
    private var web: WKWebView?
    private var pinnedCert: Data?
    private var serverHost: String?

    @MainActor func show() {
        if let w = window {
            w.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            if web?.url == nil { Task { await signIn() } }
            return
        }
        let cfg = WKWebViewConfiguration()
        cfg.websiteDataStore = .default()
        let web = WKWebView(frame: .zero, configuration: cfg)
        web.navigationDelegate = self
        web.uiDelegate = self
        web.allowsBackForwardNavigationGestures = true
        self.web = web

        let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1240, height: 820),
                         styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                         backing: .buffered, defer: false)
        w.title = "Hearth 控制台"
        w.titlebarAppearsTransparent = true
        w.isReleasedWhenClosed = false
        w.minSize = NSSize(width: 900, height: 600)
        w.backgroundColor = NSColor(red: 0.07, green: 0.08, blue: 0.10, alpha: 1)
        w.contentView = NSHostingView(rootView: ConsoleShell(web: web))
        w.center()
        w.setFrameAutosaveName("HearthConsole")
        window = w
        w.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        Task { await signIn() }
    }

    @MainActor func reload() {
        if web?.url == nil { Task { await signIn() } } else { web?.reload() }
    }

    @MainActor func openInBrowser() {
        Task {
            if let link = try? await Self.link() { NSWorkspace.shared.open(link.url) }
        }
    }

    struct Link: Decodable { var url: URL; var server: String; var ca: String }

    static func link() async throws -> Link {
        let data = try await HearthCLI.run(["panel", "--json"])
        return try JSONDecoder().decode(Link.self, from: data)
    }

    @MainActor private func signIn() async {
        do {
            let l = try await Self.link()
            serverHost = l.url.host
            pinnedCert = Self.der(fromPEMFile: l.ca)
            web?.load(URLRequest(url: l.url))
        } catch {
            let msg = error.localizedDescription.replacingOccurrences(of: "<", with: "&lt;")
            web?.loadHTMLString("""
            <html><body style="font:14px -apple-system;color:#c9ccd1;background:#121418;display:flex;align-items:center;justify-content:center;height:90vh">
            <div style="text-align:center;max-width:460px"><div style="font-size:28px">🔥</div><p>打不开控制台</p><p style="color:#8b9099">\(msg)</p></div></body></html>
            """, baseURL: nil)
        }
    }

    static func der(fromPEMFile path: String) -> Data? {
        guard let pem = try? String(contentsOfFile: path, encoding: .utf8) else { return nil }
        // The first certificate of the file: the server's own.
        guard let begin = pem.range(of: "-----BEGIN CERTIFICATE-----"),
              let end = pem.range(of: "-----END CERTIFICATE-----", range: begin.upperBound..<pem.endIndex) else { return nil }
        let b64 = pem[begin.upperBound..<end.lowerBound].components(separatedBy: .whitespacesAndNewlines).joined()
        return Data(base64Encoded: b64)
    }

    // Trust the server's self-signed certificate only when it is the one in
    // ca.pem; everything else is checked by the system as usual.
    func webView(_ webView: WKWebView, didReceive challenge: URLAuthenticationChallenge,
                 completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
              let trust = challenge.protectionSpace.serverTrust,
              challenge.protectionSpace.host == serverHost,
              let pinned = pinnedCert,
              let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate],
              let leaf = chain.first,
              SecCertificateCopyData(leaf) as Data == pinned
        else {
            completionHandler(.performDefaultHandling, nil)
            return
        }
        completionHandler(.useCredential, URLCredential(trust: trust))
    }

    // Links to other sites open in the browser.
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        if let u = action.request.url, let h = u.host, h != serverHost, u.scheme == "https" || u.scheme == "http" {
            NSWorkspace.shared.open(u)
            decisionHandler(.cancel)
            return
        }
        decisionHandler(.allow)
    }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                 for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        if let u = action.request.url { NSWorkspace.shared.open(u) }
        return nil
    }

    // The console asks before removing a machine (confirm()).
    func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (Bool) -> Void) {
        let a = NSAlert()
        a.messageText = message
        a.addButton(withTitle: "确定")
        a.addButton(withTitle: "取消")
        completionHandler(a.runModal() == .alertFirstButtonReturn)
    }

    func webView(_ webView: WKWebView, runJavaScriptAlertPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping () -> Void) {
        let a = NSAlert()
        a.messageText = message
        a.runModal()
        completionHandler()
    }

    func webView(_ webView: WKWebView, runJavaScriptTextInputPanelWithPrompt prompt: String, defaultText: String?,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (String?) -> Void) {
        let a = NSAlert()
        a.messageText = prompt
        let f = NSTextField(frame: NSRect(x: 0, y: 0, width: 280, height: 24))
        f.stringValue = defaultText ?? ""
        a.accessoryView = f
        a.addButton(withTitle: "确定")
        a.addButton(withTitle: "取消")
        completionHandler(a.runModal() == .alertFirstButtonReturn ? f.stringValue : nil)
    }
}

/// The window's content: the console page under a slim native bar.
struct ConsoleShell: View {
    let web: WKWebView
    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Spacer().frame(width: 70) // traffic lights
                Image(systemName: "flame.fill").foregroundStyle(Theme.accent)
                Text("Hearth 控制台").font(.system(size: 13, weight: .semibold)).foregroundStyle(.white.opacity(0.9))
                Spacer()
                Button { ConsoleController.shared.reload() } label: { Image(systemName: "arrow.clockwise") }
                    .help("重新加载")
                Button { ConsoleController.shared.openInBrowser() } label: { Image(systemName: "safari") }
                    .help("在浏览器中打开")
            }
            .buttonStyle(.borderless)
            .foregroundStyle(.white.opacity(0.75))
            .padding(.horizontal, 12)
            .frame(height: 38)
            .background(Color(red: 0.07, green: 0.08, blue: 0.10))
            WebView(web: web)
        }
        .ignoresSafeArea()
    }
}

struct WebView: NSViewRepresentable {
    let web: WKWebView
    func makeNSView(context: Context) -> WKWebView { web }
    func updateNSView(_ nsView: WKWebView, context: Context) {}
}
