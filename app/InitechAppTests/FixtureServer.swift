import Foundation
import Network
#if !UITESTS
@testable import InitechApp
#endif

/// FixtureServer is the app service's v1 API as eng5 posted it, served over
/// real HTTP on the loopback so the app's client runs its whole path: the
/// Bearer gate (401, empty body), every endpoint the app calls, and the SSE
/// feed with Last-Event-ID replay. It holds its state in memory; tests set it
/// and read back what the app sent.
final class FixtureServer: @unchecked Sendable {
    struct Request: Equatable {
        var method: String
        var path: String
        var query: [String: String]
        var headers: [String: String]
        var body: Data
    }

    let token = "fixture-token-0123456789abcdefghijklmnopqrstu"
    /// fingerprint is the pin of the certificate the fixture serves.
    let fingerprint: String
    private(set) var port: UInt16 = 0
    private let listener: NWListener
    private let queue = DispatchQueue(label: "fixture")
    private let lock = NSLock()

    // State the endpoints serve. Guarded by lock.
    private var _rigs: [RigInfo] = []
    private var _status = StatusReply(rigs: [])
    private var _pins: [Pin] = []
    private var _pages: [String: [Conversation]] = [:] // "rig/pane" -> pages, newest first
    private var _sendVerdict: [String: SendResult] = [:]
    private var _keyVerdict: [String: SendResult] = [:]
    private var _events: [(id: Int, type: String, data: String)] = []
    private var _feeds: [NWConnection] = []
    private var _requests: [Request] = []
    private var _feedsPaused = false
    private var _rawGET: [String: Data] = [:] // "blockers" -> the JSON GET /v1/blockers answers
    private var _rawPages: [String: [String: Data]] = [:] // "rig/pane" -> ?before value ("" for none) -> page JSON

    var pairing: Pairing { Pairing(host: "127.0.0.1", port: Int(port), token: token, fingerprint: fingerprint) }

    /// pairingURL is the pairing link the Mac's token command would print.
    var pairingURL: String {
        "initech://pair?host=127.0.0.1&port=\(port)&token=\(token)&fp=\(fingerprint)"
    }

