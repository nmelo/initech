import InitechCore
import SwiftUI

/// EffortProgressView shows initech progress per effort in each running rig:
/// pace, the in-progress beads, the critical path, engineers on it and the
/// quiet beads. It is shown, never typed, and shows no finish time.
struct EffortProgressView: View {
    @ObservedObject var progress: ProgressModel

    var body: some View {
        List {
            if progress.loaded && progress.cards.isEmpty && progress.problem == nil {
                Text("No effort is in flight.")
                    .foregroundStyle(.secondary)
            }
            if let problem = progress.problem {
                Text(problem).foregroundStyle(.red)
            }
            ForEach(progress.cards) { card in
                Section("\(card.rig) · \(card.effort)") {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(card.pace).font(.headline)
                        Text(card.worked).font(.callout).foregroundStyle(.secondary)
                        Text(card.engineers).font(.callout).foregroundStyle(.secondary)
                    }
                    .accessibilityIdentifier("progress-\(card.id)")
                    ForEach(card.beads) { b in
                        VStack(alignment: .leading, spacing: 2) {
                            Text("\(b.id) \(b.title)").lineLimit(2)
                            Text(b.line).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    if !card.criticalPath.isEmpty {
                        LabeledLines(label: "Critical path", lines: card.criticalPath)
                    }
                    if !card.stuck.isEmpty {
                        LabeledLines(label: "Stuck", lines: card.stuck)
                    }
                    LabeledLines(label: card.quietLabel, lines: card.quiet.isEmpty ? ["none"] : card.quiet)
                    if !card.waiting.isEmpty {
                        LabeledLines(label: card.waitingLabel, lines: card.waiting)
                    }
                }
            }
        }
        .navigationTitle("Progress")
        .refreshable { await progress.reload() }
        .task {
            // The Mac reruns progress every five minutes; read it as often.
            while !Task.isCancelled {
                await progress.reload()
                try? await Task.sleep(for: .seconds(300))
            }
        }
    }
}

private struct LabeledLines: View {
    var label: String
    var lines: [String]

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label).font(.caption.bold()).foregroundStyle(.secondary)
            ForEach(lines, id: \.self) { Text($0).font(.caption) }
        }
    }
}
