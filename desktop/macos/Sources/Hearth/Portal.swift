import Foundation

// What `hearth app data --json` prints (cmd/hearth/app.go, internal/client/portal.go):
// the account's overview, library and teams, the same documents the web
// console shows.

struct PortalReply: Decodable {
    var docs: Docs
    var errors: [String: String]?

    struct Docs: Decodable {
        var me: Me?
        var library: Library?
        var teams: [Team]?
        var devices: [Device]?
        var settings: MySettings?
        var intents: [Intent]?
    }
}

/// One of the account's machines as the server sees it (/v1/me/devices).
struct Device: Decodable, Identifiable, Hashable {
    var id: String
    var name: String
    var alias: String?
    var status: String?
    var created_at: Int64?
    var last_seen: Int64?
    var version: String?
    var ip: String?
    var online: Bool?
    var sessions: Int?
    var last_active: Int64?
}

struct LibLink: Decodable, Hashable, Identifiable {
    var type: String
    var name: String
    var version: Int?
    var auto: Bool?
    var source: String?
    var id: String { type + "/" + name }
}

struct MySettings: Decodable {
    var links: [LibLink]?
    var resident: [String]?
    var share_exclude: [String]?
    var share_top: Int?
}

/// A change waiting for the user's machines to apply it.
struct Intent: Decodable, Identifiable, Hashable {
    var id: Int64
    var kind: String
    var data: JSONValue?
    var created_at: Int64?
}

/// Any JSON value, kept for display.
enum JSONValue: Decodable, Hashable {
    case string(String), number(Double), bool(Bool), array([JSONValue]), object([String: JSONValue]), null
    init(from d: Decoder) throws {
        let c = try d.singleValueContainer()
        if c.decodeNil() { self = .null }
        else if let b = try? c.decode(Bool.self) { self = .bool(b) }
        else if let n = try? c.decode(Double.self) { self = .number(n) }
        else if let s = try? c.decode(String.self) { self = .string(s) }
        else if let a = try? c.decode([JSONValue].self) { self = .array(a) }
        else { self = .object(try c.decode([String: JSONValue].self)) }
    }
    var text: String {
        switch self {
        case .string(let s): return s
        case .number(let n): return n == n.rounded() ? String(Int64(n)) : String(n)
        case .bool(let b): return b ? "是" : "否"
        case .array(let a): return a.isEmpty ? "全部" : a.map(\.text).joined(separator: ", ")
        case .object(let o): return o.keys.sorted().map { "\($0): \(o[$0]!.text)" }.joined(separator: " · ")
        case .null: return ""
        }
    }
    subscript(_ k: String) -> JSONValue? { if case .object(let o) = self { return o[k] }; return nil }
}

struct NameCount: Decodable, Hashable { var name: String; var count: Int }

struct DaySeries: Decodable, Hashable { var day: String; var bytes: Int64 }

struct Account: Decodable {
    var sessions: Int?
    var devices: Int?
    var quota_bytes: Int64?
    var session_bytes: Int64?
    var library_bytes: Int64?
    var secret_bytes: Int64?
}

struct FeedItem: Decodable, Identifiable, Hashable {
    var team: String
    var type: String
    var name: String
    var version: Int
    var published_by: String
    var published_at: Int64
    var description: String?
    var id: String { "\(team)/\(type)/\(name)/\(version)" }
}

struct Me: Decodable {
    var email: String
    var admin: Bool
    var account: Account
    var used_bytes: Int64
    var series: [DaySeries]
    var top_skills: [NameCount]
    var top_agents: [NameCount]
    var feed: [FeedItem]
    var pending_changes: Int?
}

struct LibraryItem: Decodable, Identifiable, Hashable {
    var type: String
    var name: String
    var description: String
    var resident: Bool?
    var size: Int64
    var uses: Int64
    var id: String { type + "/" + name }
}

struct Library: Decodable {
    var version: Int64
    var items: [LibraryItem]
}

struct TeamMember: Decodable, Hashable { var email: String; var role: String }

struct TeamPackage: Decodable, Identifiable, Hashable {
    var type: String
    var name: String
    var description: String?
    var version: Int
    var published_by: String
    var published_at: Int64
    var uses: Int64?
    var id: String { type + "/" + name }
}

