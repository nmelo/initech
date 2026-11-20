import InitechCore
import SwiftUI
import UIKit

/// MainView is the app once unlocked: the pinned panes beside the open one on
/// iPad (a sidebar), and a stack on iPhone; both from one NavigationSplitView.
struct MainView: View {
    @StateObject var home: HomeModel
    @ObservedObject var push: PushRegistrar
    @ObservedObject var inbox: PushInbox
    let forget: () -> Void
    /// tab is the screen the list side shows, chosen in its bottom bar.
    @State private var tab: ListTab = .pinned
    @State private var blockersFocus: BlockerFocus?
    /// opened is false until home's first reload, before which a tap that
    /// opened the app cannot tell a running pane from a gone one.
    @State private var opened = false
    /// Read here, outside the split view: regular is the list beside the pane.
    @Environment(\.horizontalSizeClass) private var sizeClass

    var body: some View {
        // The list draws its own header and bottom bar, so its navigation bar is
        // hidden. On iPad the list always sits beside the pane (balanced, all
        // columns): with no bar there is no button to bring a hidden list back.
        NavigationSplitView(columnVisibility: .constant(.all)) {
            HomeList(home: home, push: push, inbox: inbox, tab: $tab,
                     blockersFocus: blockersFocus, besideThePane: sizeClass == .regular)
                .toolbar(.hidden, for: .navigationBar)
        } detail: {
            if let ref = home.selection {
                PaneView(model: PaneModel(service: home.service, ref: ref), home: home)
                    .id(ref)
            } else {
                ZStack {
                    Theme.bg.ignoresSafeArea()
                    Text("❯ pick a pane").font(Theme.mono(.body)).foregroundStyle(Theme.dim)
                        .accessibilityLabel("Pick a pane")
                }
            }
        }
        .navigationSplitViewStyle(.balanced)
        .tint(Theme.red)
        .preferredColorScheme(.dark)
        .task {
            await home.reload()
            home.startFeed()
            opened = true
            if let route = inbox.tapped { await open(route) }
            #if DEBUG
            if let screen = UITestHooks.screen { open(debug: screen) }
            #endif
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
        if tab == .blockers { return .waitingList }
        if tab == .rigs || tab == .progress { return .other }
        return home.selection.map { .pane($0) } ?? .other
    }

    /// open takes the app where a tapped push says: the pane, or home with one
    /// line when the pane is gone; Blockers at the rig for a bead; home for a
    /// push with no data.
    func open(_ route: PushRoute) async {
        inbox.tapped = nil
        switch route {
        case .home:
            tab = .pinned
            home.selection = nil
        case .pane(let ref):
            tab = .pinned
            if !home.hasPane(ref) { await home.reload() }
            if home.hasPane(ref) {
                home.open(ref)
            } else {
                home.selection = nil
                inbox.notice = "\(ref.pane) is no longer running in \(ref.rig)."
            }
        case let .bead(rig, bead):
            blockersFocus = BlockerFocus(rig: rig, bead: bead)
            tab = .blockers
        }
    }
}

#if DEBUG
extension MainView {
    /// open(debug:) takes a debug launch to the screen it named.
    func open(debug screen: DebugScreen) {
        switch screen {
        case .rigs: tab = .rigs
        case .blockers: tab = .blockers
        case .progress: tab = .progress
        case let .pane(rig, pane): home.open(PaneRef(rig: rig, pane: pane))
        }
    }
}
#endif

/// ListTab is a screen of the list side's bottom bar; its name is what the
/// prompt header shows.
enum ListTab: String, Equatable {
    case pinned, rigs, blockers, progress
}

/// HomeList is the list side of the app, drawn as a terminal would: a prompt
/// line on top, then one of two screens chosen in the bar of numbered keys at
/// the bottom. Panes is his pinned panes in his order, one or two lines each;
/// rename and unpin by swipe, reorder in edit mode. Rigs is every running
/// rig's panes he has not pinned. Blockers and Progress open from the same bar.
struct HomeList: View {
    @ObservedObject var home: HomeModel
    @ObservedObject var push: PushRegistrar
    @ObservedObject var inbox: PushInbox
    @Binding var tab: ListTab
    /// blockersFocus is the rig and bead a tapped push named, for Blockers to show.
    var blockersFocus: BlockerFocus?
    let besideThePane: Bool
    @State private var renaming: PaneRow?
    @State private var newName = ""
    @State private var editMode: EditMode = .inactive

