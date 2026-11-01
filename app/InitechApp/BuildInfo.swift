import Foundation

/// BuildInfo is the version line About shows: the marketing version, the build
/// number in parentheses, then the short commit the build was made from. The
/// commit is stamped into Info.plist (InitechCommit) by the build's
/// "Stamp the build commit" phase; a build whose commit could not be read
/// shows "unknown", never a stale sha.
enum BuildInfo {
    /// commitKey is the Info.plist key the build phase writes.
    static let commitKey = "InitechCommit"

    /// versionLine reads one Info.plist dictionary: "0.1.0 (1) 1a2b3c4".
    static func versionLine(_ info: [String: Any]) -> String {
        let version = info["CFBundleShortVersionString"] as? String ?? "unknown"
        let build = info["CFBundleVersion"] as? String ?? "unknown"
        return "\(version) (\(build)) \(commit(info))"
    }

    /// commit is the stamped short sha, or "unknown" when it is missing or is
    /// not 7 to 12 lowercase hex digits.
    static func commit(_ info: [String: Any]) -> String {
        guard let sha = info[commitKey] as? String,
              (7...12).contains(sha.count),
              sha.allSatisfy({ "0123456789abcdef".contains($0) }) else { return "unknown" }
        return sha
    }

    /// current is this build's line.
    static var current: String { versionLine(Bundle.main.infoDictionary ?? [:]) }
}
