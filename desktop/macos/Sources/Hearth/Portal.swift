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
    }
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
    var id: String { name }
}

@MainActor
final class PortalStore: ObservableObject {
    @Published var me: Me?
    @Published var library: Library?
    @Published var teams: [Team] = []
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
