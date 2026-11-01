import SwiftUI

/// BlockersView is one list per running rig: what waits on Nelson, then the
/// stuck panes, or one quiet row. Tapping an item opens its pane.
struct BlockersView: View {
    @ObservedObject var blockers: BlockersModel
    /// open opens a pane, given its rig and name (HomeModel.open).
    var open: (_ rig: String, _ pane: String) -> Void

    var body: some View {
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
                        BlockerRowView(row: row, open: open)
                    }
                }
            }
        }
        .navigationTitle("Blockers")
        .refreshable { await blockers.reload() }
        .task { await blockers.reload() }
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
                .accessibilityIdentifier("blocker-\(row.rig)-\(pane)")
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