    var body: some View {
        Group {
            switch tab {
            case .pinned: panesList
            case .rigs: rigsList
            case .blockers: BlockersScreen(home: home, focus: blockersFocus, arrived: inbox.arrived)
            case .progress: ProgressScreen(home: home)
            }
        }
        .background(Theme.bg.ignoresSafeArea())
        .safeAreaInset(edge: .top, spacing: 0) { header }
        .safeAreaInset(edge: .bottom, spacing: 0) { keys }
        .alert("Rename", isPresented: Binding(get: { renaming != nil }, set: { if !$0 { renaming = nil } })) {
            TextField("Name", text: $newName)
            Button("Save") {
                if let r = renaming { Task { await home.rename(r.ref, to: newName) } }
            }
            Button("Cancel", role: .cancel) {}
        }
    }

    /// panesList is his pinned panes.
    private var panesList: some View {
        List(selection: Binding(get: { home.selection }, set: { home.listChose($0, besideThePane: besideThePane) })) {
            Group {
                if let problem = home.problem {
                    Text(problem).foregroundStyle(Theme.red).font(Theme.mono(.footnote))
                }
                if let line = push.line {
                    PushLineView(line: line)
                }
                if let notice = inbox.notice {
                    Text(notice).font(Theme.mono(.footnote)).foregroundStyle(Theme.soft)
                        .accessibilityIdentifier("push-notice")
                }
                if home.showsNoPinsLine {
                    Button("No pinned panes yet. Pin one from Rigs.") { tab = .rigs }
                        .font(Theme.mono(.footnote))
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
                // The build this is, so an error on a device can be matched to its commit.
                Text(BuildInfo.current).font(Theme.mono(.caption2)).foregroundStyle(Theme.dim)
                    .frame(maxWidth: .infinity, alignment: .center)
                    .listRowSeparator(.hidden)
                    .accessibilityIdentifier("about.version")
            }
            .listRowBackground(Theme.bg)
            .listRowSeparatorTint(Theme.line)
            .listRowInsets(EdgeInsets(top: 9, leading: 20, bottom: 9, trailing: 20))
        }
        .listStyle(.plain)
        .scrollContentBackground(.hidden)
        .environment(\.editMode, $editMode)
        .refreshable { await home.reload() }
    }

    /// rigsList is the Rigs screen (RigsAccordion). It is a screen of the bottom
    /// bar, like Panes, not a sheet over it.
    private var rigsList: some View { RigsAccordion(home: home, besideThePane: besideThePane) }

    /// header is the prompt line: where he is, and edit on Panes.
    private var header: some View {
        HStack(spacing: 0) {
            Text("initech").font(Theme.mono(.subheadline, .bold)).foregroundStyle(Theme.bright)
            Text(" ❯ \(tab.rawValue)").font(Theme.mono(.subheadline)).foregroundStyle(Theme.text)
            Rectangle().fill(Theme.red).frame(width: 9, height: 17).padding(.leading, 6)
                .accessibilityHidden(true)
            Spacer()
            if tab == .pinned {
                Button(editMode.isEditing ? "done" : "edit") {
                    withAnimation { editMode = editMode.isEditing ? .inactive : .active }
                }
                .accessibilityLabel(editMode.isEditing ? "Done" : "Edit")
            }
        }
        .font(Theme.mono(.subheadline))
        .buttonStyle(.plain)
        .foregroundStyle(Theme.red)
        .padding(.horizontal, 20).padding(.vertical, 13)
        .background(Theme.bg)
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
        .accessibilityElement(children: .contain)
        .accessibilityAddTraits(.isHeader)
    }

    /// key is one screen's key in the bottom bar, red while its screen shows.
    private func key(_ screen: ListTab, _ label: String, spoken: String, count: Int = 0) -> some View {
        Button { tab = screen; editMode = .inactive } label: {
            HStack(spacing: 5) {
                Text(label)
                if count > 0 {
                    Text("\(count)").fontWeight(.bold).foregroundStyle(Theme.bg)
                        .padding(.horizontal, 5).background(Theme.red, in: RoundedRectangle(cornerRadius: 4))
                }
            }
            .foregroundStyle(tab == screen ? Theme.red : Theme.soft)
            .fontWeight(tab == screen ? .semibold : .regular)
            .fixedSize()
        }
        .accessibilityLabel(spoken)
        .accessibilityAddTraits(tab == screen ? .isSelected : [])
    }

    /// keys is the bottom bar: the screens, as numbered keys. It sits below the
    /// list, never over it.
    private var keys: some View {
        HStack(spacing: 0) {
            key(.pinned, "1 pinned", spoken: "Pinned")
            Spacer()
            key(.rigs, "2 rigs", spoken: "Rigs")
            Spacer()
            key(.blockers, "3 blockers", spoken: "Blockers", count: home.blockerCount)
            Spacer()
            key(.progress, "4 progress", spoken: "Progress")
        }
        // Beside the pane the list is a narrow column: smaller type, so no key is cut short.
        .font(Theme.mono(besideThePane ? .caption2 : .footnote))
        .lineLimit(1)
        .buttonStyle(.plain)
        .foregroundStyle(Theme.soft)
        .padding(.horizontal, besideThePane ? 10 : 16)
        .frame(minHeight: Theme.barHeight)
        .background(Theme.bar.ignoresSafeArea(edges: .bottom))
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
    }
}

/// PushLineView says why pushes are not coming, with Settings when he turned
/// them off.
struct PushLineView: View {
    let line: PushRegistrar.Line

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(line.words).font(Theme.mono(.footnote)).foregroundStyle(Theme.soft)
            if line == .off, let settings = URL(string: UIApplication.openSettingsURLString) {
                Button("Open Settings") { UIApplication.shared.open(settings) }.font(Theme.mono(.footnote))
            }
        }
        .accessibilityIdentifier("push-line")
    }
}

