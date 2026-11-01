import Foundation

/// PushPermission is what iOS says about notifications for the app.
enum PushPermission: Equatable, Sendable {
    case notDetermined
    case denied
    case allowed
}

/// NotificationAsking is the system's notification permission, behind a seam
/// so tests can answer for it. The app's is UNUserNotificationCenter.
protocol NotificationAsking: Sendable {
    func current() async -> PushPermission
    /// request shows the system's question; true when he allows.
    func request() async -> Bool
}

/// DeviceService is the service's two device calls: register this phone's
/// APNs device token for pushes, and remove it.
protocol DeviceService: Sendable {
    func registerDevice(token: String) async throws
    func removeDevice(token: String) async throws
}

/// TokenMemory keeps the last device token the service accepted, so that
/// unpairing can remove it after the app restarts.
protocol TokenMemory: AnyObject {
    var lastToken: String? { get set }
}

/// DefaultsTokenMemory keeps the token in UserDefaults. A device token is
/// Apple's address for this install, not a credential.
final class DefaultsTokenMemory: TokenMemory {
    private let defaults: UserDefaults
    private let key = "push.lastDeviceToken"
    init(defaults: UserDefaults = .standard) { self.defaults = defaults }
    var lastToken: String? {
        get { defaults.string(forKey: key) }
        set { defaults.set(newValue, forKey: key) }
    }
}

/// PushRegistrar registers this phone for pushes with the paired Mac. It
/// lives as long as the app process: after unlock with a pairing it asks for
/// notification permission once (only while iOS has not asked yet), then
/// asks iOS for the device token, and PUTs each token iOS hands over to the
/// service once per launch. A changed token is PUT again. Unpairing removes
/// the token from the service. Denied permission makes no call and leaves
/// the line that says pushes are off; a refused PUT leaves one line with the
/// service's words and is tried again only at the next launch.
@MainActor
final class PushRegistrar: ObservableObject {
    /// Line is the one line the home list shows about pushes, if any.
    enum Line: Equatable {
        /// Notifications are off for the app; the line offers Settings.
        case off
        /// The service did not take the token; its words.
        case refused(String)

        var words: String {
            switch self {
            case .off: "Pushes are off. Turn on notifications for initech in Settings to get them."
            case .refused(let why): "Pushes are not set up: the Mac said \(why). The app tries again next launch."
            }
        }
    }

    @Published private(set) var line: Line?

    private let asking: NotificationAsking
    private let memory: TokenMemory
    /// requestToken asks iOS for the device token; iOS answers through this
    /// registrar's received(token:), now or later, and again when it changes.
    private let requestToken: @MainActor (PushRegistrar) -> Void

    private var devices: DeviceService?
    private var pairingID: String?
    private var token: String?
    /// tried is the token already sent to this pairing in this launch, taken
    /// or refused: one PUT per launch per token.
    private var tried: String?

    init(asking: NotificationAsking, memory: TokenMemory, requestToken: @escaping @MainActor (PushRegistrar) -> Void) {
        self.asking = asking
        self.memory = memory
        self.requestToken = requestToken
    }

    /// begin runs every time the app opens unlocked with a pairing. Only the
    /// first time per pairing in a launch does anything; unlocking again
    /// after a lock is the same launch.
    func begin(devices: DeviceService, pairingID: String) async {
        guard pairingID != self.pairingID else { return }
        self.devices = devices
        self.pairingID = pairingID
        tried = nil
        var permission = await asking.current()
        if permission == .notDetermined {
            permission = await asking.request() ? .allowed : .denied
        }
        guard permission == .allowed else {
            line = .off
            return
        }
        line = nil
        requestToken(self)
        await sendIfDue()
    }

    /// received is iOS handing over the device token, as raw bytes.
    func received(token raw: Data) async {
        token = raw.map { String(format: "%02x", $0) }.joined()
        await sendIfDue()
    }

    /// unpair removes the token from the service before the pairing goes,
    /// one attempt: the Mac may already have refused this phone.
    func unpair() async {
        if let devices, let t = memory.lastToken {
            try? await devices.removeDevice(token: t)
        }
        memory.lastToken = nil
        devices = nil
        pairingID = nil
        tried = nil
    }

    private func sendIfDue() async {
        guard let devices, let token, token != tried, line != .off else { return }
        tried = token
        do {
            try await devices.registerDevice(token: token)
            memory.lastToken = token
            line = nil
        } catch {
            line = .refused(String(describing: error))
        }
    }
}

extension HTTPService: DeviceService {
    func registerDevice(token: String) async throws {
        struct Body: Encodable { var token: String }
        _ = try await call(request("PUT", "/v1/devices", body: try JSONEncoder.service.encode(Body(token: token))))
    }

    func removeDevice(token: String) async throws {
        _ = try await call(request("DELETE", "/v1/devices/\(seg(token))"))
    }
}
