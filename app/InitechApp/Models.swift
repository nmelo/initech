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
    /// before is the cursor for the next older page, an integer the app passes
    /// back as given (the service's byte offset of this page's first line; 0
    /// on the page that starts the transcript); nil when a page carries none.
    var before: Int64?

    init(session: String?, messages: [Message], startReached: Bool, before: Int64?) {
        self.session = session
        self.messages = messages
        self.startReached = startReached
        self.before = before
    }

    /// Decoding keeps every entry it can read and skips one it cannot: a
    /// single malformed entry must never cost the whole page.
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        session = try c.decodeIfPresent(String.self, forKey: .session)
        startReached = try c.decode(Bool.self, forKey: .startReached)
        before = try c.decodeIfPresent(Int64.self, forKey: .before)
        messages = try c.decode([Lossy<Message>].self, forKey: .messages).compactMap(\.value)
    }
}

/// Lossy decodes one element of a list and keeps nil when it cannot.
struct Lossy<T: Decodable>: Decodable {
    let value: T?
    init(from decoder: Decoder) throws { value = try? T(from: decoder) }
}

/// Message is one entry of the conversation view.
struct Message: Codable, Equatable, Identifiable, Sendable {
    /// Kind is what the service says an entry is: the agent's reply, what the
    /// operator typed, a message another agent sent, a tool run. A kind the app
    /// does not know yet reads as other and shows as plain text, never as a
    /// failure of the page.
    enum Kind: String, Codable, Sendable {
        case agent, nelson, message, tool, system, other

        init(from decoder: Decoder) throws {
            self = Kind(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .other
        }
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
    /// from is the agent that sent a message entry.
    var from: String?
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