/// PaneRowView is one pane as two lines of a terminal: a state dot, his name,
/// and its context as a bar of blocks; then its state, bead and where it runs.
/// A pane that is not running is one dim line.
struct PaneRowView: View {
    let row: PaneRow
    var waiting: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(alignment: .center, spacing: 9) {
                Text(row.gone ? "○" : "●").font(Theme.mono(.caption)).foregroundStyle(color)
                Text(row.name.lowercased())
                    .font(Theme.mono(.body, row.gone ? .regular : .semibold))
                    .foregroundStyle(row.gone ? Theme.dim : Theme.bright)
                    .lineLimit(1)
                Spacer(minLength: 8)
                if row.gone {
                    Text("not running").font(Theme.mono(.caption)).foregroundStyle(Theme.dim)
                } else if let pct = row.contextPercent {
                    ContextBarView(percent: pct)
                    Text("\(pct)%").font(Theme.mono(.caption)).foregroundStyle(Theme.soft)
                        .frame(minWidth: 36, alignment: .trailing)
                }
            }
            if !row.gone {
                (Text(row.stateLabel).foregroundColor(color)
                    + Text(row.bead.map { " · \($0)" } ?? "")
                    + Text(" · \(row.ref.pane)@\(row.ref.rig)"))
                    .font(Theme.mono(.caption)).foregroundStyle(Theme.dim)
                    .lineLimit(1)
                    .padding(.leading, 19)
            }
            if let waiting {
                Text(waiting).font(Theme.mono(.caption)).foregroundStyle(Theme.orange).lineLimit(2)
                    .padding(.leading, 19)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(spoken)
    }

    /// spoken is the row read aloud, and what the UI tests find it by: the
    /// same words in the same order as before the terminal look.
    private var spoken: String {
        var parts = [row.name]
        if let pct = row.contextPercent { parts.append("\(pct)%") }
        parts.append(row.stateLabel)
        if let bead = row.bead { parts.append(bead) }
        parts.append("\(row.ref.pane) · \(row.ref.rig)")
        if let waiting { parts.append(waiting) }
        return parts.joined(separator: ", ")
    }

    private var color: Color {
        if row.gone { return Theme.dim }
        if row.atDialog { return Theme.orange }
        switch row.state {
        case "running", "background": return Theme.green
        case "idle": return Theme.blue
        default: return Theme.grey
        }
    }
}
