import XCTest
@testable import InitechApp
@testable import InitechCore

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

    /// The app's moves on the device, as the scene reports them: reaching the
    /// background locks, every time, and asks nothing on the way out; being back
    /// in front asks once.
    func testReachingTheBackground_Locks_EveryTime_AndOnlyTheReturnAsks() async {
        let auth = ScriptedAuthenticator([true, true, true])
        let lock = AppLock(store: MemoryPairingStore(paired), authenticator: auth)
        await lock.moved(to: .active)?.value
        XCTAssertEqual(lock.screen, .unlocked(paired))
        XCTAssertEqual(auth.asked, 1)
        for trip in 1...2 {
            await lock.moved(to: .inactive)?.value
            await lock.moved(to: .background)?.value
            XCTAssertEqual(lock.screen, .locked, "trip \(trip): the app did not lock on reaching the background")
            XCTAssertEqual(auth.asked, trip, "trip \(trip): Face ID was asked as the app left")
            await lock.moved(to: .inactive)?.value
            XCTAssertEqual(auth.asked, trip, "trip \(trip): Face ID was asked before the app was back in front")
            await lock.moved(to: .active)?.value
            XCTAssertEqual(auth.asked, trip + 1, "trip \(trip): the return did not ask")
            XCTAssertEqual(lock.screen, .unlocked(paired))
        }
    }

    /// The lock is in place when the move returns, not a moment later: iOS takes
    /// the app switcher's picture as the app reaches the background, and a lock
    /// that ran after it would leave the app's content in that picture. This test
    /// is not async on purpose: nothing here may need another turn of the run loop.
    func testReachingTheBackground_LocksBeforeTheMoveReturns() throws {
        let lock = AppLock(store: MemoryPairingStore(), authenticator: ScriptedAuthenticator([]))
        try lock.pair(with: "initech://pair?host=h&port=1&token=t&fp=fp")
        XCTAssertEqual(lock.screen, .unlocked(paired), "the test must start unlocked, or it shows nothing")
        lock.moved(to: .background)
        XCTAssertEqual(lock.screen, .locked, "the app was still unlocked when its move to the background returned")
    }

    /// Inactive is not background: Control Center, a banner or the app switcher
    /// half-open neither lock the app nor ask for Face ID.
    func testInactiveAlone_NeitherLocksNorAsks() async {
        let auth = ScriptedAuthenticator([true, true])
        let lock = AppLock(store: MemoryPairingStore(paired), authenticator: auth)
        await lock.moved(to: .active)?.value
        await lock.moved(to: .inactive)?.value
        XCTAssertEqual(lock.screen, .unlocked(paired), "going inactive locked the app")
        await lock.moved(to: .active)?.value
        XCTAssertEqual(auth.asked, 1, "coming back from inactive asked for Face ID")
        XCTAssertEqual(lock.screen, .unlocked(paired))
    }

    /// The debug unlocked launch skips one ask and no more: the first ask of that
    /// launch is answered yes without Face ID, and every later one is Face ID's.
    func testTheDebugUnlockedLaunch_SkipsOnlyItsFirstAsk() async {
        let real = ScriptedAuthenticator([true, false])
        let lock = AppLock(store: MemoryPairingStore(paired), authenticator: LaunchUnlocked(then: real))
        await lock.moved(to: .active)?.value
        XCTAssertEqual(lock.screen, .unlocked(paired))
        XCTAssertEqual(real.asked, 0, "the launch itself asked Face ID")
        await lock.moved(to: .background)?.value
        await lock.moved(to: .active)?.value
        XCTAssertEqual(real.asked, 1, "the return after the launch did not go to Face ID")
        await lock.moved(to: .background)?.value
        await lock.moved(to: .active)?.value
        XCTAssertEqual(real.asked, 2)
        XCTAssertEqual(lock.screen, .locked, "Face ID said no and the app opened")
    }

    /// The option is a launch argument and nothing else: a launch without it is
    /// a plain launch (this test process is one), whatever an earlier launch carried.
    func testWithoutItsLaunchArgument_TheOptionIsOff() {
        XCTAssertFalse(ProcessInfo.processInfo.arguments.contains("-initech-debug-unlocked"), "this test must run without the option")
        XCTAssertFalse(UITestHooks.unlocked)
        XCTAssertNil(UITestHooks.screen)
        XCTAssertFalse(UserDefaults.standard.dictionaryRepresentation().keys.contains { $0.contains("debug-unlocked") },
                       "the option was saved somewhere that outlives a launch")
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
