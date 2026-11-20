import XCTest
@testable import InitechCore

/// When the app asks for Face ID by itself (Nelson, 2026-11-19: "if i move
/// the app to the background it does the face id check as soon as i background
/// them").
final class LockAskTests: XCTestCase {
    func testLeavingForTheBackground_AsksNothing() {
        var ask = LockAsk()
        XCTAssertTrue(ask.moved(to: .active, locked: true), "the launch asks")
        // Unlocked and in use; he leaves. The app locks as it reaches the background.
        XCTAssertFalse(ask.moved(to: .inactive, locked: false))
        XCTAssertFalse(ask.moved(to: .background, locked: true), "asked for Face ID on the way out")
    }

    func testComingBackToTheFront_AsksOnce() {
        var ask = LockAsk()
        _ = ask.moved(to: .active, locked: true)
        _ = ask.moved(to: .inactive, locked: false)
        _ = ask.moved(to: .background, locked: true)
        XCTAssertFalse(ask.moved(to: .inactive, locked: true), "asked before the app was in front")
        XCTAssertTrue(ask.moved(to: .active, locked: true), "back in front and locked: this is when to ask")
    }

    /// Face ID's own sheet takes the app out of active and gives it back when
    /// it closes. After a cancel that return must not ask again, or the sheet
    /// would come back for as long as he cancels.
    func testTheFaceIDSheetClosing_DoesNotAskAgain() {
        var ask = LockAsk()
        XCTAssertTrue(ask.moved(to: .active, locked: true))
        XCTAssertFalse(ask.moved(to: .inactive, locked: true)) // the sheet is up
        XCTAssertFalse(ask.moved(to: .active, locked: true), "a cancelled Face ID was asked again by itself")
    }

    func testInFrontAndUnlocked_AsksNothing_AndTheNextReturnStillAsks() {
        var ask = LockAsk()
        // Paired a moment ago: in front, never locked.
        XCTAssertFalse(ask.moved(to: .active, locked: false))
        _ = ask.moved(to: .background, locked: true)
        XCTAssertTrue(ask.moved(to: .active, locked: true))
    }

    /// The app switcher alone (inactive, never background) neither locks nor asks.
    /// Locked and due, but only inactive (the Face ID sheet's own phase, the app switcher): nothing is asked.
    func testInactiveWhileLockedAndDue_AsksNothing() {
        var ask = LockAsk()
        XCTAssertFalse(ask.moved(to: .inactive, locked: true))
        XCTAssertTrue(ask.due, "being inactive used up the ask")
    }

    func testTheAppSwitcherAlone_AsksNothing() {
        var ask = LockAsk()
        _ = ask.moved(to: .active, locked: true)
        XCTAssertFalse(ask.moved(to: .inactive, locked: false))
        XCTAssertFalse(ask.moved(to: .active, locked: false))
    }
}
