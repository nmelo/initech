import XCTest

/// BlockersUITests drives the real app, paired with a fixture server in this
/// process: the Blockers and Progress buttons on the Panes screen, the real
/// SSE feed into an open sheet, and a real tap on a blocker row.
final class BlockersUITests: XCTestCase {
    var server: FixtureServer!
    var app: XCUIApplication!

    /// blockers is GET /v1/blockers with the given waiting items after "Your go".
    private func blockers(_ more: String = "") -> String {
        """
        [{"rig":"initech","waiting":[{"pane":"super","text":"Your go for the patch release","recommendation":"ship it, it is small"}\(more)],\
        "stuck":[{"pane":"eng6","reason":"permission prompt open"}],"quiet":false},\
        {"rig":"theguy","waiting":[],"stuck":[],"quiet":true}]
        """
    }

    override func setUp() async throws {
        continueAfterFailure = false
        server = try FixtureServer()
        try await server.start()
        server.status = StatusReply(rigs: [
            RigStatus(rig: "initech", root: "/r/initech", readAt: nil, agents: [
                AgentStatus(role: "super", state: "running", alive: true, beads: nil, contextPercent: 65, banner: "none", screen: nil),
                AgentStatus(role: "eng6", state: "idle", alive: true, beads: nil, contextPercent: 31, banner: "none", screen: nil),
            ]),
        ])
        server.pins = [Pin(rig: "initech", pane: "eng6", name: "eng6 on the app", order: 0)]
        server.setTranscript(rig: "initech", pane: "super", [
            Message(id: "1", at: nil, kind: .agent, text: "The reply behind the blocker", footer: nil, tool: nil),
        ], pageSize: 50)
        server.setRawGET("blockers", blockers())
        server.setRawGET("progress", """
        [{"rig":"initech","prefix":"ini-r0q.","hours":3,"beads":[],"closed":4,"total":11,"worked_by_engineers":3,\
        "writing_review_only":1,"closed_recent":[{"id":"a","title":"a"},{"id":"b","title":"b"}],"critical_path":[],\
        "engineers":["eng5"],"quiet":[]}]
        """)
    }

    override func tearDown() { server.stop() }

    private func launch() {
        app = XCUIApplication()
        app.launchArguments = ["-uitest-pairing", server.pairingURL]
        app.launch()
        XCTAssertTrue(element("eng6 on the app").waitForExistence(timeout: 20), "home did not show the pins")
    }

    // AC1, AC2: both kinds show with their words; an item comes and goes on
    // the feed alone; a real tap opens the pane and closes the sheet.
    func testABlockerFollowsTheFeedAndATapOpensItsPane() throws {
        launch()
        app.buttons["Blockers"].tap()
        XCTAssertTrue(element("Your go for the patch release").waitForExistence(timeout: 10))
        XCTAssertTrue(element("Recommendation: ship it, it is small").exists)
        XCTAssertTrue(element("permission prompt open").exists)
        XCTAssertTrue(element("Nothing waiting, nothing stuck").exists, "the quiet rig has no quiet row")

        try waitForFeed()
        server.setRawGET("blockers", blockers(#",{"pane":"super","text":"Keep the old link?","recommendation":""}"#))
        server.emit("waiting_item", #"{"rig":"initech","pane":"super","text":"Keep the old link?"}"#)
        XCTAssertTrue(element("Keep the old link?").waitForExistence(timeout: 10), "a new item did not arrive on the feed")

        // A later reply is the only event when its footer drops a line.
        server.setRawGET("blockers", blockers())
        server.emit("transcript", #"{"rig":"initech","pane":"super","message":{"id":"2","kind":"agent","text":"later"}}"#)
        waitGone(element("Keep the old link?"), "a dropped item stayed")

        app.buttons["blocker-initech-super"].firstMatch.tap()
        XCTAssertTrue(element("The reply behind the blocker").waitForExistence(timeout: 10), "the tap did not open the pane")
        XCTAssertFalse(app.buttons["blocker-initech-eng6"].exists, "the sheet stayed open over the pane")
    }

    // AC3: the Progress button shows the service's numbers, and Done closes it.
    func testProgressOpensWithTheServiceNumbers() {
        launch()
        app.buttons["Progress"].tap()
        XCTAssertTrue(element("4 of 11 closed · 2 in the last 3 h").waitForExistence(timeout: 10))
        XCTAssertTrue(element("worked by engineers 3 · writing or review only 1").exists)
        app.buttons["Done"].tap()
        XCTAssertTrue(element("eng6 on the app").waitForExistence(timeout: 5), "Done did not close the sheet")
    }

    private func element(_ text: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    private func waitForFeed() throws {
        let deadline = Date().addingTimeInterval(10)
        while server.openFeeds == 0 {
            if Date() > deadline { return XCTFail("the app did not open the feed") }
            Thread.sleep(forTimeInterval: 0.05)
        }
    }

    private func waitGone(_ e: XCUIElement, _ why: String) {
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: e)
        XCTAssertEqual(XCTWaiter().wait(for: [gone], timeout: 10), .completed, why)
    }
}
