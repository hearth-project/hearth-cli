import Foundation

// What `hearth app admin-data --json` prints (cmd/hearth/app.go,
// internal/client/portal.go): the administration pages' documents. Actions go
// through `hearth app admin-call METHOD PATH [BODY]`. Only admin accounts can
// read or call any of it; the server enforces that.

struct AdminReply: Decodable {
    var docs: Docs
    var errors: [String: String]?

    struct Docs: Decodable {
        var overview: AdminOverview?
        var users: [AdminUser]?
        var invites: [AdminInvite]?
        var teams: [AdminTeam]?
        var settings: AdminSettings?
        var audit: [AuditEntry]?
    }
}

struct AdminStats: Decodable {
    var Users: Int?
    var DisabledUsers: Int?
    var ActiveUsers30d: Int?
    var Devices: Int?
    var Sessions: Int?
    var ExpiredSessions: Int?
    var SessionBytes: Int64?
    var LibraryBytes: Int64?
    var Teams: Int?
    var Packages: Int?
    var PackageBytes: Int64?
    var PendingDeletes: Int?
}

struct AdminSeries: Decodable, Hashable {
    var day: String
    var active_users: Int?
    var uploads: Int?
    var bytes: Int64?
    var signups: Int?
}

struct AdminRank: Decodable, Hashable {
    var name: String
    var count: Int
    var users: Int?
}

struct AdminVersion: Decodable, Hashable {
    var name: String
    var count: Int
}

struct AdminDevice: Decodable, Hashable, Identifiable {
    var name: String
    var ip: String?
    var email: String?
    var version: String?
    var last_seen: Int64?
    var online: Bool?
    var id: String { (email ?? "") + "/" + name }
}

struct AdminOverview: Decodable {
    var stats: AdminStats
    var pending_users: Int?
    var online_devices: Int?
    var served_version: String?
    var started_at: Int64?
    var series: [AdminSeries]?
    var top_skills: [AdminRank]?
    var top_agents: [AdminRank]?
    var client_versions: [AdminVersion]?
    var team_skills: Int?
    var team_agents: Int?
}

struct AdminUser: Decodable, Hashable, Identifiable {
    var email: String
    var status: String
    var created_at: String?
    var last_seen: String?
    var devices: Int?
    var sessions: Int?
    var quota_bytes: Int64?
    var quota_source: String?
    var used_bytes: Int64?
    var admin: Bool?
    var online: [AdminDevice]?
    var id: String { email }

    static func == (a: AdminUser, b: AdminUser) -> Bool { a.email == b.email && a.status == b.status && a.admin == b.admin && a.quota_bytes == b.quota_bytes }
    func hash(into h: inout Hasher) { h.combine(email) }
}

struct AdminInvite: Decodable, Hashable, Identifiable {
    var id: String
    var created_at: String?
    var expires_at: String?
    var used_by: String?
}

struct AdminTeam: Decodable, Hashable, Identifiable {
    var name: String
    var created_at: Int64?
    var members: Int?
    var owners: [String]?
    var skills: Int?
    var agents: Int?
    var bytes: Int64?
    var id: String { name }
}

struct AdminSettings: Decodable, Equatable {
    var registration: String
    var default_quota: Int64
    var client_auto_update: Bool
}

struct AuditEntry: Decodable, Hashable, Identifiable {
    var at: Int64
    var actor: String?
    var action: String
    var target: String?
    var detail: String?
    var id: String { "\(at)/\(actor ?? "")/\(action)/\(target ?? "")/\(detail ?? "")" }
}

struct GCResult: Decodable {
    var expired: Int?
    var deleted: Int?
    var orphans: Int?
    var orphan_bytes: Int64?
    var orphans_deleted: Int?
}

struct InviteCode: Decodable { var code: String }

struct TeamDetail: Decodable {
    var members: [TeamMember]
}

@MainActor
final class AdminStore: ObservableObject {
    @Published var overview: AdminOverview?
    @Published var users: [AdminUser] = []
    @Published var invites: [AdminInvite] = []
    @Published var teams: [AdminTeam] = []
    @Published var settings: AdminSettings?
    @Published var audit: [AuditEntry] = []
    @Published var error: String?
    @Published var loading = false
    @Published var updated: Date?
    /// Last action result or failure, shown as a banner.
    @Published var notice: String?
    @Published var noticeIsError = false

    init() {}

    init(fixture r: AdminReply) {
        apply(r)
        updated = Date()
    }

    private func apply(_ r: AdminReply) {
        overview = r.docs.overview
        users = r.docs.users ?? []
        invites = r.docs.invites ?? []
        teams = r.docs.teams ?? []
        settings = r.docs.settings
        audit = r.docs.audit ?? []
    }

    func refresh() async {
        if loading { return }
        loading = true
        defer { loading = false }
        do {
            let data = try await HearthCLI.run(["app", "admin-data", "--json"], timeout: 40)
            let r = try JSONDecoder().decode(AdminReply.self, from: data)
            apply(r)
            error = r.errors?.isEmpty == false ? r.errors!.values.sorted().joined(separator: "；") : nil
            updated = Date()
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// One admin request. Returns the reply body, or nil after showing the error.
    @discardableResult
    func call(_ method: String, _ path: String, _ body: [String: Any]? = nil, done: String? = nil, reload: Bool = true) async -> Data? {
        var args = ["app", "admin-call", method, path]
        if let body, let d = try? JSONSerialization.data(withJSONObject: body), let s = String(data: d, encoding: .utf8) {
            args.append(s)
        }
        do {
            let out = try await HearthCLI.run(args, timeout: 40)
            if let done { notice = done; noticeIsError = false }
            if reload { await refreshQuietly() }
            return out
        } catch {
            notice = error.localizedDescription
            noticeIsError = true
            return nil
        }
    }

    private func refreshQuietly() async {
        loading = false
        await refresh()
    }
}

enum AdminFmt {
    /// "2026-11-05T17:52:53+08:00" -> unix seconds.
    static func unix(_ iso: String?) -> Int64? {
        guard let iso, let d = ISO8601DateFormatter().date(from: iso) else { return nil }
        return Int64(d.timeIntervalSince1970)
    }
    static func ago(_ iso: String?) -> String {
        guard let u = unix(iso) else { return "从未" }
        return Fmt.ago(u)
    }
    static func day(_ iso: String?) -> String {
        guard let u = unix(iso) else { return "" }
        return Date(timeIntervalSince1970: TimeInterval(u)).formatted(date: .abbreviated, time: .omitted)
    }
    static func when(_ unix: Int64) -> String {
        Date(timeIntervalSince1970: TimeInterval(unix)).formatted(date: .abbreviated, time: .standard)
    }
    static func quota(_ b: Int64?) -> String {
        guard let b else { return "—" }
        return b == 0 ? "不限" : Fmt.bytes(b)
    }
}
