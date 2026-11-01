import os

/// Log is the app's one os_log helper, subsystem co.nmelo.initech, so a
/// device's log (Console, `log collect`) names the cause of anything a screen
/// showed. Categories:
///   service: each TLS pin decision and each failed request, with the raw error
///   screen:  each failure line a screen shows, in the words it shows
/// Never log the pairing token, a message's text or anything typed into a pane.
/// Use privacy: .public only for what is safe to read off the device (rig and
/// pane names, routes, error descriptions).
enum Log {
    static let service = Logger(subsystem: "co.nmelo.initech", category: "service")
    static let screen = Logger(subsystem: "co.nmelo.initech", category: "screen")
}
