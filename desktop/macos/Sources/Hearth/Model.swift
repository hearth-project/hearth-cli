import Foundation

// The data `hearth machine list --json` prints (cmd/hearth/machine.go).
struct MachineList: Decodable {
    var server: String
    var email: String
    var machines: [Machine]
}

struct Problem: Decodable, Hashable {
    var code: String
    var level: String
    var tool: String?
    var have: String?
    var latest: String?
    var behind: Int?
    var text: String
}

struct Machine: Decodable, Identifiable, Hashable {
    var id: String
    var name: String
    var alias: String?
    var label: String
    var host: String
    var this: Bool?
    var status: String
    var online: Bool
    var last_seen: Int64
    var ip: String?
    var os: String?
    var arch: String?
    var gpu: String?
    var version: String?
    var env: String
    var problems: [Problem]
    var ssh: String?
    var ssh_addr: String?
    var checked_at: Int64?

    var isThis: Bool { this ?? false }
    /// The short name shown first: the user's name for it, else the host.
    var title: String { (alias?.isEmpty == false ? alias! : name) }
    var subtitleHost: String? { alias?.isEmpty == false ? name : nil }
    var sshUsable: Bool { !isThis && (ssh == "ok" || ssh == "blocked") }
}

/// The CLI the app talks to. A Finder-started app has a minimal PATH, so the
/// usual install locations are tried first.
enum HearthCLI {
    static var binary: String? {
        if let p = ProcessInfo.processInfo.environment["HEARTH_CLI"], FileManager.default.isExecutableFile(atPath: p) {
            return p
        }
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let candidates = [home + "/.local/bin/hearth", "/usr/local/bin/hearth", "/opt/homebrew/bin/hearth", home + "/bin/hearth"]
        return candidates.first { FileManager.default.isExecutableFile(atPath: $0) }
    }

    enum Failure: LocalizedError {
        case notInstalled
        case tooOld
        case failed(String)
        var errorDescription: String? {
            switch self {
            case .notInstalled: return "没有找到 hearth 命令。先在终端安装并登录 Hearth。"
            case .tooOld: return "hearth 命令版本太旧。在终端运行 hearth self-update 后再刷新。"
            case .failed(let s): return s
            }
        }
    }

    /// Runs `hearth <args>` and returns its standard output.
    static func run(_ args: [String], timeout: TimeInterval = 20) async throws -> Data {
        guard let bin = binary else { throw Failure.notInstalled }
        return try await withCheckedThrowingContinuation { cont in
            DispatchQueue.global(qos: .userInitiated).async {
                let p = Process()
                p.executableURL = URL(fileURLWithPath: bin)
                p.arguments = args
                var env = ProcessInfo.processInfo.environment
                env["PATH"] = (env["PATH"] ?? "/usr/bin:/bin") + ":" + (bin as NSString).deletingLastPathComponent
                env["NO_COLOR"] = "1"
                p.environment = env
                let out = Pipe(), err = Pipe()
                p.standardOutput = out
                p.standardError = err
                do { try p.run() } catch { cont.resume(throwing: error); return }
                let deadline = DispatchTime.now() + timeout
                DispatchQueue.global().asyncAfter(deadline: deadline) { if p.isRunning { p.terminate() } }
                let data = out.fileHandleForReading.readDataToEndOfFile()
                let errData = err.fileHandleForReading.readDataToEndOfFile()
                p.waitUntilExit()
                if p.terminationStatus != 0 {
                    var msg = String(data: errData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                    if msg.hasPrefix("error: ") { msg = String(msg.dropFirst(7)) }
                    if msg.contains("unknown flag: --json") {
                        cont.resume(throwing: Failure.tooOld)
                    } else {
                        cont.resume(throwing: Failure.failed(msg.isEmpty ? "hearth 退出码 \(p.terminationStatus)" : msg))
                    }
                } else {
                    cont.resume(returning: data)
                }
            }
        }
    }
}

@MainActor
final class MachineStore: ObservableObject {
    @Published var list: MachineList?
    @Published var error: String?
    @Published var loading = false
    @Published var updated: Date?
    private var timer: Timer?
    /// The clock the 12-hour cutoff is measured against. Fixtures use the
    /// newest last_seen in the file so snapshots do not change with the date.
    private var now: () -> Date = Date.init

    /// Machines offline for longer than this are left out of the menu; the
    /// console still lists them.
    static let staleAfter: TimeInterval = 12 * 3600

    init(fixture: MachineList? = nil) {
        if let f = fixture {
            list = f
            updated = Date()
            let newest = f.machines.map(\.last_seen).max() ?? 0
            now = { Date(timeIntervalSince1970: TimeInterval(newest)) }
            return
        }
        Task { await refresh() }
        timer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in
            Task { await self?.refresh() }
        }
    }

    /// Machines shown in the menu: online ones, this machine, and machines
    /// seen within the last 12 hours.
    var machines: [Machine] {
        let cutoff = now().timeIntervalSince1970 - Self.staleAfter
        return (list?.machines ?? []).filter { m in
            m.online || m.isThis || TimeInterval(m.last_seen) >= cutoff
        }.sorted { a, b in
            if a.online != b.online { return a.online }
            if a.isThis != b.isThis { return a.isThis }
            return a.title.localizedStandardCompare(b.title) == .orderedAscending
        }
    }
    /// How many machines are left out for being offline too long.
    var hiddenCount: Int { (list?.machines.count ?? 0) - machines.count }
    var onlineCount: Int { machines.filter(\.online).count }
    var attentionCount: Int { machines.filter { $0.env == "error" || $0.env == "warn" }.count }
    var hasError: Bool { machines.contains { $0.env == "error" } }

    func refresh() async {
        if loading { return }
        loading = true
        defer { loading = false }
        do {
            let data = try await HearthCLI.run(["machine", "list", "--json"])
            list = try JSONDecoder().decode(MachineList.self, from: data)
            error = nil
            updated = Date()
        } catch {
            self.error = error.localizedDescription
        }
    }
}
