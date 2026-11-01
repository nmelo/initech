import XCTest
@testable import InitechApp

/// ContractDecodingTests decodes, with the app's own models, the JSON the
/// service's own handlers wrote for every route the app reads (ini-470): the
/// files in Contract/ come from `go test ./cmd -run TestAppContract
/// -update-contract`, never from a hand. A type changed on either side goes
/// red here or in that Go test before a build reaches a device; the cursor
/// that cost every conversation on Nelson's devices (a number the app read
/// as a String) is the case this exists for.
final class ContractDecodingTests: XCTestCase {
    private static let here = URL(fileURLWithPath: #filePath).deletingLastPathComponent()

    /// fixture is one contract file: from the test bundle, or beside this
    /// source file when the bundle does not carry it.
    private func fixture(_ name: String, file: StaticString = #filePath, line: UInt = #line) throws -> Data {
        if let url = Bundle(for: Self.self).url(forResource: name, withExtension: "json") {
            return try Data(contentsOf: url)
        }
        return try Data(contentsOf: Self.here.appendingPathComponent("Contract/\(name).json"))
    }

    private func decode<T: Decodable>(_ type: T.Type, _ name: String) throws -> T {
        do {
            return try JSONDecoder.service.decode(T.self, from: try fixture(name))
        } catch {
            XCTFail("\(name).json does not decode as \(T.self): \(error)")
            throw error
        }
    }

    func testEveryRouteFixture_DecodesWithTheAppsModels() throws {
        let rigs = try decode([RigInfo].self, "rigs")
        XCTAssertFalse(rigs.isEmpty)

        for name in ["status"] {
            let s = try decode(StatusReply.self, name)
            let agents = s.rigs.flatMap(\.agents)
            XCTAssertFalse(agents.isEmpty, "\(name): no agents")
            XCTAssertTrue(agents.contains { $0.contextPercent != nil && $0.state != nil }, "\(name): no agent with its state and context")
        }
        XCTAssertFalse(try decode(RigStatus.self, "status-rig").agents.isEmpty)

        let blockers = try decode([RigBlockers].self, "blockers")
        XCTAssertTrue(blockers.contains { !$0.waiting.isEmpty } && blockers.contains { !$0.stuck.isEmpty },
                      "blockers: the fixture must carry both kinds")
        for name in ["progress", "progress-rig"] {
            XCTAssertFalse(try decode([ProgressReport].self, name).isEmpty, "\(name): no report")
        }
        for name in ["pins", "pins-put"] {
            XCTAssertFalse(try decode([Pin].self, name).isEmpty, "\(name): no pin")
        }
        for name in ["send", "keys"] {
            let r = try decode(SendResult.self, name)
            XCTAssertFalse(r.verdict.isEmpty || r.words.isEmpty, "\(name): verdict and words")
        }
        struct Devices: Decodable { var devices: Int }
        XCTAssertGreaterThan(try decode(Devices.self, "devices-put").devices, 0)

        let none = try decode(Conversation.self, "conversation-no-transcript")
        XCTAssertTrue(none.startReached)
        XCTAssertTrue(none.messages.isEmpty)
    }

    func testTheConversationPages_PageBackAsTheServiceWroteThem() async throws {
        let pages = try (1...4).map { try decode(Conversation.self, "conversation-page-\($0)") }
        XCTAssertFalse(pages[0].messages.isEmpty, "the newest page carries messages")
        XCTAssertTrue(pages.dropLast().allSatisfy { !$0.startReached && ($0.before ?? 0) > 0 },
                      "every page before the start carries a positive integer cursor")
        XCTAssertTrue(pages[3].startReached)
        XCTAssertEqual(pages[3].before, 0)

        // The fixture server answers with the files themselves, keyed by the
        // cursor the app sends back: the real client pages through them.
        let server = try FixtureServer()
        try await server.start()
        defer { server.stop() }
        var byBefore = ["": try fixture("conversation-page-1")]
        for i in 0..<3 { byBefore[String(pages[i].before!)] = try fixture("conversation-page-\(i + 2)") }
        server.servePages(rig: "demo", pane: "eng1", byBefore)

        let service = HTTPService(pairing: server.pairing)
        var page = try await service.conversation(rig: "demo", pane: "eng1", before: nil, limit: 50)
        var seen = page.messages
        for _ in 0..<10 where !page.startReached {
            page = try await service.conversation(rig: "demo", pane: "eng1", before: page.before, limit: 50)
            seen = page.messages + seen
        }
        XCTAssertTrue(page.startReached, "paging never reached the start")
        XCTAssertEqual(seen, pages.reversed().flatMap(\.messages))
        let sent = server.requests.compactMap { $0.query["before"] }
        XCTAssertEqual(sent, pages.dropLast().map { String($0.before!) }, "the cursor did not go back as given")
    }

    func testEveryFeedEventFixture_Decodes() throws {
        for type in ["pane_state", "stuck", "waiting_item", "transcript", "blockers", "reset"] {
            let data = String(decoding: try fixture("feed-\(type)"), as: UTF8.self)
            XCTAssertNotNil(FeedEvent.decode(type: type, data: data), "feed-\(type).json does not decode as the app's FeedEvent")
        }
        guard case let .transcript(_, _, m)? = FeedEvent.decode(type: "transcript", data: String(decoding: try fixture("feed-transcript"), as: UTF8.self)) else {
            return XCTFail("feed-transcript.json is not a transcript event")
        }
        XCTAssertFalse(m.text.isEmpty)
    }

    /// The guard fires: the service's own page with its cursor as a String,
    /// the shape the app used to expect, must not decode.
    func testACursorThatIsAString_IsRefused() throws {
        let page = String(decoding: try fixture("conversation-page-1"), as: UTF8.self)
        let before = try XCTUnwrap(page.range(of: #""before": \d+"#, options: .regularExpression), "page-1 has no integer before")
        let asString = page.replacingCharacters(in: before, with: #""before": "1303""#)
        XCTAssertThrowsError(try JSONDecoder.service.decode(Conversation.self, from: Data(asString.utf8)))
    }

    /// Every route the app's code names has its fixture: a route added to the
    /// app without one goes red here.
    func testEveryRouteTheAppCalls_HasAFixture() throws {
        let sources = Self.here.deletingLastPathComponent().appendingPathComponent("InitechApp").resolvingSymlinksInPath()
        let files = try FileManager.default.contentsOfDirectory(at: sources, includingPropertiesForKeys: nil)
            .filter { $0.pathExtension == "swift" }
        XCTAssertFalse(files.isEmpty, "no app sources at \(sources.path)")
        var routes = Set<String>()
        let pattern = try NSRegularExpression(pattern: #""/v1/([a-z_]+)"#)
        for f in files {
            let text = try String(contentsOf: f, encoding: .utf8)
            for m in pattern.matches(in: text, range: NSRange(text.startIndex..., in: text)) {
                routes.insert(String(text[Range(m.range(at: 1), in: text)!]))
            }
        }
        XCTAssertTrue(routes.isSuperset(of: ["rigs", "status", "conversation", "feed"]), "route scan found too little: \(routes.sorted())")
        let fixtures = try FileManager.default.contentsOfDirectory(atPath: Self.here.appendingPathComponent("Contract").path)
            .map { ($0 as NSString).deletingPathExtension }
        for r in routes.sorted() {
            XCTAssertTrue(fixtures.contains { $0 == r || $0.hasPrefix(r + "-") },
                          "the app calls /v1/\(r) and Contract/ has no fixture for it")
        }
    }
}
