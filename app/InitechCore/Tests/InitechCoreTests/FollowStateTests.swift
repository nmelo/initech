import XCTest
@testable import InitechCore

final class FollowStateTests: XCTestCase {
    private func ids(_ r: ClosedRange<Int>) -> [String] { r.map { "m\($0)" } }

    func testAtTheBottom_ANewEntryIsFollowedAndNothingIsMarked() {
        var f = FollowState()
        f.sawBottom("m10")
        XCTAssertEqual(f.arrived(ids(1...11), previous: "m10", wasAtBottom: true, own: false), .follow)
        XCTAssertNil(f.markerText(ids(1...11)))
    }

    func testAwayFromTheBottom_NewEntriesAreCountedAndTheViewStays() {
        var f = FollowState()
        f.sawBottom("m10")
        f.leftBottom()
        XCTAssertEqual(f.arrived(ids(1...11), previous: "m10", wasAtBottom: false, own: false), .mark)
        XCTAssertEqual(f.markerText(ids(1...11)), "new below")
        XCTAssertEqual(f.arrived(ids(1...13), previous: "m11", wasAtBottom: false, own: false), .mark)
        XCTAssertEqual(f.markerText(ids(1...13)), "3 new below")
    }

    /// Fifty entries are counted from the entries, so they are all counted
    /// whether they land in one update or in fifty.
    func testABurstOf50_IsOneLineWithTheCount_HoweverItLands() {
        var once = FollowState()
        once.sawBottom("m10")
        XCTAssertEqual(once.arrived(ids(1...60), previous: "m10", wasAtBottom: false, own: false), .mark)
        XCTAssertEqual(once.markerText(ids(1...60)), "50 new below")

        var oneByOne = FollowState()
        oneByOne.sawBottom("m10")
        for n in 11...60 {
            XCTAssertEqual(oneByOne.arrived(ids(1...n), previous: "m\(n - 1)", wasAtBottom: false, own: false), .mark)
        }
        XCTAssertEqual(oneByOne.markerText(ids(1...60)), "50 new below")
    }

    func testHisOwnMessage_TakesHimToTheBottomAndClearsTheLine() {
        var f = FollowState()
        f.sawBottom("m10")
        _ = f.arrived(ids(1...12), previous: "m10", wasAtBottom: false, own: false)
        XCTAssertEqual(f.arrived(ids(1...13), previous: "m12", wasAtBottom: false, own: true), .follow)
        XCTAssertNil(f.markerText(ids(1...13)))
    }

    func testReachingTheBottom_ClearsTheLine() {
        var f = FollowState()
        f.sawBottom("m10")
        _ = f.arrived(ids(1...14), previous: "m10", wasAtBottom: false, own: false)
        f.sawBottom("m14")
        XCTAssertNil(f.markerText(ids(1...14)))
        XCTAssertEqual(f.arrived(ids(1...15), previous: "m14", wasAtBottom: true, own: false), .follow, "back at the bottom, it follows again")
    }

    /// The entry he saw last can leave the list (his sent copy, replaced by
    /// the transcript's entry); the count then starts at the entry before the
    /// new one.
    func testTheEntryHeSawLeavesTheList_TheCountGoesOn() {
        var f = FollowState()
        f.sawBottom("local-1")
        f.leftBottom()
        let after = ids(1...10) + ["m11"]
        XCTAssertEqual(f.arrived(after, previous: "m10", wasAtBottom: false, own: false), .mark)
        XCTAssertEqual(f.markerText(after), "new below")
    }

    // Nothing seen yet (a pane that opened away from the bottom): the count
    // starts from the entry before this one, so the line says one new entry
    // and not nothing.
    func testNothingSeenYet_TheCountStartsFromTheEntryBeforeThisOne() {
        var f = FollowState()
        XCTAssertEqual(f.arrived(ids(1...11), previous: "m10", wasAtBottom: false, own: false), .mark)
        XCTAssertEqual(f.newBelow(ids(1...11)), 1)
        XCTAssertEqual(f.markerText(ids(1...11)), "new below")
    }
}
