import Foundation
import InitechCore

/// ProgressReport is one effort's report, exactly the object initech
/// progress --json prints. Times are decoded as text and never rendered:
/// the progress view shows pace and what is stuck, never a time.
struct ProgressReport: Codable, Equatable, Sendable {
    var rig: String
    var prefix: String
    var hours: Int
    var beads: [Row]
    var closed: Int
    var total: Int
    var workedByEngineers: Int
    var writingReviewOnly: Int
    var closedRecent: [Closed]
    var criticalPath: [PathBead]
    var engineers: [String]
    var quiet: [QuietRow]
    /// stuckAfter is the rig's value in effect, such as "20m".
    var stuckAfter: String
    var stuckIds: [String]
    var waiting: [QuietRow]

    struct Row: Codable, Equatable, Sendable {
        var id: String
        var stage: String
        var owner: String
        var ac: String
        var acProven: Int?
        var acItems: Int?
        var commits: Int?
        var title: String
    }
    struct Closed: Codable, Equatable, Sendable {
        var id: String
        var title: String
    }
    struct PathBead: Codable, Equatable, Sendable {
        var id: String
        var waiting: Int
        var stage: String
        var title: String
    }
    struct QuietRow: Codable, Equatable, Sendable {
        var id: String
        var owner: String
        var wait: String
    }

    // The service sends null for an empty list; read it as empty.
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        rig = try c.decode(String.self, forKey: .rig)
        prefix = try c.decodeIfPresent(String.self, forKey: .prefix) ?? ""
        hours = try c.decodeIfPresent(Int.self, forKey: .hours) ?? 0
        beads = try c.decodeIfPresent([Row].self, forKey: .beads) ?? []
        closed = try c.decodeIfPresent(Int.self, forKey: .closed) ?? 0
        total = try c.decodeIfPresent(Int.self, forKey: .total) ?? 0
        workedByEngineers = try c.decodeIfPresent(Int.self, forKey: .workedByEngineers) ?? 0
        writingReviewOnly = try c.decodeIfPresent(Int.self, forKey: .writingReviewOnly) ?? 0
        closedRecent = try c.decodeIfPresent([Closed].self, forKey: .closedRecent) ?? []
        criticalPath = try c.decodeIfPresent([PathBead].self, forKey: .criticalPath) ?? []
        engineers = try c.decodeIfPresent([String].self, forKey: .engineers) ?? []
        quiet = try c.decodeIfPresent([QuietRow].self, forKey: .quiet) ?? []
        stuckAfter = try c.decodeIfPresent(String.self, forKey: .stuckAfter) ?? "1h"
        stuckIds = try c.decodeIfPresent([String].self, forKey: .stuckIds) ?? []
        waiting = try c.decodeIfPresent([QuietRow].self, forKey: .waiting) ?? []
    }
}

/// ProgressSource is the part of the service the progress view reads.
protocol ProgressSource: Sendable {
    func progress() async throws -> [ProgressReport]
}

extension HTTPService: ProgressSource {
    func progress() async throws -> [ProgressReport] { try await get("/v1/progress") }
}

/// ProgressCard is one effort as the progress view renders it: only text and
/// counts, never a time, so no finish time, estimate or actual can appear.
struct ProgressCard: Identifiable, Equatable, Sendable {
    struct Bead: Identifiable, Equatable, Sendable {
        var id: String
        var title: String
        /// line is "<stage> · <owner> · AC <proven>/<items> · <n> landed".
        var line: String
        /// stage is the bead's raw stage, for its icon, words and order (BeadStage).
        var stage = ""
        /// owner is who has it; empty when nobody.
        var owner = ""
        /// proof is "2 of 4 proven" when the bead has an acceptance list.
        var proof: String?
        /// landed is how many commits have landed for it.
        var landed = 0
        /// stuck, quiet, waiting and critical are the report's flags for this bead;
        /// quiet and waiting carry the wait as the service words it ("2h").
        var stuck = false
        var quiet: String?
        var waiting: String?
        var critical = false

        /// meta is the dim line under the title: id, stage in plain words, owner, proof, commits.
        var meta: String {
            var parts = [id, BeadStage.words(stage), owner.isEmpty ? "no owner" : owner]
            if let proof { parts.append(proof) }
            if landed > 0 { parts.append("\(landed) landed") }
            return parts.joined(separator: " · ")
        }
    }
    /// Done is a bead closed within the report's window.
    struct Done: Identifiable, Equatable, Sendable {
        var id: String
        var title: String
    }
    var rig: String
    var effort: String
    /// closed and total are the effort's beads, for the bar beside the pace.
    var closed: Int
    var total: Int
    /// pace is "<closed> of <total> closed · <n> in the last <h> h".
    var pace: String
    /// worked is "worked by engineers <n> · writing or review only <m>".
    var worked: String
    var beads: [Bead]
    var criticalPath: [String]
    var engineers: String
    var quiet: [String]
    /// quietLabel names the rig's value: "Quiet 20m or more".
    var quietLabel: String
    /// stuck lists the stuck beads; empty when none.
    var stuck: [String]
    var waitingLabel: String
    /// waiting lists the beads in waiting past the value, with the wait text.
    var waiting: [String]

