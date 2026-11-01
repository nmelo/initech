import XCTest

/// PushTapUITests drives the built app's push handling (ini-r0q.4 AC2, AC3,
/// AC5, AC6), paired with a fixture server in this process.
///
/// A tap that opens the app comes through `-uitest-push-tap <payload>`, fed to
/// the same inbox the notification delegate feeds: a test cannot tap a banner
/// before the app it opens exists. A push while the app is open is really
/// delivered: the test drops the payload in PUSH_DIR, and sim-test.sh's relay
/// hands it to `xcrun simctl push`, so iOS calls the app's delegate and
/// SpringBoard shows or does not show the banner.
final class PushTapUITests: XCTestCase {
    var server: FixtureServer!
    var app: XCUIApplication!
    var springboard: XCUIApplication { XCUIApplication(bundleIdentifier: "com.apple.springboard") }

    private static let panePush = #"{"aps":{"alert":{"title":"initech: initech","body":"eng1"},"sound":"default","thread-id":"initech"},"data":{"rig":"initech","pane":"eng1","reason":"stuck"}}"#
    private static let beadPush = #"{"aps":{"alert":{"title":"initech: initech","body":"fx-2"},"sound":"default","thread-id":"initech"},"data":{"rig":"initech","bead":"fx-2","reason":"nelson"}}"#

