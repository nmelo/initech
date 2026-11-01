import Foundation

// The app service's v1 shapes, as eng5 posted them on the app service bead.
// Field names follow the wire (snake_case keys are mapped by the decoder);
// a field the service may leave null is optional here, never defaulted.

/// RigInfo is one rig the service found, running or not.
struct RigInfo: Codable, Equatable, Sendable {
    var rig: String
    var root: String
    var running: Bool
}

/// StatusReply is GET /v1/status: every running rig's `initech status --json`.
struct StatusReply: Codable, Equatable, Sendable {
    var rigs: [RigStatus]
}

/// RigStatus is one rig's status, read at readAt.
struct RigStatus: Codable, Equatable, Sendable {
    var rig: String
    var root: String
    var readAt: String?
    var agents: [AgentStatus]
}

/// AgentStatus is one agent object of `initech status --json`, plus the
/// pane's last screen lines when it is at a dialog, a banner, waiting or pending.
struct AgentStatus: Codable, Equatable, Sendable {
    var role: String
    var state: String?
    var alive: Bool
    var beads: [String]?
    var contextPercent: Int?
    var banner: String?
    var screen: [String]?
}

/// Pin is one pane on the home list: where it is, the name he gave it, its place.
struct Pin: Codable, Equatable, Hashable, Sendable {
    var rig: String
    var pane: String
    var name: String
    var order: Int
}

/// Conversation is one page of a pane's transcript, oldest first.
struct Conversation: Codable, Equatable, Sendable {
    var session: String?
    var messages: [Message]
    var startReached: Bool
    var before: String?
}

/// Message is one entry of the conversation view.
struct Message: Codable, Equatable, Identifiable, Sendable {
    enum Kind: String, Codable, Sendable {
        case agent, nelson, tool, system
    }
    struct Tool: Codable, Equatable, Sendable {
        var name: String
        var summary: String
        var detail: String?
    }
    var id: String
    var at: String?
    var kind: Kind
    var text: String
    var footer: [String]?
    var tool: Tool?
}

/// SendResult is the send path's own verdict and the line it printed.
/// The keys endpoint answers in the same shape.
struct SendResult: Codable, Equatable, Sendable {
    var verdict: String
    var words: String

    /// Outcome groups the verdicts the way the message bubble shows them.
    enum Outcome: Equatable { case delivered, held, notDelivered, unsure }

    var outcome: Outcome {
        switch verdict {
        case "delivered": .delivered
        case "held", "deferred": .held
        case "not_delivered", "refused": .notDelivered
        default: .unsure // unchecked, unconfirmed: sent, but not seen taken
        }
    }
}

/// PaneKey is one key of the key strip, named as the keys endpoint takes it.
enum PaneKey: String, CaseIterable, Codable, Sendable {
    case enter, esc
    case ctrlC = "ctrl-c"
    case y, n

    var label: String {
        switch self {
        case .enter: "Enter"
        case .esc: "Esc"
        case .ctrlC: "Ctrl-C"
        case .y: "y"
        case .n: "n"
        }
    }
}

/// FeedEvent is one event of GET /v1/feed.
enum FeedEvent: Equatable, Sendable {
    case paneState(rig: String, pane: String, from: String?, to: String?, banner: String?)
    case waitingItem(rig: String, pane: String, text: String)
    case stuck(rig: String, pane: String, reason: String)
    case transcript(rig: String, pane: String, message: Message)
    /// blockers says the service's blockers list changed: read it again.
    case blockers
    /// reset says events were missed beyond the replay window: reload everything.
    case reset
}

extension JSONDecoder {
    /// service decodes the app service's snake_case JSON.
    static var service: JSONDecoder {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        return d
    }
}

extension JSONEncoder {
    /// service encodes for the app service's snake_case JSON.
    static var service: JSONEncoder {
        let e = JSONEncoder()
        e.keyEncodingStrategy = .convertToSnakeCase
        e.outputFormatting = .sortedKeys
        return e
    }
}