    /// closedRecent is the beads closed within the last `hours` hours.
    var closedRecent: [Done] = []
    var hours = 0
    /// workedCount and engineerNames are the numbers behind `worked` and `engineers`.
    var workedCount = 0
    var engineerNames: [String] = []
    /// offRows are the flags and critical-path beads that are not among `beads`: shown as lines, so none is lost.
    var offRows: [String] = []

    var id: String { "\(rig)/\(effort)" }
    /// single is an effort of one bead or none: its pace line says nothing its bead does not.
    var single: Bool { total <= 1 }
    /// sorted is the beads in reading order: stuck first, then by stage, then as the report had them.
    var sorted: [Bead] {
        beads.enumerated().sorted {
            let (a, b) = ($0.element, $1.element)
            if a.stuck != b.stuck { return a.stuck }
            let (ra, rb) = (BeadStage.rank(a.stage), BeadStage.rank(b.stage))
            return ra != rb ? ra < rb : $0.offset < $1.offset
        }.map(\.element)
    }

    init(_ r: ProgressReport) {
        rig = r.rig
        effort = r.prefix.isEmpty ? "whole rig" : r.prefix
        closed = r.closed
        total = r.total
        pace = "\(r.closed) of \(r.total) closed · \(r.closedRecent.count) in the last \(r.hours) h"
        worked = "worked by engineers \(r.workedByEngineers) · writing or review only \(r.writingReviewOnly)"
        beads = r.beads.map { b in
            // The command prints "-" for a bead with no owner.
            var parts = [b.stage, b.owner.isEmpty || b.owner == "-" ? "no owner" : b.owner]
            if let p = b.acProven, let n = b.acItems { parts.append("AC \(p)/\(n)") } else { parts.append("no AC list") }
            if let c = b.commits { parts.append("\(c) landed") }
            var bead = Bead(id: b.id, title: b.title, line: parts.joined(separator: " · "))
            bead.stage = b.stage
            bead.owner = b.owner == "-" ? "" : b.owner
            if let p = b.acProven, let n = b.acItems { bead.proof = "\(p) of \(n) proven" }
            bead.landed = b.commits ?? 0
            bead.stuck = r.stuckIds.contains(b.id)
            bead.quiet = r.quiet.first { $0.id == b.id }?.wait
            bead.waiting = r.waiting.first { $0.id == b.id }?.wait
            bead.critical = r.criticalPath.contains { $0.id == b.id }
            return bead
        }
        let shown = Set(r.beads.map(\.id))
        closedRecent = r.closedRecent.map { Done(id: $0.id, title: $0.title) }
        hours = r.hours
        workedCount = r.workedByEngineers + r.writingReviewOnly
        engineerNames = r.engineers
        offRows = r.criticalPath.filter { !shown.contains($0.id) }
                .map { "critical path: \(BeadStage.icon($0.stage)) \($0.id) \($0.title)" }
            + r.stuckIds.filter { !shown.contains($0) }.map { "stuck: \($0)" }
            + r.quiet.filter { !shown.contains($0.id) }.map { "quiet \($0.wait): \($0.id) \($0.owner)" }
            + r.waiting.filter { !shown.contains($0.id) }.map { "waiting \($0.wait): \($0.id) \($0.owner)" }
        criticalPath = r.criticalPath.map { "\($0.id) (\($0.waiting) wait, \($0.stage)) \($0.title)" }
        engineers = r.engineers.isEmpty ? "no engineers on it" : "\(r.engineers.count) on it: \(r.engineers.joined(separator: " "))"
        quiet = r.quiet.map { "\($0.id) \($0.owner): \($0.wait)" }
        quietLabel = "Quiet \(r.stuckAfter) or more"
        stuck = r.stuckIds
        waitingLabel = "Waiting \(r.stuckAfter) or more"
        waiting = r.waiting.map { "\($0.id) \($0.owner): \($0.wait)" }
    }
}

/// ProgressModel holds the progress cards, reloaded on open and on reset.
@MainActor
final class ProgressModel: ObservableObject {
    @Published private(set) var cards: [ProgressCard] = []
    @Published private(set) var loaded = false
    @Published private(set) var problem: String?

    private let source: ProgressSource

    init(source: ProgressSource) { self.source = source }

    func reload() async {
        do {
            cards = try await source.progress().map(ProgressCard.init)
            problem = nil
        } catch is CancellationError {
            return // not a failure, and not a read: the cards stay as they were
        } catch {
            problem = "Progress could not be read: \(error)"
            Log.shown(error, screen: "progress")
        }
        loaded = true
    }

    /// apply reloads on a reset; progress is rerun on the Mac every five
    /// minutes, so a pane event alone does not change it.
    func apply(_ e: FeedEvent) async {
        if case .reset = e { await reload() }
    }
}