    /// blockers is GET /v1/blockers: footer items, then two beads waiting on
    /// Nelson (fx-2 unassigned). With no footer items every row fits on
    /// screen; with fourteen, any screen must scroll to fx-2.
    private func blockers(listing beads: [String] = ["fx-1", "fx-2"], footer count: Int = 0) -> String {
        let footer = (0..<count).map { $0 + 1 }.map { #"{"pane":"super","text":"Footer question \#($0)","recommendation":""}"# }
        let waiting = footer + beads.map {
            $0 == "fx-1" ? #"{"pane":"super","text":"Approve the v2.55 release","recommendation":"","bead":"fx-1"}"#
                         : #"{"pane":"","text":"Pick the app icon","recommendation":"","bead":"fx-2"}"#
        }
        return #"[{"rig":"theguy","waiting":[],"stuck":[],"quiet":true},{"rig":"initech","waiting":["# + waiting.joined(separator: ",")
            + #"],"stuck":[{"pane":"eng1","reason":"permission prompt open"}],"quiet":false}]"#
    }

    override func setUp() async throws {
        continueAfterFailure = false
        server = try FixtureServer()
        try await server.start()
        server.status = StatusReply(rigs: [RigStatus(rig: "initech", root: "/r/initech", readAt: nil, agents: [
            AgentStatus(role: "eng1", state: "waiting", alive: true, beads: nil, contextPercent: 30, banner: "dialog", screen: nil),
            AgentStatus(role: "eng2", state: "idle", alive: true, beads: nil, contextPercent: 20, banner: "none", screen: nil),
        ])])
        server.pins = [Pin(rig: "initech", pane: "eng1", name: "eng1 pinned", order: 0),
                       Pin(rig: "initech", pane: "eng2", name: "eng2 pinned", order: 1)]
        server.setTranscript(rig: "initech", pane: "eng1", [
            Message(id: "1", at: nil, kind: .agent, text: "eng1 transcript line", footer: nil, tool: nil),
        ], pageSize: 50)
        server.setTranscript(rig: "initech", pane: "eng2", [
            Message(id: "1", at: nil, kind: .agent, text: "eng2 transcript line", footer: nil, tool: nil),
        ], pageSize: 50)
        server.setRawGET("blockers", blockers())
    }

    override func tearDown() { server.stop() }

    /// launch opens the app, with a tapped push when given one.
    private func launch(tapped payload: String? = nil, realPermission: Bool = false) {
        app = XCUIApplication()
        app.launchArguments = ["-uitest-pairing", server.pairingURL]
        if !realPermission { app.launchArguments += ["-uitest-push-permission", "allowed", "-uitest-push-token", "0ab1ff"] }
        if let payload { app.launchArguments += ["-uitest-push-tap", payload] }
        app.launch()
    }

    private func element(_ text: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    private func gets(_ path: String) -> Int {
        server.requests.filter { $0.method == "GET" && $0.path == path }.count
    }

    // AC2: a pane push opens that pane's screen.
    func testATappedPanePushOpensThePane() {
        launch(tapped: Self.panePush)
        XCTAssertTrue(element("eng1 transcript line").waitForExistence(timeout: 20), "the tap did not open eng1")
        XCTAssertFalse(app.otherElements["push-notice"].exists)
    }

    // AC2: a pane that is gone opens home, with one line saying so.
    func testATappedPushForAGonePaneOpensHomeWithOneLine() {
        launch(tapped: Self.panePush.replacingOccurrences(of: #""pane":"eng1""#, with: #""pane":"eng9""#))
        XCTAssertTrue(element("eng9 is no longer running in initech.").waitForExistence(timeout: 20), "no line for the gone pane")
        XCTAssertTrue(element("eng1 pinned").exists, "home is not showing")
        XCTAssertFalse(element("eng1 transcript line").exists, "a pane opened for a gone pane")
    }

    // AC2: a bead push opens Blockers at that rig with the bead's row in view.
    func testATappedBeadPushOpensWaitingOnYouWithTheBeadInView() {
        // Control: opened by hand, the list starts at the top and fx-2 is out
        // of view, so only the push's scroll can bring it in.
        server.setRawGET("blockers", blockers(footer: 14))
        launch()
        XCTAssertTrue(element("eng1 pinned").waitForExistence(timeout: 20))
        app.buttons["Blockers"].tap()
        XCTAssertTrue(element("Footer question 1").waitForExistence(timeout: 10), "Blockers did not open")
        RunLoop.current.run(until: Date().addingTimeInterval(1))
        XCTAssertTrue(inView(element("Footer question 1")), "control: the first row is not in view, so in-view reads nothing")
        XCTAssertFalse(inView(beadRow("fx-2")), "control: fx-2 is in view without a push, so this test proves no scroll")
        app.terminate()

        launch(tapped: Self.beadPush)
        XCTAssertTrue(beadRow("fx-2").waitForExistence(timeout: 20), "Blockers did not open with the bead's row")
        let end = Date().addingTimeInterval(10)
        while !inView(beadRow("fx-2")) && Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.25)) }
        XCTAssertTrue(inView(beadRow("fx-2")), "the bead's row is listed but not in view: \(beadRow("fx-2").frame) in \(app.windows.firstMatch.frame)")
        XCTAssertTrue(element("Pick the app icon").exists, "the bead's title is not the list's")
    }

    /// beadRow is the row's first element: SwiftUI hands the row's
    /// identifier to its icon and texts too, so the query matches several.
    private func beadRow(_ bead: String) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: "waiting-initech-\(bead)").firstMatch
    }

    /// inView is whether e's middle lies inside the window: a row the list
    /// built but scrolled away fails, whether or not it is a button.
    private func inView(_ e: XCUIElement) -> Bool {
        guard e.exists, !e.frame.isEmpty else { return false }
        return app.windows.firstMatch.frame.contains(CGPoint(x: e.frame.midX, y: e.frame.midY))
    }


    // AC2: a bead that left waiting_on_nelson since: the list as it stands,
    // no error line.
    func testATappedBeadPushForABeadThatLeftShowsTheListAsItStands() {
        server.setRawGET("blockers", blockers(listing: ["fx-1"]))
        launch(tapped: Self.beadPush)
        XCTAssertTrue(app.descendants(matching: .any)["waiting-initech-fx-1"].waitForExistence(timeout: 20), "the list did not open")
        XCTAssertFalse(app.descendants(matching: .any)["waiting-initech-fx-2"].exists)
        for words in ["could not", "no longer", "not found", "error"] {
            XCTAssertFalse(element(words).exists, "an error line (\(words)) for a bead that left the list")
        }
    }

    // AC5: a push with no data opens home.
    func testATappedPushWithNoDataOpensHome() {
        launch(tapped: #"{"aps":{"alert":{"title":"initech: initech","body":"eng1"}}}"#)
        XCTAssertTrue(element("eng1 pinned").waitForExistence(timeout: 20), "home did not open")
        XCTAssertFalse(element("eng1 transcript line").exists)
        XCTAssertFalse(app.descendants(matching: .any)["waiting-initech-fx-1"].exists)
    }

    // AC6: nothing in a push but the route reaches the screen, for either shape.
    func testNoTextFromAPushIsShownButThePaneOrTheBead() {
        for push in [Self.panePush, Self.beadPush] {
            let loud = push
                .replacingOccurrences(of: #""thread-id""#, with: #""subtitle":"SECRET-SUBTITLE","thread-id""#)
                .replacingOccurrences(of: #""reason":"#, with: #""text":"SECRET-DATA-TEXT","title":"SECRET-TITLE","reason":"#)
                .replacingOccurrences(of: #""body":"eng1""#, with: #""body":"eng1 SECRET-BODY""#)
                .replacingOccurrences(of: #""body":"fx-2""#, with: #""body":"fx-2 SECRET-BODY""#)
            launch(tapped: loud)
            XCTAssertTrue(element(push == Self.panePush ? "eng1 transcript line" : "Pick the app icon").waitForExistence(timeout: 20))
            XCTAssertFalse(element("SECRET").exists, "text from the push is on screen")
            app.terminate()
        }
    }

    // MARK: really delivered pushes (AC3)

    /// deliver drops a payload for sim-test.sh's relay, which runs
    /// `xcrun simctl push` with it, and waits for the relay to take it.
    private func deliver(_ payload: String) throws {
        guard let dir = ProcessInfo.processInfo.environment["PUSH_DIR"], !dir.isEmpty else {
            return XCTFail("PUSH_DIR is not set: run under app/scripts/sim-test.sh, whose relay delivers the push")
        }
        let body = payload.replacingOccurrences(of: #"{"aps":"#, with: #"{"Simulator Target Bundle":"co.nmelo.initech","aps":"#)
        let file = URL(fileURLWithPath: dir).appendingPathComponent("\(UUID().uuidString).apns")
        try Data(body.utf8).write(to: file)
        let sent = file.appendingPathExtension("sent")
        let end = Date().addingTimeInterval(20)
        while !FileManager.default.fileExists(atPath: sent.path) && Date() < end { Thread.sleep(forTimeInterval: 0.2) }
        XCTAssertTrue(FileManager.default.fileExists(atPath: sent.path), "the relay did not deliver \(file.lastPathComponent)")
    }

    private var banner: XCUIElement {
        springboard.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "initech: initech")).firstMatch
    }

    /// allow answers iOS's own notification question, which a real-permission
    /// launch asks, so iOS will show this app's banners.
    private func launchAllowingNotifications() {
        launch(realPermission: true)
        let allow = springboard.buttons["Allow"]
        if allow.waitForExistence(timeout: 15) { allow.tap() }
        XCTAssertTrue(element("eng1 pinned").waitForExistence(timeout: 20), "home did not open")
    }

    // AC3: a pane push for the pane on screen: no banner, the pane refreshes;
    // for another pane: a banner.
    func testAPanePushWhileOpenShowsABannerOnlyForAnotherPane() throws {
        launchAllowingNotifications()
        element("eng1 pinned").tap()
        XCTAssertTrue(element("eng1 transcript line").waitForExistence(timeout: 10))
        RunLoop.current.run(until: Date().addingTimeInterval(1))
        let before = gets("/v1/status")

        try deliver(Self.panePush)
        let end = Date().addingTimeInterval(10)
        while gets("/v1/status") == before && Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.2)) }
        XCTAssertGreaterThan(gets("/v1/status"), before, "the on-screen pane did not refresh")
        XCTAssertFalse(banner.waitForExistence(timeout: 5), "a banner for the pane already on screen")

        try deliver(Self.panePush.replacingOccurrences(of: #""pane":"eng1""#, with: #""pane":"eng2""#)
            .replacingOccurrences(of: #""body":"eng1""#, with: #""body":"eng2""#))
        XCTAssertTrue(banner.waitForExistence(timeout: 15), "no banner for another pane")
    }

    // AC3: a bead push while Waiting on you is open: no banner, the list
    // reads again; anywhere else: a banner.
    func testABeadPushWhileOpenShowsABannerOnlyAwayFromTheList() throws {
        launchAllowingNotifications()
        app.buttons["Blockers"].tap()
        XCTAssertTrue(app.descendants(matching: .any)["waiting-initech-fx-1"].waitForExistence(timeout: 10))
        RunLoop.current.run(until: Date().addingTimeInterval(1))
        let before = gets("/v1/blockers")

        try deliver(Self.beadPush)
        let end = Date().addingTimeInterval(10)
        while gets("/v1/blockers") == before && Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.2)) }
        XCTAssertGreaterThan(gets("/v1/blockers"), before, "the open list did not read again")
        XCTAssertFalse(banner.waitForExistence(timeout: 5), "a banner while the list is on screen")

        app.buttons["Done"].tap()
        XCTAssertTrue(element("eng1 pinned").waitForExistence(timeout: 5))
        try deliver(Self.beadPush)
        XCTAssertTrue(banner.waitForExistence(timeout: 15), "no banner for a bead push away from the list")
    }
}
