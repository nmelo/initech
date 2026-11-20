import InitechCore
import SwiftUI

/// EffortProgressView shows initech progress, a section per running rig: one
/// row per bead in flight, led by its stage's icon, with its title in full and
/// flags only where there is something to flag; an effort of several beads
/// keeps its pace line and bar above its rows; what closed lately ends the
/// section. It is shown, never typed, and shows no finish time.
struct EffortProgressView: View {
    @ObservedObject var progress: ProgressModel
    /// canOpen says the pane is one the app can open (it is running, or pinned);
    /// open opens it. A bead's row opens its owner's pane: who to ask about it.
    var canOpen: (PaneRef) -> Bool = { _ in false }
    var open: (PaneRef) -> Void = { _ in }

    /// rigs is the cards grouped by rig, in the order the service sent them.
    private var rigs: [(rig: String, cards: [ProgressCard])] {
        var order: [String] = []
        var by: [String: [ProgressCard]] = [:]
        for card in progress.cards {
            if by[card.rig] == nil { order.append(card.rig) }
            by[card.rig, default: []].append(card)
        }
        return order.map { ($0, by[$0] ?? []) }
    }

    var body: some View {
        List {
            Group {
                if progress.loaded && progress.cards.isEmpty && progress.problem == nil {
                    Text("No effort is in flight.").font(Theme.mono(.footnote)).foregroundStyle(Theme.dim)
                }
                if let problem = progress.problem {
                    Text(problem).font(Theme.mono(.footnote)).foregroundStyle(Theme.red)
                }
                ForEach(rigs, id: \.rig) { group in
                    Section {
                        // Beads that are an effort by themselves read as one list for the
                        // rig, what is stuck first; an effort of several beads follows as a
                        // block under its pace line.
                        ForEach(Self.loose(group.cards), id: \.bead.id) { item in
                            row(item.bead, rig: group.rig).accessibilityIdentifier("progress-\(item.card)")
                        }
                        ForEach(group.cards.filter { !$0.single }) { card in
                            EffortLine(card: card)
                            ForEach(card.sorted) { bead in
                                row(bead, rig: group.rig).accessibilityIdentifier("progress-bead-\(bead.id)")
                            }
                        }
                        ForEach(group.cards.flatMap(\.offRows), id: \.self) { line in
                            Text(line).font(Theme.mono(.caption)).foregroundStyle(Theme.orange)
                        }
                        let done = group.cards.flatMap(\.closedRecent)
                        if !done.isEmpty {
                            VStack(alignment: .leading, spacing: 6) {
                                Text("closed in the last \(group.cards.map(\.hours).max() ?? 0) h")
                                    .font(Theme.mono(.caption2, .bold)).foregroundStyle(Theme.green)
                                ForEach(done) { d in
                                    (Text("✓ ").foregroundColor(Theme.green) + Text(d.title).foregroundColor(Theme.soft)
                                        + Text("  \(d.id)").foregroundColor(Theme.dim))
                                        .font(Theme.mono(.caption)).lineLimit(2)
                                }
                            }
                        }
                    } header: {
                        HStack(alignment: .firstTextBaseline) {
                            Text(group.rig).font(Theme.mono(.subheadline, .bold)).foregroundStyle(Theme.bright)
                            Spacer()
                            Text(Self.summary(group.cards)).font(Theme.mono(.caption)).foregroundStyle(Theme.dim)
                        }
                        .textCase(nil)
                    }
                }
            }
            .listRowBackground(Theme.bg)
            .listRowSeparatorTint(Theme.line)
            .listRowInsets(EdgeInsets(top: 11, leading: 20, bottom: 11, trailing: 20))
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

    /// row is a bead's row; when its owner's pane can be opened, tapping the row opens it.
    @ViewBuilder private func row(_ bead: ProgressCard.Bead, rig: String) -> some View {
        let pane = PaneRef(rig: rig, pane: bead.owner)
        if !bead.owner.isEmpty && canOpen(pane) {
            Button { open(pane) } label: { BeadRow(bead: bead, opens: true) }
                .buttonStyle(.plain)
                .accessibilityHint("Opens \(bead.owner)'s pane")
        } else {
            BeadRow(bead: bead)
        }
    }

    /// loose is the beads of the rig's one-bead efforts as one list, in reading
    /// order: stuck first, then by stage, then as the service sent them.
    static func loose(_ cards: [ProgressCard]) -> [(card: String, bead: ProgressCard.Bead)] {
        cards.filter(\.single).flatMap { card in card.beads.map { (card: card.id, bead: $0) } }
            .enumerated().sorted {
                let (a, b) = ($0.element.bead, $1.element.bead)
                if a.stuck != b.stuck { return a.stuck }
                let (ra, rb) = (BeadStage.rank(a.stage), BeadStage.rank(b.stage))
                return ra != rb ? ra < rb : $0.offset < $1.offset
            }.map(\.element)
    }

    /// summary is the rig's line beside its name: "7 in flight · 2 closed".
    static func summary(_ cards: [ProgressCard]) -> String {
        let flying = cards.map(\.beads.count).reduce(0, +)
        let closed = cards.map(\.closedRecent.count).reduce(0, +)
        return closed > 0 ? "\(flying) in flight · \(closed) closed" : "\(flying) in flight"
    }
}

/// EffortLine is an effort of several beads: its name, its pace, its bar, and
/// who is on it. Lines of zeros are left out.
private struct EffortLine: View {
    let card: ProgressCard

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(card.effort).font(Theme.mono(.caption, .bold)).foregroundStyle(Theme.soft)
            Text(card.pace).font(Theme.mono(.subheadline, .semibold)).foregroundStyle(Theme.bright)
            ContextBarView(percent: card.total > 0 ? card.closed * 100 / card.total : 0, done: true)
            if card.workedCount > 0 {
                Text(card.worked).font(Theme.mono(.caption)).foregroundStyle(Theme.soft)
            }
            if !card.engineerNames.isEmpty {
                Text(card.engineers).font(Theme.mono(.caption)).foregroundStyle(Theme.soft)
            }
        }
        .accessibilityIdentifier("progress-\(card.id)")
    }
}

