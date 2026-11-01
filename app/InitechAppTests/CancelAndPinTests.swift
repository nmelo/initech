import XCTest
import os
@testable import InitechApp

/// ScriptedService answers the conversation with the errors it is given, in
/// order, then with a page; everything else is never called by these tests.
final class ScriptedService: Service, @unchecked Sendable {
    private var failures: [Error]
    private(set) var conversationCalls = 0
    /// hold makes each conversation read wait this long first (cancellable).
    var hold: Duration = .zero
    var page = Conversation(session: "s", messages: [Message(id: "m1", at: nil, kind: .agent, text: "hello", footer: nil, tool: nil)],
                            startReached: true, before: 0)

    init(failing: [Error]) { failures = failing }
    func failNext(_ e: Error) { failures.append(e) }

    func conversation(rig: String, pane: String, before: Int64?, limit: Int) async throws -> Conversation {
        conversationCalls += 1
        if hold > .zero { try await Task.sleep(for: hold) }
        if !failures.isEmpty { throw failures.removeFirst() }
        return before == nil ? page : Conversation(session: "s", messages: [Message(id: "m0", at: nil, kind: .agent, text: "older", footer: nil, tool: nil)], startReached: true, before: 0)
    }

    func rigs() async throws -> [RigInfo] { [] }
    func status() async throws -> StatusReply { StatusReply(rigs: []) }
    func pins() async throws -> [Pin] { [] }
    func savePins(_ pins: [Pin]) async throws {}
    func send(rig: String, pane: String, text: String) async throws -> SendResult { SendResult(verdict: "delivered", words: "") }
    func key(rig: String, pane: String, key: PaneKey) async throws -> SendResult { SendResult(verdict: "delivered", words: "") }
    func feed() -> AsyncStream<FeedEvent> { AsyncStream { $0.finish() } }
}

/// CancelAndPinTests: on Nelson's iPhone a pane read "Could not read the
/// conversation: cancelled" while the service logged the iPhone hanging up in
/// the TLS handshake. A cancel of the view's own task shows nothing and the
/// read runs once more; a handshake our pin check ended shows its reason and
/// is never taken for a cancel; the pin check is per request; any challenge
/// that is not server trust gets the system's default handling.
@MainActor
final class CancelAndPinTests: XCTestCase {
    private let ref = PaneRef(rig: "initech", pane: "eng1")

    /// logged collects what Log.shown records while body runs.
    private func logged(_ body: () async -> Void) async -> [ShownError] {
        let box = OSAllocatedUnfairLock<[ShownError]>(initialState: [])
        Log.record { e in box.withLock { $0.append(e) } }
        defer { Log.record(to: nil) }
        await body()
        return box.withLock { $0 }
    }

    func testACancelIsNotLogged_AndAShownFailureIsLoggedOnce() async {
        let quiet = await logged { await PaneModel(service: ScriptedService(failing: [CancellationError()]), ref: ref).load() }
        XCTAssertEqual(quiet, [], "a cancel, which nothing shows, was logged")
        let shown = await logged { await PaneModel(service: ScriptedService(failing: [ServiceError.handshakeEnded("no certificate")]), ref: ref).load() }
        XCTAssertEqual(shown, [ShownError(subsystem: "co.nmelo.initech", category: "pane", domain: "co.nmelo.initech.service", code: -3)])
    }

    func testACancelledRead_ShowsNothing_AndReadsOnceMore() async {
        let service = ScriptedService(failing: [CancellationError()])
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        XCTAssertNil(pane.problem, "a cancel was shown as a failure")
        XCTAssertEqual(service.conversationCalls, 2)
        XCTAssertEqual(pane.messages.map(\.id), ["m1"])
    }

    func testTwoCancelsInARow_ShowNothing_AndStop() async {
        let service = ScriptedService(failing: [CancellationError(), CancellationError()])
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        XCTAssertNil(pane.problem)
        XCTAssertEqual(service.conversationCalls, 2, "a cancel must be retried once, not looped")
    }

    func testAnEarlierLine_StaysThroughACancel() async {
        let service = ScriptedService(failing: [URLError(.timedOut), CancellationError(), CancellationError()])
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        let shown = pane.problem
        XCTAssertNotNil(shown)
        await pane.load()
        XCTAssertEqual(pane.problem, shown, "a cancel changed the line already shown")
        XCTAssertTrue(pane.messages.isEmpty)
    }

