import Foundation
import os

/// Log is the app's one os_log helper, subsystem co.nmelo.initech, so a
/// device's log (Console, `log show --predicate 'subsystem == "co.nmelo.initech"'`)
/// names the cause of anything a screen showed. Two kinds of entry:
///   service: each TLS pin decision and each failed request, with the raw
///            error (rig and pane names and routes may appear here).
///   shown:   each error a screen showed, through Log.shown, in a category
///            named for that screen. It carries the screen, the error's domain
///            and its code, and nothing else: never the error's words, a pane
///            name, a token or a URL carrying one, a pane's text, a message
///            body or a bead title.
/// Never log the pairing token, a message's text or anything typed into a pane.
public enum Log {
    public static let subsystem = "co.nmelo.initech"
    public static let service = Logger(subsystem: subsystem, category: "service")
    public static let screen = Logger(subsystem: subsystem, category: "screen")

    /// shown writes one entry for an error a screen showed the user, in the
    /// category `screen`. Call it once per shown error, beside the line that
    /// shows it; an error the app handles without showing it is not logged.
    /// The entry is built from ShownError's fields alone, so the error's
    /// words never reach the log.
    public static func shown(_ error: Error, screen: String) {
        let ns = error as NSError
        let entry = ShownError(subsystem: subsystem, category: screen, domain: ns.domain, code: ns.code)
        Logger(subsystem: subsystem, category: screen).error(
            "shown on \(entry.category, privacy: .public): \(entry.domain, privacy: .public) \(entry.code, privacy: .public)")
        recorder.withLock { $0 }?(entry)
    }

    /// record hands every later Log.shown entry to sink, or stops with nil.
    /// Tests only: os_log cannot be read back in process.
    public static func record(to sink: (@Sendable (ShownError) -> Void)?) {
        recorder.withLock { $0 = sink }
    }

    private static let recorder = OSAllocatedUnfairLock<(@Sendable (ShownError) -> Void)?>(initialState: nil)
}

/// ShownError is everything a shown-error entry carries.
public struct ShownError: Equatable, Sendable {
    public let subsystem: String
    public let category: String
    public let domain: String
    public let code: Int
}

/// ServiceError's domain and codes in the log: the HTTP status where there is
/// one, a negative code for the two failures that have none.
extension ServiceError: CustomNSError {
    public static var errorDomain: String { "co.nmelo.initech.service" }
    public var errorCode: Int {
        switch self {
        case .unauthorized: 401
        case let .status(code, _): code
        case .unreachable: -1
        case .certificateRefused: -2
        case .handshakeEnded: -3
        }
    }
}
