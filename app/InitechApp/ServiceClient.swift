import Foundation
import os

/// ServiceError names why a call to the app service failed.
enum ServiceError: Error, Equatable {
    /// The service refused the token (401): the phone must pair again.
    case unauthorized
    /// Any other non-2xx answer, with the service's words when it gave some.
    case status(Int, String?)
    case unreachable(String)
    /// The server's certificate is not the one this pairing pins.
    case certificateRefused
}

extension ServiceError: CustomStringConvertible {
    /// description is the plain words a screen shows after "The Mac did not answer: ".
    var description: String {
        switch self {
        case .unauthorized: "it refused this phone's token"
        case .certificateRefused: "its certificate is not the one this phone paired with"
        case let .status(code, words): words ?? "it answered with status \(code)"
        case let .unreachable(words): words
        }
    }
}

/// Service is everything the app asks of the Mac. Views talk to this, never
/// to URLSession, so tests and previews can stand in for the Mac.
protocol Service: Sendable {
    func rigs() async throws -> [RigInfo]
    func status() async throws -> StatusReply
    func pins() async throws -> [Pin]
    func savePins(_ pins: [Pin]) async throws
    func conversation(rig: String, pane: String, before: String?, limit: Int) async throws -> Conversation
    func send(rig: String, pane: String, text: String) async throws -> SendResult
    func key(rig: String, pane: String, key: PaneKey) async throws -> SendResult
    /// feed streams events until the task is cancelled, reconnecting after a
    /// drop with the last event id so the service replays what was missed.
    func feed() -> AsyncStream<FeedEvent>
}

/// HTTPService is the Service over HTTPS to `initech app-serve`, trusting
/// only the certificate the pairing names.
struct HTTPService: Service {
    let pairing: Pairing
    let session: URLSession
    /// retryDelay is how long the feed waits before reconnecting.
    var retryDelay: Duration

    init(pairing: Pairing, retryDelay: Duration = .seconds(2)) {
        self.pairing = pairing
        self.retryDelay = retryDelay
        session = URLSession(configuration: .ephemeral,
                             delegate: PinningDelegate(fingerprint: pairing.fingerprint),
                             delegateQueue: nil)
    }

