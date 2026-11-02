import Foundation

// The app service's v1 shapes, as eng5 posted them on the app service bead.
// Field names follow the wire (snake_case keys are mapped by the decoder);
// a field the service may leave null is optional here, never defaulted.

/// RigInfo is one rig the service found, running or not.
public struct RigInfo: Codable, Equatable, Sendable {
    public var rig: String
    public var root: String
    public var running: Bool
}

/// StatusReply is GET /v1/status: every running rig's `initech status --json`.
public struct StatusReply: Codable, Equatable, Sendable {
    public var rigs: [RigStatus]
}

/// RigStatus is one rig's status, read at readAt.
public struct RigStatus: Codable, Equatable, Sendable {
    public var rig: String
    public var root: String
    public var readAt: String?
    public var agents: [AgentStatus]
}

/// AgentStatus is one agent object of `initech status --json`, plus the
/// pane's last screen lines when it is at a dialog, a banner, waiting or pending.
public struct AgentStatus: Codable, Equatable, Sendable {
    public var role: String
    public var state: String?
    public var alive: Bool
    public var beads: [String]?
    public var contextPercent: Int?
    public var banner: String?
    public var screen: [String]?
}

/// Pin is one pane on the home list: where it is, the name he gave it, its place.
public struct Pin: Codable, Equatable, Hashable, Sendable {
    public var rig: String
    public var pane: String
    public var name: String
    public var order: Int

    public init(rig: String, pane: String, name: String, order: Int) {
        self.rig = rig
        self.pane = pane
        self.name = name
        self.order = order
    }
}

/// Conversation is one page of a pane's transcript, oldest first.
public struct Conversation: Codable, Equatable, Sendable {
    public var session: String?
    public var messages: [Message]
    public var startReached: Bool
    /// before is the cursor for the next older page, an integer the app passes
    /// back as given (the service's byte offset of this page's first line; 0
    /// on the page that starts the transcript); nil when a page carries none.
    public var before: Int64?

    public init(session: String?, messages: [Message], startReached: Bool, before: Int64?) {
        self.session = session
        self.messages = messages
        self.startReached = startReached
        self.before = before
    }

    /// Decoding keeps every entry it can read and skips one it cannot: a
    /// single malformed entry must never cost the whole page.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        session = try c.decodeIfPresent(String.self, forKey: .session)
        startReached = try c.decode(Bool.self, forKey: .startReached)
        before = try c.decodeIfPresent(Int64.self, forKey: .before)
        messages = try c.decode([Lossy<Message>].self, forKey: .messages).compactMap(\.value)
    }
}

/// Lossy decodes one element of a list and keeps nil when it cannot.
struct Lossy<T: Decodable>: Decodable {
    public let value: T?
    public init(from decoder: Decoder) throws { value = try? T(from: decoder) }
}

/// Message is one entry of the conversation view.
public struct Message: Codable, Equatable, Identifiable, Sendable {
    /// Kind is what the service says an entry is: the agent's reply, what the
    /// operator typed, a message another agent sent, a tool run. A kind the app
    /// does not know yet reads as other and shows as plain text, never as a
    /// failure of the page.
    public enum Kind: String, Codable, Sendable {
        case agent, nelson, message, tool, system, other

        public init(from decoder: Decoder) throws {
            self = Kind(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .other
        }
    }
    public struct Tool: Codable, Equatable, Sendable {
        public var name: String
        public var summary: String
        public var detail: String?
    }
    public var id: String
    public var at: String?
    public var kind: Kind
    public var text: String
    public var footer: [String]?
    public var tool: Tool?
    /// from is the agent that sent a message entry.
    public var from: String?

    public init(id: String, at: String?, kind: Kind, text: String, footer: [String]?, tool: Tool?, from: String? = nil) {
        self.id = id
        self.at = at
        self.kind = kind
        self.text = text
        self.footer = footer
        self.tool = tool
        self.from = from
    }
}

/// SendResult is the send path's own verdict and the line it printed.
/// The keys endpoint answers in the same shape.
public struct SendResult: Codable, Equatable, Sendable {
    public var verdict: String
    public var words: String

    public init(verdict: String, words: String) {
        self.verdict = verdict
        self.words = words
    }

    /// Outcome groups the verdicts the way the message bubble shows them.
    public enum Outcome: Equatable { case delivered, held, notDelivered, unsure }

    public var outcome: Outcome {
        switch verdict {
        case "delivered": .delivered
        case "held", "deferred": .held
        case "not_delivered", "refused": .notDelivered
        default: .unsure // unchecked, unconfirmed: sent, but not seen taken
        }
    }
}

/// PaneKey is one key of the key strip, named as the keys endpoint takes it.
public enum PaneKey: String, CaseIterable, Codable, Sendable {
    case enter, esc
    case ctrlC = "ctrl-c"
    case y, n

    public var label: String {
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
public enum FeedEvent: Equatable, Sendable {
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
    public static var service: JSONDecoder {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        return d
    }
}

extension JSONEncoder {
    /// service encodes for the app service's snake_case JSON.
    public static var service: JSONEncoder {
        let e = JSONEncoder()
        e.keyEncodingStrategy = .convertToSnakeCase
        e.outputFormatting = .sortedKeys
        return e
    }
}
