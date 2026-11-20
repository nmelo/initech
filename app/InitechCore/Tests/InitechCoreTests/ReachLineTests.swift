import XCTest
@testable import InitechCore

/// What the list says about reaching the Mac while its first read has no answer yet.
final class ReachLineTests: XCTestCase {
    func testNothingIsSaid_UntilTheReadHasWaitedPastPatience() {
        XCTAssertEqual(ReachLine.of(waited: 0), .none)
        XCTAssertEqual(ReachLine.of(waited: ReachLine.patience - 0.1), .none, "a read that answers quickly must not flash a line")
    }

    func testStillTrying_FromPatienceOn() {
        XCTAssertEqual(ReachLine.of(waited: ReachLine.patience), .trying)
        XCTAssertEqual(ReachLine.of(waited: 59), .trying)
    }

    /// Feed it nothing: no read in flight, or a clock that went backwards, says nothing.
    func testNoReadInFlight_OrAClockThatWentBack_SaysNothing() {
        XCTAssertEqual(ReachLine.of(waited: nil), .none)
        XCTAssertEqual(ReachLine.of(waited: -5), .none)
    }
}
