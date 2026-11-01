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
    /// The app ended the TLS handshake itself, for the reason given (no
    /// certificate reached it): never a cancel to hide.
    case handshakeEnded(String)
}

extension ServiceError: CustomStringConvertible {
    /// description is the plain words a screen shows after "The Mac did not answer: ".
    var description: String {
        switch self {
        case .unauthorized: "it refused this phone's token"
        case .certificateRefused: "its certificate is not the one this phone paired with"
        case let .status(code, words): words ?? "it answered with status \(code)"
        case let .unreachable(words): words
        case let .handshakeEnded(why): "the app ended the connection: \(why)"
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
    func conversation(rig: String, pane: String, before: Int64?, limit: Int) async throws -> Conversation
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
    /// retryDelay is how long the feed first waits before reconnecting; each
    /// failure in a row doubles it, up to maxRetryDelay.
    var retryDelay: Duration
    var maxRetryDelay: Duration

    init(pairing: Pairing, retryDelay: Duration = .seconds(2), maxRetryDelay: Duration = .seconds(60)) {
        self.pairing = pairing
        self.retryDelay = retryDelay
        self.maxRetryDelay = maxRetryDelay
        // No session delegate: every TLS challenge goes to the request's own
        // PinCheck, so one request's pin result is never read as another's.
        session = URLSession(configuration: .ephemeral)
    }

    /// request is one authorized request to the service. Internal, with call and
    /// seg, so other files can extend HTTPService with an endpoint each.
    func request(_ method: String, _ path: String, query: [URLQueryItem] = [], body: Data? = nil) -> URLRequest {
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

    func call(_ r: URLRequest) async throws -> Data {
        let data: Data
        let response: URLResponse
        let check = PinCheck(fingerprint: pairing.fingerprint)
        do {
            (data, response) = try await session.data(for: r, delegate: check)
        } catch {
            let e = Self.failure(error, check: check)
            if !(e is CancellationError) {
                Log.service.error("\(r.httpMethod ?? "", privacy: .public) \(r.url?.path ?? "", privacy: .public) failed: \(String(describing: e), privacy: .public) (\(String(describing: error), privacy: .public))")
            }
            throw e
        }
        let code = (response as? HTTPURLResponse)?.statusCode ?? 0
        if code == 401 { throw ServiceError.unauthorized }
        guard (200..<300).contains(code) else {
            let words = (try? JSONDecoder.service.decode(SendResult.self, from: data))?.words
            throw ServiceError.status(code, words)
        }
        return data
    }

    /// failure names why a request failed. Our own pin check ending the
    /// handshake wins over URLSession's plain "cancelled" for it: a refused
    /// certificate, or a handshake the check ended, is shown with its reason.
    /// Only a cancel that is not the check's (the task that asked was
    /// cancelled: the view went away or reloaded) is a CancellationError, which
    /// a screen shows as nothing.
    static func failure(_ error: Error, check: PinCheck) -> Error {
        switch check.verdict {
        case .refused: return ServiceError.certificateRefused
        case .ended(let why): return ServiceError.handshakeEnded(why)
        case .none, .matched: break
        }
        if error is CancellationError || (error as? URLError)?.code == .cancelled { return CancellationError() }
        return ServiceError.unreachable(plainWords(error))
    }

    /// plainWords is what a screen says after "The Mac did not answer: " for a
    /// transport failure: plain words, never a domain or code (those go to the
    /// log with the failed request).
    static func plainWords(_ error: Error) -> String {
        switch (error as? URLError)?.code {
        case .timedOut?: "it did not answer in time"
        case .notConnectedToInternet?, .dataNotAllowed?: "this device is offline"
        case .networkConnectionLost?: "the connection dropped"
        case .cannotFindHost?, .cannotConnectToHost?, .dnsLookupFailed?: "it could not be reached"
        case .secureConnectionFailed?, .serverCertificateUntrusted?, .serverCertificateHasBadDate?,
             .serverCertificateNotYetValid?, .serverCertificateHasUnknownRoot?, .clientCertificateRejected?:
            "the secure connection to it failed"
        default: "it could not be reached"
        }
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

    func conversation(rig: String, pane: String, before: Int64?, limit: Int) async throws -> Conversation {
        var q = [URLQueryItem(name: "limit", value: String(limit))]
        if let before { q.append(URLQueryItem(name: "before", value: String(before))) }
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

    /// feed streams events until the task is cancelled. After a drop it
    /// reconnects with the last event id, waiting longer after each failure
    /// in a row (back-off, reset by a connection that opens). A refused token
    /// (401) or certificate ends the stream: retrying cannot succeed until the
    /// phone pairs again, and a loop would knock on the Mac every few seconds.
    func feed() -> AsyncStream<FeedEvent> {
        let base = self
        return AsyncStream { continuation in
            let task = Task {
                var lastID: String?
                var delay = base.retryDelay
                while !Task.isCancelled {
                    var r = base.request("GET", "/v1/feed")
                    r.timeoutInterval = 60 // keepalives arrive every 15 s
                    if let lastID { r.setValue(lastID, forHTTPHeaderField: "Last-Event-ID") }
                    let check = PinCheck(fingerprint: base.pairing.fingerprint)
                    do {
                        let (bytes, response) = try await base.session.bytes(for: r, delegate: check)
                        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
                        if status == 401 { break }
                        if status == 200 {
                            delay = base.retryDelay
                            // Not bytes.lines: it drops blank lines, and a
                            // blank line is what ends an SSE event.
                            var parser = SSEParser()
                            for try await line in SSELines(bytes: bytes) {
                                guard let raw = parser.feed(line) else { continue }
                                if let id = raw.id { lastID = id }
                                if let e = raw.event { continuation.yield(e) }
                            }
                        }
                    } catch {
                        let e = HTTPService.failure(error, check: check)
                        if e is CancellationError, Task.isCancelled { break }
                        Log.service.error("feed dropped: \(String(describing: e), privacy: .public)")
                        if case ServiceError.certificateRefused = e { break }
                    }
                    try? await Task.sleep(for: delay)
                    delay = min(delay * 2, base.maxRetryDelay)
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    /// seg escapes one path segment (a rig or pane name).
    func seg(_ s: String) -> String {
        s.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? s
    }
}

/// PinCheck answers one request's TLS challenges: it accepts the server only
/// when its leaf certificate's SHA-256 equals the pairing's fingerprint, so
/// anything else is refused before the request, and so the token, is sent. No
/// CA is consulted: the service's certificate is self-signed and the pin is
/// the whole trust. A challenge of any other kind gets the system's default
/// handling. One PinCheck per request: its verdict is that request's alone.
final class PinCheck: NSObject, URLSessionTaskDelegate, Sendable {
    enum Verdict: Equatable, Sendable {
        case none
        case matched
        /// The certificate did not match the pin.
        case refused
        /// The check ended the handshake for another reason, given.
        case ended(String)
    }

    let fingerprint: String
    private let state = OSAllocatedUnfairLock(initialState: Verdict.none)

    var verdict: Verdict { state.withLock { $0 } }

    init(fingerprint: String) { self.fingerprint = fingerprint }

    func urlSession(_ session: URLSession, task: URLSessionTask, didReceive challenge: URLAuthenticationChallenge)
        async -> (URLSession.AuthChallengeDisposition, URLCredential?) {
        decide(method: challenge.protectionSpace.authenticationMethod, trust: challenge.protectionSpace.serverTrust,
               host: challenge.protectionSpace.host)
    }

    /// decide is the pin decision for one challenge, logged either way.
    func decide(method: String, trust: SecTrust?, host: String) -> (URLSession.AuthChallengeDisposition, URLCredential?) {
        guard method == NSURLAuthenticationMethodServerTrust else {
            Log.service.info("TLS challenge \(method, privacy: .public) from \(host, privacy: .public): default handling")
            return (.performDefaultHandling, nil)
        }
        guard let trust else {
            let why = "no certificate came with the server-trust challenge"
            state.withLock { $0 = .ended(why) }
            Log.service.error("pin \(host, privacy: .public): ended the handshake, \(why, privacy: .public)")
            return (.cancelAuthenticationChallenge, nil)
        }
        let got = Self.leafFingerprint(trust)
        guard got == fingerprint else {
            state.withLock { $0 = .refused }
            Log.service.error("pin \(host, privacy: .public): refused, leaf \(got ?? "none", privacy: .public) is not the paired \(self.fingerprint, privacy: .public)")
            return (.cancelAuthenticationChallenge, nil)
        }
        state.withLock { $0 = .matched }
        Log.service.debug("pin \(host, privacy: .public): matched")
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
