import SwiftUI

/// MainView is the app once unlocked: the pinned panes beside the open one on
/// iPad (a sidebar), and a stack on iPhone; both from one NavigationSplitView.
struct MainView: View {
    @StateObject var home: HomeModel
    let forget: () -> Void
    @State private var showRigs = false
    @State private var showBlockers = false
    @State private var showProgress = false

    var body: some View {
        NavigationSplitView {
            HomeList(home: home, showRigs: $showRigs)
                .navigationTitle("Panes")
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) { EditButton() }
                    ToolbarItem(placement: .topBarTrailing) {
                        Button("Rigs") { showRigs = true }
                    }
                    ToolbarItem(placement: .bottomBar) {
                        Button("Blockers") { showBlockers = true }
                    }
                    ToolbarItem(placement: .bottomBar) {
                        Button("Progress") { showProgress = true }
                    }
                }
        } detail: {
            if let ref = home.selection {
                PaneView(model: PaneModel(service: home.service, ref: ref), home: home)
                    .id(ref)
            } else {
                Text("Pick a pane").foregroundStyle(.secondary)
            }
        }
        .sheet(isPresented: $showRigs) {
            NavigationStack { RigsView(home: home) }
        }
        .sheet(isPresented: $showBlockers) {
            NavigationStack { BlockersScreen(home: home) }
        }
        .sheet(isPresented: $showProgress) {
            NavigationStack { ProgressScreen(home: home) }
        }
        .task {
            await home.reload()
            home.startFeed()
        }
        .onDisappear { home.stopFeed() }
        .alert("Pair again", isPresented: .constant(home.needsPairing)) {
            Button("Pair") { forget() }
        } message: {
            Text("\(home.problem ?? "") Run initech app-serve token on the Mac and scan the new code.")
        }
    }
}

/// HomeList is his pinned panes in his order; rename by swipe, reorder in edit mode.
struct HomeList: View {
    @ObservedObject var home: HomeModel
    @Binding var showRigs: Bool
    @State private var renaming: PaneRow?
    @State private var newName = ""

    var body: some View {
        List(selection: $home.selection) {
            if let problem = home.problem {
                Text(problem).foregroundStyle(.red).font(.footnote)
            }
            if home.showsNoPinsLine {
                Button("No pinned panes yet. Pin one from Rigs.") { showRigs = true }
            }
            ForEach(home.rows) { row in
                PaneRowView(row: row, waiting: home.waiting[row.ref])
                    .tag(row.ref)
                    .swipeActions {
                        Button("Unpin", role: .destructive) { Task { await home.unpin(row.ref) } }
                        Button("Rename") { newName = row.name; renaming = row }
                    }
            }
            .onMove { from, to in Task { await home.move(from: from, to: to) } }
        }
        .refreshable { await home.reload() }
        .alert("Rename", isPresented: Binding(get: { renaming != nil }, set: { if !$0 { renaming = nil } })) {
            TextField("Name", text: $newName)
            Button("Save") {
                if let r = renaming { Task { await home.rename(r.ref, to: newName) } }
            }
            Button("Cancel", role: .cancel) {}
        }
    }
}

/// PaneRowView is one pane: his name, its state, context and bead; greyed when gone.
struct PaneRowView: View {
    let row: PaneRow
    var waiting: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Circle().fill(color).frame(width: 9, height: 9)
                Text(row.name).font(.headline)
                Spacer()
                if let pct = row.contextPercent { Text("\(pct)%").font(.caption).monospacedDigit() }
            }
            HStack(spacing: 8) {
                Text(row.stateLabel)
                if let bead = row.bead { Text(bead).monospaced() }
                Text("\(row.ref.pane) · \(row.ref.rig)").foregroundStyle(.tertiary)
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            if let waiting {
                Text(waiting).font(.caption).foregroundStyle(.orange).lineLimit(2)
            }
        }
        .opacity(row.gone ? 0.4 : 1)
        .accessibilityElement(children: .combine)
    }

    private var color: Color {
        if row.gone { return .gray }
        if row.atDialog { return .orange }
        switch row.state {
        case "running", "background": return .green
        case "idle": return .blue
        default: return .gray
        }
    }
}

/// RigsView is every running rig and its panes he has not pinned; one tap pins.
struct RigsView: View {
    @ObservedObject var home: HomeModel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        List {
            if home.rigs.isEmpty {
                Text("No rig is running on the Mac.").foregroundStyle(.secondary)
            }
            ForEach(home.rigRows, id: \.rig) { rig in
                Section(rig.rig) {
                    if rig.panes.isEmpty {
                        Text("Every pane is pinned.").foregroundStyle(.secondary)
                    }
                    ForEach(rig.panes) { row in
                        HStack {
                            PaneRowView(row: row)
                            Button { Task { await home.pin(row.ref) } } label: {
                                Image(systemName: "pin")
                            }
                            .buttonStyle(.borderless)
                            .accessibilityLabel("Pin \(row.ref.pane)")
                        }
                    }
                }
            }
        }
        .navigationTitle("Rigs")
        .toolbar { Button("Done") { dismiss() } }
        .refreshable { await home.reload() }
    }
}
