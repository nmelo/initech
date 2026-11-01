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

    private var list: some View {
        List {
            if blockers.loaded && blockers.sections.isEmpty {
                Text("No rig is running.")
                    .foregroundStyle(.secondary)
            }
            if let problem = blockers.problem {
                Text(problem).foregroundStyle(.red)
            }
            ForEach(blockers.sections) { section in
                Section(section.rig) {
                    ForEach(section.rows) { row in
                        BlockerRowView(row: row, open: open).id(row.id)
                    }
                }
            }
        }
        .navigationTitle("Blockers")
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
            Image(systemName: icon).foregroundStyle(tint)
            VStack(alignment: .leading, spacing: 2) {
                Text(row.title).foregroundStyle(row.kind == .quiet ? .secondary : .primary)
                if row.kind == .waiting, let pane = row.pane {
                    Text(pane).font(.caption).foregroundStyle(.secondary)
                }
                if let detail = row.detail {
                    Text(row.kind == .waiting ? "Recommendation: \(detail)" : detail)
                        .font(.callout).foregroundStyle(.secondary)
                }
            }
        }
    }

    private var icon: String {
        switch row.kind {
        case .waiting: "person.fill.questionmark"
        case .stuck: "exclamationmark.triangle.fill"
        case .quiet: "checkmark.circle"
        }
    }

    private var tint: Color {
        switch row.kind {
        case .waiting: .blue
        case .stuck: .orange
        case .quiet: .green
        }
    }
}
