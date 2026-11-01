import SwiftUI

/// PaneView is one pane as a conversation: replies as text, tool runs as one
/// line that expands, his messages on the right, the footer at the bottom of
/// each reply, and below the input box the key strip.
struct PaneView: View {
    @StateObject var model: PaneModel
    @ObservedObject var home: HomeModel
    @State private var draft = ""
    @State private var follow = FollowState()
    @State private var loadingOlder = false
    /// visible is the entries on screen, at least half in view.
    @State private var visible: Set<String> = []

    var row: PaneRow? { home.row(for: model.ref) }

    var body: some View {
        VStack(spacing: 0) {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 14) {
                        // Older entries load on a tap, never by scrolling: a lazy
                        // stack builds its first rows whatever is on screen, so
                        // scroll-triggered paging chained pages in by itself.
                        if model.loaded && !model.startReached {
                            Button {
                                loadingOlder = true
                                // Known limit: the view lands at the top of the
                                // older page, not on the row he tapped from.
                                Task {
                                    await model.loadOlder()
                                    loadingOlder = false
                                }
                            } label: {
                                if loadingOlder { ProgressView() } else { Text("Show older messages") }
                            }
                            .disabled(loadingOlder)
                            .frame(maxWidth: .infinity)
                        }
                        if model.loaded && model.messages.isEmpty {
                            Text("Nothing in this pane's conversation yet.")
                                .foregroundStyle(.secondary).frame(maxWidth: .infinity)
                        }
                        ForEach(model.messages) { m in
                            MessageView(message: m, verdict: model.verdicts[m.id]).id(m.id)
                        }
                    }
                    .scrollTargetLayout()
                    .padding()
                }
                // A pane opens on its newest entry. No scroll position is held
                // by id: a held id went stale when an older page came in, and the
                // next entry then moved the view to it. Without one, an entry
                // added below never moves the view.
                .defaultScrollAnchor(.bottom, for: .initialOffset)
                // He is at the bottom when the newest entry is in view; reaching
                // it, by a tap or by hand, clears the new-below line.
                .onScrollTargetVisibilityChange(idType: String.self, threshold: 0.5) { ids in
                    visible = Set(ids)
                    if let last = model.messages.last?.id, visible.contains(last) {
                        follow.sawBottom(last)
                    } else if !ids.isEmpty {
                        follow.leftBottom()
                    }
                }
                // A new entry follows him only if the entry before it was in
                // view, or it is his own message; away from the bottom the view
                // stays put and the new-below line counts what came after the
                // last entry he saw.
                .onChange(of: model.messages.last?.id) { old, last in
                    guard let last else { return }
                    guard let old else {
                        proxy.scrollTo(last, anchor: .bottom)
                        follow.sawBottom(last)
                        return
                    }
                    let ids = model.messages.map(\.id)
                    let own = model.messages.last?.kind == .nelson
                    // Before the first scroll nothing has reported what is in
                    // view; the pane opened at the bottom, so that stands.
                    let wasAtBottom = visible.isEmpty ? follow.atBottom : visible.contains(old)
                    if follow.arrived(ids, previous: old, wasAtBottom: wasAtBottom, own: own) == .follow {
                        withAnimation { proxy.scrollTo(last, anchor: .bottom) }
                    }
                }
                .overlay(alignment: .bottom) {
                    if let line = follow.markerText(model.messages.map(\.id)) {
                        Button(line) {
                            if let last = model.messages.last?.id {
                                withAnimation { proxy.scrollTo(last, anchor: .bottom) }
                                follow.sawBottom(last)
                            }
                        }
                        .font(.footnote.weight(.medium))
                        .padding(.horizontal, 12).padding(.vertical, 6)
                        .background(.thinMaterial, in: Capsule())
                        .padding(.bottom, 8)
                    }
                }
            }
            if let problem = model.problem {
                Text(problem).font(.footnote).foregroundStyle(.red).padding(.horizontal)
            }
            if let row, row.atDialog, let screen = row.screen, !screen.isEmpty {
                ScreenLines(lines: screen)
            }
            InputBar(draft: $draft) { text in Task { await model.send(text) } }
            KeyStrip(last: model.lastKey, atDialog: row?.atDialog ?? false) { key in Task { await model.press(key) } }
        }
        .navigationTitle(row?.name ?? model.ref.pane)
        .navigationBarTitleDisplayMode(.inline)
        .task {
            await model.open()
        }
    }
}

