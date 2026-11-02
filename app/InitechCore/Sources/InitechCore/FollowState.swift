import Foundation

/// FollowState decides what a new entry does to the conversation: at the
/// bottom, or for his own message, the view follows it; away from the bottom
/// the view stays where he is reading and the new-below line counts the
/// entries after the last one he saw. The count comes from the entries
/// themselves, not a running total, so entries landing in one update are all
/// counted.
public struct FollowState: Equatable {
    public enum Action: Equatable { case follow, mark }

    public private(set) var atBottom = true
    /// seen is the newest entry he has had at the bottom of the screen.
    public private(set) var seen: String?

    public init() {}

    /// arrived decides for the newest of ids. wasAtBottom says the entry before
    /// it (previous) was in view.
    public mutating func arrived(_ ids: [String], previous: String?, wasAtBottom: Bool, own: Bool) -> Action {
        guard let last = ids.last else { return .follow }
        if wasAtBottom || own {
            atBottom = true
            seen = last
            return .follow
        }
        atBottom = false
        // The entry he saw can leave the list (his sent copy, replaced by the
        // transcript's); count from the entry before this one instead.
        if seen.map({ ids.contains($0) }) != true { seen = previous }
        return .mark
    }

    /// sawBottom records that the newest entry, last, is in view.
    public mutating func sawBottom(_ last: String) {
        atBottom = true
        seen = last
    }

    public mutating func leftBottom() { atBottom = false }

    /// newBelow is how many of ids come after the last one he saw.
    public func newBelow(_ ids: [String]) -> Int {
        guard let seen, let i = ids.lastIndex(of: seen) else { return 0 }
        return ids.count - 1 - i
    }

    /// markerText is the new-below line, nil when nothing arrived below.
    public func markerText(_ ids: [String]) -> String? {
        switch newBelow(ids) {
        case 0: nil
        case 1: "new below"
        case let n: "\(n) new below"
        }
    }
}