    private func request(_ method: String, _ path: String, query: [URLQueryItem] = [], body: Data? = nil) -> URLRequest {
        var c = URLComponents(url: pairing.baseURL, resolvingAgainstBaseURL: false)!
        c.percentEncodedPath = path
        if !query.isEmpty { c.queryItems = query }
        var r = URLRequest(url: c.url!)
        r.httpMethod = method
        r.setValue("Bearer \(pairing.token)", forHTTPHeaderField: "Authorization")
        if let body {
            r.httpBody = body
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        return r
    }

    private func call(_ r: URLRequest) async throws -> Data {
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: r)
        } catch {
            // A refused pin reaches here as a plain cancelled request.
            if (session.delegate as? PinningDelegate)?.refusedLast == true { throw ServiceError.certificateRefused }
            throw ServiceError.unreachable(error.localizedDescription)
        }
        let code = (response as? HTTPURLResponse)?.statusCode ?? 0
        if code == 401 { throw ServiceError.unauthorized }
        guard (200..<300).contains(code) else {
            let words = (try? JSONDecoder.service.decode(SendResult.self, from: data))?.words
            throw ServiceError.status(code, words)
        }
        return data
    }

    /// get decodes a GET answer. Internal so other files can extend HTTPService
    /// with an endpoint each (the blockers and progress views do).
    func get<T: Decodable>(_ path: String, query: [URLQueryItem] = []) async throws -> T {
        try JSONDecoder.service.decode(T.self, from: try await call(request("GET", path, query: query)))
    }

    func rigs() async throws -> [RigInfo] { try await get("/v1/rigs") }
    func status() async throws -> StatusReply { try await get("/v1/status") }
    func pins() async throws -> [Pin] { try await get("/v1/pins") }

    func savePins(_ pins: [Pin]) async throws {
        _ = try await call(request("PUT", "/v1/pins", body: try JSONEncoder.service.encode(pins)))
    }

    func conversation(rig: String, pane: String, before: String?, limit: Int) async throws -> Conversation {
        var q = [URLQueryItem(name: "limit", value: String(limit))]
        if let before { q.append(URLQueryItem(name: "before", value: before)) }
        return try await get("/v1/conversation/\(seg(rig))/\(seg(pane))", query: q)
    }

    func send(rig: String, pane: String, text: String) async throws -> SendResult {
        struct Body: Encodable { var text: String; var enter = true }
        let r = request("POST", "/v1/send/\(seg(rig))/\(seg(pane))", body: try JSONEncoder.service.encode(Body(text: text)))
        return try JSONDecoder.service.decode(SendResult.self, from: try await call(r))
    }

    func key(rig: String, pane: String, key: PaneKey) async throws -> SendResult {
        struct Body: Encodable { var key: PaneKey }
        let r = request("POST", "/v1/keys/\(seg(rig))/\(seg(pane))", body: try JSONEncoder.service.encode(Body(key: key)))
        return try JSONDecoder.service.decode(SendResult.self, from: try await call(r))
    }

    func feed() -> AsyncStream<FeedEvent> {
        let base = self
        return AsyncStream { continuation in
            let task = Task {
                var lastID: String?
                while !Task.isCancelled {
                    var r = base.request("GET", "/v1/feed")
                    r.timeoutInterval = 60 // keepalives arrive every 15 s
                    if let lastID { r.setValue(lastID, forHTTPHeaderField: "Last-Event-ID") }
                    do {
                        let (bytes, response) = try await base.session.bytes(for: r, delegate: base.session.delegate as? URLSessionTaskDelegate)
                        if (response as? HTTPURLResponse)?.statusCode == 200 {
                            // Not bytes.lines: it drops blank lines, and a
                            // blank line is what ends an SSE event.
                            var parser = SSEParser()
                            for try await line in SSELines(bytes: bytes) {
                                guard let raw = parser.feed(line) else { continue }
                                if let id = raw.id { lastID = id }
                                if let e = raw.event { continuation.yield(e) }
                            }
                        }
                    } catch {}
                    try? await Task.sleep(for: base.retryDelay)
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    /// seg escapes one path segment (a rig or pane name).
    private func seg(_ s: String) -> String {
        s.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? s
    }
}

/// PinningDelegate accepts a server only when its leaf certificate's SHA-256
/// equals the pairing's fingerprint; anything else is refused before a
/// request, and so the token, is sent. No CA is consulted: the service's
/// certificate is self-signed and the pin is the whole trust.
///
/// Both the session-level and the task-level challenge methods answer: a
/// streaming task (the feed) asks the task-level one, and without it the
/// system's default trust refuses the self-signed certificate.
final class PinningDelegate: NSObject, URLSessionTaskDelegate, Sendable {
    let fingerprint: String
    private let refused = OSAllocatedUnfairLock(initialState: false)

    /// refusedLast says the newest certificate this session saw failed the pin.
    var refusedLast: Bool { refused.withLock { $0 } }

    init(fingerprint: String) { self.fingerprint = fingerprint }

    func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge)
        async -> (URLSession.AuthChallengeDisposition, URLCredential?) {
        answer(challenge)
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didReceive challenge: URLAuthenticationChallenge)
        async -> (URLSession.AuthChallengeDisposition, URLCredential?) {
        answer(challenge)
    }

    private func answer(_ challenge: URLAuthenticationChallenge) -> (URLSession.AuthChallengeDisposition, URLCredential?) {
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
              let trust = challenge.protectionSpace.serverTrust else {
            return (.cancelAuthenticationChallenge, nil)
        }
        let matches = Self.leafFingerprint(trust) == fingerprint
        refused.withLock { $0 = !matches }
        guard matches else {
            return (.cancelAuthenticationChallenge, nil)
        }
        return (.useCredential, URLCredential(trust: trust))
    }

    /// leafFingerprint is the pairing-form fingerprint of the server's leaf certificate.
    static func leafFingerprint(_ trust: SecTrust) -> String? {
        guard let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate],
              let leaf = chain.first else { return nil }
        return (SecCertificateCopyData(leaf) as Data).sha256Base64URL
    }
}

/// SSELines splits a byte stream into lines on LF, dropping a trailing CR,
/// and keeps empty lines, which end SSE events.
struct SSELines<Base: AsyncSequence>: AsyncSequence where Base.Element == UInt8 {
    let bytes: Base

    struct AsyncIterator: AsyncIteratorProtocol {
        var base: Base.AsyncIterator
        var line: [UInt8] = []

        mutating func next() async throws -> String? {
            while let b = try await base.next() {
                if b == 0x0A {
                    if line.last == 0x0D { line.removeLast() }
                    defer { line.removeAll(keepingCapacity: true) }
                    return String(decoding: line, as: UTF8.self)
                }
                line.append(b)
            }
            return nil
        }
    }

    func makeAsyncIterator() -> AsyncIterator { AsyncIterator(base: bytes.makeAsyncIterator()) }
}

/// SSEParser assembles server-sent-event lines into events. One event ends
/// at a blank line; comment lines (a leading colon, the keepalive) are skipped.
struct SSEParser {
    struct Raw: Equatable {
        var id: String?
        var type: String?
        var data: String
        /// event decodes the raw event; an unknown type or bad data is nil.
        var event: FeedEvent? { FeedEvent.decode(type: type, data: data) }
    }

    private var id: String?
    private var type: String?
    private var data: [String] = []

    /// feed takes one line and returns an event when that line completes one.
    mutating func feed(_ line: String) -> Raw? {
        if line.isEmpty {
            defer { id = nil; type = nil; data = [] }
            guard type != nil || !data.isEmpty else { return nil }
            return Raw(id: id, type: type, data: data.joined(separator: "\n"))
        }
        if line.hasPrefix(":") { return nil }
        let field: Substring
        var value: Substring = ""
        if let colon = line.firstIndex(of: ":") {
            field = line[..<colon]
            value = line[line.index(after: colon)...]
            if value.hasPrefix(" ") { value = value.dropFirst() }
        } else {
            field = Substring(line)
        }
        switch field {
        case "id": id = String(value)
        case "event": type = String(value)
        case "data": data.append(String(value))
        default: break
        }
        return nil
    }
}

extension FeedEvent {
    /// decode reads one feed event; nil for a type the app does not use.
    static func decode(type: String?, data: String) -> FeedEvent? {
        let d = JSONDecoder.service
        let bytes = Data(data.utf8)
        struct PaneState: Decodable { var rig, pane: String; var from, to, banner: String? }
        struct Waiting: Decodable { var rig, pane, text: String }
        struct Stuck: Decodable { var rig, pane, reason: String }
        struct Transcript: Decodable { var rig, pane: String; var message: Message }
        switch type {
        case "pane_state":
            guard let e = try? d.decode(PaneState.self, from: bytes) else { return nil }
            return .paneState(rig: e.rig, pane: e.pane, from: e.from, to: e.to, banner: e.banner)
        case "waiting_item":
            guard let e = try? d.decode(Waiting.self, from: bytes) else { return nil }
            return .waitingItem(rig: e.rig, pane: e.pane, text: e.text)
        case "stuck":
            guard let e = try? d.decode(Stuck.self, from: bytes) else { return nil }
            return .stuck(rig: e.rig, pane: e.pane, reason: e.reason)
        case "transcript":
            guard let e = try? d.decode(Transcript.self, from: bytes) else { return nil }
            return .transcript(rig: e.rig, pane: e.pane, message: e.message)
        case "blockers":
            return .blockers
        case "reset":
            return .reset
        default:
            return nil
        }
    }
}
