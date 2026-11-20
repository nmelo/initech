import Foundation

/// RigStack lays out the Rigs screen as he scrolls it. Every rig's header is
/// always on screen. At the top of the scroll they sit one under another,
/// closed. Scrolling opens the first rig: its panes appear under its header
/// and the other headers slide to the bottom of the screen. Scrolling on moves
/// through its panes; when they end, the next header rises to sit under the
/// first, which closes that rig into its header, and the next rig's panes
/// follow. One rig alone starts open: there is nothing else to show first.
///
/// All of it is arithmetic on the scroll offset, so the screen never keeps a
/// state of its own for which rig is open.
public struct RigStack: Equatable, Sendable {
    /// rows is how many rows each rig has (at least one: a rig with nothing to
    /// list shows one line saying so).
    public let rows: [Int]
    public let header: Double
    public let row: Double
    /// viewport is the height the screen has for headers and rows.
    public let viewport: Double
    /// opening is how many points the first rig opens for each point scrolled.
    /// At 1 a full screen of scrolling was needed before it stood open, which
    /// felt slow; at 4 a short flick opens it.
    public let opening: Double
    public static let openingSpeed = 4.0

    public init(rows: [Int], header: Double, row: Double, viewport: Double, opening: Double = RigStack.openingSpeed) {
        self.rows = rows.map { max($0, 1) }
        self.header = header
        self.row = row
        self.viewport = viewport
        self.opening = max(1, opening)
    }

    var n: Int { rows.count }
    /// before is the height of the rows of the rigs before rig k.
    func before(_ k: Int) -> Double { Double(rows.prefix(k).reduce(0, +)) * row }
    var content: Double { before(n) }

    /// window is the room left for rows when every header is on screen.
    public var window: Double { viewport - Double(n) * header }
    /// fits says the headers leave room for at least two rows; if not, the
    /// screen falls back to a plain list.
    public var fits: Bool { n > 0 && window >= 2 * row }

    /// intro is the scroll that opens the first rig, before anything moves up.
    var intro: Double { n >= 2 ? min(window, content) / opening : 0 }
    /// length is how far the screen scrolls in all.
    public var length: Double { intro + max(0, Double(n) * header + content - viewport) }

    /// Layout is where things are at one scroll offset.
    public struct Layout: Equatable, Sendable {
        /// headerY is each header's top, from the top of the screen.
        public var headerY: [Double]
        /// window is the height showing under each header; 0 is a closed rig.
        public var window: [Double]
        /// shift is how far each rig's rows have moved up under its header (0 or less).
        public var shift: [Double]
    }

    public func layout(at offset: Double) -> Layout {
        let s = min(max(offset, 0), length)
        let moved = max(0, s - intro)          // how far the whole column has moved up
        var y: [Double] = []
        for k in 0..<n {
            let natural = Double(k) * header + before(k) - moved
            let top = Double(k) * header                               // closed, under the headers before it
            var bottom = viewport - Double(n - k) * header             // waiting at the bottom
            if n >= 2 { bottom = min(bottom, top + s * opening) }      // not yet slid down, at the start
            y.append(min(max(natural, top), max(bottom, top)))
        }
        var window: [Double] = [], shift: [Double] = []
        for k in 0..<n {
            let next = k + 1 < n ? y[k + 1] : viewport
            let natural = Double(k) * header + before(k) - moved
            // A header held above where its rig really is (closed at the start, or
            // waiting at the bottom) shows no rows: they have not been reached yet.
            if y[k] < natural - 0.001 {
                window.append(0); shift.append(0)
                continue
            }
            let rowsEnd = natural + header + Double(rows[k]) * row
            window.append(max(0, min(next, rowsEnd) - (y[k] + header)))
            shift.append(min(0, natural - y[k]))                       // rows already gone up under the header
        }
        return Layout(headerY: y, window: window, shift: shift)
    }

    /// offset(opening:) is the scroll at which rig k is the open one, at its first row.
    public func offset(opening k: Int) -> Double {
        min(length, intro + before(min(max(k, 0), n)))
    }
}