    func testTheAppsOwnReload_CancellingALoadInFlight_ShowsNothing() async throws {
        let service = ScriptedService(failing: [])
        service.hold = .seconds(5)
        let pane = PaneModel(service: service, ref: ref)
        let first = Task { await pane.load() }
        try await Task.sleep(for: .milliseconds(100))
        first.cancel() // the view's .task ends as the pane leaves the screen
        await first.value
        XCTAssertNil(pane.problem, "the cancelled load showed a line")
        XCTAssertEqual(service.conversationCalls, 1, "a load whose own task was cancelled read again")
        service.hold = .zero
        await pane.load() // the pane is shown again: its .task reads once more
        XCTAssertEqual(service.conversationCalls, 2)
        XCTAssertEqual(pane.messages.map(\.id), ["m1"])
        XCTAssertNil(pane.problem)
    }

    func testACancelledOlderPage_ShowsNothing_AndReadsOnceMore() async {
        let service = ScriptedService(failing: [])
        service.page = Conversation(session: "s", messages: service.page.messages, startReached: false, before: 40)
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        let service2Calls = service.conversationCalls
        service.failNext(CancellationError())
        await pane.loadOlder()
        XCTAssertNil(pane.problem)
        XCTAssertEqual(service.conversationCalls - service2Calls, 2, "a cancelled older page must be read once more")
        XCTAssertEqual(pane.messages.map(\.id), ["m0", "m1"])
    }

    func testATransportFailure_ShowsAPlainSentence() async {
        let pane = PaneModel(service: ScriptedService(failing: [HTTPService.failure(URLError(.timedOut), check: PinCheck(fingerprint: "x"))]), ref: ref)
        await pane.load()
        let line = pane.problem ?? ""
        XCTAssertTrue(line.hasPrefix("Could not read the conversation: "), line)
        for raw in ["URLError", "NSURLErrorDomain", "Code=", "-1001", "unreachable(", "CancellationError"] {
            XCTAssertFalse(line.contains(raw), "the line shows raw error text \(raw): \(line)")
        }
    }

    func testAPinRefusal_NeverSaysCancelled() async {
        let pane = PaneModel(service: ScriptedService(failing: [HTTPService.failure(URLError(.cancelled), check: Self.refusedCheck())]), ref: ref)
        await pane.load()
        XCTAssertFalse(pane.problem?.contains("cancel") ?? true, pane.problem ?? "no line")
    }

    static func refusedCheck() -> PinCheck {
        let c = PinCheck(fingerprint: "x")
        _ = c.decide(method: NSURLAuthenticationMethodServerTrust, trust: try! trust(), host: "h")
        return c
    }

    func testAHandshakeOurCheckEnded_IsShown_WithItsReason() async {
        let service = ScriptedService(failing: [ServiceError.handshakeEnded("no certificate came with the server-trust challenge")])
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        XCTAssertEqual(pane.problem, "Could not read the conversation: the app ended the connection: no certificate came with the server-trust challenge")
        XCTAssertEqual(service.conversationCalls, 1)
    }

    func testARefusedCertificate_IsShown() async {
        let pane = PaneModel(service: ScriptedService(failing: [ServiceError.certificateRefused]), ref: ref)
        await pane.load()
        XCTAssertEqual(pane.problem, "Could not read the conversation: its certificate is not the one this phone paired with")
    }

    // MARK: the request's own pin check

    func testOnlyACancelThatIsNotTheChecks_IsACancel() {
        let cancelled = URLError(.cancelled)
        XCTAssertTrue(HTTPService.failure(cancelled, check: PinCheck(fingerprint: "x")) is CancellationError)
        XCTAssertTrue(HTTPService.failure(CancellationError(), check: PinCheck(fingerprint: "x")) is CancellationError)

        let refused = PinCheck(fingerprint: "x")
        _ = refused.decide(method: NSURLAuthenticationMethodServerTrust, trust: try! Self.trust(), host: "100.1.2.3")
        XCTAssertEqual(refused.verdict, .refused)
        XCTAssertEqual(HTTPService.failure(cancelled, check: refused) as? ServiceError, .certificateRefused)

        let ended = PinCheck(fingerprint: "x")
        _ = ended.decide(method: NSURLAuthenticationMethodServerTrust, trust: nil, host: "100.1.2.3")
        XCTAssertEqual(HTTPService.failure(cancelled, check: ended) as? ServiceError,
                       .handshakeEnded("no certificate came with the server-trust challenge"))

        XCTAssertEqual(HTTPService.failure(URLError(.timedOut), check: PinCheck(fingerprint: "x")) as? ServiceError,
                       .unreachable("it did not answer in time"))
    }

