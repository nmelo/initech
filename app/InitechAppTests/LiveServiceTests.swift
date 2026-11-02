import XCTest
import SwiftUI
@testable import InitechApp
@testable import InitechCore

/// LiveServiceTests reads a real initech app-serve through the app's own
/// pinned client: TEST_RUNNER_LIVE_PAIRING holds its pairing link. They skip
/// without it, so an ordinary run reports them as skipped. With SHOT_DIR set
/// they write what the app rendered, for a script to compare with the command.
@MainActor
final class LiveServiceTests: XCTestCase {
    private var service: HTTPService!

    override func setUpWithError() throws {
        let link = ProcessInfo.processInfo.environment["LIVE_PAIRING"] ?? ""
        try XCTSkipIf(link.isEmpty, "needs a running initech app-serve in TEST_RUNNER_LIVE_PAIRING")
        service = HTTPService(pairing: try Pairing.parse(link))
    }

    private func write(_ name: String, _ value: some Encodable) throws {
        guard let dir = ProcessInfo.processInfo.environment["SHOT_DIR"], !dir.isEmpty else { return }
        try JSONEncoder().encode(value).write(to: URL(fileURLWithPath: dir).appendingPathComponent(name))
    }

    private struct Row: Encodable { var rig, kind: String; var pane: String?; var title: String; var detail: String? }
    private struct Card: Encodable { var rig, effort, pace, worked, engineers: String; var beads, criticalPath, quiet: [String] }

    func testBlockersFromTheRealService() async throws {
        let m = BlockersModel(source: service)
        await m.reload()
        XCTAssertNil(m.problem)
        XCTAssertFalse(m.sections.isEmpty, "no running rig: nothing was read")
        try write("live-blockers.json", m.sections.flatMap { s in
            s.rows.map { Row(rig: s.rig, kind: "\($0.kind)", pane: $0.pane, title: $0.title, detail: $0.detail) }
        })
        _ = try await ScreenshotTests().shot("live-blockers", NavigationStack { BlockersView(blockers: m) { _, _ in } })
    }

    func testProgressFromTheRealService() async throws {
        let m = ProgressModel(source: service)
        await m.reload()
        XCTAssertNil(m.problem)
        XCTAssertFalse(m.cards.isEmpty, "the service's progress run returned no effort")
        try write("live-cards.json", m.cards.map {
            Card(rig: $0.rig, effort: $0.effort, pace: $0.pace, worked: $0.worked, engineers: $0.engineers,
                 beads: $0.beads.map { "\($0.id) \($0.line)" }, criticalPath: $0.criticalPath, quiet: $0.quiet)
        })
        _ = try await ScreenshotTests().shot("live-progress", NavigationStack { EffortProgressView(progress: m) })
    }

    // AC1 end to end, with LIVE_RIG naming a test rig whose real super a script
    // drives: after its opening read, this list moves on feed events alone. The
    // script and the test hand off through files in SHOT_DIR.
    func testARealReplyComesAndGoesOnTheFeed() async throws {
        let env = ProcessInfo.processInfo.environment
        let rig = env["LIVE_RIG"] ?? ""
        try XCTSkipIf(rig.isEmpty, "needs a test rig in TEST_RUNNER_LIVE_RIG")
        let dir = try XCTUnwrap(env["SHOT_DIR"])
        let item = try XCTUnwrap(env["LIVE_ITEM"])
        let recommendation = try XCTUnwrap(env["LIVE_REC"])
        func mark(_ name: String) { FileManager.default.createFile(atPath: "\(dir)/\(name)", contents: nil) }
        func rows(_ m: BlockersModel) -> [BlockerRow] { m.sections.first { $0.rig == rig }?.rows ?? [] }
        func waitFor(_ what: String, _ cond: () -> Bool) async throws {
            let deadline = Date().addingTimeInterval(240)
            while !cond() {
                if Date() > deadline { throw XCTSkip("timed out: \(what)") }
                try await Task.sleep(for: .milliseconds(500))
            }
        }

        let m = BlockersModel(source: service)
        let home = HomeModel(service: service)
        home.listeners["blockers"] = m
        home.startFeed()
        defer { home.stopFeed() }
        await m.reload()
        XCTAssertFalse(rows(m).contains { $0.title == item }, "the item is there before the reply")
        mark("open.ready")

        do {
            try await waitFor("the item to arrive on the feed") { rows(m).contains { $0.title == item } }
        } catch { return XCTFail("\(error)") }
        let row = try XCTUnwrap(rows(m).first { $0.title == item })
        XCTAssertEqual(row.kind, .waiting)
        XCTAssertEqual(row.detail, recommendation)
        XCTAssertEqual(row.pane, "super")
        _ = try await ScreenshotTests().shot("rig-waiting", NavigationStack { BlockersView(blockers: m) { _, _ in } })
        mark("item.ready")

        do {
            try await waitFor("the script's no-footer reply") {
                FileManager.default.fileExists(atPath: "\(dir)/nofooter.done")
            }
        } catch { return XCTFail("\(error)") }
        await m.reload()
        XCTAssertTrue(rows(m).contains { $0.title == item }, "a reply with no footer cleared the item")
        mark("stay.ready")

        do {
            try await waitFor("the item to go on the feed") { !rows(m).contains { $0.title == item } }
        } catch { return XCTFail("\(error)") }
        _ = try await ScreenshotTests().shot("rig-cleared", NavigationStack { BlockersView(blockers: m) { _, _ in } })
        mark("cleared.ready")
    }

    /// CountingSource counts reads of the real service's list.
    private final class CountingSource: BlockersSource, @unchecked Sendable {
        let base: BlockersSource
        private let lock = NSLock()
        private var n = 0
        var calls: Int { lock.withLock { n } }
        init(_ base: BlockersSource) { self.base = base }
        func blockers() async throws -> [RigBlockers] {
            lock.withLock { n += 1 }
            return try await base.blockers()
        }
    }

    // A real event from the service's feed reaches the open list through home.
    func testARealFeedEventReloadsTheList() async throws {
        let src = CountingSource(service)
        let m = BlockersModel(source: src)
        let home = HomeModel(service: service)
        home.listeners["blockers"] = m
        home.startFeed()
        defer { home.stopFeed() }
        let deadline = Date().addingTimeInterval(240)
        while src.calls == 0 {
            if Date() > deadline { return XCTFail("no reload from the feed in 240 s") }
            try await Task.sleep(for: .milliseconds(500))
        }
        XCTAssertNil(m.problem)
    }
}
