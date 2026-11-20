import XCTest
@testable import InitechCore

/// The pane's last screen lines, tidied for the block that shows them.
final class ScreenTailTests: XCTestCase {
    func testBlankLinesAtEitherEndGo_AndRunsOfBlanksBecomeOne() {
        XCTAssertEqual(ScreenTail.tidy(["", "  ", "a", "", "", "", "b", " ", ""]), ["a", "", "b"])
    }

    func testTheIndentEveryLineSharesGoes_AndDeeperIndentStays() {
        XCTAssertEqual(ScreenTail.tidy(["  ❯ 1. Yes", "    2. No", "", "  esc to cancel"]), ["❯ 1. Yes", "  2. No", "", "esc to cancel"])
    }

    func testTrailingSpacesGo_AndALineAtTheEdgeKeepsEveryLineInPlace() {
        XCTAssertEqual(ScreenTail.tidy(["* Worked for 2s   ", "  status"]), ["* Worked for 2s", "  status"])
    }

    /// Feed it nothing: no lines, or only blank ones, is no lines.
    func testNothing_OrOnlyBlanks_IsNothing() {
        XCTAssertEqual(ScreenTail.tidy([]), [])
        XCTAssertEqual(ScreenTail.tidy(["", "   ", "\t"]), [])
    }
}
