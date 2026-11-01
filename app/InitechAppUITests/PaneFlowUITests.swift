import XCTest

/// PaneFlowUITests taps through the real app: pin from Rigs, rename, reorder,
/// open a pane, send, and press Enter and Esc at a prompt. simctl has no tap,
/// so this is how those taps get driven at all.
///
/// With no environment they run against FixtureServer. Against a real
/// `initech app-serve`, app/scripts/live-ui-test.sh sets:
///   TEST_RUNNER_LIVE_PAIRING  the pairing link
///   TEST_RUNNER_LIVE_PANES    two panes of a running rig, "eng1,eng2"; the
///                             first takes the send and the prompts
///   TEST_RUNNER_SHOT_DIR      where the test asks the script for a prompt:
///                             it writes prompt-<key>.need, and prompt-<key>.pressed
///                             once the key is pressed, so the script can check
///                             the effect in the rig
final class PaneFlowUITests: XCTestCase {
    private var server: FixtureServer?
    private var link = ""
    private var paneA = "eng1"
    private var paneB = "eng2"
    private var dir = ""
    private var app: XCUIApplication!

    /// A live pane's state reaches the app on the service's 30 s poll.
    private var stateTimeout: TimeInterval { server == nil ? 90 : 10 }

    private static let promptLines = ["Do you want to create hello.txt?", "❯ 1. Yes", "  2. No"]

    override func setUp() async throws {
        continueAfterFailure = false
        let env = ProcessInfo.processInfo.environment
        if let live = env["LIVE_PAIRING"], !live.isEmpty {
            link = live
            let panes = (env["LIVE_PANES"] ?? "").split(separator: ",").map(String.init)
            guard panes.count == 2 else { throw XCTSkip("TEST_RUNNER_LIVE_PANES needs two panes") }
            (paneA, paneB) = (panes[0], panes[1])
            dir = env["SHOT_DIR"] ?? ""
            return
        }
        let s = try FixtureServer()
        try await s.start()
        s.status = status(a: "idle", banner: "none", screen: nil)
        s.setTranscript(rig: "initech", pane: paneA,
                        (1...120).map { Message(id: "m\($0)", at: nil, kind: .agent, text: "entry \($0) of the transcript", footer: nil, tool: nil) },
                        pageSize: 50)
        server = s
        link = s.pairingURL
    }

    override func tearDown() { server?.stop() }

    private func status(a state: String, banner: String, screen: [String]?) -> StatusReply {
        StatusReply(rigs: [RigStatus(rig: "initech", root: "/r/initech", readAt: nil, agents: [
            AgentStatus(role: paneA, state: state, alive: true, beads: nil, contextPercent: 20, banner: banner, screen: screen),
            AgentStatus(role: paneB, state: "idle", alive: true, beads: nil, contextPercent: 30, banner: "none", screen: nil),
        ])])
    }

    // MARK: helpers

    private func launch() {
        app = XCUIApplication()
        app.launchArguments = ["-uitest-pairing", link]
        app.launch()
        XCTAssertTrue(app.buttons["Rigs"].waitForExistence(timeout: 30), "home did not open")
    }

