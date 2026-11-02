import XCTest
@testable import InitechApp
@testable import InitechCore

/// PushRouteTests (ini-r0q.4 AC2, AC3, AC5, AC6): a push is routed from its
/// data alone, read from the payloads the service puts on the wire
/// (Contract/push-pane.json and push-bead.json, written by `go test ./cmd
/// -run TestAppContract -update-contract`); the foreground rule; and the
/// waiting rows a bead push scrolls to.
@MainActor
final class PushRouteTests: XCTestCase {
    private static let here = URL(fileURLWithPath: #filePath).deletingLastPathComponent()

    /// payload is a contract push as APNs hands it to the app: its JSON object.
    private func payload(_ name: String) throws -> [String: Any] {
        let data = try Data(contentsOf: Self.here.appendingPathComponent("Contract/\(name).json"))
        return try XCTUnwrap(try JSONSerialization.jsonObject(with: data) as? [String: Any], "\(name).json is not an object")
    }

    // AC2: the service's pane push routes to that pane, its bead push to the
    // bead at that rig.
    func testTheServicesTwoPushesRouteToThePaneAndTheBead() throws {
        XCTAssertEqual(PushRoute(userInfo: try payload("push-pane")), .pane(PaneRef(rig: "demo", pane: "eng1")))
        XCTAssertEqual(PushRoute(userInfo: try payload("push-bead")), .bead(rig: "demo", bead: "fx-1"))
    }

    // AC5: a push with no data, no rig, or neither a pane nor a nelson bead
    // opens home; a bead with any other reason is not a bead push.
    func testAPushWithNoUsableDataGoesHome() throws {
        let cases: [[AnyHashable: Any]] = [
            [:],
            ["aps": ["alert": ["title": "initech: demo", "body": "eng1"]]],
            ["data": "not an object"],
            ["data": ["pane": "eng1", "reason": "stuck"]],
            ["data": ["rig": "", "pane": "eng1"]],
            ["data": ["rig": "demo", "reason": "stuck"]],
            ["data": ["rig": "demo", "bead": "fx-1", "reason": "stuck"]],
            ["data": ["rig": "demo", "pane": 7]],
        ]
        for (i, info) in cases.enumerated() {
            XCTAssertEqual(PushRoute(userInfo: info), .home, "case \(i): \(info)")
        }
    }

    // AC6: whatever else a push carries (a body, an extra string in aps or in
    // data) never reaches the route, the only thing the app takes from it.
    func testExtraStringsInAPushNeverReachTheRoute() throws {
        for name in ["push-pane", "push-bead"] {
            var p = try payload(name)
            let bare = PushRoute(userInfo: p)
            var aps = try XCTUnwrap(p["aps"] as? [String: Any])
            var alert = try XCTUnwrap(aps["alert"] as? [String: Any])
            alert["body"] = "SECRET-BODY-TEXT"
            alert["subtitle"] = "SECRET-SUBTITLE"
            aps["alert"] = alert
            p["aps"] = aps
            var data = try XCTUnwrap(p["data"] as? [String: Any])
            data["text"] = "SECRET-DATA-TEXT"
            data["title"] = "SECRET-BEAD-TITLE"
            p["data"] = data
            let routed = PushRoute(userInfo: p)
            XCTAssertEqual(routed, bare, "\(name): an extra string changed the route")
            XCTAssertFalse(String(describing: routed).contains("SECRET"), "\(name): \(routed)")
        }
    }

    // AC3: the foreground rule. No banner for the pane on screen, or for a
    // bead while the Waiting on you list is on screen; a banner otherwise.
    func testTheForegroundRule() {
        let eng1 = PaneRef(rig: "demo", pane: "eng1"), eng2 = PaneRef(rig: "demo", pane: "eng2")
        let other = PaneRef(rig: "theguy", pane: "eng1")
        let bead = PushRoute.bead(rig: "demo", bead: "fx-1")
        let table: [(PushRoute, OnScreen, Bool)] = [
            (.pane(eng1), .pane(eng1), false),
            (.pane(eng1), .pane(eng2), true),
            (.pane(eng1), .pane(other), true),
            (.pane(eng1), .waitingList, true),
            (.pane(eng1), .other, true),
            (bead, .waitingList, false),
            (bead, .pane(eng1), true),
            (bead, .other, true),
            (.home, .other, true),
            (.home, .waitingList, true),
        ]
        for (route, screen, banner) in table {
            XCTAssertEqual(PushInbox.showsBanner(route, onScreen: screen), banner, "\(route) on \(screen)")
        }
    }

    // AC3: the inbox counts each arrival, so the screen refreshes with or
    // without a banner, and answers by what is on screen.
    func testTheInboxCountsArrivalsAndAnswersByTheScreen() {
        let inbox = PushInbox()
        let eng1 = PaneRef(rig: "demo", pane: "eng1")
        inbox.onScreen = .pane(eng1)
        XCTAssertFalse(inbox.present(.pane(eng1)))
        XCTAssertTrue(inbox.present(.pane(PaneRef(rig: "demo", pane: "eng2"))))
        XCTAssertEqual(inbox.arrived, 2)
        XCTAssertNil(inbox.tapped, "a push that only arrived was taken as a tap")
    }

    // AC2: a tap clears the last tap's line and leaves the route for MainView.
    func testATapReplacesTheLastNotice() {
        let inbox = PushInbox()
        inbox.notice = "eng9 is no longer running in demo."
        inbox.tap(.home)
        XCTAssertEqual(inbox.tapped, .home)
        XCTAssertNil(inbox.notice)
    }

    // AC2: the service's blockers list carries the bead, so its row is the
    // one a bead push scrolls to; a bead that left the list scrolls to its
    // rig, and with no such rig nothing moves.
    func testABeadPushScrollsToItsRowWhileListedElseToTheRig() {
        let rigs = [RigBlockers(rig: "demo", waiting: [
            WaitingItem(pane: "super", text: "Approve the v2.55 release", recommendation: "", bead: "fx-1"),
            WaitingItem(pane: "", text: "Pick the app icon", recommendation: "", bead: "fx-2"),
        ], stuck: [StuckPane(pane: "eng1", reason: "permission prompt open")], quiet: false)]
        let sections = BlockersModel.sections(rigs)
        let rows = sections[0].rows
        XCTAssertEqual(rows.map(\.bead), ["fx-1", "fx-2", nil])

        XCTAssertEqual(ScrollKey(focus: BlockerFocus(rig: "demo", bead: "fx-2"), sections: sections).target, rows[1].id)
        XCTAssertEqual(ScrollKey(focus: BlockerFocus(rig: "demo", bead: "fx-9"), sections: sections).target, rows[0].id,
                       "a bead that left the list did not fall back to its rig")
        XCTAssertNil(ScrollKey(focus: BlockerFocus(rig: "gone", bead: "fx-1"), sections: sections).target)
        XCTAssertNil(ScrollKey(focus: nil, sections: sections).target)
    }

    // A waiting bead nobody is assigned has no pane: its row opens nothing,
    // instead of a pane named "".
    func testAnUnassignedWaitingBeadHasNoPaneToOpen() {
        let rows = BlockersModel.sections([RigBlockers(rig: "demo", waiting: [
            WaitingItem(pane: "", text: "Pick the app icon", recommendation: "", bead: "fx-2"),
        ], stuck: [], quiet: false)])[0].rows
        XCTAssertEqual(rows.count, 1)
        XCTAssertNil(rows[0].pane)
        XCTAssertEqual(rows[0].bead, "fx-2")
    }

    // hasPane reads the last status: a running agent of that rig, never a dead one.
    func testHasPaneIsARunningAgentOfThatRig() async throws {
        let server = try FixtureServer()
        try await server.start()
        defer { server.stop() }
        var dead = Fx.agent("eng2")
        dead.alive = false
        server.status = StatusReply(rigs: [Fx.rig("demo", [Fx.agent("eng1"), dead])])
        let home = HomeModel(service: HTTPService(pairing: server.pairing, retryDelay: .milliseconds(50)))
        await home.reload()
        XCTAssertTrue(home.hasPane(PaneRef(rig: "demo", pane: "eng1")))
        XCTAssertFalse(home.hasPane(PaneRef(rig: "demo", pane: "eng2")), "a dead agent counts as running")
        XCTAssertFalse(home.hasPane(PaneRef(rig: "demo", pane: "eng9")))
        XCTAssertFalse(home.hasPane(PaneRef(rig: "theguy", pane: "eng1")))
    }
}
