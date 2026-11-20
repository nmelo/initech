import Foundation

/// BeadStage is a bead's stage as the app shows it: the icon the Mac shows
/// for it, plain words in place of the raw name, and an order for lists.
public enum BeadStage {
    /// icon is the stage's mark, the same one initech's board shows; a stage
    /// this build does not know gets a neutral dot.
    public static func icon(_ stage: String) -> String {
        switch stage {
        case "open": "○"
        case "in_progress": "▶"
        case "waiting", "waiting_for_release": "☾"
        case "blocked": "■"
        case "waiting_on_nelson": "?"
        case "in_qa", "ready_for_qa": "◎"
        case "qa_passed", "ready_to_ship": "◆"
        case "deferred": "❄"
        case "closed": "✓"
        default: "·"
        }
    }

    /// words is the stage in plain words: "in progress", never "in_progress".
    public static func words(_ stage: String) -> String {
        switch stage {
        case "": "no stage"
        case "ready_for_qa": "ready for QA"
        case "in_qa": "in QA"
        case "qa_passed": "QA passed"
        case "waiting_on_nelson": "waiting on you"
        default: stage.replacingOccurrences(of: "_", with: " ")
        }
    }

    /// rank orders a list: what needs someone first, work in flight next, parked last.
    public static func rank(_ stage: String) -> Int {
        switch stage {
        case "blocked": 0
        case "waiting_on_nelson": 1
        case "in_progress": 2
        case "ready_for_qa", "in_qa": 3
        case "qa_passed", "ready_to_ship": 4
        case "waiting", "waiting_for_release": 5
        case "open": 6
        case "deferred": 8
        case "closed": 9
        default: 7
        }
    }
}
