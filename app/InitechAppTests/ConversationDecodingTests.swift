import XCTest
@testable import InitechApp

/// ConversationDecodingTests: on Nelson's iPad a real pane failed to show its
/// conversation at all, DecodingError.dataCorrupted at messages[7].kind,
/// because the service sends a kind ("message", another agent's message) the
/// app did not know. A kind the app does not know shows as plain text, and one
/// entry it cannot read never costs the page. The JSON is the service's shape
/// (internal/appserve/transcript.go).
final class ConversationDecodingTests: XCTestCase {
    private func page(_ entries: String) throws -> Conversation {
        let json = #"{"session":"s","start_reached":true,"messages":[\#(entries)]}"#
        return try JSONDecoder.service.decode(Conversation.self, from: Data(json.utf8))
    }

    private let agent = #"{"id":"1","at":"2026-11-01T13:00:00-05:00","kind":"agent","text":"done","footer":["▶ ship?"]}"#
    private let fromAgent = #"{"id":"2","at":"2026-11-01T13:01:00-05:00","kind":"message","from":"super","text":"[from super] go ahead"}"#

    func testAMessageFromAnotherAgent_Decodes_WithItsSender() throws {
        let p = try page([agent, fromAgent].joined(separator: ","))
        XCTAssertEqual(p.messages.map(\.kind), [.agent, .message])
        XCTAssertEqual(p.messages[1].from, "super")
        XCTAssertEqual(p.messages[1].text, "[from super] go ahead")
    }

    func testAKindTheAppDoesNotKnow_ReadsAsPlainText() throws {
        let future = #"{"id":"3","at":"2026-11-01T13:02:00-05:00","kind":"something_new","text":"still readable"}"#
        let p = try page([agent, future].joined(separator: ","))
        XCTAssertEqual(p.messages.map(\.kind), [.agent, .other])
        XCTAssertEqual(p.messages[1].text, "still readable")
    }

    func testOneEntryTheAppCannotRead_IsSkipped_AndThePageStillShows() throws {
        let broken = #"{"at":"2026-11-01T13:03:00-05:00","kind":"agent"}"#   // no id, no text
        let p = try page([agent, broken, fromAgent].joined(separator: ","))
        XCTAssertEqual(p.messages.map(\.id), ["1", "2"])
    }

    /// The P1 on Nelson's iPhone and iPad: "Could not read the conversation:
    /// DecodingError.typeMismatch ... Path: before. Expected to decode String
    /// but found number". The service's cursor is a number, the byte offset of
    /// the page's first line, and 0 on the page that starts the transcript
    /// (internal/appserve/transcript.go Page). This is that page, verbatim.
    func testTheServicesCursor_IsANumber_AndTheStartPageSaysZero() throws {
        let older = #"{"session":"s","messages":[\#(agent)],"start_reached":false,"before":48213,"skipped_lines":0}"#
        let p = try JSONDecoder.service.decode(Conversation.self, from: Data(older.utf8))
        XCTAssertEqual(p.before, 48213)
        XCTAssertFalse(p.startReached)
        let start = #"{"session":"s","messages":[\#(agent)],"start_reached":true,"before":0,"skipped_lines":0}"#
        let q = try JSONDecoder.service.decode(Conversation.self, from: Data(start.utf8))
        XCTAssertEqual(q.before, 0)
        XCTAssertTrue(q.startReached)
        let none = #"{"session":"s","messages":[\#(agent)],"start_reached":true}"#
        XCTAssertNil(try JSONDecoder.service.decode(Conversation.self, from: Data(none.utf8)).before)
    }

    func testALiveMessageEntry_FromTheFeed_IsShown() {
        let e = FeedEvent.decode(type: "transcript", data: #"{"rig":"initech","pane":"eng1","message":\#(fromAgent)}"#)
        guard case let .transcript(_, _, m)? = e else { return XCTFail("the feed dropped a message entry: \(String(describing: e))") }
        XCTAssertEqual(m.kind, .message)
        XCTAssertEqual(m.from, "super")
    }
}
