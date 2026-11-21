import SwiftUI

/// A team's bound git repository on the Teams page: which repository and
/// branch, who syncs it, how the last sync went, and what to do when the
/// repository refused the review.
struct TeamRepoRow: View {
    @ObservedObject var portal: PortalStore
    var team: Team
    var repo: TeamRepo
    /// Opens the binding sheet at the commit prefix (owners only).
    var setPrefix: () -> Void
    @State private var showDetail = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var detail: String { (repo.detail ?? "").trimmingCharacters(in: .whitespacesAndNewlines) }
    private var failed: Bool { repo.status == "failed" }
    private var waiting: Bool { (repo.requested_at ?? 0) > 0 }
    private var report: RepoSyncReport? { detail.isEmpty ? nil : RepoSyncReport(detail, failed: failed) }
    private var machine: String { repo.device_name.flatMap { $0.isEmpty ? nil : $0 } ?? repo.device }

    var body: some View {
        // Everything after the icon shares one left edge: the name, the
        // status, the failure, the notes and the raw report.
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "arrow.triangle.2.circlepath").font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.blue)
                .frame(width: 26, height: 26)
                .background(Theme.blue.opacity(0.12), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
            VStack(alignment: .leading, spacing: 8) {
                header
                status
                if failed, let r = report { callout(r) }
                if let r = report, !r.notes.isEmpty { notes(r) }
                if showDetail { raw }
            }
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(Color.primary.opacity(0.035)))
        .animation(Theme.motion(Theme.spring, reduce: reduceMotion), value: showDetail)
    }

    private var header: some View {
        HStack(alignment: .top, spacing: 10) {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(Self.shortName(repo.url)).font(.system(size: 13, weight: .semibold)).lineLimit(1)
                    Badge(text: (repo.ref ?? "").isEmpty ? "默认分支" : repo.ref!, color: .secondary)
                    Badge(text: Self.pushMode(repo.push), color: .secondary)
                    if let p = repo.commit_prefix, !p.isEmpty { Badge(text: "前缀 \(p)", color: .secondary) }
                }
                Text(repo.url).font(.system(size: 11, design: .monospaced)).foregroundStyle(.tertiary)
                    .lineLimit(1).truncationMode(.middle).textSelection(.enabled)
            }
            Spacer(minLength: 8)
            if !detail.isEmpty {
                Button(showDetail ? "收起详情" : "详情") { showDetail.toggle() }.controlSize(.small)
            }
            if team.canPublish {
                Button {
                    Task { await portal.call("POST", "/v1/me/teams/\(adminPath(team.name))/repo/sync", [:], done: "已通知 \(machine) 立即同步，几分钟内完成") }
                } label: { Label("立即同步", systemImage: "arrow.clockwise") }
                    .controlSize(.small).disabled(waiting)
            }
        }
    }

    private var status: some View {
        HStack(spacing: 6) {
            Circle().fill(statusColor).frame(width: 7, height: 7).frame(width: 12, alignment: .leading)
            Text(statusText).font(.system(size: 11.5)).foregroundStyle(.secondary).lineLimit(1)
            if let v = repo.review, v.hasPrefix("http"), let u = URL(string: v) {
                Link(destination: u) { Label("查看评审", systemImage: "arrow.up.right.square") }.font(.system(size: 11.5))
            }
        }
    }

    private var statusColor: Color {
        if waiting { return Theme.blue }
        guard (repo.synced_at ?? 0) > 0 else { return Theme.idle }
        return failed ? Theme.bad : Theme.ok
    }

    private var statusText: String {
        if waiting { return "已请求同步，等待 \(machine) 执行…" }
        guard let at = repo.synced_at, at > 0 else { return "由 \(machine) 同步 · 还没同步过" }
        var s = "由 \(machine) 同步 · \(Fmt.ago(at))同步\(failed ? "失败" : "成功")"
        if let r = report, r.failure == nil { s += " · 发布 \(r.published) 项，提交评审 \(r.proposed) 项" }
        return s
    }

    private func callout(_ r: RepoSyncReport) -> some View {
        let prefix = repo.commit_prefix ?? ""
        let owner = team.role == "owner"
        let title: String
        let text: String
        if r.needsCard {
            title = prefix.isEmpty ? "仓库要求提交信息带 iCafe 卡片号" : "提交前缀「\(prefix)」不是可用的 iCafe 卡片号"
            text = "团队里的改动以评审提交回仓库时，被仓库的提交检查拒绝了。把仓库绑定的「提交前缀」设为一个存在的卡片号（如 proj-123），下次同步会带上它。"
                + (owner ? "" : "只有团队所有者\(repo.owner.map { " \($0) " } ?? "")能修改仓库绑定。")
        } else {
            title = r.pushError != nil ? "评审没能推送到仓库" : "同步失败"
            text = r.reason ?? r.failure ?? detail
        }
        return HStack(alignment: .top, spacing: 8) {
            Image(systemName: "exclamationmark.triangle.fill").font(.system(size: 12)).foregroundStyle(Theme.bad).padding(.top, 1)
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.system(size: 12, weight: .semibold))
                Text(text).font(.system(size: 11)).foregroundStyle(.secondary)
                    .lineLimit(r.needsCard ? nil : 3).fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 8)
            if r.needsCard && owner {
                Button(prefix.isEmpty ? "设置提交前缀…" : "修改提交前缀…", action: setPrefix).controlSize(.small)
            }
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 8, style: .continuous).fill(Theme.bad.opacity(0.08)))
        .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Theme.bad.opacity(0.18)))
    }

    private func notes(_ r: RepoSyncReport) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            ForEach(Array(r.notes.enumerated()), id: \.offset) { _, n in
                let note = RepoSyncReport.note(n)
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Image(systemName: note.warn ? "exclamationmark.circle" : "info.circle")
                        .foregroundStyle(note.warn ? Theme.warn : Color.secondary).frame(width: 12, alignment: .leading)
                    Text(note.text).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
                .font(.system(size: 11))
            }
        }
    }

    private var raw: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text("同步机器报告的原文").font(.system(size: 11, weight: .semibold)).foregroundStyle(.secondary)
                Spacer()
                Button {
                    Actions.copy(detail)
                    portal.noticeIsError = false
                    portal.notice = "已复制"
                } label: { Label("复制", systemImage: "doc.on.doc") }.controlSize(.small)
            }
            Text(detail).font(.system(size: 10.5, design: .monospaced)).textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading).fixedSize(horizontal: false, vertical: true)
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 8, style: .continuous).fill(Color.primary.opacity(0.04)))
        .transition(.opacity)
    }

    /// "group/repo" from ssh://host:port/group/repo, git@host:group/repo.git or https URLs.
    static func shortName(_ url: String) -> String {
        var s = url.trimmingCharacters(in: .whitespaces)
        while s.hasSuffix("/") { s.removeLast() }
        if s.hasSuffix(".git") { s.removeLast(4) }
        let parts = s.split { $0 == "/" || $0 == ":" }.map(String.init)
        return parts.count >= 2 ? parts.suffix(2).joined(separator: "/") : s
    }

    static func pushMode(_ p: String?) -> String {
        switch p ?? "cr" {
        case "branch": return "推到分支"
        case "off": return "不回推"
        default: return "提评审"
        }
    }
}