/// MessageView draws one message in its shape.
struct MessageView: View {
    let message: Message
    var verdict: SendResult?
    @State private var expanded = false

    var body: some View {
        switch message.kind {
        case .nelson:
            VStack(alignment: .trailing, spacing: 4) {
                Text(message.text)
                    .padding(12)
                    .background(.quaternary, in: RoundedRectangle(cornerRadius: 18))
                if let verdict { VerdictLabel(result: verdict) }
            }
            .frame(maxWidth: .infinity, alignment: .trailing)
            .padding(.leading, 40)
        case .tool:
            VStack(alignment: .leading, spacing: 6) {
                Button { expanded.toggle() } label: {
                    HStack {
                        Image(systemName: "terminal")
                        Text(message.tool.map { "\($0.name) \($0.summary)" } ?? message.text).lineLimit(1)
                        Spacer()
                        Image(systemName: expanded ? "chevron.down" : "chevron.right")
                    }
                    .foregroundStyle(.secondary)
                    .font(.callout)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Tool run \(message.tool?.name ?? "")")
                if expanded {
                    Text(message.tool?.detail ?? message.text)
                        .font(.caption.monospaced())
                        .textSelection(.enabled)
                        .padding(8)
                        .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
                }
            }
        case .agent:
            VStack(alignment: .leading, spacing: 8) {
                Text(LocalizedStringKey(message.text)).textSelection(.enabled)
                if let footer = message.footer, !footer.isEmpty {
                    Divider()
                    ForEach(Array(footer.enumerated()), id: \.offset) { _, line in
                        Text(line).font(.callout)
                    }
                }
            }
        case .message:
            VStack(alignment: .leading, spacing: 4) {
                if let from = message.from, !from.isEmpty {
                    Text("from \(from)").font(.caption).foregroundStyle(.secondary)
                }
                Text(message.text)
                    .textSelection(.enabled)
                    .padding(12)
                    .background(.quaternary.opacity(0.6), in: RoundedRectangle(cornerRadius: 18))
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.trailing, 40)
        case .other:
            Text(message.text).textSelection(.enabled)
        case .system:
            Text(message.text).font(.caption).foregroundStyle(.secondary)
                .frame(maxWidth: .infinity)
        }
    }
}

/// VerdictLabel says what the send path did with a message, in the send
/// path's own words (they already name the verdict: "deferred to ...", "not
/// delivered to ...: ..."); a delivered message just says so.
struct VerdictLabel: View {
    let result: SendResult

    var body: some View {
        Label(text, systemImage: icon)
            .font(.caption)
            .foregroundStyle(color)
    }

    private var text: String {
        if result.outcome == .delivered { return "Delivered" }
        return result.words.isEmpty ? result.verdict : result.words
    }

    private var icon: String {
        switch result.outcome {
        case .delivered: "checkmark"
        case .held: "pause.circle"
        case .notDelivered: "exclamationmark.triangle"
        case .unsure: "questionmark.circle"
        }
    }

    private var color: Color {
        switch result.outcome {
        case .delivered: .secondary
        case .held, .unsure: .orange
        case .notDelivered: .red
        }
    }
}

/// ScreenLines is the pane's last screen lines when it is at a dialog or banner.
struct ScreenLines: View {
    let lines: [String]

