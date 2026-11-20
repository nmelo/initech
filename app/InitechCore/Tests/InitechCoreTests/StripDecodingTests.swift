import XCTest
@testable import InitechCore

/// A pane's list strip in the status answer: read when the service sends it,
/// and nothing breaks when it does not.
final class StripDecodingTests: XCTestCase {
    private func agent(_ json: String) throws -> AgentStatus {
        try JSONDecoder.service.decode(AgentStatus.self, from: Data(json.utf8))
    }

    func testStrip_IsReadInTheAgentsOrder() throws {
        let a = try agent(##"{"role":"super","alive":true,"strip":["#1 Release rig-a? Recommended yes.","#2 Register the CI runner?"]}"##)
        XCTAssertEqual(a.strip, ["#1 Release rig-a? Recommended yes.", "#2 Register the CI runner?"])
    }

    /// Feed it nothing: a service that sends no strip, a null one and an empty one all decode.
    func testStrip_AbsentNullOrEmpty_Decodes() throws {
        XCTAssertNil(try agent(##"{"role":"super","alive":true}"##).strip)
        XCTAssertNil(try agent(##"{"role":"super","alive":true,"strip":null}"##).strip)
        XCTAssertEqual(try agent(##"{"role":"super","alive":true,"strip":[]}"##).strip, [])
    }
}
