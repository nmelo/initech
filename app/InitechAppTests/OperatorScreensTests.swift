import XCTest
import SwiftUI
@testable import InitechApp

@MainActor
final class OperatorScreensTests: XCTestCase {
    var server: FixtureServer!
    var service: HTTPService!

    override func setUp() async throws {
        server = try FixtureServer()
        try await server.start()
        service = HTTPService(pairing: server.pairing, retryDelay: .milliseconds(50))
    }

    override func tearDown() { server.stop() }

    // AC1: a waiting item reaches the open Blockers list within one feed
    // event, over the real SSE path through HomeModel.
    func testAWaitingItemReachesTheOpenBlockersListThroughTheFeed() async throws {
        let home = HomeModel(service: service)
        let src = FakeBlockers([])
        let blockers = BlockersModel(source: src)
        home.listeners["blockers"] = blockers
        home.startFeed()
        defer { home.stopFeed() }
        let srv = server!
        try await waitFor { srv.openFeeds == 1 }
        src.answer = [RigBlockers(rig: "initech", waiting: [WaitingItem(pane: "super", text: "Your go", recommendation: "go")],
                                  stuck: [], quiet: false)]
        server.emit("waiting_item", #"{"rig":"initech","pane":"super","text":"Your go"}"#)
        try await waitForMain { blockers.sections.first?.rows.first?.title == "Your go" }
        XCTAssertEqual(src.calls, 1, "one event, one read")
    }

    // A closed sheet takes no events: its listener is gone.
    func testAClosedListTakesNoEvents() async {
        let home = HomeModel(service: service)
        let src = FakeBlockers([])
        home.listeners["blockers"] = BlockersModel(source: src)
        home.listeners["blockers"] = nil
        await home.apply(.stuck(rig: "a", pane: "b", reason: "c"))
        XCTAssertEqual(src.calls, 0)
    }

    // AC1: tapping a blocker opens its pane.
    func testATapOpensThePane() {
        let home = HomeModel(service: service)
        home.openBlocker(rig: "theguy", pane: "super")
        XCTAssertEqual(home.selection, PaneRef(rig: "theguy", pane: "super"))
    }

    // Reachability: the app's real service serves both views, and reads
    // /v1/blockers and /v1/progress with the token.
    func testTheRealServiceServesBothViews() async {
        let home = HomeModel(service: service)
        XCTAssertTrue(home.blockersSource is HTTPService)
        XCTAssertTrue(home.progressSource is HTTPService)
        await BlockersModel(source: home.blockersSource).reload()
        await ProgressModel(source: home.progressSource).reload()
        let asked = server.requests.filter { $0.path.hasPrefix("/v1/blockers") || $0.path.hasPrefix("/v1/progress") }
        XCTAssertEqual(asked.map(\.path), ["/v1/blockers", "/v1/progress"])
        XCTAssertEqual(Set(asked.map { $0.headers["authorization"] }), ["Bearer \(server.token)"])
    }

    // A service that serves neither shows why, instead of an empty list.
    func testAServiceWithoutTheEndpointsShowsWhy() async {
        let m = BlockersModel(source: Unserved())
        await m.reload()
        XCTAssertEqual(m.problem, "Blockers could not be read: this service does not serve it")
        let p = ProgressModel(source: Unserved())
        await p.reload()
        XCTAssertEqual(p.problem, "Progress could not be read: this service does not serve it")
    }
}
