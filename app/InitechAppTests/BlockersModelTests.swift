import XCTest
@testable import InitechApp

/// FakeBlockers answers blockers() with a fixed list and counts the calls.
final class FakeBlockers: BlockersSource, @unchecked Sendable {
    var answer: [RigBlockers]
    var calls = 0
    init(_ answer: [RigBlockers]) { self.answer = answer }
    func blockers() async throws -> [RigBlockers] {
        calls += 1
        return answer
    }
}

/// The JSON GET /v1/blockers serves (internal/appserve TestBlockers_JSONShape).
let blockersJSON = """
[{"rig":"initech","waiting":[],"stuck":[],"quiet":true},\
{"rig":"theguy","waiting":[{"pane":"super","text":"Your go","recommendation":""}],"stuck":[],"quiet":false}]
"""

@MainActor
final class BlockersModelTests: XCTestCase {
    let mixed = RigBlockers(
        rig: "initech",
        waiting: [WaitingItem(pane: "super", text: "Your go for the patch release", recommendation: "ship it, it is small")],
        stuck: [StuckPane(pane: "eng1", reason: "permission prompt open")],
        quiet: false)

    func testDecodesTheServiceJSON() throws {
        let rigs = try JSONDecoder.service.decode([RigBlockers].self, from: Data(blockersJSON.utf8))
        XCTAssertEqual(rigs.count, 2)
        XCTAssertTrue(rigs[0].quiet)
        XCTAssertEqual(rigs[1].waiting, [WaitingItem(pane: "super", text: "Your go", recommendation: "")])
    }

    // AC1: a waiting item shows its question and recommendation and opens
    // its pane; AC2: a stuck pane shows its reason and opens its pane.
    func testRowsCarryTheQuestionRecommendationReasonAndPane() async {
        let m = BlockersModel(source: FakeBlockers([mixed]))
        await m.reload()
        let rows = m.sections.first?.rows ?? []
        XCTAssertEqual(rows.map(\.kind), [.waiting, .stuck])
        XCTAssertEqual(rows[0].title, "Your go for the patch release")
        XCTAssertEqual(rows[0].detail, "ship it, it is small")
        XCTAssertEqual([rows[0].rig, rows[0].pane], ["initech", "super"])
        XCTAssertEqual(rows[1].detail, "permission prompt open")
        XCTAssertEqual([rows[1].rig, rows[1].pane], ["initech", "eng1"])
    }

    // AC6: a footer with no recommendation line shows no recommendation.
    func testNoRecommendationShowsNone() async {
        var r = mixed
        r.waiting[0].recommendation = ""
        let m = BlockersModel(source: FakeBlockers([r]))
        await m.reload()
        XCTAssertNil(m.sections.first?.rows.first?.detail)
    }

    // AC2: a rig with nothing is one quiet row that opens nothing.
    func testQuietRigIsOneQuietRow() async {
        let m = BlockersModel(source: FakeBlockers([RigBlockers(rig: "hover", waiting: [], stuck: [], quiet: true)]))
        await m.reload()
        XCTAssertEqual(m.sections.count, 1)
        XCTAssertEqual(m.sections[0].rows.map(\.kind), [.quiet])
        XCTAssertNil(m.sections[0].rows.first?.pane)
    }

    // AC6: no running rig gives no section, and the model reads loaded.
    func testNoRunningRig() async {
        let m = BlockersModel(source: FakeBlockers([]))
        XCTAssertFalse(m.loaded)
        await m.reload()
        XCTAssertTrue(m.loaded)
        XCTAssertTrue(m.sections.isEmpty)
    }

    // AC1: the list updates within a feed event. A reply in a super pane is
    // what drops an item: no other event says a later footer lacks its line.
    func testFeedEventsReload() async {
        let reply = Message(id: "m1", at: nil, kind: .agent, text: "hi", footer: nil, tool: nil)
        let toolLine = Message(id: "m2", at: nil, kind: .tool, text: "", footer: nil, tool: nil)
        let src = FakeBlockers([])
        let m = BlockersModel(source: src)
        await m.apply(.transcript(rig: "initech", pane: "eng1", message: reply))
        await m.apply(.transcript(rig: "initech", pane: "super", message: toolLine))
        XCTAssertEqual(src.calls, 0, "only a reply in a super pane reloads")

        src.answer = [mixed]
        await m.apply(.waitingItem(rig: "initech", pane: "super", text: "Your go for the patch release"))
        XCTAssertEqual(src.calls, 1)
        XCTAssertEqual(m.sections.first?.rows.first?.title, "Your go for the patch release")

        src.answer = []
        await m.apply(.transcript(rig: "initech", pane: "super", message: reply))
        XCTAssertEqual(src.calls, 2)
        XCTAssertTrue(m.sections.isEmpty, "a later reply without the item drops it")

        for e: FeedEvent in [.stuck(rig: "a", pane: "b", reason: "c"), .paneState(rig: "a", pane: "b", from: nil, to: "idle", banner: nil), .reset] {
            await m.apply(e)
        }
        XCTAssertEqual(src.calls, 5)
    }
}
