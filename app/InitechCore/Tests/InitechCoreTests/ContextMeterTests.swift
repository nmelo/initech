import XCTest
@testable import InitechCore

final class ContextMeterTests: XCTestCase {
    func testFilled_RoundsToTheNearestBlock() {
        XCTAssertEqual(ContextMeter(percent: 0).filled, 0)
        XCTAssertEqual(ContextMeter(percent: 4).filled, 0)
        XCTAssertEqual(ContextMeter(percent: 5).filled, 1)
        XCTAssertEqual(ContextMeter(percent: 27).filled, 3)
        XCTAssertEqual(ContextMeter(percent: 82).filled, 8)
        XCTAssertEqual(ContextMeter(percent: 100).filled, 10)
    }

    /// The service may report anything: the meter never draws fewer than none or more than all.
    func testFilled_StaysBetweenNoneAndAll() {
        XCTAssertEqual(ContextMeter(percent: -20).filled, 0)
        XCTAssertEqual(ContextMeter(percent: 180).filled, 10)
    }

    func testHigh_StartsAt75() {
        XCTAssertFalse(ContextMeter(percent: 74).high)
        XCTAssertTrue(ContextMeter(percent: 75).high)
        XCTAssertTrue(ContextMeter(percent: 100).high)
        XCTAssertFalse(ContextMeter(percent: 0).high)
    }
}
