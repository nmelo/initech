import XCTest
@testable import InitechApp

/// Fixtures for the shapes the app service sends, used across the app's tests.
enum Fx {
    static func agent(_ role: String, state: String? = "idle", beads: [String]? = [], context: Int? = 40,
                      banner: String? = "none", screen: [String]? = nil) -> AgentStatus {
        AgentStatus(role: role, state: state, alive: true, beads: beads, contextPercent: context, banner: banner, screen: screen)
    }
    static func rig(_ name: String, _ agents: [AgentStatus]) -> RigStatus {
        RigStatus(rig: name, root: "/r/\(name)", readAt: "2026-11-01T03:00:00Z", agents: agents)
    }
    static func msg(_ id: String, _ kind: Message.Kind, _ text: String, footer: [String]? = nil,
                    tool: Message.Tool? = nil) -> Message {
        Message(id: id, at: nil, kind: kind, text: text, footer: footer, tool: tool)
    }
}

final class ServiceClientTests: XCTestCase {
    var server: FixtureServer!
    var service: HTTPService!

    override func setUp() async throws {
        server = try FixtureServer()
        try await server.start()
        service = HTTPService(pairing: server.pairing, retryDelay: .milliseconds(50))
    }

    override func tearDown() { server.stop() }

    func testEveryRequestCarriesTheBearerToken() async throws {
        _ = try await service.rigs()
        _ = try await service.pins()
        XCTAssertEqual(server.requests.map { $0.headers["authorization"] },
                       Array(repeating: "Bearer \(server.token)", count: 2))
    }

    func testAWrongCertificatePin_IsRefusedBeforeAnyRequest() async {
        var p = server.pairing
        p.fingerprint = Data("some other certificate".utf8).sha256Base64URL
        do {
            _ = try await HTTPService(pairing: p).status()
            XCTFail("a server whose certificate does not match the pin was answered")
        } catch {
            guard case .unreachable = error as? ServiceError else { return XCTFail("\(error)") }
        }
        XCTAssertTrue(server.requests.isEmpty, "a request, and so the token, reached an unpinned server")
    }

    func testTheFeed_AsTheFirstRequestOfASession_IsPinnedAndOpens() async throws {
        var it = HTTPService(pairing: server.pairing).feed().makeAsyncIterator()
        let srv = server!
        try await waitFor { srv.openFeeds == 1 }
        server.emit("reset", "{}")
        let e = await it.next()
        XCTAssertEqual(e, .reset)
    }

    func testTheRightPin_IsAccepted() async throws {
        _ = try await service.status()
        XCTAssertEqual(server.requests.count, 1)
    }

    func testFingerprint_IsBase64URLSHA256OfTheCertificateDER() {
        // openssl x509 -in cert.pem -outform der | openssl dgst -sha256 -binary | base64url
        XCTAssertEqual(server.fingerprint, "VHB_Alrix0-c7Y54C8jARvt75FtDg9pE7fzwrcILHXs")
    }

    func testAWrongToken_IsUnauthorized() async {
        let wrong = HTTPService(pairing: { var p = server.pairing; p.token = "wrong"; return p }())
        do {
            _ = try await wrong.status()
            XCTFail("a wrong token was answered")
        } catch {
            XCTAssertEqual(error as? ServiceError, .unauthorized)
        }
    }

    func testStatus_DecodesTheStatusJSONShape() async throws {
        server.status = StatusReply(rigs: [Fx.rig("initech", [Fx.agent("eng1", state: "waiting", beads: ["ini-1"], context: 62,
                                                                      banner: "dialog", screen: ["Allow?", "1. Yes"])])])
        let s = try await service.status()
        XCTAssertEqual(s.rigs.first?.agents.first, server.status.rigs[0].agents[0])
    }