    private func element(_ text: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    /// row is a home row: every row's label ends "<pane> · <rig>".
    private func row(_ text: String) -> XCUIElement {
        app.descendants(matching: .any)
            .matching(NSPredicate(format: "label CONTAINS %@ AND label CONTAINS %@", text, " · ")).firstMatch
    }

    /// unpinAll empties the list, so a live run starts from a known place.
    private func unpinAll() {
        for _ in 0..<12 {
            let any = app.cells.containing(NSPredicate(format: "label CONTAINS %@", " · ")).firstMatch
            guard any.waitForExistence(timeout: 2) else { break }
            any.swipeLeft()
            guard app.buttons["Unpin"].waitForExistence(timeout: 3) else { break }
            app.buttons["Unpin"].tap()
            _ = any.waitForNonExistence(timeout: 3)
        }
        XCTAssertTrue(element("No pinned panes yet").waitForExistence(timeout: 10), "the list did not empty")
    }

    private func pinFromRigs(_ panes: [String]) {
        app.buttons["Rigs"].tap()
        for pane in panes {
            let pin = app.buttons["Pin \(pane)"]
            XCTAssertTrue(pin.waitForExistence(timeout: 10), "Rigs does not offer \(pane)")
            pin.tap()
            XCTAssertTrue(pin.waitForNonExistence(timeout: 10), "\(pane) stayed unpinned in Rigs")
        }
        app.buttons["Done"].tap()
    }

    private func rename(_ from: String, to: String) {
        let cell = row(from)
        XCTAssertTrue(cell.waitForExistence(timeout: 10), "no row for \(from)")
        cell.swipeLeft()
        XCTAssertTrue(app.buttons["Rename"].waitForExistence(timeout: 5))
        app.buttons["Rename"].tap()
        let field = app.alerts.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        field.tap()
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: from.count) + to)
        app.alerts.buttons["Save"].tap()
        XCTAssertTrue(row(to).waitForExistence(timeout: 10), "the new name did not show")
    }

    private func assertAbove(_ upper: String, _ lower: String, _ why: String, line: UInt = #line) {
        XCTAssertTrue(row(upper).waitForExistence(timeout: 10), "no row for \(upper)", line: line)
        XCTAssertTrue(row(lower).waitForExistence(timeout: 10), "no row for \(lower)", line: line)
        XCTAssertLessThan(row(upper).frame.minY, row(lower).frame.minY, why, line: line)
    }

    private func touch(_ name: String) {
        guard !dir.isEmpty else { return }
        FileManager.default.createFile(atPath: "\(dir)/\(name)", contents: nil)
    }

    // MARK: the flows

    /// AC 2: pin from Rigs, rename and reorder by tap; a relaunch reads the
    /// same list back from the Mac.
    func testRigsPinRenameReorder_PersistOnTheMac() throws {
        launch()
        unpinAll()
        pinFromRigs([paneA, paneB])
        assertAbove(paneA, paneB, "pins keep the order they were pinned in")

        rename(paneA, to: "first name")
        rename(paneB, to: "second name")

        app.buttons["Edit"].tap()
        let handles = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "Reorder"))
        XCTAssertTrue(handles.element(boundBy: 1).waitForExistence(timeout: 5), "no reorder handles in edit mode")
        handles.element(boundBy: 1).press(forDuration: 0.6, thenDragTo: handles.element(boundBy: 0))
        app.buttons["Done"].tap()
        assertAbove("second name", "first name", "the drag did not reorder the rows")

        if let server {
            XCTAssertEqual(server.pins.map(\.name), ["second name", "first name"], "the Mac does not hold the list")
            XCTAssertEqual(server.pins.map(\.order), [0, 1])
        }

        app.terminate()
        launch()
        assertAbove("second name", "first name", "a relaunch did not read the same list")
    }

    /// AC 3, AC 4: a pane opens from its row, scrolls back to its start (on the
    /// fixture, whose transcript has a known start) and a send shows its verdict.
    func testAPaneOpens_ScrollsBack_AndASendShowsItsVerdict() throws {
        launch()
        unpinAll()
        pinFromRigs([paneA])
        XCTAssertTrue(row(paneA).waitForExistence(timeout: 10))
        row(paneA).tap()

        let field = app.textFields["Message"].exists ? app.textFields["Message"] : app.textViews.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 15), "the pane did not open")

        if server != nil {
            XCTAssertTrue(element("entry 120 of the transcript").waitForExistence(timeout: 10), "the newest page did not show")
            let start = app.staticTexts["entry 1 of the transcript"]
            // Older entries load on a tap of "Show older messages" at the top:
            // the first tap brings in entries 21-70. (Known limit: the view then
            // shows the top of that page, not the row he tapped from.)
            let older = app.buttons["Show older messages"]
            for _ in 0..<20 where !older.isHittable { app.scrollViews.firstMatch.swipeDown(velocity: .fast) }
            XCTAssertTrue(older.isHittable, "no Show older messages at the top")
            XCTAssertFalse(app.staticTexts["entry 70 of the transcript"].exists, "an older page came in before the tap")
            older.tap()
            XCTAssertTrue(app.staticTexts["entry 21 of the transcript"].waitForExistence(timeout: 10), "the tap brought no older page")
            for _ in 0..<10 where !start.exists {
                for _ in 0..<20 where !older.isHittable { app.scrollViews.firstMatch.swipeDown(velocity: .fast) }
                if older.exists { older.tap() }
                _ = start.waitForExistence(timeout: 3)
            }
            for _ in 0..<20 where !start.isHittable { app.scrollViews.firstMatch.swipeDown(velocity: .fast) }
            XCTAssertTrue(start.exists, "scrolling up did not reach the transcript start")

            // A new entry must not pull him away from what he is reading.
            server?.emit("transcript", #"{"rig":"initech","pane":"\#(paneA)","message":{"id":"m121","kind":"agent","text":"entry 121 of the transcript"}}"#)
            Thread.sleep(forTimeInterval: 2)
            // KNOWN DEFECT, open (super's ruling on ini-r0q.2; its own P1 bead,
            // blocking the first TestFlight build): a new entry arriving while he
            // reads the start scrolls him away from it. Strict: once the fix
            // lands this check passes, the expected failure goes unmet, and the
            // test goes red until this block is removed.
            XCTExpectFailure("a new entry scrolls him away from what he reads back (known defect, ini-r0q.2)",
                             options: .init()) {
                XCTAssertTrue(start.isHittable, "a new entry scrolled him away from the start")
            }
        }

        field.tap()
        let text = "say hello from the ui test"
        field.typeText(text)
        app.buttons["Send"].tap()
        XCTAssertTrue(element(text).waitForExistence(timeout: 10), "the sent message did not show")
        let verdict = app.descendants(matching: .any).matching(NSPredicate(
            format: "label == 'Delivered' OR label CONTAINS 'deferred to' OR label CONTAINS 'not delivered' OR label CONTAINS 'not confirmed'")).firstMatch
        XCTAssertTrue(verdict.waitForExistence(timeout: 30), "no verdict on the message")
        if server != nil { XCTAssertEqual(verdict.label, "Delivered") }
    }

    /// AC 7: at a prompt the screen lines and the hint show; Esc and then Enter
    /// reach the pane and the prompt closes. Live, the script checks what each
    /// key did in the rig: Esc creates no file, Enter creates it.
    func testKeyStrip_EnterAndEsc_AtAPrompt() throws {
        launch()
        unpinAll()
        pinFromRigs([paneA])
        XCTAssertTrue(row(paneA).waitForExistence(timeout: 10))
        row(paneA).tap()
        XCTAssertTrue(app.buttons["Esc"].waitForExistence(timeout: 15), "the pane did not open")

        for (key, label) in [("esc", "Esc"), ("enter", "Enter")] {
            touch("prompt-\(key).need")
            if let server {
                server.status = status(a: "waiting", banner: "permission_prompt", screen: Self.promptLines)
                server.emit("pane_state", #"{"rig":"initech","pane":"\#(paneA)","from":"idle","to":"waiting","banner":"permission_prompt"}"#)
            }
            let hint = element("Enter says Yes and Esc says No")
            XCTAssertTrue(hint.waitForExistence(timeout: stateTimeout), "\(label): the pane never showed the prompt")
            XCTAssertTrue(element("Do you want").waitForExistence(timeout: 10), "\(label): no screen lines above the input")

            app.buttons[label].tap()
            XCTAssertTrue(element("\(label): key \(key) delivered to \(paneA)").waitForExistence(timeout: 15),
                          "\(label): the key's result did not show")
            touch("prompt-\(key).pressed")

            if let server {
                let bodies = server.requests.filter { $0.path == "/v1/keys/initech/\(paneA)" }
                    .map { String(decoding: $0.body, as: UTF8.self) }
                XCTAssertEqual(bodies.last, #"{"key":"\#(key)"}"#, "the service did not get that key")
                server.status = status(a: "idle", banner: "none", screen: nil)
                server.emit("pane_state", #"{"rig":"initech","pane":"\#(paneA)","from":"waiting","to":"idle","banner":"none"}"#)
            }
            XCTAssertTrue(hint.waitForNonExistence(timeout: stateTimeout), "\(label): the prompt did not close")
        }
    }
}
