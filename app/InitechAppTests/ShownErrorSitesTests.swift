import XCTest
@testable import InitechApp
@testable import InitechCore

/// FailingService fails every call with one error, so each real model below
/// reaches the line it shows.
final class FailingService: Service, BlockersSource, ProgressSource, DeviceService, @unchecked Sendable {
    let error: Error
    init(_ error: Error) { self.error = error }
    func rigs() async throws -> [RigInfo] { throw error }
    func status() async throws -> StatusReply { throw error }
    func pins() async throws -> [Pin] { throw error }
    func savePins(_ pins: [Pin]) async throws { throw error }
    func conversation(rig: String, pane: String, before: Int64?, limit: Int) async throws -> Conversation { throw error }
    func send(rig: String, pane: String, text: String) async throws -> SendResult { throw error }
    func key(rig: String, pane: String, key: PaneKey) async throws -> SendResult { throw error }
    func feed() -> AsyncStream<FeedEvent> { AsyncStream { $0.finish() } }
    func blockers() async throws -> [RigBlockers] { throw error }
    func progress() async throws -> [ProgressReport] { throw error }
    func registerDevice(token: String) async throws { throw error }
    func removeDevice(token: String) async throws {}
}

/// ShownErrorSitesTests: every error a screen shows writes exactly one log
/// entry, through Log.shown, from the real model that shows it (ini-j9i AC1,
/// AC3). The list of sites is on the bead; a new site gets a case here.
@MainActor
final class ShownErrorSitesTests: XCTestCase {
    private let ref = PaneRef(rig: "initech", pane: "eng1")
    private let unreachable = ServiceError.unreachable("The request timed out.")
    private let refused = ServiceError.status(409, "the pane is gone")

    private func entry(_ category: String, _ code: Int) -> ShownError {
        ShownError(subsystem: "co.nmelo.initech", category: category, domain: "co.nmelo.initech.service", code: code)
    }

    /// logged collects what Log.shown records while body runs.
    private func logged(_ body: () async -> Void) async -> [ShownError] {
        let entries = Entries()
        Log.record { entries.add($0) }
        defer { Log.record(to: nil) }
        await body()
        return entries.all
    }

    func testHome_A401AndACertificateAndNoAnswerEachLogOneEntry() async {
        for (error, code) in [(ServiceError.unauthorized, 401), (.certificateRefused, -2), (unreachable, -1)] {
            let home = HomeModel(service: FailingService(error))
            let got = await logged { await home.reload() }
            XCTAssertEqual(got, [entry("home", code)], "\(error)")
            XCTAssertNotNil(home.problem, "\(error) was not shown")
        }
    }

    func testPane_AConversationThatFailsToLoadLogsOneEntry() async {
        let pane = PaneModel(service: FailingService(unreachable), ref: ref)
        let got = await logged { await pane.load() }
        XCTAssertEqual(got, [entry("pane", -1)])
        XCTAssertNotNil(pane.problem)
    }

    func testPane_ARefusedSendAndAnUnansweredSendEachLogOneEntry() async {
        for (error, code) in [(refused, 409), (unreachable, -1)] {
            let pane = PaneModel(service: FailingService(error), ref: ref)
            let got = await logged { await pane.send("hello") }
            XCTAssertEqual(got, [entry("pane", code)], "\(error)")
        }
    }

    func testPane_ARefusedKeyAndAnUnansweredKeyEachLogOneEntry() async {
        for (error, code) in [(refused, 409), (unreachable, -1)] {
            let pane = PaneModel(service: FailingService(error), ref: ref)
            let got = await logged { await pane.press(.enter) }
            XCTAssertEqual(got, [entry("pane", code)], "\(error)")
            XCTAssertEqual(pane.lastKey?.result.verdict, "not_delivered")
        }
    }

    func testBlockers_AFailedReadLogsOneEntry() async {
        let blockers = BlockersModel(source: FailingService(unreachable))
        let got = await logged { await blockers.reload() }
        XCTAssertEqual(got, [entry("blockers", -1)])
        XCTAssertNotNil(blockers.problem)
    }

    func testProgress_AFailedReadLogsOneEntry() async {
        let progress = ProgressModel(source: FailingService(unreachable))
        let got = await logged { await progress.reload() }
        XCTAssertEqual(got, [entry("progress", -1)])
        XCTAssertNotNil(progress.problem)
    }

    func testPush_ARefusedRegistrationLogsOneEntry() async {
        let push = PushRegistrar(asking: FakeAsking(.allowed), memory: MemoryTokens()) { _ in }
        let service = FailingService(ServiceError.status(503, "devices are not stored here"))
        let got = await logged {
            await push.begin(devices: service, pairingID: "p")
            await push.received(token: Data([0x0a]))
        }
        XCTAssertEqual(got, [entry("push", 503)])
        XCTAssertEqual(push.line, .refused("devices are not stored here"))
    }

    // Notifications turned off is his setting, not an error: the line shows
    // and nothing is logged (positive twin: the refusal above is).
    func testPush_NotificationsOffShowsItsLineAndLogsNothing() async {
        let push = PushRegistrar(asking: FakeAsking(.denied), memory: MemoryTokens()) { _ in }
        let got = await logged { await push.begin(devices: FailingService(unreachable), pairingID: "p") }
        XCTAssertEqual(got, [])
        XCTAssertEqual(push.line, .off)
    }
}
