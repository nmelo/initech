import Foundation

/// ToolSummary is the one line a run of tool calls is shown as, the way Claude
/// Code writes it: "Read 2 files, ran 5 shell commands". Calls of the same kind
/// are counted together, kinds keep the order they first appeared in, and only
/// the first word is capitalised.
public enum ToolSummary {
    /// Part is a piece of the line; number says it is a count, which is set in bold.
    public struct Part: Equatable, Sendable {
        public var text: String
        public var number: Bool
    }

    /// Kind is what a tool does, in the words the line uses.
    enum Kind: Hashable {
        case read, shell, edit, search, fetch, web, agent, other

        init(_ name: String) {
            switch name.lowercased() {
            case "read", "notebookread": self = .read
            case "bash", "bashoutput", "killshell": self = .shell
            case "edit", "multiedit", "write", "notebookedit": self = .edit
            case "grep", "glob", "ls", "toolsearch": self = .search
            case "webfetch": self = .fetch
            case "websearch": self = .web
            case "task", "agent": self = .agent
            default: self = .other
            }
        }

        /// words is the verb, then the thing counted, one and many.
        var words: (verb: String, one: String, many: String) {
            switch self {
            case .read: ("read", "file", "files")
            case .shell: ("ran", "shell command", "shell commands")
            case .edit: ("edited", "file", "files")
            case .search: ("searched for", "pattern", "patterns")
            case .fetch: ("fetched", "page", "pages")
            case .web: ("searched the web", "time", "times")
            case .agent: ("ran", "agent", "agents")
            case .other: ("used", "tool", "tools")
            }
        }
    }

    /// parts is the line for these tool names, in pieces. No names is no line.
    public static func parts(_ names: [String]) -> [Part] {
        var order: [Kind] = [], counts: [Kind: Int] = [:]
        for name in names {
            let kind = Kind(name)
            if counts[kind] == nil { order.append(kind) }
            counts[kind, default: 0] += 1
        }
        var out: [Part] = []
        for (i, kind) in order.enumerated() {
            let n = counts[kind] ?? 0, w = kind.words
            let verb = i == 0 ? w.verb.prefix(1).uppercased() + w.verb.dropFirst() : w.verb
            out.append(Part(text: (i == 0 ? "" : ", ") + verb + " ", number: false))
            out.append(Part(text: String(n), number: true))
            out.append(Part(text: " " + (n == 1 ? w.one : w.many), number: false))
        }
        return out
    }

    /// text is the line as plain text.
    public static func text(_ names: [String]) -> String { parts(names).map(\.text).joined() }
}

/// ConversationRow is one row of the conversation as it is shown: a message, or
/// a run of tool calls that came one after another, shown as one line.
public struct ConversationRow: Equatable, Identifiable, Sendable {
    public enum Kind: Equatable, Sendable {
        case message(Message)
        case tools([Message])
    }
    /// id is the message's id, or the id of the run's first tool call.
    public var id: String
    public var kind: Kind

    /// group turns the messages into rows, joining each unbroken run of tool calls.
    public static func group(_ messages: [Message]) -> [ConversationRow] {
        var rows: [ConversationRow] = []
        for m in messages {
            if m.kind == .tool, case .tools(let run)? = rows.last?.kind {
                rows[rows.count - 1].kind = .tools(run + [m])
            } else if m.kind == .tool {
                rows.append(ConversationRow(id: m.id, kind: .tools([m])))
            } else {
                rows.append(ConversationRow(id: m.id, kind: .message(m)))
            }
        }
        return rows
    }
}
