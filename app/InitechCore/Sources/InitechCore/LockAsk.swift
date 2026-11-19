import Foundation

/// AppPhase is where the app is on the device: in front and taking touches,
/// in front but not taking them (the app switcher, a system sheet such as
/// Face ID's own), or off screen.
public enum AppPhase: Equatable, Sendable {
    case active, inactive, background
}

/// LockAsk decides when the app asks for Face ID by itself. It asks once each
/// time the app comes to the front locked: at launch, and after every trip to
/// the background. It never asks on the way out (the app locks as it leaves,
/// and asking then put the Face ID sheet over the home screen), and never a
/// second time when the Face ID sheet itself closes: after a cancel, the Unlock
/// button is the way in.
public struct LockAsk: Equatable, Sendable {
    /// due says the next arrival in front, locked, asks.
    public private(set) var due = true

    public init() {}

    /// moved takes the app's new phase and whether it is locked, and says
    /// whether to ask for Face ID now.
    public mutating func moved(to phase: AppPhase, locked: Bool) -> Bool {
        switch phase {
        case .background:
            due = true
            return false
        case .inactive:
            return false
        case .active:
            guard locked, due else { return false }
            due = false
            return true
        }
    }
}
