import XCTest
@testable import InitechCore

// Debug builds only: a release build contains none of this.
#if DEBUG

final class DebugLaunchTests: XCTestCase {
    func testScreen_ReadsEachName() {
        XCTAssertEqual(DebugScreen.parse(["app", "-debug-screen", "rigs"]), .rigs)
        XCTAssertEqual(DebugScreen.parse(["app", "-debug-screen", "blockers"]), .blockers)
        XCTAssertEqual(DebugScreen.parse(["app", "-debug-screen", "progress"]), .progress)
        XCTAssertEqual(DebugScreen.parse(["app", "-debug-unlocked", "-debug-screen", "about"]), .about)
        XCTAssertEqual(DebugScreen.parse(["app", "-debug-screen", "pane:hover/eng6"]), .pane(rig: "hover", pane: "eng6"))
    }

    /// Feed it nothing, and things it does not know: the app opens on Panes.
    func testScreen_NoArgumentOrAnUnknownName_IsNil() {
        XCTAssertNil(DebugScreen.parse([]))
        XCTAssertNil(DebugScreen.parse(["app"]))
        XCTAssertNil(DebugScreen.parse(["app", "-debug-screen"]))
        XCTAssertNil(DebugScreen.parse(["app", "-debug-screen", "settings"]))
        XCTAssertNil(DebugScreen.parse(["app", "rigs"]), "a name without its flag opened a screen")
        XCTAssertNil(DebugScreen.parse(["app", "-debug-screen", "pane:"]))
        XCTAssertNil(DebugScreen.parse(["app", "-debug-screen", "pane:hover"]))
        XCTAssertNil(DebugScreen.parse(["app", "-debug-screen", "pane:/eng6"]))
        XCTAssertNil(DebugScreen.parse(["app", "-debug-screen", "pane:hover/"]))
    }

    /// Only the first ask is answered: the launch opens, every later return asks Face ID.
    func testFirstAsk_SaysYesOnce() {
        var first = FirstAsk()
        XCTAssertTrue(first.take())
        XCTAssertFalse(first.take(), "a second ask was answered without Face ID")
        XCTAssertFalse(first.take())
    }
}
#endif
