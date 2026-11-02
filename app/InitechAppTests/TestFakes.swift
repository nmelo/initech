import Foundation
@testable import InitechApp
@testable import InitechCore

// The fakes the app's own tests share. The package's tests keep their own
// copies: a test target cannot import another test target.

/// FakeBlockers answers blockers() with a fixed list and counts the calls.
final class FakeBlockers: BlockersSource, @unchecked Sendable {
    var answer: [RigBlockers]
    var calls = 0
    init(_ answer: [RigBlockers]) { self.answer = answer }
    func blockers() async throws -> [RigBlockers] {
        calls += 1
        return answer
    }
}

/// The JSON GET /v1/blockers serves (internal/appserve TestBlockers_JSONShape).
let blockersJSON = """
[{"rig":"initech","waiting":[],"stuck":[],"quiet":true},\
{"rig":"theguy","waiting":[{"pane":"super","text":"Your go","recommendation":""}],"stuck":[],"quiet":false}]
"""

final class FakeProgress: ProgressSource, @unchecked Sendable {
    var answer: [ProgressReport]
    var calls = 0
    init(_ answer: [ProgressReport]) { self.answer = answer }
    func progress() async throws -> [ProgressReport] {
        calls += 1
        return answer
    }
}

/// One report as initech progress --json prints it (internal/progress
/// Report), with a time on every field that has one.
let progressJSON = """
[{"rig":"initech","prefix":"ini-r0q.","at":"2026-11-01T04:00:00Z","hours":3,
"beads":[{"id":"ini-r0q.3","stage":"in_progress","owner":"eng7","ac":"2/6","ac_proven":2,"ac_items":6,"commits":1,
"last_move":"2026-11-01T03:50:00Z","by":"eng7","title":"App step 2B: the blockers view and the progress view"},
{"id":"ini-r0q.9","stage":"in_qa","owner":"qa1","ac":"-","ac_proven":null,"ac_items":null,"commits":null,
"last_move":"2026-11-01T03:00:00Z","by":"","title":"keystroke"}],
"closed":4,"total":11,"worked_by_engineers":3,"writing_review_only":1,"stages":{"closed":4,"in_progress":5},
"closed_recent":[{"id":"ini-r0q.11","title":"a","closed_at":"2026-11-01T02:10:00Z"},{"id":"ini-r0q.12","title":"b","closed_at":"2026-11-01T03:10:00Z"}],
"critical_path":[{"id":"ini-r0q.1","waiting":4,"stage":"in_progress","title":"service"}],
"engineers":["eng5","eng6","eng7"],
"quiet":[{"id":"ini-r0q.9","owner":"qa1","wait":"stalled"}],"notes":[]}]
"""

/// An effort with nothing listed: the service may print null lists.
let emptyProgressJSON = """
[{"rig":"hover","prefix":"hov-a.","at":"2026-11-01T04:00:00Z","hours":3,"beads":null,"closed":0,"total":1,
"worked_by_engineers":0,"writing_review_only":0,"stages":null,"closed_recent":null,"critical_path":null,
"engineers":null,"quiet":null,"notes":null}]
"""
