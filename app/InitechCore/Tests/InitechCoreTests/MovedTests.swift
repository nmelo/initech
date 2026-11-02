import XCTest
@testable import InitechCore

final class MovedTests: XCTestCase {
    func testMovedPutsTheOffsetsJustBeforeTheDestination() {
        let list = ["a", "b", "c", "d"]
        XCTAssertEqual(list.moved(fromOffsets: [2], toOffset: 0), ["c", "a", "b", "d"])
        XCTAssertEqual(list.moved(fromOffsets: [0], toOffset: 4), ["b", "c", "d", "a"])
        XCTAssertEqual(list.moved(fromOffsets: [0], toOffset: 2), ["b", "a", "c", "d"])
        XCTAssertEqual(list.moved(fromOffsets: [0, 2], toOffset: 4), ["b", "d", "a", "c"])
        XCTAssertEqual(list.moved(fromOffsets: [], toOffset: 1), list)
    }
}
