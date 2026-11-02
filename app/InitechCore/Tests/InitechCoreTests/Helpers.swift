import Foundation
import XCTest
@testable import InitechCore

/// Fixtures for the shapes the app service sends, used across the app's tests.
enum Fx {
    static func agent(_ role: String, state: String? = "idle", beads: [String]? = [], context: Int? = 40,
                      banner: String? = "none", screen: [String]? = nil) -> AgentStatus {
        AgentStatus(role: role, state: state, alive: true, beads: beads, contextPercent: context, banner: banner, screen: screen)
    }
    static func rig(_ name: String, _ agents: [AgentStatus]) -> RigStatus {
        RigStatus(rig: name, root: "/r/\(name)", readAt: "2026-11-01T03:00:00Z", agents: agents)
    }
    static func msg(_ id: String, _ kind: Message.Kind, _ text: String, footer: [String]? = nil,
                    tool: Message.Tool? = nil) -> Message {
        Message(id: id, at: nil, kind: kind, text: text, footer: footer, tool: tool)
    }
}

/// waitFor polls a condition for at most two seconds.
func waitFor(_ cond: @escaping @Sendable () -> Bool, file: StaticString = #filePath, line: UInt = #line) async throws {
    let deadline = Date().addingTimeInterval(2)
    while !cond() {
        if Date() > deadline { XCTFail("condition not met in 2 s", file: file, line: line); return }
        try await Task.sleep(for: .milliseconds(10))
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
