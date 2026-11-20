import XCTest
@testable import InitechCore

final class ToolSummaryTests: XCTestCase {
    func testLine_CountsEachKind_InTheOrderTheyFirstAppeared() {
        XCTAssertEqual(ToolSummary.text(["Read", "Bash", "Bash", "Read", "Bash", "Bash", "Bash"]), "Read 2 files, ran 5 shell commands")
        XCTAssertEqual(ToolSummary.text(["Bash"]), "Ran 1 shell command")
        XCTAssertEqual(ToolSummary.text(["Bash", "Read"]), "Ran 1 shell command, read 1 file")
    }

    func testLine_EveryKindHasItsWords() {
        XCTAssertEqual(ToolSummary.text(["Edit", "Write", "MultiEdit"]), "Edited 3 files")
        XCTAssertEqual(ToolSummary.text(["Grep", "Glob"]), "Searched for 2 patterns")
        XCTAssertEqual(ToolSummary.text(["WebFetch"]), "Fetched 1 page")
        XCTAssertEqual(ToolSummary.text(["WebSearch", "WebSearch"]), "Searched the web 2 times")
        XCTAssertEqual(ToolSummary.text(["Task"]), "Ran 1 agent")
        XCTAssertEqual(ToolSummary.text(["bash", "READ"]), "Ran 1 shell command, read 1 file", "tool names are matched whatever their case")
    }

    /// A tool the app does not know is still counted, as a tool.
    func testLine_AnUnknownTool_IsCountedAsATool() {
        XCTAssertEqual(ToolSummary.text(["mcp__easel__chart"]), "Used 1 tool")
        XCTAssertEqual(ToolSummary.text(["Read", "SomethingNew", "", "Another"]), "Read 1 file, used 3 tools")
    }

    /// Feed it nothing: no line.
    func testLine_NoTools_IsEmpty() {
        XCTAssertEqual(ToolSummary.text([]), "")
        XCTAssertEqual(ToolSummary.parts([]), [])
    }

    /// Only the counts are marked as numbers, so only they are set in bold.
    func testParts_OnlyTheCountsAreNumbers() {
        let parts = ToolSummary.parts(["Read", "Read", "Bash"])
        XCTAssertEqual(parts.filter(\.number).map(\.text), ["2", "1"])
        XCTAssertEqual(parts.filter { !$0.number }.map(\.text), ["Read ", " files", ", ran ", " shell command"])
    }

    private func m(_ id: String, _ kind: Message.Kind, tool: String? = nil) -> Message {
        Message(id: id, at: nil, kind: kind, text: "", footer: nil, tool: tool.map { .init(name: $0, summary: "", detail: nil) })
    }

    func testRows_AnUnbrokenRunOfToolCallsIsOneRow() {
        let rows = ConversationRow.group([m("1", .nelson), m("2", .tool, tool: "Read"), m("3", .tool, tool: "Bash"),
                                          m("4", .agent), m("5", .tool, tool: "Bash"), m("6", .agent)])
        XCTAssertEqual(rows.map(\.id), ["1", "2", "4", "5", "6"])
        guard case .tools(let run) = rows[1].kind else { return XCTFail("the two tool calls are not one row") }
        XCTAssertEqual(run.map(\.id), ["2", "3"])
        guard case .tools(let one) = rows[3].kind else { return XCTFail("a single tool call is not a row of its own") }
        XCTAssertEqual(one.map(\.id), ["5"])
        guard case .message(let first) = rows[0].kind else { return XCTFail("a message became a tool row") }
        XCTAssertEqual(first.id, "1")
    }

    func testRows_NoMessages_NoRows_AndNoToolsChangesNothing() {
        XCTAssertEqual(ConversationRow.group([]), [])
        XCTAssertEqual(ConversationRow.group([m("1", .agent), m("2", .nelson)]).map(\.id), ["1", "2"])
    }
}