struct Team: Decodable, Identifiable, Hashable {
    var name: String
    var role: String
    var members: [TeamMember]
    var packages: [TeamPackage]
    var shared_with: [String]?
    var shared_from: [String]?
    var via: String?
    var id: String { name }
    var canPublish: Bool { role == "owner" || role == "maintainer" }
}

@MainActor
final class PortalStore: ObservableObject {
    @Published var me: Me?
    @Published var library: Library?
    @Published var teams: [Team] = []
    @Published var devices: [Device] = []
    @Published var settings: MySettings?
    @Published var intents: [Intent] = []
    /// Last action result or failure, shown once as a toast on the topmost layer.
    /// Set `noticeIsError` before `notice`.
    @Published var notice: String? {
        didSet { if let n = notice { Toasts.shared.show(n, error: noticeIsError) } else { Toasts.shared.hide() } }
    }
    @Published var noticeIsError = false
    /// Resident skills just chosen here, shown until the server reports them.
    @Published var pendingResident: [String]?
    @Published var error: String?
    @Published var loading = false
    @Published var loginExpired = false
    @Published var updated: Date?

    init() {}

    /// Sample data for snapshots.
    init(fixture r: PortalReply) {
        me = r.docs.me
        library = r.docs.library
        teams = r.docs.teams ?? []
        devices = r.docs.devices ?? []
        settings = r.docs.settings
        intents = r.docs.intents ?? []
        updated = Date()
    }

    func refresh() async {
        if loading { return }
        loading = true
        defer { loading = false }
        do {
            let data = try await HearthCLI.run(["app", "data", "--json"], timeout: 40)
            let r = try JSONDecoder().decode(PortalReply.self, from: data)
            me = r.docs.me
            library = r.docs.library
            teams = r.docs.teams ?? []
            devices = r.docs.devices ?? []
            settings = r.docs.settings
            intents = r.docs.intents ?? []
            if let p = pendingResident, Set(p) == Set(settings?.resident ?? []) { pendingResident = nil }
            error = r.errors?.isEmpty == false ? r.errors!.values.sorted().joined(separator: "；") : nil
            loginExpired = false
            updated = Date()
        } catch {
            if case HearthCLI.Failure.loginExpired = error {
                loginExpired = true
                me = nil; library = nil; teams = []
                self.error = nil
            } else {
                self.error = error.localizedDescription
            }
        }
    }

    func device(_ m: Machine) -> Device? { devices.first { $0.id == m.id } }

    /// One request to the account's console API (`hearth app call`).
    /// Returns the reply body, or nil after showing the error.
    @discardableResult
    func call(_ method: String, _ path: String, _ body: [String: Any]? = nil, done: String? = nil, reload: Bool = true) async -> Data? {
        var args = ["app", "call", method, path]
        if let body, let d = try? JSONSerialization.data(withJSONObject: body), let s = String(data: d, encoding: .utf8) {
            args.append(s)
        }
        do {
            let out = try await HearthCLI.run(args, timeout: 40)
            if let done { noticeIsError = false; notice = done }
            if reload { loading = false; await refresh() }
            return out
        } catch {
            noticeIsError = true
            notice = error.localizedDescription
            return nil
        }
    }

    /// Queue a change for the user's machines (resident, share-top, link, unlink).
    /// The switch flips at once; the page refreshes in the background.
    func queue(_ kind: String, _ data: [String: Any]) async {
        if kind == "resident" { pendingResident = data["names"] as? [String] }
        let ok = await call("POST", "/v1/me/intents", ["kind": kind, "data": data],
                            done: "已保存，你的机器会在一分钟左右内应用。", reload: false) != nil
        if !ok, kind == "resident" { pendingResident = nil }
        if ok { Task { await refresh() } }
    }

    /// Skills currently loaded into every agent. Empty setting means all.
    var residentNames: [String] {
        let r = pendingResident ?? settings?.resident ?? []
        if !r.isEmpty { return r }
        return (library?.items ?? []).filter { $0.type == "skill" }.map(\.name)
    }
}

enum Fmt {
    static func bytes(_ n: Int64) -> String {
        ByteCountFormatter.string(fromByteCount: n, countStyle: .file)
    }
    static func ago(_ unix: Int64) -> String {
        let s = Int(Date().timeIntervalSince1970) - Int(unix)
        if s < 90 { return "刚刚" }
        if s < 3600 { return "\(s / 60) 分钟前" }
        if s < 86400 { return "\(s / 3600) 小时前" }
        return "\(s / 86400) 天前"
    }
}
