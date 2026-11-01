import XCTest
@testable import InitechApp

/// MemoryPairingStore is a PairingStore for tests that never touches the keychain.
final class MemoryPairingStore: PairingStore, @unchecked Sendable {
    var stored: Pairing?
    init(_ p: Pairing? = nil) { stored = p }
    func load() -> Pairing? { stored }
    func save(_ pairing: Pairing) throws { stored = pairing }
    func clear() throws { stored = nil }
}

/// ScriptedAuthenticator answers each Face ID request with the next scripted answer.
final class ScriptedAuthenticator: Authenticator, @unchecked Sendable {
    var answers: [Bool]
    private(set) var asked = 0
    init(_ answers: [Bool]) { self.answers = answers }
    func authenticate(reason: String) async -> Bool {
        asked += 1
        return answers.isEmpty ? false : answers.removeFirst()
    }
}

@MainActor
final class AppLockTests: XCTestCase {
    let paired = Pairing(host: "h", port: 1, token: "t", fingerprint: "fp")

    func testNoToken_ShowsTheTokenScreen() {
        let lock = AppLock(store: MemoryPairingStore(), authenticator: ScriptedAuthenticator([true]))
        XCTAssertEqual(lock.screen, .pairing)
    }

    func testWithToken_OpensLockedAndAsksForFaceID() async {
        let auth = ScriptedAuthenticator([true])
        let lock = AppLock(store: MemoryPairingStore(paired), authenticator: auth)
        XCTAssertEqual(lock.screen, .locked)
        await lock.unlock()
        XCTAssertEqual(auth.asked, 1)
        XCTAssertEqual(lock.screen, .unlocked(paired))
    }

    func testFaceIDRefused_StaysLocked() async {
        let lock = AppLock(store: MemoryPairingStore(paired), authenticator: ScriptedAuthenticator([false]))
        await lock.unlock()
        XCTAssertEqual(lock.screen, .locked)
    }

    func testLeavingTheForeground_LocksAgain() async {
        let auth = ScriptedAuthenticator([true, true])
        let lock = AppLock(store: MemoryPairingStore(paired), authenticator: auth)
        await lock.unlock()
        lock.lock()
        XCTAssertEqual(lock.screen, .locked)
        await lock.unlock()
        XCTAssertEqual(auth.asked, 2, "returning to the app must ask for Face ID again")
    }

    func testPairing_StoresTheTokenAndOpens() throws {
        let store = MemoryPairingStore()
        let lock = AppLock(store: store, authenticator: ScriptedAuthenticator([]))
        try lock.pair(with: "initech://pair?host=h&port=1&token=t&fp=fp")
        XCTAssertEqual(store.stored, paired)
        XCTAssertEqual(lock.screen, .unlocked(paired))
    }

    func testBadPairingCode_StoresNothingAndStaysOnTheTokenScreen() {
        let store = MemoryPairingStore()
        let lock = AppLock(store: store, authenticator: ScriptedAuthenticator([]))
        XCTAssertThrowsError(try lock.pair(with: "not a code"))
        XCTAssertNil(store.stored)
        XCTAssertEqual(lock.screen, .pairing)
    }

    func testForget_ReturnsToTheTokenScreen() async throws {
        let store = MemoryPairingStore(paired)
        let lock = AppLock(store: store, authenticator: ScriptedAuthenticator([true]))
        await lock.unlock()
        try lock.forget()
        XCTAssertNil(store.stored)
        XCTAssertEqual(lock.screen, .pairing)
    }
}

final class KeychainPairingStoreTests: XCTestCase {
    let store = KeychainPairingStore(service: "co.nmelo.initech.tests.\(UUID().uuidString)")

    override func tearDown() { try? store.clear() }

    func testRoundTrip_ThenClear() throws {
        XCTAssertNil(store.load())
        let p = Pairing(host: "100.64.1.2", port: 7443, token: "abc", fingerprint: "fp")
        try store.save(p)
        XCTAssertEqual(store.load(), p)
        try store.save(Pairing(host: "h2", port: 2, token: "new", fingerprint: "fp"))
        XCTAssertEqual(store.load()?.token, "new", "a second pairing replaces the first")
        try store.clear()
        XCTAssertNil(store.load())
    }
}
