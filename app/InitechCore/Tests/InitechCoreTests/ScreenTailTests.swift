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

    // MARK: the unsent text in the input box. The shapes are the ones running panes showed; the
    // words are made up: a fixture in this repo carries no rig's, agent's or account's own text.

    private let rule = String(repeating: "─", count: 80)
    private let footer = ["  ⎇ main │ Fable 5.2 high │ Account │ 64% │ 71% ▓▓▓▓▓▓▓┃░░", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"]

    /// One line typed and not sent, under the end of a reply and above Claude Code's footer; the box's top rule carries the pane's title.
    func testUnsent_OneLine_IsThatLineAlone() {
        let screen = ["  The last line of a reply, as the pane drew it.", "", "✻ Cooked for 2s · done 9:46 PM",
                      String(repeating: " ", count: 60) + "✔ Update installed · /restart to apply",
                      String(repeating: "─", count: 68) + " A title ─", "❯ aaaa", rule] + footer
        XCTAssertEqual(ScreenTail.unsent(screen), ["aaaa"])
    }

    /// A long unsent message, its top scrolled out of the box.
    func testUnsent_MoreLinesAbove_SaysSo() {
        let screen = ["✻ Worked for 2s · done 9:37 PM", "", "─── ↑ 11 more " + String(repeating: "─", count: 40),
                      "  the last line of a long draft…", rule] + footer
        XCTAssertEqual(ScreenTail.unsent(screen), ["↑ 11 more", "the last line of a long draft…"])
    }

    /// Claude Code puts a no-break space after its prompt mark (seen on a device: the text came out one space in).
    func testUnsent_ANoBreakSpaceAfterTheMark_IsNotKept() {
        XCTAssertEqual(ScreenTail.unsent([rule, "❯\u{00A0}aaaa", rule] + footer), ["aaaa"])
        XCTAssertEqual(ScreenTail.unsent([rule, "❯\u{00A0}first", "  second", rule]), ["first", "second"])
    }

    func testUnsent_SeveralLines_KeepTheirShape() {
        let screen = [rule, "❯ first line", "  second line", "    indented third", rule] + footer
        XCTAssertEqual(ScreenTail.unsent(screen), ["first line", "second line", "  indented third"])
    }

    /// Feed it nothing it can be sure of, and it cuts nothing: the caller shows the screen as it is.
    func testUnsent_NoBoxFound_IsNil() {
        XCTAssertNil(ScreenTail.unsent([]))
        XCTAssertNil(ScreenTail.unsent(["Do you want to create hello.txt?", "❯ 1. Yes", "  2. No"]), "a dialog has no input box")
        XCTAssertNil(ScreenTail.unsent(["a reply", rule, "❯ aaaa"]), "one rule is not a box")
        XCTAssertNil(ScreenTail.unsent([rule, "❯ ", rule] + footer), "an empty box has no unsent text")
        XCTAssertNil(ScreenTail.unsent([rule, "", rule]), "an empty box has no unsent text")
        XCTAssertNil(ScreenTail.unsent([rule, "some text with no prompt mark", rule]), "between two rules, but not the input box")
        XCTAssertNil(ScreenTail.unsent(["a - b - c - d - e - f - g", "❯ x", "------------------------------"]), "hyphens are not the box's rule")
    }
}
