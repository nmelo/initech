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

    /// unsent is the text left in the pane's input box, cut out of its last
    /// screen lines, or nil when the box cannot be found for certain: the
    /// caller then shows the lines as they are, never a wrong cut. The box is
    /// what lies between the last two rules (lines drawn mostly of "─"); its
    /// first line carries the prompt mark "❯", unless the top rule says more
    /// lines are above ("↑ 11 more"), which becomes the first line returned.
    public static func unsent(_ lines: [String]) -> [String]? {
        let rules = lines.indices.filter { isRule(lines[$0]) }
        guard rules.count >= 2 else { return nil }
        let (top, bottom) = (rules[rules.count - 2], rules[rules.count - 1])
        var box = Array(lines[(top + 1)..<bottom])
        let above = lines[top].replacingOccurrences(of: "─", with: "").trimmingCharacters(in: .whitespaces)
        let more = above.hasPrefix("↑") && above.hasSuffix("more")
        guard let first = box.first else { return nil }
        let marked = first.trimmingCharacters(in: .whitespaces).hasPrefix("❯")
        guard marked || more else { return nil }
        if marked, let mark = first.firstIndex(of: "❯") {
            // The mark and the space after it are the width of the indent the next lines carry,
            // whatever kind of space follows it (Claude Code uses a no-break space).
            var rest = first[first.index(after: mark)...]
            if rest.first?.isWhitespace == true { rest = rest.dropFirst() }
            box[0] = String(repeating: " ", count: first.distance(from: first.startIndex, to: mark) + 2) + rest
        }
        let text = tidy(box)
        guard !text.isEmpty else { return nil }
        return (more ? [above] : []) + text
    }

    /// isRule is true for a line of the input box's frame: long, and almost all "─".
    static func isRule(_ line: String) -> Bool {
        let t = line.trimmingCharacters(in: .whitespaces)
        guard t.count >= 20, t.hasPrefix("─") else { return false }
        return t.filter { $0 == "─" }.count * 10 >= t.count * 6
    }
}
