import Foundation

// Debug builds only: a release build contains none of this.
#if DEBUG

/// DebugScreen is a screen a debug build can be told to open on, by a launch
/// argument. Only the app's debug code reads it; a release build has no such
/// code (the app's own hooks are compiled under DEBUG).
public enum DebugScreen: Equatable, Sendable {
    case rigs, blockers, progress, about
    case pane(rig: String, pane: String)

    /// parse reads `-debug-screen <name>` from the launch arguments: rigs,
    /// blockers, progress, about, or pane:<rig>/<pane>. No argument, or a name
    /// it does not know, is nil: the app opens on Panes as it always does.
    public static func parse(_ args: [String]) -> DebugScreen? {
        guard let i = args.firstIndex(of: "-debug-screen"), i + 1 < args.count else { return nil }
        let name = args[i + 1]
        switch name {
        case "rigs": return .rigs
        case "blockers": return .blockers
        case "progress": return .progress
        case "about": return .about
        default:
            guard name.hasPrefix("pane:") else { return nil }
            let parts = name.dropFirst("pane:".count).split(separator: "/", maxSplits: 1, omittingEmptySubsequences: false)
            guard parts.count == 2, !parts[0].isEmpty, !parts[1].isEmpty else { return nil }
            return .pane(rig: String(parts[0]), pane: String(parts[1]))
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
