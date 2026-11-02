import XCTest

/// `swift test` may run on the shared Mac only because nothing in this package
/// opens a socket: a listening binary can put a local-network question on the
/// screen of the person at that Mac (app/README.md). A server test belongs in
/// the app's tests, on a simulator. The package also stays free of the UI, so
/// it keeps building on a Mac.
final class NoListenerCensusTests: XCTestCase {
    /// The needles are built here, so this file does not match itself.
    static let banned = ["import " + "Network", "NW" + "Listener", "NW" + "Connection", "Fixture" + "Server",
                         "CFSocket" + "Create", "socket" + "(", "listen" + "(", "import " + "UIKit", "import " + "SwiftUI"]

    /// hits is every banned phrase in text.
    static func hits(_ text: String) -> [String] { banned.filter(text.contains) }

    private static let package = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()

    private func swiftFiles(_ dir: String) throws -> [URL] {
        let root = Self.package.appendingPathComponent(dir)
        let walker = try XCTUnwrap(FileManager.default.enumerator(at: root, includingPropertiesForKeys: nil))
        return walker.compactMap { $0 as? URL }.filter { $0.pathExtension == "swift" }
    }

    func testNothingInThePackageListensOrImportsTheUI() throws {
        let files = try swiftFiles("Tests") + swiftFiles("Sources")
        XCTAssertGreaterThanOrEqual(files.count, 20, "the census read too little: \(files.count) files")
        for f in files {
            let found = Self.hits(try String(contentsOf: f, encoding: .utf8))
            XCTAssertEqual(found, [], "\(f.lastPathComponent) must not contain these")
        }
    }

    // The census reads what it claims: each banned phrase is caught, and a
    // file of ordinary code passes.
    func testTheCensusCatchesEachPhrase() {
        for phrase in Self.banned {
            XCTAssertEqual(Self.hits("let x = 1\n\(phrase)\n"), [phrase])
        }
        XCTAssertEqual(Self.hits("import Foundation\nlet u = URLSession.shared\n"), [])
    }
}
