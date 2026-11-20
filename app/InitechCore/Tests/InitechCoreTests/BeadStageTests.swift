import XCTest
@testable import InitechCore

/// A bead's stage as the app shows it: the icon the Mac shows, plain words, and an order.
final class BeadStageTests: XCTestCase {
    func testIcons_AreTheOnesTheMacShows() {
        let want = ["open": "○", "in_progress": "▶", "waiting": "☾", "blocked": "■", "waiting_on_nelson": "?",
                    "in_qa": "◎", "ready_for_qa": "◎", "qa_passed": "◆", "ready_to_ship": "◆", "deferred": "❄", "closed": "✓"]
        for (stage, icon) in want { XCTAssertEqual(BeadStage.icon(stage), icon, stage) }
    }

    func testWords_ArePlain_NeverTheRawName() {
        XCTAssertEqual(BeadStage.words("in_progress"), "in progress")
        XCTAssertEqual(BeadStage.words("ready_for_qa"), "ready for QA")
        XCTAssertEqual(BeadStage.words("in_qa"), "in QA")
        XCTAssertEqual(BeadStage.words("qa_passed"), "QA passed")
        XCTAssertEqual(BeadStage.words("waiting_on_nelson"), "waiting on you")
        XCTAssertEqual(BeadStage.words("waiting_for_release"), "waiting for release")
    }

    /// Feed it a stage it does not know, or nothing: a neutral mark and the name with spaces, never a crash or a blank.
    func testAnUnknownStage_GetsANeutralMarkAndReadableWords() {
        XCTAssertEqual(BeadStage.icon("some_new_state"), "·")
        XCTAssertEqual(BeadStage.words("some_new_state"), "some new state")
        XCTAssertEqual(BeadStage.icon(""), "·")
        XCTAssertEqual(BeadStage.words(""), "no stage")
    }

    func testOrder_PutsWhatNeedsSomeoneFirst_AndParkedLast() {
        let sorted = ["deferred", "in_progress", "qa_passed", "blocked", "waiting", "in_qa", "waiting_on_nelson", "open"]
            .sorted { BeadStage.rank($0) < BeadStage.rank($1) }
        XCTAssertEqual(sorted, ["blocked", "waiting_on_nelson", "in_progress", "in_qa", "qa_passed", "waiting", "open", "deferred"])
    }
}
