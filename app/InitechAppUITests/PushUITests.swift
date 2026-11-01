import XCTest

/// PushUITests drives the built app's push registration from its real entry
/// point (ini-r0q.4 AC1, AC5): the device token reaches the service once per
/// launch, a lock and unlock is the same launch, and with notifications off
/// the app says so, offers Settings, and registers nothing. A simulator gets
/// no APNs token, so `-uitest-push-token` hands the app the one iOS would,
/// and `-uitest-push-permission` answers the permission question.
final class PushUITests: XCTestCase {
    private var server: FixtureServer!
    private var app: XCUIApplication!
    private let hex = "0ab1ff007c55aa"

    override func setUp() async throws {
        continueAfterFailure = false
        server = try FixtureServer()
        try await server.start()
        server.status = StatusReply(rigs: [RigStatus(rig: "initech", root: "/r/initech", readAt: nil, agents: [
            AgentStatus(role: "eng1", state: "idle", alive: true, beads: nil, contextPercent: 20, banner: "none", screen: nil),
        ])])
    }

    override func tearDown() { server.stop() }

    private func launch(permission: String) {
        app = XCUIApplication()
        app.launchArguments = ["-uitest-pairing", server.pairingURL,
                               "-uitest-push-permission", permission, "-uitest-push-token", hex]
        app.launch()
        XCTAssertTrue(app.buttons["Rigs"].waitForExistence(timeout: 30), "home did not open")
    }

    private var puts: Int { server.requests.filter { $0.method == "PUT" && $0.path == "/v1/devices" }.count }

    private func waitFor(_ what: String, timeout: TimeInterval = 15, _ ok: () -> Bool) {
        let end = Date().addingTimeInterval(timeout)
        while !ok() && Date() < end { RunLoop.current.run(until: Date().addingTimeInterval(0.2)) }
        XCTAssertTrue(ok(), what)
    }

    func testEachLaunchPutsItsTokenOnce_AndALockDoesNot() {
        launch(permission: "allowed")
        waitFor("the launch did not register its token") { server.devices == [hex] }
        XCTAssertEqual(puts, 1)

        // Home and back: the app locks and unlocks, still the same launch.
        XCUIDevice.shared.press(.home)
        app.activate()
        XCTAssertTrue(app.buttons["Rigs"].waitForExistence(timeout: 30), "home did not come back after the lock")
        RunLoop.current.run(until: Date().addingTimeInterval(3))
        XCTAssertEqual(puts, 1, "a lock and unlock registered the token again")

        app.terminate()
        launch(permission: "allowed")
        waitFor("a new launch did not register again") { puts == 2 }
        XCTAssertFalse(app.otherElements["push-line"].exists, "a registered phone shows a push line")
    }

    func testPushesOff_SaysSo_OffersSettings_AndRegistersNothing() {
        launch(permission: "denied")
        let line = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "Pushes are off")).firstMatch
        XCTAssertTrue(line.waitForExistence(timeout: 15), "nothing says pushes are off")
        XCTAssertTrue(app.buttons["Open Settings"].exists, "no way to Settings")
        RunLoop.current.run(until: Date().addingTimeInterval(3))
        XCTAssertEqual(puts, 0, "registration was attempted with pushes off")
    }
}
