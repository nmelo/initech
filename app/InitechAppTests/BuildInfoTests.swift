import XCTest
@testable import InitechApp
@testable import InitechCore

final class BuildInfoTests: XCTestCase {
    func testVersionLine_ShowsVersionBuildAndCommit() {
        let info: [String: Any] = ["CFBundleShortVersionString": "0.1.0", "CFBundleVersion": "1", "InitechCommit": "1a2b3c4"]
        XCTAssertEqual(BuildInfo.versionLine(info), "0.1.0 (1) 1a2b3c4")
    }

    // A build whose commit could not be read, or whose stamp is not a short
    // sha, shows unknown, never the bad value.
    func testVersionLine_UnknownWhenTheCommitIsMissingOrNotASha() {
        for sha in [nil, "", "unknown", "abc", "1A2B3C4", "1a2b3c4-dirty", "1a2b3c4d5e6f7"] as [String?] {
            var info: [String: Any] = ["CFBundleShortVersionString": "0.1.0", "CFBundleVersion": "1"]
            if let sha { info["InitechCommit"] = sha }
            XCTAssertEqual(BuildInfo.versionLine(info), "0.1.0 (1) unknown", "stamp \(sha ?? "nil")")
        }
    }

    // AC4: this build's line, as About shows it, has the shape the bead asks for.
    func testCurrent_HasVersionBuildAndAStampedSha() {
        let line = BuildInfo.current
        XCTAssertNotNil(line.range(of: #"^\d+\.\d+\.\d+ \(\d+\) [0-9a-f]{7,12}$"#, options: .regularExpression),
                        "About shows \(line)")
    }

    // AC4: the stamped sha is the checkout's. app-tests.yml passes the
    // checkout's short sha as TEST_RUNNER_INITECH_EXPECT_COMMIT; a run without
    // it skips, so the skip count shows the check did not run.
    func testCurrent_CommitIsTheCheckoutTheBuildWasMadeFrom() throws {
        guard let want = ProcessInfo.processInfo.environment["INITECH_EXPECT_COMMIT"], !want.isEmpty else {
            throw XCTSkip("INITECH_EXPECT_COMMIT not set: run through app-tests.yml")
        }
        XCTAssertEqual(BuildInfo.commit(Bundle.main.infoDictionary ?? [:]), want)
    }
}
