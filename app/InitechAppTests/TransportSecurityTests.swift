import XCTest
@testable import InitechApp
@testable import InitechCore

/// TransportSecurityTests: the Mac's service is reached at a tailnet IP with
/// its own self-signed certificate. Without an App Transport Security setting
/// that lets the app's pin decide, iOS fails every connection with "ATS failed
/// system trust" (-9802) before the pin check runs. The simulator tests reach
/// 127.0.0.1, which ATS does not hold to that rule, so this guard reads the
/// setting itself; the device log is the proof that it works.
final class TransportSecurityTests: XCTestCase {
    func testTheAppLetsItsPinDecideTrust_NotSystemTrust() throws {
        let ats = try XCTUnwrap(Bundle.main.object(forInfoDictionaryKey: "NSAppTransportSecurity") as? [String: Any],
                                "no NSAppTransportSecurity: iOS refuses the Mac's self-signed certificate before the pin runs")
        XCTAssertEqual(ats["NSAllowsArbitraryLoads"] as? Bool, true, "ATS still requires system trust for the Mac's address: \(ats)")
        XCTAssertNil(ats["NSAllowsLocalNetworking"], "with NSAllowsLocalNetworking set, iOS ignores NSAllowsArbitraryLoads")
    }

    // Without this key every uploaded build waits in App Store Connect on the
    // export-compliance question, and no tester can install it.
    func testTheBuildDeclaresItUsesNoEncryptionOfItsOwn() {
        XCTAssertEqual(Bundle.main.object(forInfoDictionaryKey: "ITSAppUsesNonExemptEncryption") as? Bool, false,
                       "the built Info.plist does not answer the export-compliance question")
    }
}
