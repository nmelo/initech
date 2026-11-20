import XCTest
@testable import InitechCore

/// Where a row slid left comes to rest when the finger lifts.
final class SwipeSettleTests: XCTestCase {
    func testAShortSlide_ClosesAgain() {
        XCTAssertEqual(SwipeSettle.of(end: 0, keys: 1, key: 96), .closed)
        XCTAssertEqual(SwipeSettle.of(end: -47, keys: 2, key: 96), .closed)
        XCTAssertEqual(SwipeSettle.of(end: 30, keys: 1, key: 96), .closed, "a slide to the right opens nothing")
    }

    func testPastHalfAKey_StaysOpen() {
        XCTAssertEqual(SwipeSettle.of(end: -48, keys: 1, key: 96), .open)
        XCTAssertEqual(SwipeSettle.of(end: -150, keys: 1, key: 96), .open)
        XCTAssertEqual(SwipeSettle.of(end: -60, keys: 2, key: 96), .open)
    }

    func testAFarSlide_DoesTheAction_OnlyWhenTheRowHasOneKey() {
        XCTAssertEqual(SwipeSettle.of(end: -160, keys: 1, key: 96), .act)
        XCTAssertEqual(SwipeSettle.of(end: -400, keys: 2, key: 96), .open, "with two keys a far slide must not pick one for him")
    }

    /// Feed it nothing: a row with no keys never opens.
    func testNoKeys_NeverOpens() {
        XCTAssertEqual(SwipeSettle.of(end: -300, keys: 0, key: 96), .closed)
    }
}
