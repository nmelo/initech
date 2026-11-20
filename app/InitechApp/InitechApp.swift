import InitechCore
import SwiftUI
import UIKit
import UserNotifications
import os

/// InitechApp is the phone and iPad client of initech's app service on the Mac.
@main
struct InitechApp: App {
    @StateObject private var lock = AppLock(store: InitechApp.store, authenticator: InitechApp.authenticator)
    @UIApplicationDelegateAdaptor private var delegate: AppDelegate

    private static var store: PairingStore {
        #if DEBUG
        if let p = UITestHooks.pairing { return FixedPairingStore(pairing: p) }
        #endif
        return KeychainPairingStore()
    }

    private static var authenticator: Authenticator {
        #if DEBUG
        if UITestHooks.pairing != nil { return AlwaysYes() }
        if UITestHooks.unlocked { return LaunchUnlocked(then: BiometricAuthenticator()) }
        #endif
        return BiometricAuthenticator()
    }
    @Environment(\.scenePhase) private var phase

    var body: some Scene {
        WindowGroup {
            RootView(lock: lock, push: delegate.push, inbox: delegate.inbox)
                // The lock hears every move, the first included: leaving
                // locks, and only being back in front asks for Face ID.
                .onChange(of: phase, initial: true) { _, now in
                    Task { await lock.moved(to: AppPhase(now)) }
                }
        }
    }
}

/// RootView shows the one screen AppLock says is due.
struct RootView: View {
    @ObservedObject var lock: AppLock
    @ObservedObject var push: PushRegistrar
    @ObservedObject var inbox: PushInbox

    var body: some View {
        switch lock.screen {
        case .pairing:
            PairingView { code in try lock.pair(with: code) }
        case .locked:
            LockView { await lock.unlock() }
        case .unlocked(let pairing):
            MainView(home: HomeModel(service: HTTPService(pairing: pairing)), push: push, inbox: inbox) {
                Task {
                    await push.unpair()
                    try? lock.forget()
                }
            }
            .task(id: pairing) { await push.begin(devices: HTTPService(pairing: pairing), pairingID: pairing.token) }
        }
    }
}

/// LockView is what shows while the app is locked. It asks for nothing by
/// itself: AppLock asks for Face ID when the app comes to the front, and the
/// button asks again after a cancel.
struct LockView: View {
    let unlock: () async -> Void

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "lock.fill").font(.largeTitle)
            Text("initech is locked")
            Button("Unlock") { Task { await unlock() } }
                .buttonStyle(.borderedProminent)
        }
    }
}

extension AppPhase {
    /// init reads SwiftUI's scene phase.
    init(_ phase: ScenePhase) {
        switch phase {
        case .active: self = .active
        case .background: self = .background
        default: self = .inactive
        }
    }
}

/// AppDelegate is where iOS hands over the APNs device token and each push;
/// it owns the app's one PushRegistrar and one PushInbox, which live as long
/// as the process.
@MainActor
final class AppDelegate: NSObject, UIApplicationDelegate, ObservableObject {
    let push: PushRegistrar
    let inbox = PushInbox()

    override init() {
        #if DEBUG
        if let fake = UITestHooks.push {
            let token = fake.token
            push = PushRegistrar(asking: fake.asking, memory: DefaultsTokenMemory()) { registrar in
                guard let token else { return }
                Task { await registrar.received(token: token) }
            }
            super.init()
            return
        }
        #endif
        push = PushRegistrar(asking: SystemNotificationAsking(), memory: DefaultsTokenMemory()) { _ in
            UIApplication.shared.registerForRemoteNotifications()
        }
        super.init()
    }

    /// The notification delegate is set before launch ends, so a push he
    /// tapped to open the app reaches the inbox.
    func application(_ application: UIApplication,
                     didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        #if DEBUG
        if let route = UITestHooks.tappedPush { inbox.tap(route) }
        #endif
        return true
    }

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        Task { await push.received(token: deviceToken) }
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        // No token, nothing to send: the next launch asks iOS again.
    }
}

