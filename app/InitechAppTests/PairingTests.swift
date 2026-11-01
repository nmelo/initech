import XCTest
@testable import InitechApp

final class PairingTests: XCTestCase {
    func testParse_ReadsHostPortAndToken() throws {
        let p = try Pairing.parse("initech://pair?host=100.64.1.2&port=7443&token=abc123&fp=VHB_Alrix0-c7Y54C8jARvt75FtDg9pE7fzwrcILHXs")
        XCTAssertEqual(p, Pairing(host: "100.64.1.2", port: 7443, token: "abc123", fingerprint: "VHB_Alrix0-c7Y54C8jARvt75FtDg9pE7fzwrcILHXs"))
        XCTAssertEqual(p.baseURL.absoluteString, "https://100.64.1.2:7443")
    }

    func testParse_IgnoresThePastedLinesWhitespace() throws {
        let p = try Pairing.parse("  initech://pair?host=h&port=1&token=t&fp=f\n")
        XCTAssertEqual(p.token, "t")
    }

    func testParse_RefusesAnythingThatIsNotAPairingCode() {
        for code in ["", "https://pair?host=h&port=1&token=t", "initech://other?host=h&port=1&token=t", "abc123"] {
            XCTAssertThrowsError(try Pairing.parse(code), code) {
                XCTAssertEqual($0 as? PairingError, .notAPairingCode, code)
            }
        }
    }

    func testParse_RefusesAMissingOrEmptyField() {
        let cases: [(String, String)] = [
            ("initech://pair?port=1&token=t", "host"),
            ("initech://pair?host=h&token=t", "port"),
            ("initech://pair?host=h&port=1", "token"),
            ("initech://pair?host=h&port=1&token=&fp=f", "token"),
            ("initech://pair?host=h&port=1&token=t", "fp"),
            ("initech://pair?host=h&port=1&token=t&fp=", "fp"),
        ]
        for (code, field) in cases {
            XCTAssertThrowsError(try Pairing.parse(code), code) {
                XCTAssertEqual($0 as? PairingError, .missing(field), code)
            }
        }
    }

    func testParse_RefusesAPortOutOfRange() {
        for port in ["0", "65536", "x"] {
            XCTAssertThrowsError(try Pairing.parse("initech://pair?host=h&port=\(port)&token=t&fp=f")) {
                XCTAssertEqual($0 as? PairingError, .badPort, port)
            }
        }
    }
}
