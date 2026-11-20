import SwiftUI

/// BlockersView is one list per running rig: what waits on Nelson, then the
/// stuck panes, or one quiet row. Tapping an item opens its pane.
struct BlockersView: View {
    @ObservedObject var blockers: BlockersModel
    /// focus is the rig and bead a tapped push names: the list scrolls to the
    /// bead's row while it is listed, else to the rig.
    var focus: BlockerFocus? = nil
    /// open opens a pane, given its rig and name (HomeModel.open).
    var open: (_ rig: String, _ pane: String) -> Void

    var body: some View {
        ScrollViewReader { proxy in
            list
                .onChange(of: ScrollKey(focus: focus, sections: blockers.sections), initial: true) { _, key in
                    guard let target = key.target else { return }
                    proxy.scrollTo(target, anchor: .center)
                    // A scroll while the sheet is still sliding up can be
                    // dropped; once more after the presentation settles.
                    Task {
                        try? await Task.sleep(for: .milliseconds(500))
                        proxy.scrollTo(target, anchor: .center)
                    }
                }
        }
    }

    /// active is the rigs with something to say; quiet is the names of the rest,
    /// which share one line so that silence takes no room.
    private var active: [BlockerSection] { blockers.sections.filter { $0.rows.contains { $0.kind != .quiet } } }
    private var quiet: [String] { blockers.sections.filter { !$0.rows.contains { $0.kind != .quiet } }.map(\.rig) }

    private var list: some View {
        List {
            Group {
                if blockers.loaded && blockers.sections.isEmpty {
                    Text("No rig is running.").font(Theme.mono(.footnote)).foregroundStyle(Theme.dim)
                }
                if let problem = blockers.problem {
                    Text(problem).font(Theme.mono(.footnote)).foregroundStyle(Theme.red)
                }
                if blockers.loaded && !blockers.sections.isEmpty && active.isEmpty {
                    Text("✓ Nothing waits on you. Nothing is stuck.")
                        .font(Theme.mono(.subheadline)).foregroundStyle(Theme.green)
                        .accessibilityIdentifier("blockers-clear")
                }
                ForEach(active) { section in
                    Section {
                        ForEach(section.rows) { row in
                            BlockerRowView(row: row, open: open).id(row.id)
                        }
                    } header: {
                        Text(section.rig).font(Theme.mono(.caption, .bold)).foregroundStyle(Theme.soft).textCase(nil)
                    }
                }
                if !quiet.isEmpty {
                    Text("quiet: \(quiet.joined(separator: ", "))")
                        .font(Theme.mono(.caption)).foregroundStyle(Theme.dim)
                        .listRowSeparator(.hidden)
                        .accessibilityIdentifier("blockers-quiet")
                }
            }
            .listRowBackground(Theme.bg)
            .listRowSeparatorTint(Theme.line)
            .listRowInsets(EdgeInsets(top: 10, leading: 20, bottom: 10, trailing: 20))
        }
        .listStyle(.plain)
        .scrollContentBackground(.hidden)
        .background(Theme.bg.ignoresSafeArea())
        .navigationTitle("Waiting")
        .refreshable { await blockers.reload() }
        .task { await blockers.reload() }
    }
}

/// BlockerFocus is the rig and bead of a tapped waiting-on-Nelson push.
struct BlockerFocus: Equatable, Sendable {
    var rig: String
    var bead: String
}

/// ScrollKey is what the list scrolls by: the focus, and the rows as they
/// stand, so a focus set before the list loads still scrolls once it does.
struct ScrollKey: Equatable {
    var focus: BlockerFocus?
    var sections: [BlockerSection]

    /// target is the focused bead's row while listed, else the rig's first
    /// row; nil with no focus or no such rig.
    var target: String? {
        guard let focus, let section = sections.first(where: { $0.rig == focus.rig }) else { return nil }
        return (section.rows.first { $0.bead == focus.bead } ?? section.rows.first)?.id
    }
}

/// BlockerRowView is one row; a row with a pane is a button that opens it.
struct BlockerRowView: View {
    var row: BlockerRow
    var open: (_ rig: String, _ pane: String) -> Void

    var body: some View {
        if let pane = row.pane {
            // Plain, so the label keeps its own colours: a list button tints it
            // all, even over .primary. The shape keeps the whole row tappable.
            Button { open(row.rig, pane) } label: { content.contentShape(Rectangle()) }
                .buttonStyle(.plain)
                .accessibilityIdentifier(row.bead.map { "waiting-\(row.rig)-\($0)" } ?? "blocker-\(row.rig)-\(pane)")
        } else if let bead = row.bead {
            content.accessibilityIdentifier("waiting-\(row.rig)-\(bead)")
        } else {
            content.accessibilityIdentifier("blocker-quiet-\(row.rig)")
        }
    }

    private var content: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(mark).font(Theme.mono(.callout, .bold)).foregroundStyle(tint)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 4) {
                Text(row.title).font(Theme.mono(.callout, row.kind == .quiet ? .regular : .semibold))
                    .foregroundStyle(row.kind == .quiet ? Theme.dim : Theme.bright)
                if row.kind == .waiting, let pane = row.pane {
                    (Text(pane) + Text(row.bead.map { " · \($0)" } ?? "") + Text(" · \(row.rig)"))
                        .font(Theme.mono(.caption)).foregroundStyle(Theme.dim)
                }
                if let detail = row.detail {
                    Text(row.kind == .waiting ? "Recommendation: \(detail)" : detail)
                        .font(Theme.mono(.footnote)).foregroundStyle(Theme.soft)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// mark is the row's sign: a question waiting on him, a pane that is stuck.
    private var mark: String {
        switch row.kind {
        case .waiting: "?"
        case .stuck: "!"
        case .quiet: "✓"
        }
    }

    private var tint: Color {
        switch row.kind {
        case .waiting: Theme.orange
        case .stuck: Theme.red
        case .quiet: Theme.green
        }
    }
}
