import Foundation
import InitechCore

/// PushRoute is where a push takes the app: one pane, the Waiting on you list
/// at a rig with a bead's row, or home. It is read from the push's data only
/// (rig, pane, bead, reason): nothing else in a push is ever shown.
enum PushRoute: Equatable, Sendable {
    /// pane is a stuck pane's push: data rig and pane.
    case pane(PaneRef)
    /// bead is a waiting-on-Nelson push: data rig and bead, reason nelson.
    case bead(rig: String, bead: String)
    /// home is a push with no usable data.
    case home

    /// init reads a push's userInfo as APNs delivers it: {"aps": {...},
    /// "data": {"rig", "pane" | "bead", "reason"}}. A push with no rig, or with
    /// neither a pane nor a nelson bead, goes home.
    init(userInfo: [AnyHashable: Any]) {
        let data = userInfo["data"] as? [String: Any] ?? [:]
        func field(_ k: String) -> String { (data[k] as? String)?.trimmingCharacters(in: .whitespaces) ?? "" }
        let rig = field("rig"), pane = field("pane"), bead = field("bead")
        if rig.isEmpty {
            self = .home
        } else if field("reason") == "nelson", !bead.isEmpty {
            self = .bead(rig: rig, bead: bead)
        } else if !pane.isEmpty {
            self = .pane(PaneRef(rig: rig, pane: pane))
        } else {
            self = .home
        }
    }
}

/// OnScreen is what the app shows now, as far as a push cares: one pane, the
/// Waiting on you list, or anything else.
enum OnScreen: Equatable, Sendable {
    case pane(PaneRef)
    case waitingList
    case other
}

/// PushInbox is where pushes meet the screen. The notification delegate
/// hands it each push; MainView tells it what is on screen and acts on a
/// tapped push. It lives as long as the process, like PushRegistrar.
@MainActor
final class PushInbox: ObservableObject {
    /// tapped is the route of the last push he tapped, until MainView opens it.
    @Published var tapped: PushRoute?
    /// arrived counts pushes that came while the app was open, so MainView
    /// reloads what they change.
    @Published private(set) var arrived = 0
    /// notice is the one line home shows after a tap that cannot open its
    /// pane; cleared by the next tap.
    @Published var notice: String?
    /// onScreen is set by MainView whenever the screen changes.
    var onScreen: OnScreen = .other

    /// showsBanner is the foreground rule: a push for what is already on
    /// screen (that pane, or a bead while the Waiting on you list is open)
    /// shows no banner, the screen refreshes instead; any other shows one.
    nonisolated static func showsBanner(_ route: PushRoute, onScreen: OnScreen) -> Bool {
        switch (route, onScreen) {
        case let (.pane(p), .pane(s)): p != s
        case (.bead, .waitingList): false
        default: true
        }
    }

    /// present is a push arriving while the app is open: it counts the
    /// arrival and says whether iOS shows its banner.
    func present(_ route: PushRoute) -> Bool {
        arrived += 1
        return Self.showsBanner(route, onScreen: onScreen)
    }

    /// tap is him tapping a push, the app open or not.
    func tap(_ route: PushRoute) {
        notice = nil
        tapped = route
    }
}

extension HomeModel {
    /// hasPane is whether the service's last status lists ref as running.
    func hasPane(_ ref: PaneRef) -> Bool {
        rigs.first { $0.rig == ref.rig }?.agents.contains { $0.role == ref.pane && $0.alive } ?? false
    }
}
