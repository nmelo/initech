import SwiftUI

/// EffortProgressView shows initech progress per effort in each running rig:
/// pace, the in-progress beads, the critical path, engineers on it and the
/// quiet beads. It is shown, never typed, and shows no finish time.
struct EffortProgressView: View {
    @ObservedObject var progress: ProgressModel

    var body: some View {
        List {
            Group {
                if progress.loaded && progress.cards.isEmpty && progress.problem == nil {
                    Text("No effort is in flight.").font(Theme.mono(.footnote)).foregroundStyle(Theme.dim)
                }
                if let problem = progress.problem {
                    Text(problem).font(Theme.mono(.footnote)).foregroundStyle(Theme.red)
                }
                ForEach(progress.cards) { card in
                    Section {
                        VStack(alignment: .leading, spacing: 6) {
                            Text(card.pace).font(Theme.mono(.subheadline, .semibold)).foregroundStyle(Theme.bright)
                            ContextBarView(percent: card.total > 0 ? card.closed * 100 / card.total : 0, done: true)
                            Text(card.worked).font(Theme.mono(.caption)).foregroundStyle(Theme.soft)
                            Text(card.engineers).font(Theme.mono(.caption)).foregroundStyle(Theme.soft)
                        }
                        .accessibilityIdentifier("progress-\(card.id)")
                        ForEach(card.beads) { b in
                            VStack(alignment: .leading, spacing: 3) {
                                (Text(b.id).foregroundColor(Theme.soft) + Text(" \(b.title)"))
                                    .font(Theme.mono(.footnote)).foregroundStyle(Theme.text).lineLimit(2)
                                Text(b.line).font(Theme.mono(.caption)).foregroundStyle(Theme.dim)
                            }
                        }
                        if !card.criticalPath.isEmpty {
                            LabeledLines(label: "Critical path", lines: card.criticalPath, tint: Theme.blue)
                        }
                        if !card.stuck.isEmpty {
                            LabeledLines(label: "Stuck", lines: card.stuck, tint: Theme.red)
                        }
                        LabeledLines(label: card.quietLabel, lines: card.quiet.isEmpty ? ["none"] : card.quiet,
                                     tint: card.quiet.isEmpty ? Theme.dim : Theme.orange)
                        if !card.waiting.isEmpty {
                            LabeledLines(label: card.waitingLabel, lines: card.waiting, tint: Theme.orange)
                        }
                    } header: {
                        Text("\(card.rig) · \(card.effort)").font(Theme.mono(.caption, .bold)).foregroundStyle(Theme.soft)
                            .textCase(nil)
                    }
                }
            }
            .listRowBackground(Theme.bg)
            .listRowSeparatorTint(Theme.line)
            .listRowInsets(EdgeInsets(top: 10, leading: 20, bottom: 10, trailing: 20))
        }
        .listStyle(.plain)
        .scrollContentBackground(.hidden)
        .background(Theme.bg.ignoresSafeArea())
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

/// LabeledLines is a small coloured label over its lines.
private struct LabeledLines: View {
    var label: String
    var lines: [String]
    var tint: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(label).font(Theme.mono(.caption, .bold)).foregroundStyle(tint)
            ForEach(lines, id: \.self) { Text($0).font(Theme.mono(.caption)).foregroundStyle(Theme.soft) }
        }
    }
}
