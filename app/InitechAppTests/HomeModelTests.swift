import XCTest
@testable import InitechApp
@testable import InitechCore

@MainActor
final class HomeModelTests: XCTestCase {
    var server: FixtureServer!
    var service: HTTPService!

    override func setUp() async throws {
        server = try FixtureServer()
        try await server.start()
        service = HTTPService(pairing: server.pairing, retryDelay: .milliseconds(50))
        server.status = StatusReply(rigs: [
            Fx.rig("initech", [Fx.agent("super", state: "running", beads: ["ini-r0q"], context: 65),
                               Fx.agent("eng6", state: "idle", beads: [], context: 12)]),
            Fx.rig("theguy", [Fx.agent("super", state: "waiting", beads: nil, context: nil, banner: "dialog",
                                       screen: ["Do you want to proceed?", "❯ 1. Yes", "  2. No"])]),
        ])
        server.pins = [Pin(rig: "theguy", pane: "super", name: "theguy", order: 1),
                       Pin(rig: "initech", pane: "super", name: "hover super", order: 0)]
    }

    override func tearDown() { server.stop() }

    func testHome_ListsPinsInHisOrderWithNameStateContextAndBead() async {
        let home = HomeModel(service: service)
        await home.reload()
        XCTAssertEqual(home.rows.map(\.name), ["hover super", "theguy"])
        let first = home.rows[0]
        XCTAssertEqual(first.stateLabel, "working")
        XCTAssertEqual(first.contextPercent, 65)
        XCTAssertEqual(first.bead, "ini-r0q")
        XCTAssertEqual(home.rows[1].stateLabel, "dialog")
        XCTAssertTrue(home.rows[1].atDialog)
        XCTAssertEqual(home.rows[1].screen?.first, "Do you want to proceed?")
    }