    var body: some View {
        ScrollView(.horizontal) {
            Text(lines.joined(separator: "\n"))
                .font(.caption2.monospaced())
                .padding(8)
        }
        .frame(maxHeight: 160)
        .background(.black.opacity(0.85))
        .foregroundStyle(.white)
    }
}

/// InputBar is the box he types a message into.
struct InputBar: View {
    @Binding var draft: String
    let send: (String) -> Void

    var body: some View {
        HStack(alignment: .bottom) {
            TextField("Message", text: $draft, axis: .vertical)
                .lineLimit(1...6)
                .padding(10)
                .background(.quaternary, in: RoundedRectangle(cornerRadius: 18))
            Button {
                send(draft)
                draft = ""
            } label: {
                Image(systemName: "arrow.up.circle.fill").font(.title)
            }
            .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            .accessibilityLabel("Send")
        }
        .padding(.horizontal)
        .padding(.top, 6)
    }
}

/// KeyStrip sends Enter, Esc, Ctrl-C, y or n to the pane. "Delivered" means
/// the key reached the pane, not that it did anything, so a pane at a dialog
/// also says which keys act there.
struct KeyStrip: View {
    var last: (key: PaneKey, result: SendResult)?
    var atDialog = false
    let press: (PaneKey) -> Void

    /// hint is the line a pane at a dialog shows under the keys.
    static func hint(atDialog: Bool) -> String? {
        atDialog ? "On Claude's numbered prompt, Enter says Yes and Esc says No; y, n and Ctrl-C do nothing there." : nil
    }

    var body: some View {
        VStack(spacing: 2) {
            HStack {
                ForEach(PaneKey.allCases, id: \.self) { key in
                    Button(key.label) { press(key) }
                        .buttonStyle(.bordered)
                        .font(.callout.monospaced())
                }
            }
            if let hint = Self.hint(atDialog: atDialog) {
                Text(hint).font(.caption2).foregroundStyle(.secondary).multilineTextAlignment(.center)
            }
            if let last {
                Text("\(last.key.label): \(last.result.words)")
                    .font(.caption2)
                    .foregroundStyle(last.result.outcome == .delivered ? Color.secondary : Color.red)
            }
        }
        .padding(.vertical, 6)
    }
}

/// FollowState decides what a new entry does to the conversation: at the
/// bottom, or for his own message, the view follows it; away from the bottom
/// the view stays where he is reading and the new-below line counts the
/// entries after the last one he saw. The count comes from the entries
/// themselves, not a running total, so entries landing in one update are all
/// counted.
struct FollowState: Equatable {
    enum Action: Equatable { case follow, mark }

    private(set) var atBottom = true
    /// seen is the newest entry he has had at the bottom of the screen.
    private(set) var seen: String?

    /// arrived decides for the newest of ids. wasAtBottom says the entry before
    /// it (previous) was in view.
    mutating func arrived(_ ids: [String], previous: String?, wasAtBottom: Bool, own: Bool) -> Action {
        guard let last = ids.last else { return .follow }
        if wasAtBottom || own {
            atBottom = true
            seen = last
            return .follow
        }
        atBottom = false
        // The entry he saw can leave the list (his sent copy, replaced by the
        // transcript's); count from the entry before this one instead.
        if seen.map({ ids.contains($0) }) != true { seen = previous }
        return .mark
    }

    /// sawBottom records that the newest entry, last, is in view.
    mutating func sawBottom(_ last: String) {
        atBottom = true
        seen = last
    }

    mutating func leftBottom() { atBottom = false }

    /// newBelow is how many of ids come after the last one he saw.
    func newBelow(_ ids: [String]) -> Int {
        guard let seen, let i = ids.lastIndex(of: seen) else { return 0 }
        return ids.count - 1 - i
    }

    /// markerText is the new-below line, nil when nothing arrived below.
    func markerText(_ ids: [String]) -> String? {
        switch newBelow(ids) {
        case 0: nil
        case 1: "new below"
        case let n: "\(n) new below"
        }
    }
}
