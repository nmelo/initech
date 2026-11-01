import SwiftUI

/// InitechApp is the phone and iPad client of initech's app service on the Mac.
@main
struct InitechApp: App {
    @StateObject private var lock = AppLock(store: KeychainPairingStore(),
                                            authenticator: BiometricAuthenticator())
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
            Text("Paired with \(pairing.host)") // the pinned-pane home lands in the next pieces
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