/// BeadRow is one bead in flight: its stage's icon, its title in full, a dim
/// line of who and where it stands, and its flags when it has any.
private struct BeadRow: View {
    let bead: ProgressCard.Bead
    /// opens is true when tapping the row opens the owner's pane: the row then shows where it leads.
    var opens = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(BeadStage.icon(bead.stage)).font(Theme.mono(.callout, .bold)).foregroundStyle(tone)
                .frame(width: 16, alignment: .leading)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 5) {
                Text(bead.title).font(Theme.mono(.subheadline)).foregroundStyle(Theme.bright)
                    .lineLimit(4).fixedSize(horizontal: false, vertical: true)
                Text(bead.meta).font(Theme.mono(.caption)).foregroundStyle(Theme.dim)
                ForEach(notes, id: \.self) { note in
                    Text(note).font(Theme.mono(.caption)).foregroundStyle(Theme.orange)
                        .lineLimit(3).fixedSize(horizontal: false, vertical: true)
                }
                if opens {
                    Text("❯ \(bead.owner)").font(Theme.mono(.caption, .semibold)).foregroundStyle(Theme.red)
                }
                if !flags.isEmpty {
                    HStack(spacing: 8) {
                        ForEach(flags, id: \.word) { flag in
                            Text(flag.word).font(Theme.mono(.caption2, .bold)).foregroundStyle(flag.tint)
                                .padding(.horizontal, 7).padding(.vertical, 3)
                                .overlay { RoundedRectangle(cornerRadius: 4).stroke(flag.tint.opacity(0.6), lineWidth: 1) }
                        }
                    }
                    .padding(.top, 1)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
    }

    /// tone is the icon's colour: red for what is stuck, orange for what waits, green or blue for work moving.
    private var tone: Color {
        if bead.stuck { return Theme.red }
        switch bead.stage {
        case "blocked": return Theme.red
        case "waiting", "waiting_on_nelson", "waiting_for_release": return Theme.orange
        case "in_progress": return Theme.green
        case "ready_for_qa", "in_qa", "qa_passed", "ready_to_ship": return Theme.blue
        default: return Theme.soft
        }
    }

    /// short is a wait the service worded briefly ("2h", "stalled"): it fits a flag.
    /// A longer one is the bead's own sentence about what it waits on, and reads as a line.
    private func short(_ wait: String) -> Bool { wait.count <= 12 }

    private var flags: [(word: String, tint: Color)] {
        var out: [(String, Color)] = []
        if bead.stuck { out.append(("stuck", Theme.red)) }
        if let wait = bead.quiet, short(wait) { out.append(("quiet \(wait)", Theme.orange)) }
        if let wait = bead.waiting, short(wait) { out.append(("waiting \(wait)", Theme.orange)) }
        if bead.critical { out.append(("critical path", Theme.blue)) }
        return out
    }

    private var notes: [String] {
        [bead.quiet.flatMap { short($0) ? nil : "quiet: \($0)" },
         bead.waiting.flatMap { short($0) ? nil : "waits on: \($0)" }].compactMap { $0 }
    }
}
