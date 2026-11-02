import XCTest
@testable import InitechApp
@testable import InitechCore

/// Entries collects what Log.shown recorded during one test.
final class Entries: @unchecked Sendable {
    private let lock = NSLock()
    private var items: [ShownError] = []
    func add(_ e: ShownError) { lock.lock(); items.append(e); lock.unlock() }
    var all: [ShownError] { lock.lock(); defer { lock.unlock() }; return items }
}

@MainActor
final class LogTests: XCTestCase {
    /// record collects Log.shown entries until the test ends.
    private func record() -> Entries {
        let entries = Entries()
        Log.record { entries.add($0) }
        addTeardownBlock { Log.record(to: nil) }
        return entries
    }

    // AC1, pairing: a refused pairing code logs one entry from the screen
    // that shows it.
    func testPairingShown_LogsOneEntryAndReturnsTheLine() {
        let entries = record()
        let line = PairingView.shown(PairingError.badPort)
        XCTAssertEqual(line, "The pairing code has a bad port.")
        let e = entries.all
        XCTAssertEqual(e.count, 1)
        XCTAssertEqual(e.first?.category, "pairing")
        XCTAssertEqual(e.first?.domain, (PairingError.badPort as NSError).domain)
        XCTAssertEqual(e.first?.code, (PairingError.badPort as NSError).code)
    }

    // AC2: an error the app handles without showing it logs nothing; the
    // recorder itself still works (twin: the next shown error is recorded).
    func testAnErrorNotShownLogsNothing() {
        let entries = record()
        XCTAssertNil(try? Pairing.parse("not a code"))
        XCTAssertEqual(entries.all.count, 0, "parsing failed without a screen showing it, yet an entry was logged")
        _ = PairingView.shown(PairingError.notAPairingCode)
        XCTAssertEqual(entries.all.count, 1)
    }
}
