import XCTest

/// ScreenshotTests drives the real app, paired with a fixture server in this
/// process, to home and to a pane at a dialog. At each stop it checks what is
/// on screen, and when SHOT_DIR is set (TEST_RUNNER_SHOT_DIR from xcodebuild)
/// it writes <stop>.ready there and waits for <stop>.done, so a script on the
/// Mac can take an `xcrun simctl io screenshot` of exactly that screen.
final class ScreenshotTests: XCTestCase {
    var server: FixtureServer!

    override func setUp() async throws {
        continueAfterFailure = false
        server = try FixtureServer()
        try await server.start()
        server.rigs = [RigInfo(rig: "initech", root: "/r/initech", running: true),
                       RigInfo(rig: "theguy", root: "/r/theguy", running: true)]
        server.status = StatusReply(rigs: [
            RigStatus(rig: "initech", root: "/r/initech", readAt: nil, agents: [
                AgentStatus(role: "super", state: "running", alive: true, beads: ["ini-r0q"], contextPercent: 65, banner: "none", screen: nil),
                AgentStatus(role: "eng6", state: "idle", alive: true, beads: ["ini-r0q.2"], contextPercent: 31, banner: "none", screen: nil),
                AgentStatus(role: "qa3", state: "pending", alive: true, beads: [], contextPercent: 8, banner: "none", screen: ["❯ looks good, landing"]),
            ]),
            RigStatus(rig: "theguy", root: "/r/theguy", readAt: nil, agents: [
                AgentStatus(role: "super", state: "waiting", alive: true, beads: nil, contextPercent: 72, banner: "dialog",
                            screen: ["Do you want to make this edit to design.md?", "❯ 1. Yes",
                                     "  2. Yes, allow all edits during this session", "  3. No, and tell Claude what to do differently"]),
            ]),
        ])
        server.pins = [Pin(rig: "theguy", pane: "super", name: "theguy", order: 0),
                       Pin(rig: "initech", pane: "super", name: "hover super", order: 1),
                       Pin(rig: "initech", pane: "eng6", name: "eng6 on the app", order: 2),
                       Pin(rig: "initech", pane: "qa3", name: "qa3", order: 3),
                       Pin(rig: "gone", pane: "eng9", name: "eng9 on the Rust twin", order: 4)]
        server.setTranscript(rig: "theguy", pane: "super", [
            Message(id: "1", at: nil, kind: .nelson, text: "We should replicate remote control here, something like this", footer: nil, tool: nil),
            Message(id: "2", at: nil, kind: .tool, text: "", footer: nil,
                    tool: .init(name: "Bash", summary: "initech send super 'route the record-shape note'", detail: "delivered to super")),
            Message(id: "3", at: nil, kind: .agent, text: "Routed: the record-shape issue is fixed on initech's side, and the accounts rig makes its reader tolerant once its lock fix is in.",
                    footer: ["▶ Conversation view or terminal mirror?"], tool: nil),
            Message(id: "4", at: nil, kind: .tool, text: "", footer: nil,
                    tool: .init(name: "Edit", summary: "design.md", detail: "+ the pane view is the conversation")),
        ], pageSize: 50)
        server.setSendVerdict(rig: "theguy", pane: "super", SendResult(verdict: "deferred", words: "deferred to super — target has a modal open; will deliver when it closes"))
    }

    override func tearDown() { server.stop() }

    func testHomeAndAPaneAtADialog() throws {
        let app = XCUIApplication()
        app.launchArguments = ["-uitest-pairing",
                               server.pairingURL]
        app.launch()

        XCTAssertTrue(element(app, "hover super").waitForExistence(timeout: 20), "home did not show the pins")
        XCTAssertTrue(element(app, "eng9 on the Rust twin").exists)
        try stop("home")

        element(app, "theguy").tap()
        XCTAssertTrue(element(app, "Do you want to make this edit").waitForExistence(timeout: 10),
                      "the dialog's screen lines are not above the key strip")
        // Expand before typing: the keyboard would cover the tool run.
        XCTAssertFalse(element(app, "delivered to super").exists, "the tool run is expanded before a tap")
        app.buttons["Tool run Bash"].tap()
        XCTAssertTrue(element(app, "delivered to super").waitForExistence(timeout: 5), "the tool run did not expand")
        let field = app.textFields["Message"].exists ? app.textFields["Message"] : app.textViews.firstMatch
        field.tap()
        field.typeText("go ahead")
        app.buttons["Send"].tap()
        XCTAssertTrue(element(app, "deferred to super").waitForExistence(timeout: 10),
                      "the deferred verdict is not on the message")
        XCTAssertTrue(element(app, "Enter says Yes and Esc says No").exists, "the dialog does not say which keys act")
        app.buttons["Enter"].tap()
        XCTAssertTrue(element(app, "Enter: key enter delivered to super").waitForExistence(timeout: 10), "the key's verdict is not shown")
        try stop("pane")
    }

    /// element is the first element whose label contains text.
    private func element(_ app: XCUIApplication, _ text: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    /// stop hands the screen to the Mac script for a screenshot, when one is waiting.
    private func stop(_ name: String) throws {
        guard let dir = ProcessInfo.processInfo.environment["SHOT_DIR"], !dir.isEmpty else { return }
        let fm = FileManager.default
        fm.createFile(atPath: "\(dir)/\(name).ready", contents: nil)
        let deadline = Date().addingTimeInterval(60)
        while !fm.fileExists(atPath: "\(dir)/\(name).done") {
            if Date() > deadline { XCTFail("no screenshot taken of \(name) in 60 s"); return }
            Thread.sleep(forTimeInterval: 0.2)
        }
    }
}
