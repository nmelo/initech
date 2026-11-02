import InitechCore
import SwiftUI

/// BlockersScreen is the Blockers sheet. While open it owns the list's model
/// and takes the feed from home; a tap opens the pane and closes the sheet.
/// focus is a tapped bead push's rig and bead; arrived counts pushes that came
/// while the app was open, each a reason to read the list again.
struct BlockersScreen: View {
    let home: HomeModel
    var focus: BlockerFocus? = nil
    var arrived = 0
    @StateObject private var model: BlockersModel
    @Environment(\.dismiss) private var dismiss

    init(home: HomeModel, focus: BlockerFocus? = nil, arrived: Int = 0) {
        self.home = home
        self.focus = focus
        self.arrived = arrived
        _model = StateObject(wrappedValue: BlockersModel(source: home.blockersSource))
    }

    var body: some View {
        BlockersView(blockers: model, focus: focus) { rig, pane in
            home.openBlocker(rig: rig, pane: pane)
            dismiss()
        }
        .onChange(of: arrived) { Task { await model.reload() } }
        .onChange(of: focus) { Task { await model.reload() } }
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
