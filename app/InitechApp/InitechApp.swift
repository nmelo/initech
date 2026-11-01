import SwiftUI

/// InitechApp is the phone and iPad client of initech's app service on the Mac.
@main
struct InitechApp: App {
    @StateObject private var lock = AppLock(store: InitechApp.store, authenticator: InitechApp.authenticator)

    private static var store: PairingStore {
        #if DEBUG
        if let p = UITestHooks.pairing { return FixedPairingStore(pairing: p) }
        #endif
        return KeychainPairingStore()
    }

    private static var authenticator: Authenticator {
        #if DEBUG
        if UITestHooks.pairing != nil { return AlwaysYes() }
        #endif
        return BiometricAuthenticator()
    }
    @Environment(\.scenePhase) private var phase

    var body: some Scene {
        WindowGroup {
            RootView(lock: lock)
                .onChange(of: phase) { _, now in
                    if now == .background { lock.lock() }
                }
        }
    }
}

/// RootView shows the one screen AppLock says is due.
struct RootView: View {
    @ObservedObject var lock: AppLock

    var body: some View {
        switch lock.screen {
        case .pairing:
            PairingView { code in try lock.pair(with: code) }
        case .locked:
            LockView { await lock.unlock() }
        case .unlocked(let pairing):
            MainView(home: HomeModel(service: HTTPService(pairing: pairing))) { try? lock.forget() }
        }
    }
}

/// LockView asks for Face ID as soon as it appears, and again on a tap.
struct LockView: View {
    let unlock: () async -> Void

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "lock.fill").font(.largeTitle)
            Text("initech is locked")
            Button("Unlock") { Task { await unlock() } }
                .buttonStyle(.borderedProminent)
        }
        .task { await unlock() }
    }
}

#if DEBUG
/// UITestHooks lets the UI tests point a debug build at their fixture server:
/// `-uitest-pairing <pairing url>` pairs with it and skips Face ID, which a
/// simulator cannot answer unattended. Release builds do not contain this.
enum UITestHooks {
    static var pairing: Pairing? {
        let args = ProcessInfo.processInfo.arguments
        guard let i = args.firstIndex(of: "-uitest-pairing"), i + 1 < args.count else { return nil }
        return try? Pairing.parse(args[i + 1])
    }
}

struct FixedPairingStore: PairingStore {
    let pairing: Pairing
    func load() -> Pairing? { pairing }
    func save(_ pairing: Pairing) throws {}
    func clear() throws {}
}

struct AlwaysYes: Authenticator {
    func authenticate(reason: String) async -> Bool { true }
}
#endif
