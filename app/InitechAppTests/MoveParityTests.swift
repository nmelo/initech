import SwiftUI
import XCTest
@testable import InitechCore

/// The package has no SwiftUI, so HomeModel reorders with its own moved. List's
/// onMove hands it the offsets SwiftUI's move takes, so the two must agree:
/// every list up to five long, every set of offsets, every destination.
final class MoveParityTests: XCTestCase {
    func testMovedAgreesWithSwiftUIsMove() {
        var checked = 0
        for n in 0...5 {
            let list = Array(0..<n)
            for bits in 0..<(1 << n) {
                let source = IndexSet((0..<n).filter { bits & (1 << $0) != 0 })
                for destination in 0...n {
                    var theirs = list
                    theirs.move(fromOffsets: source, toOffset: destination)
                    XCTAssertEqual(list.moved(fromOffsets: source, toOffset: destination), theirs,
                                   "n \(n) source \(Array(source)) destination \(destination)")
                    checked += 1
                }
            }
        }
        XCTAssertEqual(checked, 321, "the sweep did not cover every case")
    }
}
