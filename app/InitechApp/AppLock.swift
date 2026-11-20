import Foundation
import InitechCore
import LocalAuthentication

/// Authenticator asks the person holding the phone to prove they are its owner.
protocol Authenticator: Sendable {
    func authenticate(reason: String) async -> Bool
}

/// BiometricAuthenticator asks for Face ID (or Touch ID), falling back to the
/// device passcode, never to "no lock": a phone with no passcode set stays locked.
struct BiometricAuthenticator: Authenticator {
    func authenticate(reason: String) async -> Bool {
        let ctx = LAContext()
        var err: NSError?
        guard ctx.canEvaluatePolicy(.deviceOwnerAuthentication, error: &err) else { return false }
        return (try? await ctx.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason)) ?? false
    }
}

/// AppLock decides what the app shows: the token screen with no pairing, the
/// lock until the owner is proven, the app after. It locks again every time
/// the app leaves the foreground.
@MainActor
final class AppLock: ObservableObject {
    enum Screen: Equatable {
        case pairing
        case locked
        case unlocked(Pairing)
    }

    @Published private(set) var screen: Screen
    private let store: PairingStore
    private let authenticator: Authenticator
    /// ask is when the app asks for Face ID by itself (LockAsk).
    private var ask = LockAsk()

    init(store: PairingStore, authenticator: Authenticator) {
        self.store = store
        self.authenticator = authenticator
        self.screen = store.load() == nil ? .pairing : .locked
    }

    /// unlock asks for Face ID; the app opens only on a yes.
    func unlock() async {
        guard screen == .locked else { return }
        let ok = await authenticator.authenticate(reason: "Unlock initech")
        guard ok, let pairing = store.load() else { return }
        screen = .unlocked(pairing)
    }

    /// lock runs when the app goes to the background.
    func lock() {
        if case .unlocked = screen { screen = .locked }
    }

    /// moved is the app's place on the device changing. It is not async, and the
    /// scene calls it straight from its phase change: reaching the background
    /// locks before this returns, so the lock screen is what the app holds when
    /// iOS takes the app switcher's picture. A lock that ran a turn later, in a
    /// task, could leave the app's content in that picture. Only the Face ID ask
    /// waits, for the person, so it alone runs as a task, returned for a caller
    /// that needs its end. Face ID is asked only once the app is back in front,
    /// once per return: asked as the app left, its sheet came up over the home screen.
    @discardableResult
    func moved(to phase: AppPhase) -> Task<Void, Never>? {
        if phase == .background { lock() }
        guard ask.moved(to: phase, locked: screen == .locked) else { return nil }
        return Task { await unlock() }
    }

    /// pair stores a scanned or pasted pairing code. The person just proved
    /// they are at the Mac, so the app opens without a second check.
    func pair(with code: String) throws {
        let pairing = try Pairing.parse(code)
        try store.save(pairing)
        screen = .unlocked(pairing)
    }

    /// forget removes the pairing; the token screen comes back.
    func forget() throws {
        try store.clear()
        screen = .pairing
    }
}
