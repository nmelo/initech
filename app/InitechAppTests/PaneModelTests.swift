import XCTest
@testable import InitechApp

@MainActor
final class PaneModelTests: XCTestCase {
    var server: FixtureServer!
    var service: HTTPService!
    let ref = PaneRef(rig: "initech", pane: "eng1")

    override func setUp() async throws {
        server = try FixtureServer()
        try await server.start()
        service = HTTPService(pairing: server.pairing, retryDelay: .milliseconds(50))
    }

    override func tearDown() { server.stop() }

    func testConversation_ScrollsBackToTheTranscriptStart() async {
        let all = (1...120).map { Fx.msg("m\($0)", $0 % 3 == 0 ? .tool : .agent, "entry \($0)") }
        server.setTranscript(rig: ref.rig, pane: ref.pane, all, pageSize: PaneModel.pageSize)
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        XCTAssertEqual(pane.messages.count, 50)
        XCTAssertEqual(pane.messages.last?.id, "m120", "the newest page comes first")
        for _ in 0..<10 where !pane.startReached { await pane.loadOlder() }
        XCTAssertEqual(pane.messages, all)
        await pane.loadOlder()
        XCTAssertEqual(pane.messages.count, 120, "nothing past the start")
    }

    func testConversation_KeepsTheShapes() async {
        let msgs = [Fx.msg("1", .nelson, "status?"),
                    Fx.msg("2", .tool, "Bash", tool: .init(name: "Bash", summary: "git log -1", detail: "abc123 fix")),
                    Fx.msg("3", .agent, "All landed.", footer: ["▶ Ship it?"])]
        server.setTranscript(rig: ref.rig, pane: ref.pane, msgs, pageSize: 50)
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        XCTAssertEqual(pane.messages, msgs)
    }

    func testEmptyTranscript_LoadsWithNothing() async {
        let pane = PaneModel(service: service, ref: PaneRef(rig: "initech", pane: "fresh"))
        await pane.load()
        XCTAssertTrue(pane.loaded)
        XCTAssertTrue(pane.messages.isEmpty)
        XCTAssertTrue(pane.startReached)
    }

    func testFeed_AppendsNewEntriesForThisPaneOnly() async throws {
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        pane.startFeed()
        defer { pane.stopFeed() }
        let srv = server!
        try await waitFor { srv.openFeeds == 1 }
        server.emit("transcript", #"{"rig":"initech","pane":"other","message":{"id":"x","kind":"agent","text":"not mine"}}"#)
        server.emit("transcript", #"{"rig":"initech","pane":"eng1","message":{"id":"n1","kind":"agent","text":"new reply","footer":["▶ ok?"]}}"#)
        try await waitForMain { pane.messages.count == 1 }
        XCTAssertEqual(pane.messages.first?.text, "new reply")
        XCTAssertEqual(pane.messages.first?.footer, ["▶ ok?"])
    }

}
