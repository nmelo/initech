import Foundation

// Debug builds only: a release build contains none of this.
#if DEBUG

/// DebugScreen is a screen a debug build can be told to open on, by a launch
/// argument. Only the app's debug code reads it; a release build has no such
/// code (the app's own hooks are compiled under DEBUG).
public enum DebugScreen: Equatable, Sendable {
    case rigs, blockers, progress
    /// lock is the lock screen drawn to be looked at: nothing is locked and Face ID is not asked.
    case lock
    case pane(rig: String, pane: String)
    /// rigsPane is the Rigs screen with a pane opened from it.
    case rigsPane(rig: String, pane: String)

    /// parse reads `-initech-debug-screen <name>` from the launch arguments: rigs,
    /// blockers, progress, pane:<rig>/<pane>, or rigs:<rig>/<pane> (that pane
    /// opened from Rigs). No argument, or a name it does not know, is nil: the
    /// app opens on Pinned as it always does.
    public static func parse(_ args: [String]) -> DebugScreen? {
        guard let i = args.firstIndex(of: "-initech-debug-screen"), i + 1 < args.count else { return nil }
        let name = args[i + 1]
        switch name {
        case "rigs": return .rigs
        case "blockers": return .blockers
        case "progress": return .progress
        case "lock": return .lock
        default:
            let fromRigs = name.hasPrefix("rigs:")
            guard fromRigs || name.hasPrefix("pane:") else { return nil }
            let parts = name.dropFirst(5).split(separator: "/", maxSplits: 1, omittingEmptySubsequences: false)
            guard parts.count == 2, !parts[0].isEmpty, !parts[1].isEmpty else { return nil }
            let (rig, pane) = (String(parts[0]), String(parts[1]))
            return fromRigs ? .rigsPane(rig: rig, pane: pane) : .pane(rig: rig, pane: pane)
        }
    }
}

/// FirstAsk says yes once: to the first ask after it is made, and never again.
/// A debug launch that opens unlocked uses it so that only the launch itself
/// skips Face ID; every later return to the app asks as usual.
public struct FirstAsk: Equatable, Sendable {
    public private(set) var used = false

    public init() {}

    public mutating func take() -> Bool {
        if used { return false }
        used = true
        return true
    }
}
#endif
