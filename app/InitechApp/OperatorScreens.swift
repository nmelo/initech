import SwiftUI

/// FeedListener is a model that HomeModel hands every feed event to, before
/// it applies the event to the home list.
@MainActor
protocol FeedListener: AnyObject {
    func apply(_ e: FeedEvent) async
}

extension BlockersModel: FeedListener {}
extension ProgressModel: FeedListener {}

/// Unserved answers for a service that does not serve the blockers or the
/// progress endpoint, so the view shows why it is empty instead of nothing.
struct Unserved: BlockersSource, ProgressSource {
    struct Missing: Error, CustomStringConvertible {
        var description: String { "this service does not serve it" }
    }
    func blockers() async throws -> [RigBlockers] { throw Missing() }
    func progress() async throws -> [ProgressReport] { throw Missing() }
}

extension HomeModel {
    /// blockersSource is the service as the blockers list reads it.
    var blockersSource: BlockersSource { service as? BlockersSource ?? Unserved() }
    /// progressSource is the service as the progress view reads it.
    var progressSource: ProgressSource { service as? ProgressSource ?? Unserved() }

    /// openBlocker opens the pane a blocker row names.
    func openBlocker(rig: String, pane: String) { open(PaneRef(rig: rig, pane: pane)) }
}

/// BlockersScreen is the Blockers sheet. While open it owns the list's model
/// and takes the feed from home; a tap opens the pane and closes the sheet.
struct BlockersScreen: View {
    let home: HomeModel
    @StateObject private var model: BlockersModel
    @Environment(\.dismiss) private var dismiss

    init(home: HomeModel) {
        self.home = home
        _model = StateObject(wrappedValue: BlockersModel(source: home.blockersSource))
    }

    var body: some View {
        BlockersView(blockers: model) { rig, pane in
            home.openBlocker(rig: rig, pane: pane)
            dismiss()
        }
        .toolbar { Button("Done") { dismiss() } }
        .onAppear { home.listeners["blockers"] = model }
        .onDisappear { home.listeners["blockers"] = nil }
    }
}

/// ProgressScreen is the Progress sheet; while open it takes the feed from home.
struct ProgressScreen: View {
    let home: HomeModel
    @StateObject private var model: ProgressModel
    @Environment(\.dismiss) private var dismiss

    init(home: HomeModel) {
        self.home = home
        _model = StateObject(wrappedValue: ProgressModel(source: home.progressSource))
    }

    var body: some View {
        EffortProgressView(progress: model)
            .toolbar { Button("Done") { dismiss() } }
            .onAppear { home.listeners["progress"] = model }
            .onDisappear { home.listeners["progress"] = nil }
    }
}
