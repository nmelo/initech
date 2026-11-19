import Foundation

/// ContextMeter is how a pane's context percentage is drawn: as ten blocks,
/// and in the warning colour once it runs high.
public struct ContextMeter: Equatable, Sendable {
    public static let blocks = 10
    /// highFrom is the percentage from which context counts as running high.
    public static let highFrom = 75

    /// filled is how many of the blocks are lit, 0 to 10.
    public let filled: Int
    public let high: Bool

    public init(percent: Int) {
        let p = min(max(percent, 0), 100)
        filled = Int((Double(p) / 10).rounded())
        high = p >= Self.highFrom
    }
}