    func testHome_UpdatesLiveWhenAPaneStateChanges() async throws {
        let home = HomeModel(service: service)
        await home.reload()
        home.startFeed()
        defer { home.stopFeed() }
        let srv = server!
        try await waitFor { srv.openFeeds == 1 }
        server.emit("pane_state", #"{"rig":"initech","pane":"super","from":"running","to":"idle","banner":"none"}"#)
        try await waitForMain { home.rows.first?.stateLabel == "idle" }
    }

    func testHome_ShowsAWaitingItemFromTheFeed() async throws {
        let home = HomeModel(service: service)
        await home.reload()
        home.startFeed()
        defer { home.stopFeed() }
        let srv = server!
        try await waitFor { srv.openFeeds == 1 }
        server.emit("waiting_item", #"{"rig":"initech","pane":"super","text":"▶ Ship v2.50?"}"#)
        try await waitForMain { home.waiting[PaneRef(rig: "initech", pane: "super")] == "▶ Ship v2.50?" }
    }

    private func blockersJSON(_ item: String?) -> String {
        let waiting = item.map { #"[{"pane":"super","text":"\#($0)","recommendation":""}]"# } ?? "[]"
        return #"[{"rig":"initech","waiting":\#(waiting),"stuck":[],"quiet":false}]"#
    }

    func testAWaitingItemThatDropsOff_ClearsFromItsRow() async {
        let ref = PaneRef(rig: "initech", pane: "super")
        let home = HomeModel(service: service)
        await home.reload()
        await home.apply(.waitingItem(rig: "initech", pane: "super", text: "▶ Ship it?"))
        XCTAssertEqual(home.waiting[ref], "▶ Ship it?")
        server.setRawGET("blockers", blockersJSON(nil))
        await home.apply(.blockers)
        XCTAssertNil(home.waiting[ref], "the item left the list and stayed on the row")
    }

    func testHome_ShowsWhatIsWaitingWhenItOpens() async {
        server.setRawGET("blockers", blockersJSON("▶ Ship it?"))
        let home = HomeModel(service: service)
        await home.reload()
        XCTAssertEqual(home.waiting[PaneRef(rig: "initech", pane: "super")], "▶ Ship it?")
    }

    func testABlockersReadThatFails_LeavesTheRowAsItWas() async {
        let ref = PaneRef(rig: "initech", pane: "super")
        let home = HomeModel(service: service)
        await home.reload()
        await home.apply(.waitingItem(rig: "initech", pane: "super", text: "▶ Ship it?"))
        await home.apply(.blockers) // the fixture answers 404: nothing says the item left
        XCTAssertEqual(home.waiting[ref], "▶ Ship it?")
        XCTAssertNil(home.problem, "home still works without the blockers route")
    }

    func testRigs_ListsOnlyUnpinnedPanes_AndOneTapPins() async {
        let home = HomeModel(service: service)
        await home.reload()
        XCTAssertEqual(home.rigRows.map(\.rig), ["initech", "theguy"])
        XCTAssertEqual(home.rigRows[0].panes.map(\.ref.pane), ["eng6"])
        XCTAssertEqual(home.rigRows[1].panes.count, 0)
        await home.pin(PaneRef(rig: "initech", pane: "eng6"))
        XCTAssertEqual(server.pins.last, Pin(rig: "initech", pane: "eng6", name: "eng6", order: 2))
        XCTAssertEqual(home.rows.map(\.name), ["hover super", "theguy", "eng6"])
    }

    func testPinRenameReorder_PersistOnTheMac_AndASecondDeviceSeesTheSame() async {
        let phone = HomeModel(service: service)
        await phone.reload()
        await phone.pin(PaneRef(rig: "initech", pane: "eng6"))
        await phone.rename(PaneRef(rig: "initech", pane: "eng6"), to: "  eng6 on the app  ")
        await phone.move(from: IndexSet(integer: 2), to: 0)
        XCTAssertEqual(phone.rows.map(\.name), ["eng6 on the app", "hover super", "theguy"])

        let ipad = HomeModel(service: HTTPService(pairing: server.pairing))
        await ipad.reload()
        XCTAssertEqual(ipad.rows.map(\.name), ["eng6 on the app", "hover super", "theguy"])
        XCTAssertEqual(server.pins.map(\.order), [0, 1, 2])
    }

    func testRename_ToBlankKeepsTheName() async {
        let home = HomeModel(service: service)
        await home.reload()
        await home.rename(PaneRef(rig: "initech", pane: "super"), to: "   ")
        XCTAssertEqual(home.rows[0].name, "hover super")
    }

    func testUnpin_RemovesTheRowOnTheMac() async {
        let home = HomeModel(service: service)
        await home.reload()
        await home.unpin(PaneRef(rig: "theguy", pane: "super"))
        XCTAssertEqual(server.pins, [Pin(rig: "initech", pane: "super", name: "hover super", order: 0)])
    }

    func testNoPins_IsAnEmptyList() async {
        server.pins = []
        let home = HomeModel(service: service)
        await home.reload()
        XCTAssertTrue(home.pins.isEmpty)
        XCTAssertTrue(home.rows.isEmpty)
        XCTAssertTrue(home.showsNoPinsLine)
    }

    func testARigThatWentAway_GreysItsRows() async {
        server.status = StatusReply(rigs: [server.status.rigs[0]]) // theguy stopped
        let home = HomeModel(service: service)
        await home.reload()
        let theguy = home.rows.first { $0.ref.rig == "theguy" }
        XCTAssertEqual(theguy?.gone, true)
        XCTAssertEqual(theguy?.stateLabel, "gone")
        XCTAssertEqual(home.rows.first { $0.ref.rig == "initech" }?.gone, false)
    }

    func testAResetFromTheFeed_ReloadsEverything() async {
        let home = HomeModel(service: service)
        await home.reload()
        server.pins = []
        await home.apply(.reset)
        XCTAssertTrue(home.rows.isEmpty)
    }

    func testOpen_SelectsAPaneFromOutsideTheList() async {
        let home = HomeModel(service: service)
        await home.reload()
        home.open(PaneRef(rig: "initech", pane: "eng6")) // not pinned
        XCTAssertEqual(home.selection, PaneRef(rig: "initech", pane: "eng6"))
        XCTAssertEqual(home.row(for: home.selection!)?.stateLabel, "idle")
    }

    func testARefusedCertificate_SaysSoAndDoesNotClaimNothingIsPinned() async throws {
        var p = server.pairing
        p.fingerprint = Data("some other certificate".utf8).sha256Base64URL
        let home = HomeModel(service: HTTPService(pairing: p))
        await home.reload()
        XCTAssertFalse(home.showsNoPinsLine, "the list failed to load; it is not known to be empty")
        let problem = try XCTUnwrap(home.problem)
        XCTAssertTrue(problem.contains("certificate"), problem)
        XCTAssertTrue(home.needsPairing, "a changed certificate must offer pairing again")
    }

    func testAMacThatDoesNotAnswer_ReadsPlainlyAndDoesNotAskToPair() async throws {
        let home = HomeModel(service: service)
        server.stop()
        await home.reload()
        XCTAssertFalse(home.showsNoPinsLine)
        let problem = try XCTUnwrap(home.problem)
        XCTAssertTrue(problem.hasPrefix("The Mac did not answer"), problem)
        XCTAssertFalse(problem.contains("unreachable("), problem)
        XCTAssertFalse(home.needsPairing)
    }

    func testARefusedToken_AsksToPairAgain() async {
        let home = HomeModel(service: HTTPService(pairing: { var p = server.pairing; p.token = "old"; return p }()))
        await home.reload()
        XCTAssertTrue(home.needsPairing)
    }
}

/// waitForMain polls a main-actor condition for at most two seconds.
@MainActor
func waitForMain(_ cond: () -> Bool, file: StaticString = #filePath, line: UInt = #line) async throws {
    let deadline = Date().addingTimeInterval(2)
    while !cond() {
        if Date() > deadline { XCTFail("condition not met in 2 s", file: file, line: line); return }
        try await Task.sleep(for: .milliseconds(10))
    }
}
