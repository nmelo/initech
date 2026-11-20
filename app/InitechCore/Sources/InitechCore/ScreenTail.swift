import Foundation

/// ScreenTail tidies the last lines of a pane's terminal screen for the block
/// that shows them: the lines arrive as the terminal drew them, padded and
/// indented, with blank rows between and after.
public enum ScreenTail {
    /// tidy drops the blank lines at either end, makes each run of blank lines
    /// one, removes trailing spaces, and removes the indent every line shares
    /// (a deeper indent stays, so a dialog keeps its shape).
    public static func tidy(_ lines: [String]) -> [String] {
        var out: [String] = []
        for raw in lines {
            var line = raw
            while let last = line.last, last == " " || last == "\t" { line.removeLast() }
            if line.isEmpty && (out.isEmpty || out.last == "") { continue }
            out.append(line)
        }
        while out.last == "" { out.removeLast() }
        let indent = out.filter { !$0.isEmpty }.map { $0.prefix { $0 == " " }.count }.min() ?? 0
        return indent == 0 ? out : out.map { String($0.dropFirst(min(indent, $0.count))) }
    }
}
