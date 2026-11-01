import SwiftUI
import UIKit

/// MainView is the app once unlocked: the pinned panes beside the open one on
/// iPad (a sidebar), and a stack on iPhone; both from one NavigationSplitView.
struct MainView: View {
    @StateObject var home: HomeModel
    @ObservedObject var push: PushRegistrar
    @ObservedObject var inbox: PushInbox
    let forget: () -> Void
    @State private var showRigs = false
    @State private var showBlockers = false
    @State private var showProgress = false
    @State private var showAbout = false
    @State private var blockersFocus: BlockerFocus?
    /// opened is false until home's first reload, before which a tap that
    /// opened the app cannot tell a running pane from a gone one.
    @State private var opened = false

    var body: some View {
        NavigationSplitView {
            HomeList(home: home, push: push, inbox: inbox, showRigs: $showRigs)
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
                    ToolbarItem(placement: .bottomBar) {
                        Button("About") { showAbout = true }
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
            NavigationStack { BlockersScreen(home: home, focus: blockersFocus, arrived: inbox.arrived) }
        }
        .sheet(isPresented: $showProgress) {
            NavigationStack { ProgressScreen(home: home) }
        }
        .sheet(isPresented: $showAbout) {
            NavigationStack { AboutView() }
        }
        .task {
            await home.reload()
            home.startFeed()
            opened = true
            if let route = inbox.tapped { await open(route) }
        }
        .onChange(of: inbox.tapped) { _, route in
            if opened, let route { Task { await open(route) } }
        }
        .onChange(of: inbox.arrived) { Task { await home.reload() } }
        .onChange(of: screen, initial: true) { _, now in inbox.onScreen = now }
        .onDisappear { home.stopFeed() }
        .alert("Pair again", isPresented: .constant(home.needsPairing)) {
            Button("Pair") { forget() }
        } message: {
            Text("\(home.problem ?? "") Run initech app-serve token on the Mac and scan the new code.")
        }
    }
}

extension MainView {
    /// screen is what is on screen for the push rule: a sheet over
    /// everything, else the open pane.
    var screen: OnScreen {
        if showBlockers { return .waitingList }
        if showRigs || showProgress || showAbout { return .other }
        return home.selection.map { .pane($0) } ?? .other
    }

    /// open takes the app where a tapped push says: the pane, or home with one
    /// line when the pane is gone; Blockers at the rig for a bead; home for a
    /// push with no data.
    func open(_ route: PushRoute) async {
        inbox.tapped = nil
        showRigs = false
        showProgress = false
        showAbout = false
        switch route {
        case .home:
            showBlockers = false
            home.selection = nil
        case .pane(let ref):
            showBlockers = false
            if !home.hasPane(ref) { await home.reload() }
            if home.hasPane(ref) {
                home.open(ref)
            } else {
                home.selection = nil
                inbox.notice = "\(ref.pane) is no longer running in \(ref.rig)."
            }
        case let .bead(rig, bead):
            blockersFocus = BlockerFocus(rig: rig, bead: bead)
            showBlockers = true
        }
    }
}

/// HomeList is his pinned panes in his order; rename by swipe, reorder in edit mode.
struct HomeList: View {
    @ObservedObject var home: HomeModel
    @ObservedObject var push: PushRegistrar
    @ObservedObject var inbox: PushInbox
    @Binding var showRigs: Bool
    @State private var renaming: PaneRow?
    @State private var newName = ""

    var body: some View {
        List(selection: $home.selection) {
            if let problem = home.problem {
                Text(problem).foregroundStyle(.red).font(.footnote)
            }
            if let line = push.line {
                PushLineView(line: line)
            }
            if let notice = inbox.notice {
                Text(notice).font(.footnote).foregroundStyle(.secondary)
                    .accessibilityIdentifier("push-notice")
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

/// PushLineView says why pushes are not coming, with Settings when he turned
/// them off.
struct PushLineView: View {
    let line: PushRegistrar.Line

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(line.words).font(.footnote).foregroundStyle(.secondary)
            if line == .off, let settings = URL(string: UIApplication.openSettingsURLString) {
                Button("Open Settings") { UIApplication.shared.open(settings) }.font(.footnote)
            }
        }
        .accessibilityIdentifier("push-line")
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
