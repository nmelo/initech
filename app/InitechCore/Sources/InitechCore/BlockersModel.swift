import Combine
import Foundation

/// RigBlockers is one rig's row of GET /v1/blockers: what waits on Nelson
/// and which panes are stuck. quiet is true when the rig has neither.
public struct RigBlockers: Codable, Equatable, Sendable {
    public var rig: String
    public var waiting: [WaitingItem]
    public var stuck: [StuckPane]
    public var quiet: Bool
}

/// WaitingItem is one item waiting on Nelson. recommendation is "" when there
/// is none. bead is the bead waiting on him, when the item is one; pane is
/// then its assignee ("" when it has none) and text its title.
public struct WaitingItem: Codable, Equatable, Hashable, Sendable {
    public var pane: String
    public var text: String
    public var recommendation: String
    public var bead: String?
}

/// StuckPane is a pane that cannot act, with the reason in plain words.
public struct StuckPane: Codable, Equatable, Hashable, Sendable {
    public var pane: String
    public var reason: String
}

/// BlockersSource is the part of the service the blockers view reads.
public protocol BlockersSource: Sendable {
    func blockers() async throws -> [RigBlockers]
}

extension HTTPService: BlockersSource {
    public func blockers() async throws -> [RigBlockers] { try await get("/v1/blockers") }
}

/// BlockerRow is one rendered row of the blockers list.
public struct BlockerRow: Identifiable, Equatable, Sendable {
    public enum Kind: Equatable, Sendable { case waiting, stuck, quiet }
    public var kind: Kind
    public var rig: String
    /// pane is the pane a tap opens; nil for a quiet row and for a waiting
    /// bead nobody is assigned.
    public var pane: String?
    public var title: String
    /// detail is the recommendation of a waiting item, or the reason of a
    /// stuck pane; nil when there is none.
    public var detail: String?
    /// bead is the waiting bead's id, when the row is one.
    public var bead: String? = nil

    public var id: String { "\(rig)/\(kind)/\(bead ?? "")/\(pane ?? "")/\(title)" }
}

/// BlockerSection is one rig in the blockers list.
public struct BlockerSection: Identifiable, Equatable, Sendable {
    public var rig: String
    public var rows: [BlockerRow]
    public var id: String { rig }
}

/// BlockersModel holds the blockers list, reloaded from the service on every
/// feed event that can change it.
@MainActor
public final class BlockersModel: ObservableObject {
    @Published public private(set) var sections: [BlockerSection] = []
    /// loaded is false until the first reload answers.
    @Published public private(set) var loaded = false
    @Published public private(set) var problem: String?

    private let source: BlockersSource

    public init(source: BlockersSource) { self.source = source }

    public func reload() async {
        do {
            sections = Self.sections(try await source.blockers())
            problem = nil
        } catch is CancellationError {
            return // not a failure, and not a read: the list stays as it was
        } catch {
            problem = "Blockers could not be read: \(error)"
            Log.shown(error, screen: "blockers")
        }
        loaded = true
    }

    /// apply reloads on the events that can change blockers: a pane state, a
    /// waiting item, a stuck pane, the service's blockers event, a reset, or a
    /// reply in a super pane. Only the blockers event and that reply come when
    /// a later footer drops a line; waiting_item announces new items only.
    /// Other transcript lines do not reload.
    public func apply(_ e: FeedEvent) async {
        switch e {
        case .paneState, .waitingItem, .stuck, .blockers, .reset:
            await reload()
        case let .transcript(_, pane, message):
            if pane == "super", message.kind == .agent { await reload() }
        }
    }

    /// sections renders rigs: waiting items first, then stuck panes; a quiet
    /// rig is one quiet row.
    public nonisolated static func sections(_ rigs: [RigBlockers]) -> [BlockerSection] {
        rigs.map { r in
            var rows: [BlockerRow] = r.waiting.map {
                BlockerRow(kind: .waiting, rig: r.rig, pane: $0.pane.isEmpty ? nil : $0.pane, title: $0.text,
                           detail: $0.recommendation.isEmpty ? nil : $0.recommendation,
                           bead: $0.bead.flatMap { $0.isEmpty ? nil : $0 })
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
