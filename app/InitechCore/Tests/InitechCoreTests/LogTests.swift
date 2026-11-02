import XCTest
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

    // AC1: one entry per shown error, with the subsystem, the screen's
    // category, the error's domain and its code.
    func testShown_RecordsSubsystemCategoryDomainAndCode() {
        let entries = record()
        Log.shown(ServiceError.unauthorized, screen: "home")
        Log.shown(ServiceError.status(503, "busy"), screen: "home")
        Log.shown(ServiceError.unreachable("The request timed out."), screen: "home")
        Log.shown(ServiceError.certificateRefused, screen: "home")
        XCTAssertEqual(entries.all, [
            ShownError(subsystem: "co.nmelo.initech", category: "home", domain: "co.nmelo.initech.service", code: 401),
            ShownError(subsystem: "co.nmelo.initech", category: "home", domain: "co.nmelo.initech.service", code: 503),
            ShownError(subsystem: "co.nmelo.initech", category: "home", domain: "co.nmelo.initech.service", code: -1),
            ShownError(subsystem: "co.nmelo.initech", category: "home", domain: "co.nmelo.initech.service", code: -2),
        ])
    }

    // AC2: an error whose words carry a token-like string, a URL with a
    // token, a pane name and a message body leaves none of them in the entry.
    func testShown_KeepsTheErrorsWordsOutOfTheEntry() {
        let entries = record()
        let secret = "phc_tok3nAbCdEf0123456789"
        let words = "GET https://100.64.1.2:7443/panes/eng4?token=\(secret) failed for pane eng4: message body hello world"
        let err = NSError(domain: NSURLErrorDomain, code: NSURLErrorTimedOut,
                          userInfo: [NSLocalizedDescriptionKey: words, NSURLErrorFailingURLStringErrorKey: "https://h/x?token=\(secret)"])
        Log.shown(err, screen: "home")
        Log.shown(ServiceError.status(409, words), screen: "home")
        Log.shown(ServiceError.unreachable(words), screen: "home")
        let all = entries.all
        XCTAssertEqual(all.count, 3)
        for e in all {
            let text = "\(e.subsystem) \(e.category) \(e.domain) \(e.code)"
            for leak in [secret, "token", "eng4", "hello world", "https://", words] {
                XCTAssertFalse(text.contains(leak), "entry \(text) carries \(leak)")
            }
        }
        // Positive twin: the fields that should be there are.
        XCTAssertEqual(all.first?.domain, NSURLErrorDomain)
        XCTAssertEqual(all.first?.code, NSURLErrorTimedOut)
    }
}
