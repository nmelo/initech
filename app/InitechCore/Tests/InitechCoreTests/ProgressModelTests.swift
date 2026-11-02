import XCTest
@testable import InitechCore

final class FakeProgress: ProgressSource, @unchecked Sendable {
    var answer: [ProgressReport]
    var calls = 0
    init(_ answer: [ProgressReport]) { self.answer = answer }
    func progress() async throws -> [ProgressReport] {
        calls += 1
        return answer
    }
}

/// One report as initech progress --json prints it (internal/progress
/// Report), with a time on every field that has one.
let progressJSON = """
[{"rig":"initech","prefix":"ini-r0q.","at":"2026-11-01T04:00:00Z","hours":3,
"beads":[{"id":"ini-r0q.3","stage":"in_progress","owner":"eng7","ac":"2/6","ac_proven":2,"ac_items":6,"commits":1,
"last_move":"2026-11-01T03:50:00Z","by":"eng7","title":"App step 2B: the blockers view and the progress view"},
{"id":"ini-r0q.9","stage":"in_qa","owner":"qa1","ac":"-","ac_proven":null,"ac_items":null,"commits":null,
"last_move":"2026-11-01T03:00:00Z","by":"","title":"keystroke"}],
"closed":4,"total":11,"worked_by_engineers":3,"writing_review_only":1,"stages":{"closed":4,"in_progress":5},
"closed_recent":[{"id":"ini-r0q.11","title":"a","closed_at":"2026-11-01T02:10:00Z"},{"id":"ini-r0q.12","title":"b","closed_at":"2026-11-01T03:10:00Z"}],
"critical_path":[{"id":"ini-r0q.1","waiting":4,"stage":"in_progress","title":"service"}],
"engineers":["eng5","eng6","eng7"],
"quiet":[{"id":"ini-r0q.9","owner":"qa1","wait":"stalled"}],"notes":[]}]
"""

/// An effort with nothing listed: the service may print null lists.
let emptyProgressJSON = """
[{"rig":"hover","prefix":"hov-a.","at":"2026-11-01T04:00:00Z","hours":3,"beads":null,"closed":0,"total":1,
"worked_by_engineers":0,"writing_review_only":0,"stages":null,"closed_recent":null,"critical_path":null,
"engineers":null,"quiet":null,"notes":null}]
"""

@MainActor
final class ProgressModelTests: XCTestCase {
    func reports(_ json: String) throws -> [ProgressReport] {
        try JSONDecoder.service.decode([ProgressReport].self, from: Data(json.utf8))
    }

    // AC3: the card shows the command's numbers.
    func testCardShowsTheCommandNumbers() throws {
        let card = ProgressCard(try reports(progressJSON)[0])
        XCTAssertEqual(card.rig, "initech")
        XCTAssertEqual(card.effort, "ini-r0q.")
        XCTAssertEqual(card.pace, "4 of 11 closed · 2 in the last 3 h")
        XCTAssertEqual(card.worked, "worked by engineers 3 · writing or review only 1")
        XCTAssertEqual(card.beads.map(\.line), ["in_progress · eng7 · AC 2/6 · 1 landed", "in_qa · qa1 · no AC list"])
        XCTAssertEqual(card.criticalPath, ["ini-r0q.1 (4 wait, in_progress) service"])
        XCTAssertEqual(card.engineers, "3 on it: eng5 eng6 eng7")
        XCTAssertEqual(card.quiet, ["ini-r0q.9 qa1: stalled"])
    }

