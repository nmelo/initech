import Foundation

/// ReachLine is what the list says about reaching the Mac while its first read
/// has no answer yet. An unanswered connection is given a minute before it
/// fails, and until then nothing else on the screen says why the list is empty.
public enum ReachLine: Equatable, Sendable {
    /// none: nothing to say. trying: the read has waited long enough to say so.
    case none, trying

    /// patience is how long a first read may take before the list says it is
    /// still trying; a read that answers sooner shows no line at all.
    public static let patience: TimeInterval = 3

    /// of is the line for a first read that has waited this long; nil is no read in flight.
    public static func of(waited: TimeInterval?) -> ReachLine {
        guard let waited, waited >= patience else { return .none }
        return .trying
    }
}
