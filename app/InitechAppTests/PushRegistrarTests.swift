import XCTest
@testable import InitechApp

/// FakeAsking answers for iOS's notification permission and counts the asks.
final class FakeAsking: NotificationAsking, @unchecked Sendable {
    var now: PushPermission
    var answer: Bool
    private(set) var asked = 0
    init(_ now: PushPermission, answer: Bool = true) { self.now = now; self.answer = answer }
    func current() async -> PushPermission { now }
    func request() async -> Bool {
        asked += 1
        now = answer ? .allowed : .denied
        return answer
    }
}

/// MemoryTokens is TokenMemory in memory, shared between two "launches".
final class MemoryTokens: TokenMemory {
    var lastToken: String?
}

/// PushRegistrarTests: the app registers its APNs device token with the
/// service once per launch, again when the token changes, and removes it on
/// unpairing; denied permission and a refused PUT each leave one line and
/// make no further call (ini-r0q.4 AC1, AC5). The real HTTPService talks to
/// the fixture server, which serves PUT and DELETE /v1/devices as the service does.
@MainActor
final class PushRegistrarTests: XCTestCase {
    var server: FixtureServer!
    var service: HTTPService!
    var memory: MemoryTokens!
    var tokenAsks = 0

    let tokenA = Data([0x0a, 0xb1, 0xff, 0x00, 0x7c])
    let hexA = "0ab1ff007c"
    let tokenB = Data([0xde, 0xad, 0xbe, 0xef])
    let hexB = "deadbeef"

    override func setUp() async throws {
        server = try FixtureServer()
        try await server.start()
        service = HTTPService(pairing: server.pairing)
        memory = MemoryTokens()
        tokenAsks = 0
    }

    override func tearDown() { server.stop() }

    /// launch is one app process: a new registrar over the same token memory.
    private func launch(_ asking: NotificationAsking) -> PushRegistrar {
        PushRegistrar(asking: asking, memory: memory) { [unowned self] _ in self.tokenAsks += 1 }
    }

    private func calls(_ method: String) -> [FixtureServer.Request] {
        server.requests.filter { $0.method == method && $0.path.hasPrefix("/v1/devices") }
    }

    func testALaunch_PutsItsTokenOnce_AsHex() async throws {
        let push = launch(FakeAsking(.allowed))
        await push.begin(devices: service, pairingID: server.token)
        await push.received(token: tokenA)
        // He locks and unlocks: the same launch, so no second PUT.
        await push.begin(devices: service, pairingID: server.token)
        await push.received(token: tokenA)

        XCTAssertEqual(calls("PUT").count, 1, "one launch must PUT its token exactly once")
        XCTAssertEqual(calls("PUT").first.map { String(decoding: $0.body, as: UTF8.self) }, #"{"token":"\#(hexA)"}"#)
        XCTAssertEqual(server.devices, [hexA])
        XCTAssertEqual(tokenAsks, 1)
        XCTAssertNil(push.line)
    }

    func testEveryLaunch_PutsAgain() async throws {
        let first = launch(FakeAsking(.allowed))
        await first.begin(devices: service, pairingID: server.token)
        await first.received(token: tokenA)
        let second = launch(FakeAsking(.allowed))
        await second.begin(devices: service, pairingID: server.token)
        await second.received(token: tokenA)
        XCTAssertEqual(calls("PUT").count, 2, "each launch registers, so a service that lost the device gets it back")
    }

    func testAChangedToken_IsPutAgain() async throws {
        let push = launch(FakeAsking(.allowed))
        await push.begin(devices: service, pairingID: server.token)
        await push.received(token: tokenA)
        await push.received(token: tokenB)
        XCTAssertEqual(calls("PUT").count, 2)
        XCTAssertEqual(server.devices, [hexA, hexB])
        XCTAssertEqual(memory.lastToken, hexB)
    }

    func testATokenBeforeBegin_IsPutOnceUnlocked() async throws {
        let push = launch(FakeAsking(.allowed))
        await push.received(token: tokenA) // iOS answered before he unlocked
        XCTAssertTrue(calls("PUT").isEmpty, "no pairing yet, nothing to PUT to")
        await push.begin(devices: service, pairingID: server.token)
        XCTAssertEqual(server.devices, [hexA])
    }

    func testUnpairing_DeletesTheToken() async throws {
        let push = launch(FakeAsking(.allowed))
        await push.begin(devices: service, pairingID: server.token)
        await push.received(token: tokenA)
        await push.unpair()
        XCTAssertEqual(calls("DELETE").map(\.path), ["/v1/devices/\(hexA)"])
        XCTAssertTrue(server.devices.isEmpty)
        XCTAssertNil(memory.lastToken)
    }

    func testUnpairingAfterARestart_DeletesTheRememberedToken() async throws {
        let first = launch(FakeAsking(.allowed))
        await first.begin(devices: service, pairingID: server.token)
        await first.received(token: tokenA)
        let second = launch(FakeAsking(.allowed))
        await second.begin(devices: service, pairingID: server.token)
        await second.unpair() // iOS has not handed this launch its token yet
        XCTAssertEqual(calls("DELETE").map(\.path), ["/v1/devices/\(hexA)"])
    }

    func testPermissionIsAskedOnlyWhileIOSHasNotAsked() async throws {
        let undecided = FakeAsking(.notDetermined)
        let push = launch(undecided)
        await push.begin(devices: service, pairingID: server.token)
        XCTAssertEqual(undecided.asked, 1)
        let allowed = FakeAsking(.allowed)
        await launch(allowed).begin(devices: service, pairingID: server.token)
        XCTAssertEqual(allowed.asked, 0, "the app asked again after he had answered")
    }

    func testPermissionDenied_MakesNoCall_AndSaysPushesAreOff() async throws {
        for asking in [FakeAsking(.denied), FakeAsking(.notDetermined, answer: false)] {
            let push = launch(asking)
            await push.begin(devices: service, pairingID: server.token)
            await push.received(token: tokenA)
            XCTAssertEqual(push.line, .off)
            XCTAssertTrue(push.line?.words.contains("Settings") == true)
        }
        XCTAssertEqual(tokenAsks, 0, "registration was attempted with pushes off")
        XCTAssertTrue(calls("PUT").isEmpty)
    }

    func testARefusedPut_LeavesOneLine_AndWaitsForTheNextLaunch() async throws {
        server.refuseDevices(503)
        let push = launch(FakeAsking(.allowed))
        await push.begin(devices: service, pairingID: server.token)
        await push.received(token: tokenA)
        await push.received(token: tokenA)
        await push.begin(devices: service, pairingID: server.token)
        XCTAssertEqual(calls("PUT").count, 1, "a refused PUT was retried in the same launch")
        XCTAssertEqual(push.line, .refused("devices are not stored here"))
        XCTAssertNil(memory.lastToken)

        server.refuseDevices(nil)
        let next = launch(FakeAsking(.allowed))
        await next.begin(devices: service, pairingID: server.token)
        await next.received(token: tokenA)
        XCTAssertEqual(server.devices, [hexA])
        XCTAssertNil(next.line)
    }

    func testANewPairing_RegistersWithIt() async throws {
        let push = launch(FakeAsking(.allowed))
        await push.begin(devices: service, pairingID: server.token)
        await push.received(token: tokenA)
        await push.unpair()
        await push.begin(devices: service, pairingID: server.token + "-new")
        XCTAssertEqual(calls("PUT").count, 2, "after pairing again the token must reach the new pairing")
    }
}