    func testStatus_ReadsNullFieldsAsAbsent() throws {
        // initech status --json prints every key, null when unread.
        let json = #"{"rigs":[{"rig":"r","root":"/r","read_at":null,"agents":[{"role":"a","host":"","state":null,"alive":false,"visible":true,"beads":null,"bead_note":"","context_percent":null,"background":{"shells":null,"monitors":null,"tasks":null},"banner":null,"banner_age_sec":null,"background_age_sec":null,"queued_count":0,"account":{"user":null,"model":null},"dnd":null,"dnd_age_sec":null,"screen":null}]}]}"#
        let s = try JSONDecoder.service.decode(StatusReply.self, from: Data(json.utf8))
        let a = s.rigs[0].agents[0]
        XCTAssertNil(a.state); XCTAssertNil(a.beads); XCTAssertNil(a.contextPercent); XCTAssertNil(a.screen)
    }

    func testSavePins_ReplacesTheListOnTheMac() async throws {
        let pins = [Pin(rig: "initech", pane: "super", name: "hover super", order: 0)]
        try await service.savePins(pins)
        XCTAssertEqual(server.pins, pins)
        let again = try await service.pins()
        XCTAssertEqual(again, pins)
    }

    func testConversation_PagesBackToTheStart() async throws {
        let all = (1...5).map { Fx.msg("m\($0)", .agent, "reply \($0)") }
        server.setTranscript(rig: "r", pane: "eng1", all, pageSize: 2)
        var page = try await service.conversation(rig: "r", pane: "eng1", before: nil, limit: 2)
        var seen = page.messages
        for _ in 0..<10 where !page.startReached {
            page = try await service.conversation(rig: "r", pane: "eng1", before: page.before, limit: 2)
            seen = page.messages + seen
        }
        XCTAssertEqual(seen, all)
    }

    func testSend_ReturnsTheSendPathsVerdictAndWords() async throws {
        server.setSendVerdict(rig: "r", pane: "eng1", SendResult(verdict: "held", words: "held: eng1 is in do-not-disturb"))
        let r = try await service.send(rig: "r", pane: "eng1", text: "hello 'quoted' $HOME")
        XCTAssertEqual(r.outcome, .held)
        XCTAssertEqual(r.words, "held: eng1 is in do-not-disturb")
        struct Body: Decodable, Equatable { var text: String; var enter: Bool }
        let body = try JSONDecoder().decode(Body.self, from: server.requests.last!.body)
        XCTAssertEqual(body, Body(text: "hello 'quoted' $HOME", enter: true), "the text reaches the service byte for byte")
    }

