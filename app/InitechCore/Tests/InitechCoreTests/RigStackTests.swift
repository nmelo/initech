import XCTest
@testable import InitechCore

/// The Rigs screen: every rig's header always showing, one rig open at a time,
/// driven by the scroll alone.
final class RigStackTests: XCTestCase {
    /// Three rigs of 12, 5 and 20 panes on a 700-high screen: headers 50, rows 60.
    /// These open at the scroll's own speed, so the numbers are easy to follow; the
    /// faster opening the screen uses has its own test below.
    let stack = RigStack(rows: [12, 5, 20], header: 50, row: 60, viewport: 700, opening: 1)

    func testAtTheTop_EveryRigIsClosed_HeadersOneUnderAnother() {
        let l = stack.layout(at: 0)
        XCTAssertEqual(l.headerY, [0, 50, 100])
        XCTAssertEqual(l.window, [0, 0, 0], "a rig showed panes before he scrolled")
    }

    func testScrolling_OpensTheFirstRig_AndTheOthersSlideDown() {
        let l = stack.layout(at: 200)
        XCTAssertEqual(l.headerY, [0, 250, 300])
        XCTAssertEqual(l.window, [200, 0, 0])
        XCTAssertEqual(l.shift[0], 0, "the first rig's panes moved before it was fully open")
        // Fully open: the others wait at the bottom of the screen.
        let open = stack.layout(at: 550)
        XCTAssertEqual(open.headerY, [0, 600, 650])
        XCTAssertEqual(open.window, [550, 0, 0])
    }

    func testScrollingOn_MovesThroughTheFirstRigsPanes() {
        let l = stack.layout(at: 550 + 100)
        XCTAssertEqual(l.headerY, [0, 600, 650])
        XCTAssertEqual(l.window[0], 550)
        XCTAssertEqual(l.shift[0], -100)
    }

    func testWhenTheFirstRigEnds_ItClosesIntoItsHeader_AndTheNextOpens() {
        let s = stack.offset(opening: 1)
        XCTAssertEqual(s, 550 + 12 * 60)
        let l = stack.layout(at: s)
        XCTAssertEqual(l.headerY[0], 0)
        XCTAssertEqual(l.headerY[1], 50, "the second header did not rise to sit under the first")
        XCTAssertEqual(l.window[0], 0, "the first rig stayed open")
        XCTAssertEqual(l.window[1], 300, "the second rig's five panes are not all showing")
        XCTAssertEqual(l.shift[1], 0)
        // Half way there: the first rig is half closed, the second half risen.
        let mid = stack.layout(at: s - 275)
        XCTAssertEqual(mid.headerY[1], 50 + 275)
        XCTAssertEqual(mid.window[0], 275)
    }

    func testAtTheEnd_TheLastRigIsOpenToItsLastPane() {
        let l = stack.layout(at: stack.length)
        XCTAssertEqual(l.headerY, [0, 50, 100])
        XCTAssertEqual(l.window, [0, 0, 550])
        XCTAssertEqual(l.shift[2], -(20 * 60 - 550))
        XCTAssertEqual(stack.layout(at: stack.length + 500), l, "scrolling past the end moved something")
        XCTAssertEqual(stack.layout(at: -80), stack.layout(at: 0), "pulling down past the top moved something")
    }

    func testOneRigAlone_StartsOpen() {
        let one = RigStack(rows: [4], header: 50, row: 60, viewport: 700, opening: 1)
        let l = one.layout(at: 0)
        XCTAssertEqual(l.headerY, [0])
        XCTAssertEqual(l.window, [240], "a single rig started closed, with nothing else to show")
        XCTAssertEqual(one.length, 0)
    }

    func testWhenEverythingFits_ScrollingOpensEveryRig() {
        let small = RigStack(rows: [2, 3], header: 50, row: 60, viewport: 700, opening: 1)
        XCTAssertEqual(small.layout(at: 0).window, [0, 0])
        XCTAssertEqual(small.length, 300)
        let l = small.layout(at: small.length)
        XCTAssertEqual(l.headerY, [0, 170])
        XCTAssertEqual(l.window, [120, 180])
    }

    /// The screen opens the first rig four times faster than the finger moves:
    /// a quarter of the scroll opens it fully, and from there on nothing differs.
    func testTheFirstRigOpensFourTimesFasterThanTheScroll() {
        let fast = RigStack(rows: [12, 5, 20], header: 50, row: 60, viewport: 700)
        XCTAssertEqual(fast.opening, 4)
        XCTAssertEqual(fast.layout(at: 0).window, [0, 0, 0])
        XCTAssertEqual(fast.layout(at: 50).headerY, [0, 250, 300])
        XCTAssertEqual(fast.layout(at: 50).window, [200, 0, 0])
        let open = fast.layout(at: 137.5)
        XCTAssertEqual(open.headerY, [0, 600, 650])
        XCTAssertEqual(open.window, [550, 0, 0])
        // Past the opening, it moves through the panes exactly as the slow one does.
        XCTAssertEqual(fast.layout(at: 137.5 + 100), stack.layout(at: 550 + 100))
        XCTAssertEqual(fast.offset(opening: 1), 137.5 + 12 * 60)
        XCTAssertEqual(fast.length, stack.length - 550 + 137.5)
        // Everything fitting: still opens every rig, in a quarter of the scroll.
        let small = RigStack(rows: [2, 3], header: 50, row: 60, viewport: 700)
        XCTAssertEqual(small.length, 75)
        XCTAssertEqual(small.layout(at: 75).window, [120, 180])
    }

    /// Feed it nothing, and too much.
    func testNoRigs_AndTooManyRigs() {
        let none = RigStack(rows: [], header: 50, row: 60, viewport: 700)
        XCTAssertFalse(none.fits)
        XCTAssertEqual(none.layout(at: 100), RigStack.Layout(headerY: [], window: [], shift: []))
        let many = RigStack(rows: Array(repeating: 3, count: 13), header: 50, row: 60, viewport: 700)
        XCTAssertFalse(many.fits, "thirteen headers leave no room for rows on this screen")
        XCTAssertTrue(stack.fits)
    }

    /// A rig with nothing to list still has its one line.
    func testARigWithNothingToList_HasOneRow() {
        let s = RigStack(rows: [0, 3], header: 50, row: 60, viewport: 700)
        XCTAssertEqual(s.rows, [1, 3])
    }

    /// At every offset: headers in order and never overlapping, nothing negative.
    func testEveryOffset_KeepsHeadersInOrder() {
        var s = 0.0
        while s <= stack.length {
            let l = stack.layout(at: s)
            for k in 0..<3 {
                XCTAssertGreaterThanOrEqual(l.window[k], 0)
                XCTAssertLessThanOrEqual(l.shift[k], 0)
                if k > 0 { XCTAssertGreaterThanOrEqual(l.headerY[k], l.headerY[k - 1] + 50, "headers overlap at \(s)") }
                XCTAssertLessThanOrEqual(l.headerY[k], 700 - Double(3 - k) * 50, "a header left the screen at \(s)")
            }
            s += 7
        }
    }
}