    func testAChallengeThatIsNotServerTrust_GetsDefaultHandling() {
        let check = PinCheck(fingerprint: "x")
        for method in [NSURLAuthenticationMethodClientCertificate, NSURLAuthenticationMethodHTTPBasic] {
            let (disposition, credential) = check.decide(method: method, trust: nil, host: "100.1.2.3")
            XCTAssertEqual(disposition, .performDefaultHandling, "\(method) was cancelled, which hangs up the handshake")
            XCTAssertNil(credential)
        }
        XCTAssertEqual(check.verdict, .none)
    }

    func testThePinnedCertificate_IsUsed_AndEachCheckKeepsItsOwnVerdict() throws {
        let trust = try Self.trust()
        let good = PinCheck(fingerprint: PinCheck.leafFingerprint(trust)!)
        let bad = PinCheck(fingerprint: "some other certificate")
        XCTAssertEqual(bad.decide(method: NSURLAuthenticationMethodServerTrust, trust: trust, host: "h").0, .cancelAuthenticationChallenge)
        XCTAssertEqual(good.decide(method: NSURLAuthenticationMethodServerTrust, trust: trust, host: "h").0, .useCredential)
        XCTAssertEqual(bad.verdict, .refused, "a later request's match overwrote this request's refusal")
        XCTAssertEqual(good.verdict, .matched)
    }

    /// trust is a SecTrust over the fixture server's test certificate.
    static func trust() throws -> SecTrust {
        let der = try XCTUnwrap(Data(base64Encoded: testCertificateDER))
        let cert = try XCTUnwrap(SecCertificateCreateWithData(nil, der as CFData))
        var trust: SecTrust?
        SecTrustCreateWithCertificates(cert, SecPolicyCreateBasicX509(), &trust)
        return try XCTUnwrap(trust)
    }
}

/// testCertificateDER is a self-signed P-256 certificate (CN=initech-test-pin)
/// made for these tests only; its key was never kept and nothing trusts it.
let testCertificateDER = "MIICGTCCAcACCQDsTq16jmHA+TAKBggqhkjOPQQDAjAbMRkwFwYDVQQDDBBpbml0ZWNoLXRlc3QtcGluMB4XDTI2MTEwMTIwMDUyNFoXDTM2MTAyOTIwMDUyNFowGzEZMBcGA1UEAwwQaW5pdGVjaC10ZXN0LXBpbjCCAUswggEDBgcqhkjOPQIBMIH3AgEBMCwGByqGSM49AQECIQD/////AAAAAQAAAAAAAAAAAAAAAP///////////////zBbBCD/////AAAAAQAAAAAAAAAAAAAAAP///////////////AQgWsY12Ko6k+ez671VdpiGvGUdBrDMU7D2O848PifSYEsDFQDEnTYIhucEk2pmeOETnSa3gZ9+kARBBGsX0fLhLEJH+Lzm5WOkQPJ3A32BLeszoPShOUXYmMKWT+NC4v4af5uO5+tKfA+eFivOM1drMV7Oy7ZAaDe/UfUCIQD/////AAAAAP//////////vOb6racXnoTzucrC/GMlUQIBAQNCAAQ82O4TvCSEqAgCLG+3RQjj3FNmbWYsCRKoWPpGNbQgnNebxxZLWwOn1KBC7hn7awZCrqh/Bi1Ctw7cX4dfJwJpMAoGCCqGSM49BAMCA0cAMEQCIDZsAKrnj4ANIEZWV3MyQqcil3o1PZRpK+iRcEdKGza4AiA/dGfYq9M2kRiJ8y4CBXZyn3XLSs92g2ntgcEwfaSNnQ=="
