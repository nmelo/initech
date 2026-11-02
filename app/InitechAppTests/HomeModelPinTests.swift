import XCTest
@testable import InitechApp
@testable import InitechCore

/// The one HomeModel test that needs a real TLS handshake: a changed
/// certificate, refused by the pin. The rest run in the package on an
/// in-memory service.
@MainActor
final class HomeModelPinTests: XCTestCase {
    var server: FixtureServer!

    override func setUp() async throws {
        server = try FixtureServer()
        try await server.start()
    }

    override func tearDown() { server.stop() }

    func testARefusedCertificate_SaysSoAndDoesNotClaimNothingIsPinned() async throws {
        var p = server.pairing
        p.fingerprint = Data("some other certificate".utf8).sha256Base64URL
        let home = HomeModel(service: HTTPService(pairing: p))
        await home.reload()
        XCTAssertFalse(home.showsNoPinsLine, "the list failed to load; it is not known to be empty")
        let problem = try XCTUnwrap(home.problem)
        XCTAssertTrue(problem.contains("certificate"), problem)
        XCTAssertTrue(home.needsPairing, "a changed certificate must offer pairing again")
    }
}

/// waitForMain polls a main-actor condition for at most two seconds.
@MainActor
func waitForMain(_ cond: () -> Bool, file: StaticString = #filePath, line: UInt = #line) async throws {
    let deadline = Date().addingTimeInterval(2)
    while !cond() {
        if Date() > deadline { XCTFail("condition not met in 2 s", file: file, line: line); return }
        try await Task.sleep(for: .milliseconds(10))
    }
}