    init() throws {
        let (identity, certificate) = try Self.testIdentity()
        fingerprint = (SecCertificateCopyData(certificate) as Data).sha256Base64URL
        let tls = NWProtocolTLS.Options()
        sec_protocol_options_set_local_identity(tls.securityProtocolOptions, sec_identity_create(identity)!)
        let params = NWParameters(tls: tls)
        params.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: .any)
        listener = try NWListener(using: params)
    }

    /// testP12 is a self-signed P-256 identity (CN=initech-test-fixture,
    /// passphrase "fixture") made for these tests only; nothing trusts it.
    private static let testP12 = "MIIDpwIBAzCCA20GCSqGSIb3DQEHAaCCA14EggNaMIIDVjCCAg8GCSqGSIb3DQEHBqCCAgAwggH8AgEAMIIB9QYJKoZIhvcNAQcBMBwGCiqGSIb3DQEMAQYwDgQIDeNOIgVxPDsCAggAgIIByAwIeUqmllxfBuCc9pVkg9dLlZFV62JvnBHZdHfWQqxMdWhIY47eyaGqdLNg0sEbTYUngQdLcedOudOzWJ132Qn0HN2jG+d6nRhFFrd3C24jLI2DfVvKniljNd7BHObLPuZNl4CJ/8Bj6yyC4/2n597LKF3Tq6HaLc+zoXyWXgmtDEEHd+14tggxaZbZWoBH3T9PJP9ex4kAteaafNauq1v467EcMKqwtrsqzk8Gszm5ktKjs7Vng8ji6+D4OvpNAVDqS6hD0HX69nj2319JrNnA/M8OGJxP04jX1NLhj6ByYBZ7IktbipawRKyFbZsbm2mBmmyqe8C/NL6Nh+7RaqrBzeEmNmPAXtWDfKv3Smbf5/wlS50jd2cHt1NT+SighyofDzQDc5QJkblAMfKD1QMFVd3r2zlT5fWeVihha6jAKqd+bXoWOMkHJdlu1rrRZAZlv8PZgFxT01Wg6wkLdew4rXly/YPtNNyhRJixqepJTIGKAPZWmpRGrRGOXEgmDrlp45ION+Bml1Pq7Kh53cSbQi+7j3mK8hwJgFu+7gpSZrGZ2VYx9OE/QmyJP9O2USRz7lETHA7eLqYEfgg0cj/S2OUT8n+bCDCCAT8GCSqGSIb3DQEHAaCCATAEggEsMIIBKDCCASQGCyqGSIb3DQEMCgECoIG0MIGxMBwGCiqGSIb3DQEMAQMwDgQIKvjuqxIXsH4CAggABIGQpbzgEPfyfWf2Qmfl/ArIRQDyCcRgKaM/wp+HyJ0BWxGfiMW5XB0dzZOoLpqS7E0GMdVyDRJcDSDvIN+tPCKzKnN1xYxC3tMTMzVrzb8m/72m/cU4ZcozRHXMZMWGAPVJIjjCK65RF8ODPTtYP6rAom/BIO6qrnw8fZUUoa8ZWMidIKkDUPhIFI94lqgpYibuMV4wIwYJKoZIhvcNAQkVMRYEFIgSIZaFLGhjYGG476MKfsZ2RGDSMDcGCSqGSIb3DQEJFDEqHigAaQBuAGkAdABlAGMAaAAtAHQAZQBzAHQALQBmAGkAeAB0AHUAcgBlMDEwITAJBgUrDgMCGgUABBSDa+7G9kOWDH56FGGsHYZC4EFp5AQIBhVH2qOfxs0CAggA"

    private static func testIdentity() throws -> (SecIdentity, SecCertificate) {
        var items: CFArray?
        let status = SecPKCS12Import(Data(base64Encoded: testP12)! as CFData,
                                     [kSecImportExportPassphrase as String: "fixture"] as CFDictionary, &items)
        guard status == errSecSuccess,
              let first = (items as? [[String: Any]])?.first,
              let any = first[kSecImportItemIdentity as String] else {
            throw NSError(domain: NSOSStatusErrorDomain, code: Int(status))
        }
        let identity = any as! SecIdentity
        var cert: SecCertificate?
        SecIdentityCopyCertificate(identity, &cert)
        return (identity, cert!)
    }

    /// start listens and returns once the port is known.
    func start() async throws {
        let ready = AsyncStream<UInt16?>.makeStream()
        listener.stateUpdateHandler = { [listener] state in
            switch state {
            case .ready: ready.continuation.yield(listener.port?.rawValue); ready.continuation.finish()
            case .failed, .cancelled: ready.continuation.yield(nil); ready.continuation.finish()
            default: break
            }
        }
        listener.newConnectionHandler = { [weak self] c in self?.accept(c) }
        listener.start(queue: queue)
        for await p in ready.stream {
            guard let p else { throw URLError(.cannotConnectToHost) }
            port = p
        }
    }

    func stop() {
        listener.cancel()
        locked { _feeds.forEach { $0.cancel() }; _feeds = [] }
    }

    // MARK: state

    private func locked<T>(_ f: () -> T) -> T { lock.lock(); defer { lock.unlock() }; return f() }

    var rigs: [RigInfo] { get { locked { _rigs } } set { locked { _rigs = newValue } } }
    var status: StatusReply { get { locked { _status } } set { locked { _status = newValue } } }
    var pins: [Pin] { get { locked { _pins } } set { locked { _pins = newValue } } }
    var requests: [Request] { locked { _requests } }

    /// setTranscript serves messages (oldest first) for a pane in pages of pageSize.
    func setTranscript(rig: String, pane: String, _ messages: [Message], pageSize: Int) {
        var pages: [Conversation] = []
        var end = messages.count
        while end > 0 || pages.isEmpty {
            let start = max(0, end - pageSize)
            let page = Array(messages[start..<end])
            // As the service does: the cursor is where this page starts (the
            // service's byte offset; here the index of its first message), 0 on
            // the page that starts the transcript; ?before=<n> is the page
            // that ends where n starts.
            pages.append(Conversation(session: "s1", messages: page, startReached: start == 0,
                                      before: Int64(start)))
            end = start
        }
        locked { _pages["\(rig)/\(pane)"] = pages }
    }

    func setSendVerdict(rig: String, pane: String, _ r: SendResult) { locked { _sendVerdict["\(rig)/\(pane)"] = r } }
    func setKeyVerdict(rig: String, pane: String, _ r: SendResult) { locked { _keyVerdict["\(rig)/\(pane)"] = r } }
    /// setRawGET makes GET /v1/<name> answer this JSON; for a route whose shape
    /// the test target does not compile. Until it is set, the route is a 404.
    func setRawGET(_ name: String, _ json: String) { locked { _rawGET[name] = Data(json.utf8) } }

    /// servePages makes GET /v1/conversation/<rig>/<pane> answer the service's
    /// own page JSON (the contract fixtures), chosen by the ?before value the
    /// app sends: "" for none, otherwise the cursor exactly as given. A cursor
    /// with no page is a 400, as the service answers a bad before.
    func servePages(rig: String, pane: String, _ byBefore: [String: Data]) {
        locked { _rawPages["\(rig)/\(pane)"] = byBefore }
    }

    /// emit records a feed event and writes it to every open feed.
    @discardableResult
    func emit(_ type: String, _ json: String) -> Int {
        let (id, feeds) = locked { () -> (Int, [NWConnection]) in
            let id = (_events.last?.id ?? 0) + 1
            _events.append((id, type, json))
            return (id, _feeds)
        }
        let frame = Self.chunk(Self.frame(id: id, type: type, data: json))
        feeds.forEach { $0.send(content: frame, completion: .idempotent) }
        return id
    }

    /// forgetBefore drops events up to id, as the service's ten-minute window does.
    func forgetBefore(_ id: Int) { locked { _events.removeAll { $0.id <= id } } }

    /// dropFeeds closes every open feed connection, as a network drop would.
    func dropFeeds() { locked { _feeds.forEach { $0.cancel() }; _feeds = [] } }

    var openFeeds: Int { locked { _feeds.count } }

    /// feedsPaused answers feed requests with 503 while true, so a test can
    /// change what was missed before the client is let back in.
    var feedsPaused: Bool { get { locked { _feedsPaused } } set { locked { _feedsPaused = newValue } } }

    /// chunk wraps bytes as one HTTP/1.1 chunk: the feed streams with chunked
    /// transfer encoding, as Go's server does for a flushed response.
    private static func chunk(_ d: Data) -> Data {
        var out = Data(String(d.count, radix: 16).utf8)
        out.append(Data("\r\n".utf8)); out.append(d); out.append(Data("\r\n".utf8))
        return out
    }

    private static func frame(id: Int, type: String, data: String) -> Data {
        Data("id: \(id)\nevent: \(type)\ndata: \(data)\n\n".utf8)
    }

    // MARK: HTTP

    private func accept(_ c: NWConnection) {
        c.start(queue: queue)
        read(c, buffer: Data())
    }

    private func read(_ c: NWConnection, buffer: Data) {
        c.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [weak self] data, _, done, err in
            guard let self else { return }
            var buf = buffer
            if let data { buf.append(data) }
            if let req = Self.parse(buf) {
                self.handle(req, on: c)
            } else if done || err != nil {
                c.cancel()
            } else {
                self.read(c, buffer: buf)
            }
        }
    }

    /// parse returns a whole request once its headers and Content-Length body have arrived.
    static func parse(_ buf: Data) -> Request? {
        guard let end = buf.range(of: Data("\r\n\r\n".utf8)) else { return nil }
        let head = String(decoding: buf[..<end.lowerBound], as: UTF8.self).components(separatedBy: "\r\n")
        let start = head[0].split(separator: " ")
        guard start.count >= 2 else { return nil }
        var headers: [String: String] = [:]
        for line in head.dropFirst() {
            guard let colon = line.firstIndex(of: ":") else { continue }
            headers[line[..<colon].lowercased()] = line[line.index(after: colon)...].trimmingCharacters(in: .whitespaces)
        }
        let length = Int(headers["content-length"] ?? "0") ?? 0
        let body = buf[end.upperBound...]
        guard body.count >= length else { return nil }
        let comps = URLComponents(string: String(start[1]))
        var query: [String: String] = [:]
        comps?.queryItems?.forEach { query[$0.name] = $0.value ?? "" }
        return Request(method: String(start[0]), path: comps?.percentEncodedPath ?? "", query: query,
                       headers: headers, body: Data(body.prefix(length)))
    }

    private func respond(_ c: NWConnection, _ code: Int, _ body: Data? = nil) {
        var head = "HTTP/1.1 \(code) X\r\nConnection: close\r\nContent-Length: \(body?.count ?? 0)\r\n"
        if body != nil { head += "Content-Type: application/json\r\n" }
        var out = Data((head + "\r\n").utf8)
        if let body { out.append(body) }
        c.send(content: out, completion: .contentProcessed { _ in c.cancel() })
    }

    private func json<T: Encodable>(_ c: NWConnection, _ v: T, code: Int = 200) {
        respond(c, code, try! JSONEncoder.service.encode(v))
    }

    private func handle(_ r: Request, on c: NWConnection) {
        locked { _requests.append(r) }
        guard r.headers["authorization"] == "Bearer \(token)" else { return respond(c, 401) }
        let parts = r.path.split(separator: "/").map { String($0).removingPercentEncoding ?? String($0) }
        switch (r.method, parts.count > 1 ? parts[1] : "") {
        case ("GET", "rigs"): json(c, rigs)
        case ("GET", "status"): json(c, status)
        case ("GET", "pins"): json(c, pins)
        case ("PUT", "pins"):
            guard let p = try? JSONDecoder.service.decode([Pin].self, from: r.body) else { return respond(c, 400) }
            pins = p
            respond(c, 204)
        case ("GET", "conversation") where parts.count == 4 && locked({ _rawPages["\(parts[2])/\(parts[3])"] }) != nil:
            let byBefore = locked { _rawPages["\(parts[2])/\(parts[3])"] } ?? [:]
            guard let page = byBefore[r.query["before"] ?? ""] else { return respond(c, 400) }
            respond(c, 200, page)
        case ("GET", "conversation") where parts.count == 4:
            let pages = locked { _pages["\(parts[2])/\(parts[3])"] } ?? [Conversation(session: nil, messages: [], startReached: true, before: 0)]
            // ?before=0 or none is the newest page, as on the service.
            guard let n = r.query["before"].map({ Int64($0) }) ?? 0 else { return respond(c, 400) }
            let idx = n == 0 ? 0 : pages.firstIndex { ($0.before ?? 0) + Int64($0.messages.count) == n }
            guard let idx else { return respond(c, 400) }
            json(c, pages[idx])
        case ("POST", "send") where parts.count == 4:
            json(c, locked { _sendVerdict["\(parts[2])/\(parts[3])"] } ?? SendResult(verdict: "delivered", words: "delivered to \(parts[3])"))
        case ("POST", "keys") where parts.count == 4:
            struct Body: Decodable { var key: String }
            guard let b = try? JSONDecoder.service.decode(Body.self, from: r.body),
                  PaneKey(rawValue: b.key) != nil else {
                return json(c, SendResult(verdict: "refused", words: "unknown key"), code: 400)
            }
            json(c, locked { _keyVerdict["\(parts[2])/\(parts[3])"] } ?? SendResult(verdict: "delivered", words: "key \(b.key) delivered to \(parts[3])"))
        case ("GET", let name) where locked({ _rawGET[name] }) != nil:
            respond(c, 200, locked { _rawGET[name] })
        case ("GET", "feed") where feedsPaused:
            respond(c, 503)
        case ("GET", "feed"):
            openFeed(c, after: r.headers["last-event-id"].flatMap(Int.init))
        default:
            respond(c, 404)
        }
    }

    private func openFeed(_ c: NWConnection, after: Int?) {
        let head = Data("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nCache-Control: no-cache\r\nTransfer-Encoding: chunked\r\n\r\n".utf8)
            + Self.chunk(Data(": open\n\n".utf8))
        let backlog: Data = locked {
            var out = Data()
            if let after {
                if let first = _events.first, first.id > after + 1 {
                    out.append(Data("event: reset\ndata: {}\n\n".utf8))
                }
                for e in _events where e.id > after { out.append(Self.frame(id: e.id, type: e.type, data: e.data)) }
            }
            _feeds.append(c)
            return out
        }
        c.send(content: backlog.isEmpty ? head : head + Self.chunk(backlog), completion: .idempotent)
    }
}
