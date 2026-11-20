import XCTest
@testable import InitechCore

/// ScreenTail.unsent reads an agent's pane, so it is tested against every
/// capture in the repo's footer corpus (internal/tui/testdata/footer_corpus:
/// the distinct footers and input boxes captured from every running rig,
/// rewritten to fixtures), not only the shapes its author met. The corpus is
/// the Go tests' own; which captures hold text in the box, and what text, is
/// their table (b5w_pending_input_test.go, cnp_labelled_box_test.go).
final class ScreenTailCorpusTests: XCTestCase {
    /// corpus is the folder, from this file's own place in the checkout.
    private var corpus: URL {
        var root = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { root.deleteLastPathComponent() } // InitechCoreTests, Tests, InitechCore, app, repo
        return root.appendingPathComponent("internal/tui/testdata/footer_corpus")
    }

    /// tail is what the service sends for a pane: the last 20 lines of its screen, trailing blank lines dropped.
    private func tail(_ name: String) throws -> [String] {
        var lines = try String(contentsOf: corpus.appendingPathComponent(name), encoding: .utf8).components(separatedBy: "\n")
        while let last = lines.last, last.trimmingCharacters(in: .whitespaces).isEmpty { lines.removeLast() }
        return Array(lines.suffix(20))
    }

    private func squeezed(_ s: String) -> String { s.filter { !$0.isWhitespace } }

    private static let short = "[from b] CNP03 short ping, no reply."
    private static let two = "[from b] CNP04 two lines: the rig moved to the new build.\nSecond line: no action needed; end CNP04."
    private static let long: String = {
        let tail = " end CNP01."
        var s = "[from b] CNP01 long one line: " + String(repeating: "the landing is on main and CI is green on the sha; ", count: 8)
        s = s.trimmingCharacters(in: .whitespaces)
        return String(s.prefix(300 - tail.count)) + tail
    }()

    /// holds is each capture taken with text waiting in the box, and the text (or how it begins).
    private static let holds: [String: String] = {
        var m = ["haiku312_busy_collapsed_w49.txt": "",
                 "haiku312_state_pending1_w32.txt": "[from b] B5W one line typed", "haiku312_state_pending1_w80.txt": "[from b] B5W one line typed",
                 "haiku312_state_pending2_w32.txt": "[from b] B5W two lines", "haiku312_state_pending2_w80.txt": "[from b] B5W two lines pasted, first line here"]
        for (body, text) in [("short", short), ("long", long), ("two", two)] {
            for cols in ["32", "40", "80"] { m["haiku312_label_\(body)_w\(cols).txt"] = text }
        }
        return m
    }()

    /// Feed it nothing: a missing or thinned corpus fails here, instead of passing every check below on no captures.
    func testTheCorpusIsThere() throws {
        let files = try FileManager.default.contentsOfDirectory(atPath: corpus.path).filter { $0.hasSuffix(".txt") }
        XCTAssertGreaterThanOrEqual(files.count, 40, "the footer corpus at \(corpus.path) holds \(files.count) captures")
        for name in Self.holds.keys { XCTAssertTrue(files.contains(name), "\(name) is not in the corpus: the table below names a capture that is gone") }
    }

    /// tallerThanTheBox is the captures whose draft is taller than Claude Code's input box: the box then
    /// shows only the draft's last lines, with its prompt mark on the first one it shows and nothing that
    /// says more is above. The pane does not show the start, so neither can the reader: it gives the end.
    private static let tallerThanTheBox: Set<String> = ["haiku312_label_long_w32.txt", "haiku312_label_long_w40.txt"]

    /// Every capture taken with text in its box gives that text, in order, whatever the pane's width:
    /// from its start, or, for a draft taller than the box, its end.
    func testEveryCaptureWithTextInItsBox_GivesThatText() throws {
        for (name, want) in Self.holds.sorted(by: { $0.key < $1.key }) {
            guard let got = ScreenTail.unsent(try tail(name)) else { XCTFail("\(name): no unsent text found"); continue }
            let text = squeezed(got.joined(separator: "\n"))
            XCTAssertFalse(text.isEmpty, name)
            if Self.tallerThanTheBox.contains(name) {
                XCTAssertTrue(squeezed(want).hasSuffix(text), "\(name): not the end of the draft: \(got)")
                XCTAssertGreaterThanOrEqual(got.count, 6, "\(name): fewer lines than the box shows: \(got)")
            } else {
                XCTAssertTrue(text.hasPrefix(squeezed(want)), "\(name): got \(got)")
            }
            XCTAssertFalse(got.contains { ScreenTail.isRule($0) }, "\(name): a rule of the box is in the text: \(got)")
            XCTAssertFalse(text.contains("bypasspermissions") || text.contains("⏵⏵"), "\(name): Claude Code's footer is in the text: \(got)")
        }
    }

    /// The labelled captures whose draft fits its box hold exactly their body: nothing of the label, the rules or the footer is added.
    func testTheLabelledCaptures_GiveExactlyTheirBody() throws {
        var checked = 0
        defer { XCTAssertEqual(checked, 7, "the labelled captures that fit their box") }
        for (name, want) in Self.holds where name.contains("_label_") && !Self.tallerThanTheBox.contains(name) {
            checked += 1
            let got = try XCTUnwrap(ScreenTail.unsent(try tail(name)), name)
            XCTAssertEqual(squeezed(got.joined(separator: "\n")), squeezed(want), name)
        }
    }

    /// Every other capture (an empty box, a dialog, a banner, a busy pane) gives nothing: never a cut where nothing is typed.
    func testEveryCaptureWithAnEmptyBox_GivesNothing() throws {
        let files = try FileManager.default.contentsOfDirectory(atPath: corpus.path).filter { $0.hasSuffix(".txt") }.sorted()
        var checked = 0
        for name in files where Self.holds[name] == nil {
            checked += 1
            let got = ScreenTail.unsent(try tail(name))
            XCTAssertNil(got, "\(name): read as unsent text: \(got ?? [])")
        }
        XCTAssertGreaterThanOrEqual(checked, 30, "only \(checked) captures without text were checked")
    }
}
