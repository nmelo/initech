import XCTest
@testable import InitechApp
@testable import InitechCore

/// KeyStrip is a view, so its test stays in the app; PaneModel's are in the package.
@MainActor
final class KeyStripTests: XCTestCase {
    func testKeyStrip_SaysWhichKeysActOnlyAtADialog() throws {
        let hint = try XCTUnwrap(KeyStrip.hint(atDialog: true))
        XCTAssertTrue(hint.contains("Enter says Yes"), hint)
        XCTAssertTrue(hint.contains("Esc says No"), hint)
        XCTAssertTrue(hint.contains("Ctrl-C do nothing"), hint)
        XCTAssertNil(KeyStrip.hint(atDialog: false))
    }
}