    // The command prints "-" for a bead with no owner; the card says so in words.
    func testADashOwnerReadsAsNoOwner() throws {
        let json = progressJSON.replacingOccurrences(of: #""owner":"qa1","ac":"-""#, with: #""owner":"-","ac":"-""#)
        XCTAssertNotEqual(json, progressJSON, "the fixture no longer has the line this test edits")
        XCTAssertEqual(ProgressCard(try reports(json)[0]).beads.last?.line, "in_qa · no owner · no AC list")
    }

    // AC6: no efforts in flight, and an effort whose lists are all null.
    func testNoEffortsAndNullLists() async throws {
        let none = ProgressModel(source: FakeProgress([]))
        await none.reload()
        XCTAssertTrue(none.loaded)
        XCTAssertTrue(none.cards.isEmpty)

        let card = ProgressCard(try reports(emptyProgressJSON)[0])
        XCTAssertEqual(card.pace, "0 of 1 closed · 0 in the last 3 h")
        XCTAssertTrue(card.beads.isEmpty)
        XCTAssertEqual(card.engineers, "no engineers on it")
        XCTAssertTrue(card.quiet.isEmpty)
    }

    func testResetReloadsAndPaneEventsDoNot() async {
        let src = FakeProgress([])
        let m = ProgressModel(source: src)
        await m.apply(.stuck(rig: "a", pane: "b", reason: "c"))
        XCTAssertEqual(src.calls, 0)
        await m.apply(.reset)
        XCTAssertEqual(src.calls, 1)
    }

    // AC4: no finish time, estimate or actual anywhere in the rendered
    // models: no time-typed field, and no text that names one.
    func testRenderedModelsCarryNoFinishTimeEstimateOrActual() throws {
        let card = ProgressCard(try reports(progressJSON)[0])
        let rows = BlockersModel.sections([RigBlockers(
            rig: "initech",
            waiting: [WaitingItem(pane: "super", text: "Your go", recommendation: "go")],
            stuck: [StuckPane(pane: "eng1", reason: "ini-a1 quiet 1h30m, its owner idle")],
            quiet: false)])
        var texts: [String] = []
        for value in [card as Any] + rows.map({ $0 as Any }) {
            collect(value, into: &texts)
        }
        XCTAssertFalse(texts.isEmpty, "the walk read no text: it is not reading the models")
        let banned = try NSRegularExpression(pattern: #"(?i)\b(eta|finish(es|ed)?|estimate[sd]?|actual|remaining|done by|due)\b|\b\d{1,2}:\d{2}\b"#)
        for t in texts {
            let hit = banned.firstMatch(in: t, range: NSRange(t.startIndex..., in: t))
            XCTAssertNil(hit, "rendered text names a time or estimate: \(t)")
        }
        // Positive twin: the walk and the pattern catch a planted finish time.
        var planted = card
        planted.pace += " · finishes 04:30"
        var plantedTexts: [String] = []
        collect(planted, into: &plantedTexts)
        XCTAssertTrue(plantedTexts.contains { banned.firstMatch(in: $0, range: NSRange($0.startIndex..., in: $0)) != nil })
    }

    /// collect walks value and gathers every string; a time-typed field
    /// fails the test outright.
    private func collect(_ value: Any, into texts: inout [String]) {
        if value is Date || value is DateComponents || value is TimeInterval {
            XCTFail("a rendered model carries a time field: \(value)")
            return
        }
        if let s = value as? String {
            texts.append(s)
            return
        }
        for child in Mirror(reflecting: value).children {
            collect(child.value, into: &texts)
        }
    }
}

/// The screen says the rig's own value and lists the stuck and the waiting
/// beads the report names; an older service's report reads as one hour.
final class ProgressStuckAfterTests: XCTestCase {
    private func card(_ json: String) throws -> ProgressCard {
        ProgressCard(try JSONDecoder.service.decode([ProgressReport].self, from: Data(json.utf8))[0])
    }

    func testTheCardNamesTheValueTheStuckAndTheWaiting() throws {
        let c = try card("""
        [{"rig":"hover","prefix":"hov-w.","stuck_after":"20m","stuck_ids":["hov-w.5"],
          "waiting":[{"id":"hov-w.1","owner":"eng1","wait":"parked for the release"},{"id":"hov-w.4","owner":"-","wait":"no comment"}]}]
        """)
        XCTAssertEqual(c.quietLabel, "Quiet 20m or more")
        XCTAssertEqual(c.waitingLabel, "Waiting 20m or more")
        XCTAssertEqual(c.stuck, ["hov-w.5"])
        XCTAssertEqual(c.waiting, ["hov-w.1 eng1: parked for the release", "hov-w.4 -: no comment"])
    }

    func testAReportWithoutTheFieldsReadsAsOneHourAndNothingListed() throws {
        let c = try card(#"[{"rig":"hover"}]"#)
        XCTAssertEqual(c.quietLabel, "Quiet 1h or more")
        XCTAssertEqual(c.stuck, [])
        XCTAssertEqual(c.waiting, [])
    }
}
