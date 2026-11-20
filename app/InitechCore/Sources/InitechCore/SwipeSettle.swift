import CoreGraphics

/// SwipeSettle is where a row slid left comes to rest when the finger lifts:
/// closed again, open on its keys, or, slid far, doing its action.
public enum SwipeSettle: Equatable, Sendable {
    case closed, open, act

    /// of takes where the slide ended (negative is left), how many keys the row
    /// has behind it and one key's width. Past half a key the row stays open. A
    /// far slide does the action only on a row with one key: with more, the row
    /// just opens, so a slide never picks between rename and unpin for him.
    public static func of(end: CGFloat, keys: Int, key: CGFloat) -> SwipeSettle {
        guard keys > 0, end <= -key / 2 else { return .closed }
        return keys == 1 && end < -key * 1.6 ? .act : .open
    }
}
