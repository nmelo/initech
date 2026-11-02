import Foundation
@testable import InitechCore

/// MemoryService is the in-memory Service the model tests run on, the fuller
/// sibling of ScriptedService: no socket, so `swift test` may run on the
/// shared Mac. It keeps the names and the paging rule of the app tests' local
/// server, so a test reads the same on either. What HTTP adds (status codes, the
/// token, TLS, the pin, the feed's bytes) is tested in the app's
/// ServiceClientTests on a simulator.
final class MemoryService: Service, BlockersSource, @unchecked Sendable {
    struct Request { var method: String; var path: String; var body: Data }

    private let lock = NSLock()
    private var _status = StatusReply(rigs: [])
    private var _pins: [Pin] = []
    private var _pages: [String: [Conversation]] = [:]
    private var _sendVerdict: [String: SendResult] = [:]
    private var _keyVerdict: [String: SendResult] = [:]
    private var _blockers: String?
    private var _requests: [Request] = []
    private var _failure: ServiceError?
    private var _feeds: [UUID: AsyncStream<FeedEvent>.Continuation] = [:]

    private func locked<T>(_ f: () -> T) -> T { lock.lock(); defer { lock.unlock() }; return f() }

    var status: StatusReply { get { locked { _status } } set { locked { _status = newValue } } }
    var pins: [Pin] { get { locked { _pins } } set { locked { _pins = newValue } } }
    var requests: [Request] { locked { _requests } }
    var openFeeds: Int { locked { _feeds.count } }
    /// failure makes every call throw it, as a service that refuses does.
    var failure: ServiceError? { get { locked { _failure } } set { locked { _failure = newValue } } }

    /// stop is the Mac no longer answering.
    func stop() { failure = .unreachable("the connection failed") }

    /// setTranscript serves messages (oldest first) for a pane in pages of
    /// pageSize; the cursor is the index of a page's first message.
    func setTranscript(rig: String, pane: String, _ messages: [Message], pageSize: Int) {
        var pages: [Conversation] = []
        var end = messages.count
        while end > 0 || pages.isEmpty {
            let start = max(0, end - pageSize)
            pages.append(Conversation(session: "s1", messages: Array(messages[start..<end]),
                                      startReached: start == 0, before: Int64(start)))
            end = start
        }
        locked { _pages["\(rig)/\(pane)"] = pages }
    }

    func setSendVerdict(rig: String, pane: String, _ r: SendResult) { locked { _sendVerdict["\(rig)/\(pane)"] = r } }
    func setKeyVerdict(rig: String, pane: String, _ r: SendResult) { locked { _keyVerdict["\(rig)/\(pane)"] = r } }
    /// setRawGET makes the named route answer this JSON; only blockers is read.
    func setRawGET(_ name: String, _ json: String) {
        precondition(name == "blockers", "MemoryService serves no raw \(name)")
        locked { _blockers = json }
    }

    /// emit hands one event, in the wire's type and JSON, to every open feed.
    func emit(_ type: String, _ json: String) {
        guard let e = FeedEvent.decode(type: type, data: json) else { preconditionFailure("no such event: \(type)") }
        locked { Array(_feeds.values) }.forEach { $0.yield(e) }
    }

    private func enter(_ method: String, _ path: String, _ body: Data = Data()) throws {
        locked { _requests.append(Request(method: method, path: path, body: body)) }
        if let f = failure { throw f }
    }

    func rigs() async throws -> [RigInfo] { try enter("GET", "/v1/rigs"); return [] }
    func status() async throws -> StatusReply { try enter("GET", "/v1/status"); return status }
    func pins() async throws -> [Pin] { try enter("GET", "/v1/pins"); return pins }
    func savePins(_ next: [Pin]) async throws { try enter("PUT", "/v1/pins"); pins = next }

    func conversation(rig: String, pane: String, before: Int64?, limit: Int) async throws -> Conversation {
        try enter("GET", "/v1/conversation/\(rig)/\(pane)")
        try Task.checkCancellation()
        let pages = locked { _pages["\(rig)/\(pane)"] } ?? [Conversation(session: nil, messages: [], startReached: true, before: 0)]
        let n = before ?? 0 // none, or 0, is the newest page, as on the service
        guard let i = n == 0 ? 0 : pages.firstIndex(where: { ($0.before ?? 0) + Int64($0.messages.count) == n }) else {
            throw ServiceError.status(400, nil)
        }
        return pages[i]
    }

    func send(rig: String, pane: String, text: String) async throws -> SendResult {
        try enter("POST", "/v1/send/\(rig)/\(pane)")
        return locked { _sendVerdict["\(rig)/\(pane)"] } ?? SendResult(verdict: "delivered", words: "delivered to \(pane)")
    }

    func key(rig: String, pane: String, key: PaneKey) async throws -> SendResult {
        try enter("POST", "/v1/keys/\(rig)/\(pane)", Data(#"{"key":"\#(key.rawValue)"}"#.utf8))
        return locked { _keyVerdict["\(rig)/\(pane)"] } ?? SendResult(verdict: "delivered", words: "key \(key.rawValue) sent to \(pane)")
    }

    func feed() -> AsyncStream<FeedEvent> {
        let id = UUID()
        return AsyncStream { c in
            locked { _feeds[id] = c }
            c.onTermination = { [weak self] _ in _ = self?.locked { self?._feeds.removeValue(forKey: id) } }
        }
    }

    /// blockers answers the raw JSON, or 404 with none set, as the fixture does.
    func blockers() async throws -> [RigBlockers] {
        try enter("GET", "/v1/blockers")
        guard let json = locked({ _blockers }) else { throw ServiceError.status(404, nil) }
        return try JSONDecoder.service.decode([RigBlockers].self, from: Data(json.utf8))
    }
}
