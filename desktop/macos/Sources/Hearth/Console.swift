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
    /// When the console last signed in with a fresh link. The page's session
    /// cookie lives 12 hours; the device login behind the menu lives 30 days.
    private var lastSignIn: Date?
    private var keyObserver: NSObjectProtocol?

    @MainActor func show() {
        if let w = window {
            w.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            if web?.url == nil { Task { await signIn() } } else { ensureSignedIn() }
            return
        }
        let cfg = WKWebViewConfiguration()
        cfg.websiteDataStore = .default()
        cfg.userContentController.addUserScript(Self.hideScrollbars)
        let web = WKWebView(frame: .zero, configuration: cfg)
        web.navigationDelegate = self
        web.uiDelegate = self
        web.allowsBackForwardNavigationGestures = true
        self.web = web

        let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1240, height: 820),
                         styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                         backing: .buffered, defer: false)
        w.title = "Hearth 控制台"
        w.titleVisibility = .hidden // the page has its own header; the title stays for the Window menu
        w.titlebarAppearsTransparent = true
        w.isReleasedWhenClosed = false
        w.minSize = NSSize(width: 900, height: 600)
        w.backgroundColor = .windowBackgroundColor
        w.contentView = NSHostingView(rootView: ConsoleShell(web: web))
        w.center()
        w.setFrameAutosaveName("HearthConsole")
        window = w
        keyObserver = NotificationCenter.default.addObserver(forName: NSWindow.didBecomeKeyNotification, object: w, queue: .main) { [weak self] _ in
            Task { @MainActor in self?.ensureSignedIn() }
        }
        w.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        Task { await signIn() }
    }

    /// No scroll bars in the console (pages still scroll). The page's CSP
    /// allows no inline styles, so the rule goes in as a constructed style
    /// sheet, or through the CSSOM on older WebKit.
    private static let hideScrollbars = WKUserScript(source: """
        (() => {
          const css = "*{scrollbar-width:none}::-webkit-scrollbar{display:none;width:0;height:0}";
          try {
            const s = new CSSStyleSheet();
            s.replaceSync(css);
            document.adoptedStyleSheets = [...document.adoptedStyleSheets, s];
          } catch (e) {
            const s = document.styleSheets[0];
            if (s) for (const r of css.split("}").filter(Boolean)) { try { s.insertRule(r + "}", s.cssRules.length); } catch (e) {} }
          }
        })();
        """, injectionTime: .atDocumentEnd, forMainFrameOnly: false)

    @MainActor func reload() {
        // A fresh link each time: reloading the old page would show the
        // login form once its session has expired.
        Task { await signIn() }
    }

    /// If the page sits on its login form (the web session expired while
    /// this Mac's login is still good), signs in again with a fresh link.
    /// At most once every 15 seconds, so a server that refuses the link
    /// does not loop.
    @MainActor func ensureSignedIn() {
        guard let web, web.url != nil else { return }
        if let t = lastSignIn, Date().timeIntervalSince(t) < 15 { return }
        web.evaluateJavaScript("(()=>{const a=document.getElementById('auth');return !!a&&!a.classList.contains('hidden')})()") { [weak self] r, _ in
            if (r as? Bool) == true { Task { @MainActor in await self?.signIn() } }
        }
    }

    @MainActor func openInBrowser() {
        Task {
            if let link = try? await Self.link() { NSWorkspace.shared.open(link.url) }
        }
    }

    struct Link: Decodable {
        var url: URL
        var server: String
        var ca: String
        var vault_key: String?

        /// The sign-in link with this Mac's vault key in the fragment (the
        /// fragment is never sent): the console then makes one-time install
        /// commands without asking for the password.
        var withKey: URL {
            guard let k = vault_key, var c = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return url }
            c.fragment = "hearth-vk=" + k
            return c.url ?? url
        }
    }

    static func link() async throws -> Link {
        let data = try await HearthCLI.run(["panel", "--json"])
        return try JSONDecoder().decode(Link.self, from: data)
    }

    @MainActor private func signIn() async {
        lastSignIn = Date()
        do {
            let l = try await Self.link()
            serverHost = l.url.host
            pinnedCert = Self.der(fromPEMFile: l.ca)
            web?.load(URLRequest(url: l.withKey))
        } catch {
            var msg = error.localizedDescription.replacingOccurrences(of: "<", with: "&lt;")
            if case HearthCLI.Failure.loginExpired = error { msg = "登录已过期。点菜单栏 Hearth 里的「重新登录」，登录后再点这里的刷新。" }
            web?.loadHTMLString("""
            <html><head><meta name="color-scheme" content="light dark"></head>
            <body style="font:14px -apple-system;display:flex;align-items:center;justify-content:center;height:90vh">
            <div style="text-align:center;max-width:460px"><div style="font-size:28px">🔥</div><p>打不开控制台</p><p style="opacity:.6">\(msg)</p></div></body></html>
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

/// The window's content: the console page under a slim native bar (traffic
/// lights, reload, open in browser) that follows the system appearance.
struct ConsoleShell: View {
    let web: WKWebView
    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Spacer()
                Button { ConsoleController.shared.reload() } label: { Image(systemName: "arrow.clockwise") }
                    .help("重新加载")
                Button { ConsoleController.shared.openInBrowser() } label: { Image(systemName: "safari") }
                    .help("在浏览器中打开")
            }
            .buttonStyle(.borderless)
            .foregroundStyle(.secondary)
            .padding(.horizontal, 12)
            .frame(height: 38)
            .background(.bar)
            .overlay(alignment: .bottom) { Divider().opacity(0.6) }
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
