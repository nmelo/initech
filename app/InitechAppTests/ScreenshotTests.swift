import XCTest
import SwiftUI
import UIKit
@testable import InitechApp

/// ScreenshotTests renders the Blockers and Progress views with fixture data
/// in a window of the test host (inside the simulator, never on the Mac's
/// screen) and writes each as a PNG to SHOT_DIR when the run sets it
/// (xcodebuild passes TEST_RUNNER_SHOT_DIR through as SHOT_DIR).
@MainActor
final class ScreenshotTests: XCTestCase {
    func testBlockersAndProgressRender() async throws {
        let rigs = try JSONDecoder.service.decode([RigBlockers].self, from: Data(shotBlockersJSON.utf8))
        let blockers = BlockersModel(source: FakeBlockers(rigs))
        let reports = try JSONDecoder.service.decode([ProgressReport].self, from: Data(progressJSON.utf8))
        let progress = ProgressModel(source: FakeProgress(reports))

        let b = try await shot("blockers", NavigationStack { BlockersView(blockers: blockers) { _, _ in } })
        XCTAssertTrue(blockers.loaded, "the view read its model")
        XCTAssertEqual(blockers.sections.map(\.rig), ["initech", "theguy"])
        let p = try await shot("progress", NavigationStack { EffortProgressView(progress: progress) })
        XCTAssertEqual(progress.cards.count, 1)
        XCTAssertFalse(b.isEmpty || p.isEmpty)
    }

    /// shot hosts the view full screen, lets it load, and returns its PNG.
    private func shot<V: View>(_ name: String, _ view: V) async throws -> Data {
        let scene = try XCTUnwrap(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first)
        let window = UIWindow(windowScene: scene)
        window.rootViewController = UIHostingController(rootView: view)
        window.makeKeyAndVisible()
        defer { window.isHidden = true }
        try await Task.sleep(for: .milliseconds(800)) // the view's .task reload and a layout pass
        let image = UIGraphicsImageRenderer(bounds: window.bounds).image { _ in
            window.drawHierarchy(in: window.bounds, afterScreenUpdates: true)
        }
        let png = try XCTUnwrap(image.pngData())
        if let dir = ProcessInfo.processInfo.environment["SHOT_DIR"], !dir.isEmpty {
            try png.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name).png"))
        }
        return png
    }
}

/// shotBlockersJSON is one of each row kind: a waiting item with and without
/// a recommendation, two stuck panes, and a quiet rig.
let shotBlockersJSON = """
[{"rig":"initech","waiting":[{"pane":"super","text":"Your go for the v2.50 patch release","recommendation":"ship it: two fixes, both QA passed"},\
{"pane":"super","text":"Keep the old pairing link working for a week?","recommendation":""}],\
"stuck":[{"pane":"eng4","reason":"permission prompt open"},{"pane":"eng2","reason":"ini-a1 quiet 1h40m, its owner idle"}],"quiet":false},\
{"rig":"theguy","waiting":[],"stuck":[],"quiet":true}]
"""
