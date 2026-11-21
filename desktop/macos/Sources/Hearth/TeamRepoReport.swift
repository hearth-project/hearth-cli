import Foundation

/// What the last sync of a team's repository reported. The syncing machine
/// writes TeamRepoResult.Summary (internal/client/teamrepo.go):
/// "review not pushed: <why>; N published to the team, M proposed to the
/// repository; <note>; <note>…", or only an error when the sync failed.
struct RepoSyncReport: Equatable {
    var pushError: String?
    var failure: String?
    var published = 0
    var proposed = 0
    var notes: [String] = []

    init(_ detail: String, failed: Bool) {
        let s = detail.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let m = s.range(of: #"(\d+) published to the team, (\d+) proposed to the repository"#, options: .regularExpression) else {
            if !s.isEmpty { if failed { failure = s } else { notes = [s] } }
            return
        }
        let n = s[m].split { !$0.isNumber }.compactMap { Int($0) }
        if n.count == 2 { (published, proposed) = (n[0], n[1]) }
        var head = s[..<m.lowerBound].trimmingCharacters(in: .whitespaces)
        if head.hasSuffix(";") { head.removeLast() }
        let refused = "review not pushed:"
        if head.hasPrefix(refused) { head = head.dropFirst(refused.count).trimmingCharacters(in: .whitespaces) }
        if !head.isEmpty { pushError = head }
        var tail = s[m.upperBound...].trimmingCharacters(in: .whitespaces)
        if tail.hasPrefix(";") { tail = tail.dropFirst().trimmingCharacters(in: .whitespaces) }
        for part in tail.components(separatedBy: "; ") where !part.isEmpty {
            // "… was removed from the repository; still in the team (…)" is one note.
            if part.hasPrefix("still in the team"), let last = notes.popLast() {
                notes.append(last + "; " + part)
            } else {
                notes.append(part)
            }
        }
        if failed && pushError == nil { failure = s }
    }

    /// The repository's hooks want an issue id in the commit message, like
    /// icode's iCafe card check (HCK_F508).
    var needsCard: Bool {
        guard let e = pushError?.lowercased() else { return false }
        return e.contains("hck_f508") || e.contains("icafe") || e.contains("--commit-prefix")
    }

    /// The push error without the CLI's hint about --commit-prefix.
    var reason: String? {
        guard var e = pushError else { return nil }
        if let r = e.range(of: " (if the repository needs an issue id") { e = String(e[..<r.lowerBound]) }
        if e.hasSuffix("How to fix:") { e = String(e.dropLast("How to fix:".count)).trimmingCharacters(in: .whitespaces) }
        return e
    }

    /// A note in Chinese when it is one the CLI writes; warn marks the ones
    /// that left something out.
    static func note(_ n: String) -> (text: String, warn: Bool) {
        if let g = match(#"^(skill|agent) (\S+) not published: refusing to upload: (\d+) likely secret"#, n) {
            return ("\(g[0]) \(g[1]) 没有发布到团队：疑似含 \(g[2]) 处凭证。去掉仓库里的凭证后，下次同步会发布；不需要共享就把它加进「不同步」。", true)
        }
        if let g = match(#"^(skill|agent) (\S+) not published: (.+)$"#, n) {
            return ("\(g[0]) \(g[1]) 没有发布到团队：\(g[2])", true)
        }
        if let g = match(#"^(skill|agent) (\S+) (changed in both|differs from the repository): "#, n) {
            let why = g[2] == "changed in both" ? "两边都改过" : "和仓库里的不一致"
            return ("\(g[0]) \(g[1]) \(why)：保留团队的版本，并提交到仓库评审", false)
        }
        if let g = match(#"^(skill|agent) (\S+) was removed from the repository; still in the team \((.+)\)$"#, n) {
            return ("\(g[0]) \(g[1]) 已从仓库删除，团队里还保留着（要删除可运行 \(g[2])）", false)
        }
        if let g = match(#"^(skill|agent) (\S+): looks like it holds a credential"#, n) {
            return ("\(g[0]) \(g[1]) 疑似含凭证，没有提交到仓库", true)
        }
        if let g = match(#"^not synced \(excluded\): (.+)$"#, n) { return ("不同步：\(g[0])", false) }
        if let g = match(#"^(\d+) item\(s\) already proposed, waiting for the review$"#, n) {
            return ("\(g[0]) 项已在评审中，等待合入", false)
        }
        if let g = match(#"^skill (\S+) is in both (\S+) and (\S+): (\S+) is used$"#, n) {
            return ("skill \(g[0]) 同时在 \(g[1]) 和 \(g[2]) 里，使用 \(g[3])", false)
        }
        return (n, true)
    }

    private static func match(_ pattern: String, _ s: String) -> [String]? {
        guard let re = try? NSRegularExpression(pattern: pattern),
              let m = re.firstMatch(in: s, range: NSRange(s.startIndex..., in: s)) else { return nil }
        return (1..<m.numberOfRanges).map { i in Range(m.range(at: i), in: s).map { String(s[$0]) } ?? "" }
    }
}
