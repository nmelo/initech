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
            XCTAssertTrue(start.isHittable, "a new entry scrolled him away from the start")
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

    // MARK: keeping his place (fixture only: the entries come from the fixture's feed)

    private func entry(_ n: Int) -> XCUIElement { app.staticTexts["entry \(n) of the transcript"] }

    /// emit sends n new entries to paneA's conversation, numbered from first.
    private func emit(_ n: Int, from first: Int) {
        for i in first..<(first + n) {
            server?.emit("transcript", #"{"rig":"initech","pane":"\#(paneA)","message":{"id":"m\#(i)","kind":"agent","text":"entry \#(i) of the transcript"}}"#)
        }
    }

    /// topEntry is the first entry line on screen and where it sits.
    private func topEntry() -> (label: String, y: CGFloat)? {
        let top = app.scrollViews.firstMatch.frame.minY
        return app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'entry '")).allElementsBoundByIndex
            .filter { $0.isHittable && $0.frame.minY >= top }
            .map { ($0.label, $0.frame.minY) }
            .min { $0.1 < $1.1 }
    }

    /// openPaneA opens paneA on its newest entry, with paneB pinned as well
    /// when reopening is what the test does (see reopenPaneA).
    private func openPaneA(toReopen: Bool = false) throws {
        if toReopen {
            server?.setTranscript(rig: "initech", pane: paneB,
                                  [Message(id: "o1", at: nil, kind: .agent, text: "the other pane", footer: nil, tool: nil)],
                                  pageSize: 50)
        }
        launch()
        unpinAll()
        pinFromRigs(toReopen ? [paneA, paneB] : [paneA])
        XCTAssertTrue(row(paneA).waitForExistence(timeout: 10))
        row(paneA).tap()
        XCTAssertTrue(entry(120).waitForExistence(timeout: 15), "the pane did not open on its newest entry")
    }

    /// Two screens up, new entries leave the line he reads where it is and show
    /// "3 new below"; a tap goes to the bottom and clears it; at the bottom the
    /// view follows and no line shows.
    func testNewEntries_KeepHisPlace_AndSayNewBelow() throws {
        try XCTSkipIf(server == nil, "fixture only")
        try openPaneA()
        app.scrollViews.firstMatch.swipeDown(velocity: .slow)
        app.scrollViews.firstMatch.swipeDown(velocity: .slow)
        let before = try XCTUnwrap(topEntry(), "no entry on screen two screens up")
        emit(3, from: 121)
        XCTAssertTrue(element("3 new below").waitForExistence(timeout: 10), "no new-below line")
        let after = try XCTUnwrap(topEntry())
        XCTAssertEqual(after.label, before.label, "the first line he was reading changed")
        XCTAssertEqual(after.y, before.y, accuracy: 4, "the line he was reading moved")
        XCTAssertFalse(entry(123).isHittable, "the view went to the new entries")

        element("3 new below").tap()
        XCTAssertTrue(entry(123).waitForExistence(timeout: 5))
        XCTAssertTrue(entry(123).isHittable, "the tap did not go to the bottom")
        XCTAssertFalse(element("new below").exists, "the line stayed after the tap")

        emit(1, from: 124)
        XCTAssertTrue(entry(124).waitForExistence(timeout: 10), "the entry did not arrive")
        Thread.sleep(forTimeInterval: 1)
        XCTAssertTrue(entry(124).isHittable, "at the bottom, the view did not follow")
        XCTAssertFalse(element("new below").exists, "a line showed at the bottom")
    }

    /// Scrolling back to the bottom by hand clears the line; a burst of 50 is
    /// one line with its count, and the view stays put.
    func testABurstOf50_IsOneLine_AndScrollingDownClearsIt() throws {
        try XCTSkipIf(server == nil, "fixture only")
        try openPaneA()
        app.scrollViews.firstMatch.swipeDown(velocity: .slow)
        let before = try XCTUnwrap(topEntry())
        emit(50, from: 121)
        Thread.sleep(forTimeInterval: 3)
        XCTAssertTrue(element("50 new below").waitForExistence(timeout: 15), "no single line with the count")
        XCTAssertEqual(try XCTUnwrap(topEntry()).label, before.label, "the burst moved the view")
        for _ in 0..<30 where element("new below").exists { app.scrollViews.firstMatch.swipeUp(velocity: .fast) }
        XCTAssertFalse(element("new below").exists, "reaching the bottom by hand did not clear the line")
    }

    /// assertNotEditingTheList fails if the pane list is in edit mode, where a
    /// tap on a row lands on its Reorder control and opens nothing.
    private func assertNotEditingTheList() {
        let reorder = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Reorder'"))
        XCTAssertEqual(reorder.count, 0, "the pane list is in edit mode")
    }

    /// reopenPaneA closes paneA and opens it again, each way that happens. On
    /// iPhone the back button goes to the list. On iPad the list stays beside
    /// the pane and there is no back button: paneA closes when another pane
    /// opens, so paneB opens first and paneA has to be gone.
    private func reopenPaneA() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            assertNotEditingTheList()
            row(paneB).tap()
            XCTAssertTrue(app.staticTexts["the other pane"].waitForExistence(timeout: 10), "the other pane did not open")
            XCTAssertFalse(entry(120).exists, "paneA did not leave")
        } else {
            app.navigationBars.buttons["BackButton"].tap()
        }
        XCTAssertTrue(row(paneA).waitForExistence(timeout: 10))
        assertNotEditingTheList()
        row(paneA).tap()
    }

    /// A pane opened again is at the bottom before anything scrolls, and
    /// nothing has yet reported what is in view: the first new entry is
    /// followed and no line shows.
    func testAReopenedPane_FollowsTheFirstNewEntry() throws {
        try XCTSkipIf(server == nil, "fixture only")
        try openPaneA(toReopen: true)
        reopenPaneA()
        XCTAssertTrue(entry(120).waitForExistence(timeout: 15), "the reopened pane shows nothing")
        emit(1, from: 121)
        XCTAssertTrue(entry(121).waitForExistence(timeout: 10), "the new entry is not on screen: the view did not follow it")
        Thread.sleep(forTimeInterval: 1)
        XCTAssertTrue(entry(121).isHittable, "a reopened pane did not follow the new entry")
        XCTAssertFalse(element("new below").exists, "a line showed at the bottom")
    }

    /// Closing the pane and opening it again opens it on its newest entry.
    func testReopening_OpensAtTheBottom() throws {
        try XCTSkipIf(server == nil, "fixture only")
        try openPaneA(toReopen: true)
        app.scrollViews.firstMatch.swipeDown(velocity: .slow)
        reopenPaneA()
        XCTAssertTrue(entry(120).waitForExistence(timeout: 15), "the reopened pane shows nothing")
        XCTAssertTrue(entry(120).isHittable, "reopening did not open at the bottom")
    }

    /// A feed reset while he reads back keeps his place; reopening the pane
    /// opens at the bottom.
    func testAReset_KeepsHisPlace_AndReopeningOpensAtTheBottom() throws {
        try XCTSkipIf(server == nil, "fixture only")
        try openPaneA(toReopen: true)
        app.scrollViews.firstMatch.swipeDown(velocity: .slow)
        let before = try XCTUnwrap(topEntry())
        server?.emit("reset", "{}")
        Thread.sleep(forTimeInterval: 2)
        XCTAssertEqual(try XCTUnwrap(topEntry()).label, before.label, "a reset moved him")

        reopenPaneA()
        XCTAssertTrue(entry(120).waitForExistence(timeout: 15))
        XCTAssertTrue(entry(120).isHittable, "reopening did not open at the bottom")
    }

    /// Paging back while entries arrive keeps his place: entries that came in
    /// before the older page are still counted, and entries after it leave the
    /// line he reads where it is.
    func testPagingBack_WhileEntriesArrive_KeepsHisPlace() throws {
        try XCTSkipIf(server == nil, "fixture only")
        try openPaneA()
        let older = app.buttons["Show older messages"]
        for _ in 0..<20 where !older.isHittable { app.scrollViews.firstMatch.swipeDown(velocity: .fast) }
        XCTAssertTrue(older.isHittable, "no Show older messages at the top")
        emit(2, from: 121)
        XCTAssertTrue(element("2 new below").waitForExistence(timeout: 10), "no new-below line before the page")
        older.tap()
        XCTAssertTrue(entry(21).waitForExistence(timeout: 10), "the tap brought no older page")
        Thread.sleep(forTimeInterval: 1)
        let before = try XCTUnwrap(topEntry(), "no entry on screen after paging back")
        emit(2, from: 123)
        XCTAssertTrue(element("4 new below").waitForExistence(timeout: 10), "the count did not carry across the page")
        let after = try XCTUnwrap(topEntry())
        XCTAssertEqual(after.label, before.label, "the first line he was reading changed")
        XCTAssertEqual(after.y, before.y, accuracy: 4, "the line he was reading moved")
        XCTAssertFalse(entry(124).isHittable, "the view went to the new entries")
    }

    /// A conversation shorter than a screen never shows the line.
    func testAShortConversation_NeverShowsTheLine() throws {
        let server = try XCTUnwrap(server, "fixture only")
        server.setTranscript(rig: "initech", pane: paneB,
                             (1...3).map { Message(id: "s\($0)", at: nil, kind: .agent, text: "short \($0)", footer: nil, tool: nil) },
                             pageSize: 50)
        launch()
        unpinAll()
        pinFromRigs([paneB])
        row(paneB).tap()
        XCTAssertTrue(app.staticTexts["short 3"].waitForExistence(timeout: 15))
        server.emit("transcript", #"{"rig":"initech","pane":"\#(paneB)","message":{"id":"s4","kind":"agent","text":"short 4"}}"#)
        XCTAssertTrue(app.staticTexts["short 4"].waitForExistence(timeout: 10))
        XCTAssertFalse(element("new below").exists, "a short conversation showed the line")
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
