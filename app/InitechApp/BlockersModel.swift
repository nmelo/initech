import Foundation

/// RigBlockers is one rig's row of GET /v1/blockers: what waits on Nelson
/// and which panes are stuck. quiet is true when the rig has neither.
struct RigBlockers: Codable, Equatable, Sendable {
    var rig: String
    var waiting: [WaitingItem]
    var stuck: [StuckPane]
    var quiet: Bool
}

/// WaitingItem is one ▶ line of a super's footer. recommendation is "" when
/// the reply that carried it gives none.
struct WaitingItem: Codable, Equatable, Hashable, Sendable {
    var pane: String
    var text: String
    var recommendation: String
}

/// StuckPane is a pane that cannot act, with the reason in plain words.
struct StuckPane: Codable, Equatable, Hashable, Sendable {
    var pane: String
    var reason: String
}

/// BlockersSource is the part of the service the blockers view reads.
protocol BlockersSource: Sendable {
    func blockers() async throws -> [RigBlockers]
}

extension HTTPService: BlockersSource {
    func blockers() async throws -> [RigBlockers] { try await get("/v1/blockers") }
}

/// BlockerRow is one rendered row of the blockers list.
struct BlockerRow: Identifiable, Equatable, Sendable {
    enum Kind: Equatable, Sendable { case waiting, stuck, quiet }
    var kind: Kind
    var rig: String
    /// pane is the pane a tap opens; nil for a quiet row.
    var pane: String?
    var title: String
    /// detail is the recommendation of a waiting item, or the reason of a
    /// stuck pane; nil when there is none.
    var detail: String?

    var id: String { "\(rig)/\(kind)/\(pane ?? "")/\(title)" }
}

/// BlockerSection is one rig in the blockers list.
struct BlockerSection: Identifiable, Equatable, Sendable {
    var rig: String
    var rows: [BlockerRow]
    var id: String { rig }
}

/// BlockersModel holds the blockers list, reloaded from the service on every
/// feed event that can change it.
@MainActor
final class BlockersModel: ObservableObject {
    @Published private(set) var sections: [BlockerSection] = []
    /// loaded is false until the first reload answers.
    @Published private(set) var loaded = false
    @Published private(set) var problem: String?

    private let source: BlockersSource

    init(source: BlockersSource) { self.source = source }

    func reload() async {
        do {
            sections = Self.sections(try await source.blockers())
            problem = nil
        } catch {
            problem = "Blockers could not be read: \(error)"
        }
        loaded = true
    }

    /// apply reloads on the events that can change blockers: a pane state, a
    /// waiting item, a stuck pane, a reset, or a reply in a super pane. That
    /// reply is the only event when a later footer drops a line, and the
    /// service decides from it what still waits. Other transcript lines do
    /// not reload.
    func apply(_ e: FeedEvent) async {
        switch e {
        case .paneState, .waitingItem, .stuck, .reset:
            await reload()
        case let .transcript(_, pane, message):
            if pane == "super", message.kind == .agent { await reload() }
        }
    }

    /// sections renders rigs: waiting items first, then stuck panes; a quiet
    /// rig is one quiet row.
    nonisolated static func sections(_ rigs: [RigBlockers]) -> [BlockerSection] {
        rigs.map { r in
            var rows: [BlockerRow] = r.waiting.map {
                BlockerRow(kind: .waiting, rig: r.rig, pane: $0.pane, title: $0.text,
                           detail: $0.recommendation.isEmpty ? nil : $0.recommendation)
            }
            rows += r.stuck.map {
                BlockerRow(kind: .stuck, rig: r.rig, pane: $0.pane, title: $0.pane, detail: $0.reason)
            }
            if rows.isEmpty {
                rows = [BlockerRow(kind: .quiet, rig: r.rig, pane: nil, title: "Nothing waiting, nothing stuck", detail: nil)]
            }
            return BlockerSection(rig: r.rig, rows: rows)
        }
    }
}