    func testKey_PostsTheKeyName() async throws {
        let r = try await service.key(rig: "r", pane: "eng1", key: .ctrlC)
        XCTAssertEqual(r.outcome, .delivered)
        XCTAssertEqual(server.requests.last?.path, "/v1/keys/r/eng1")
        XCTAssertEqual(String(decoding: server.requests.last!.body, as: UTF8.self), #"{"key":"ctrl-c"}"#)
    }

    func testVerdicts_GroupAsTheMessageShowsThem() {
        let cases: [(String, SendResult.Outcome)] = [
            ("delivered", .delivered), ("held", .held), ("deferred", .held),
            ("not_delivered", .notDelivered), ("refused", .notDelivered),
            ("unchecked", .unsure), ("unconfirmed", .unsure),
        ]
        for (v, want) in cases { XCTAssertEqual(SendResult(verdict: v, words: "").outcome, want, v) }
    }

    func testFeed_DeliversEventsAndReplaysAfterADrop() async throws {
        var it = service.feed().makeAsyncIterator()
        let srv = server!
        try await waitFor { srv.openFeeds == 1 }
        server.emit("pane_state", #"{"rig":"r","pane":"eng1","from":"running","to":"waiting","banner":"dialog"}"#)
        let first = await it.next()
        XCTAssertEqual(first, .paneState(rig: "r", pane: "eng1", from: "running", to: "waiting", banner: "dialog"))

        server.feedsPaused = true
        server.dropFeeds()
        // Missed while disconnected: replayed on reconnect via Last-Event-ID.
        server.emit("stuck", #"{"rig":"r","pane":"eng1","reason":"dialog"}"#)
        server.feedsPaused = false
        let second = await it.next()
        XCTAssertEqual(second, .stuck(rig: "r", pane: "eng1", reason: "dialog"))
        let reconnect = server.requests.last { $0.path == "/v1/feed" }
        XCTAssertEqual(reconnect?.headers["last-event-id"], "1")
    }

    func testFeed_SaysResetWhenTheGapIsBeyondTheReplayWindow() async throws {
        var it = service.feed().makeAsyncIterator()
        let srv = server!
        try await waitFor { srv.openFeeds == 1 }
        server.emit("waiting_item", #"{"rig":"r","pane":"super","text":"approve?"}"#)
        _ = await it.next()
        server.feedsPaused = true
        server.dropFeeds()
        server.emit("stuck", #"{"rig":"r","pane":"a","reason":"x"}"#)
        server.emit("stuck", #"{"rig":"r","pane":"b","reason":"x"}"#)
        server.forgetBefore(2) // event 2 aged out of the window
        server.feedsPaused = false
        let next = await it.next()
        XCTAssertEqual(next, .reset)
    }

    /// A short event reaches a consumer that is already waiting, while the
    /// stream stays open: the blank line that ends it must not be dropped.
    func testFeed_AShortEventArrivesWhileTheStreamStaysOpen() async throws {
        let svc: HTTPService = service
        let got = Task { () -> FeedEvent? in
            var it = svc.feed().makeAsyncIterator()
            return await it.next()
        }
        let srv = server!
        try await waitFor { srv.openFeeds == 1 }
        server.emit("stuck", #"{"rig":"r","pane":"a","reason":"x"}"#)
        let deadline = Task { try await Task.sleep(for: .seconds(2)); got.cancel() }
        let e = await got.value
        deadline.cancel()
        XCTAssertEqual(e, .stuck(rig: "r", pane: "a", reason: "x"))
        XCTAssertEqual(server.openFeeds, 1, "delivered without the connection dropping")
    }

    func testSSELines_KeepsBlankLinesAndDropsCR() async throws {
        let bytes = AsyncStream<UInt8> { c in
            for b in Array("id: 1\r\nevent: x\n\n: k\n".utf8) { c.yield(b) }
            c.finish()
        }
        var lines: [String] = []
        for try await l in SSELines(bytes: bytes) { lines.append(l) }
        XCTAssertEqual(lines, ["id: 1", "event: x", "", ": k"])
    }

    func testSSEParser_SkipsKeepalivesAndJoinsDataLines() {
        var p = SSEParser()
        XCTAssertNil(p.feed(": keepalive"))
        XCTAssertNil(p.feed(""))
        XCTAssertNil(p.feed("id: 7"))
        XCTAssertNil(p.feed("event: x"))
        XCTAssertNil(p.feed("data: a"))
        XCTAssertNil(p.feed("data: b"))
        XCTAssertEqual(p.feed(""), SSEParser.Raw(id: "7", type: "x", data: "a\nb"))
    }

    func testFeedEvent_UnknownTypeOrBadDataIsDropped() {
        XCTAssertNil(FeedEvent.decode(type: "something_new", data: "{}"))
        XCTAssertNil(FeedEvent.decode(type: "pane_state", data: "not json"))
    }
}

/// waitFor polls a condition the fixture sets from its own queue, for at most two seconds.
func waitFor(_ cond: @escaping @Sendable () -> Bool, file: StaticString = #filePath, line: UInt = #line) async throws {
    let deadline = Date().addingTimeInterval(2)
    while !cond() {
        if Date() > deadline { XCTFail("condition not met in 2 s", file: file, line: line); return }
        try await Task.sleep(for: .milliseconds(10))
    }
}