extension AppDelegate: UNUserNotificationCenterDelegate {
    /// A push while the app is open: no banner for what is already on
    /// screen (the inbox's rule); the screen refreshes either way.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter,
                                            willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        let route = PushRoute(userInfo: notification.request.content.userInfo)
        return await inbox.present(route) ? [.banner, .list, .sound] : []
    }

    /// A tapped push opens what it names.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter,
                                            didReceive response: UNNotificationResponse) async {
        let route = PushRoute(userInfo: response.notification.request.content.userInfo)
        await inbox.tap(route)
    }
}

/// SystemNotificationAsking is iOS's own notification permission.
struct SystemNotificationAsking: NotificationAsking {
    func current() async -> PushPermission {
        switch await UNUserNotificationCenter.current().notificationSettings().authorizationStatus {
        case .notDetermined: .notDetermined
        case .denied: .denied
        default: .allowed
        }
    }

    func request() async -> Bool {
        (try? await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge])) ?? false
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

extension UITestHooks {
    /// push stands in for iOS in UI tests: `-uitest-push-permission
    /// allowed|denied|undecided` answers the permission, and `-uitest-push-token
    /// <hex>` is the device token iOS hands over (a simulator gets none).
    static var push: (asking: NotificationAsking, token: Data?)? {
        let args = ProcessInfo.processInfo.arguments
        guard let i = args.firstIndex(of: "-uitest-push-permission"), i + 1 < args.count else { return nil }
        let now: PushPermission = switch args[i + 1] {
        case "allowed": .allowed
        case "denied": .denied
        default: .notDetermined
        }
        var token: Data?
        if let j = args.firstIndex(of: "-uitest-push-token"), j + 1 < args.count {
            let hex = Array(args[j + 1].utf8)
            token = Data(stride(from: 0, to: hex.count - 1, by: 2).compactMap {
                UInt8(String(decoding: hex[$0...$0 + 1], as: UTF8.self), radix: 16)
            })
        }
        return (FixedAsking(now: now), token)
    }
}

extension UITestHooks {
    /// tappedPush is a push the UI test says he tapped to open the app:
    /// `-uitest-push-tap <payload JSON>`, fed to the same inbox a real tap
    /// reaches. A really delivered push comes through simctl (sim-test.sh).
    static var tappedPush: PushRoute? {
        let args = ProcessInfo.processInfo.arguments
        guard let i = args.firstIndex(of: "-uitest-push-tap"), i + 1 < args.count,
              let info = try? JSONSerialization.jsonObject(with: Data(args[i + 1].utf8)) as? [String: Any]
        else { return nil }
        return PushRoute(userInfo: info)
    }
}

/// FixedAsking answers the permission as the UI test says; asked once, he allows.
struct FixedAsking: NotificationAsking {
    let now: PushPermission
    func current() async -> PushPermission { now }
    func request() async -> Bool { true }
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

extension UITestHooks {
    /// unlocked is `-debug-unlocked`: a debug build started from the Mac over
    /// the cable opens without Face ID, with the pairing the device already
    /// holds, so a change can be looked at without unlocking by hand. Only that
    /// launch is skipped (LaunchUnlocked). The app opened from its icon carries
    /// no argument and asks as always. Release builds do not contain this.
    static var unlocked: Bool { ProcessInfo.processInfo.arguments.contains("-debug-unlocked") }

    /// screen is `-debug-screen <name>`: the screen such a launch opens on.
    static var screen: DebugScreen? { DebugScreen.parse(ProcessInfo.processInfo.arguments) }

    /// scroll is `-debug-scroll <points>`: how far down that screen starts, so a
    /// scrolled state can be looked at without a finger. Rigs reads it.
    static var scroll: CGFloat? {
        let args = ProcessInfo.processInfo.arguments
        guard let i = args.firstIndex(of: "-debug-scroll"), i + 1 < args.count, let y = Double(args[i + 1]) else { return nil }
        return CGFloat(y)
    }
}

/// LaunchUnlocked answers the first ask of a launch with yes, and hands every
/// later one to the real authenticator: after a trip to the background the app
/// asks for Face ID again.
final class LaunchUnlocked: Authenticator, Sendable {
    private let first = OSAllocatedUnfairLock(initialState: FirstAsk())
    private let real: Authenticator

    init(then real: Authenticator) { self.real = real }

    func authenticate(reason: String) async -> Bool {
        if first.withLock({ $0.take() }) { return true }
        return await real.authenticate(reason: reason)
    }
}
#endif
