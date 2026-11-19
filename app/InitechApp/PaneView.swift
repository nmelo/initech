import InitechCore
import SwiftUI

/// PaneView is one pane as a conversation, in the terminal look: replies as
/// text, tool runs as one line that expands, his messages as prompt lines, the
/// footer at the bottom of each reply, and below the input line the key strip.
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
                                if loadingOlder { ProgressView() } else { Text("▲ show older messages") }
                            }
                            .accessibilityLabel("Show older messages")
                            .font(Theme.mono(.footnote))
                            .disabled(loadingOlder)
                            .frame(maxWidth: .infinity)
                        }
                        if model.loaded && model.messages.isEmpty {
                            Text("Nothing in this pane's conversation yet.")
                                .font(Theme.mono(.footnote)).foregroundStyle(Theme.dim).frame(maxWidth: .infinity)
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
                        .font(Theme.mono(.footnote, .semibold))
                        .foregroundStyle(Theme.bg)
                        .padding(.horizontal, 12).padding(.vertical, 6)
                        .background(Theme.red, in: RoundedRectangle(cornerRadius: 6))
                        .padding(.bottom, 8)
                    }
                }
            }
            if let problem = model.problem {
                Text(problem).font(Theme.mono(.footnote)).foregroundStyle(Theme.red)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 16).padding(.vertical, 6)
            }
            if let row, row.atDialog, let screen = row.screen, !screen.isEmpty {
                ScreenLines(lines: screen)
            }
            InputBar(draft: $draft) { text in Task { await model.send(text) } }
            KeyStrip(last: model.lastKey, atDialog: row?.atDialog ?? false) { key in Task { await model.press(key) } }
        }
        .background(Theme.bg.ignoresSafeArea())
        .navigationTitle(row?.name ?? model.ref.pane)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            // The pane's name and its state, as the Panes list writes them.
            ToolbarItem(placement: .principal) {
                VStack(spacing: 1) {
                    Text((row?.name ?? model.ref.pane).lowercased())
                        .font(Theme.mono(.subheadline, .semibold)).foregroundStyle(Theme.bright)
                    Text(subtitle).font(Theme.mono(.caption2)).foregroundStyle(Theme.dim)
                }
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(row?.name ?? model.ref.pane)
            }
        }
        .toolbarBackground(Theme.bg, for: .navigationBar)
        .task {
            await model.open()
        }
    }
}

extension PaneView {
    /// subtitle is the line under the pane's name: its state and where it runs.
    var subtitle: String {
        let place = "\(model.ref.pane)@\(model.ref.rig)"
        guard let row else { return place }
        return "\(row.stateLabel) · \(place)"
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
            // His message, as the line he typed at a prompt.
            VStack(alignment: .leading, spacing: 5) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text("❯").foregroundStyle(Theme.red).accessibilityHidden(true)
                    Text(message.text).foregroundStyle(Theme.bright)
                }
                .font(Theme.mono(.callout, .semibold))
                if let verdict { VerdictLabel(result: verdict).padding(.leading, 18) }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 12).padding(.vertical, 9)
            .background(Theme.bar, in: RoundedRectangle(cornerRadius: 8))
            .overlay(alignment: .leading) { Rectangle().fill(Theme.red).frame(width: 2) }
        case .tool:
            VStack(alignment: .leading, spacing: 6) {
                Button { expanded.toggle() } label: {
                    HStack(spacing: 8) {
                        Text(expanded ? "▾" : "▸").foregroundStyle(Theme.dim)
                        Text(message.tool?.name ?? "tool").foregroundStyle(Theme.soft).fontWeight(.semibold)
                        Text(message.tool?.summary ?? message.text).foregroundStyle(Theme.dim).lineLimit(1)
                        Spacer(minLength: 0)
                    }
                    .font(Theme.mono(.footnote))
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Tool run \(message.tool?.name ?? "")")
                if expanded {
                    Text(message.tool?.detail ?? message.text)
                        .font(Theme.mono(.caption))
                        .foregroundStyle(Theme.soft)
                        .textSelection(.enabled)
                        .padding(9)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Theme.bar, in: RoundedRectangle(cornerRadius: 6))
                }
            }
        case .agent:
            VStack(alignment: .leading, spacing: 8) {
                Text(LocalizedStringKey(message.text)).textSelection(.enabled)
                    .font(Theme.mono(.subheadline)).foregroundStyle(Theme.text).lineSpacing(3)
                if let footer = message.footer, !footer.isEmpty {
                    Rectangle().fill(Theme.line).frame(height: 1)
                    ForEach(Array(footer.enumerated()), id: \.offset) { _, line in
                        Text(line).font(Theme.mono(.footnote)).foregroundStyle(Theme.soft)
                    }
                }
            }
        case .message:
            // A message another agent sent into the pane.
            VStack(alignment: .leading, spacing: 4) {
                if let from = message.from, !from.isEmpty {
                    Text("from \(from)").font(Theme.mono(.caption, .semibold)).foregroundStyle(Theme.blue)
                }
                Text(message.text)
                    .font(Theme.mono(.subheadline)).foregroundStyle(Theme.soft).lineSpacing(3)
                    .textSelection(.enabled)
            }
            .padding(.leading, 12)
            .frame(maxWidth: .infinity, alignment: .leading)
            .overlay(alignment: .leading) { Rectangle().fill(Theme.blue.opacity(0.7)).frame(width: 2) }
        case .other:
            Text(message.text).textSelection(.enabled)
                .font(Theme.mono(.subheadline)).foregroundStyle(Theme.text).lineSpacing(3)
        case .system:
            Text(message.text).font(Theme.mono(.caption)).foregroundStyle(Theme.dim)
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
            .font(Theme.mono(.caption))
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
        case .delivered: Theme.dim
        case .held, .unsure: Theme.orange
        case .notDelivered: Theme.red
        }
    }
}

/// ScreenLines is the pane's last screen lines when it is at a dialog or banner.
struct ScreenLines: View {
    let lines: [String]

    var body: some View {
        ScrollView(.horizontal) {
            Text(lines.joined(separator: "\n"))
                .font(Theme.mono(.caption2))
                .padding(.horizontal, 14).padding(.vertical, 9)
        }
        .frame(maxWidth: .infinity, maxHeight: 160, alignment: .leading)
        .background(Theme.bar)
        .foregroundStyle(Theme.text)
        .overlay(alignment: .leading) { Rectangle().fill(Theme.orange).frame(width: 2) }
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
    }
}

/// InputBar is the box he types a message into.
struct InputBar: View {
    @Binding var draft: String
    let send: (String) -> Void
    private var empty: Bool { draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    var body: some View {
        HStack(alignment: .center, spacing: 9) {
            Text("❯").font(Theme.mono(.body, .bold)).foregroundStyle(Theme.red).accessibilityHidden(true)
            TextField("Message", text: $draft, prompt: Text("message").foregroundStyle(Theme.dim), axis: .vertical)
                .lineLimit(1...6)
                .font(Theme.mono(.body))
                .foregroundStyle(Theme.bright)
                .tint(Theme.red)
            Button {
                send(draft)
                draft = ""
            } label: {
                Text("send").font(Theme.mono(.footnote, .bold))
                    .foregroundStyle(empty ? Theme.dim : Theme.bg)
                    .padding(.horizontal, 10).padding(.vertical, 5)
                    .background(empty ? Theme.faint : Theme.red, in: RoundedRectangle(cornerRadius: 6))
            }
            .buttonStyle(.plain)
            .disabled(empty)
            .accessibilityLabel("Send")
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 11)
        .background(Theme.bar)
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
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
        VStack(spacing: 6) {
            HStack(spacing: 7) {
                ForEach(PaneKey.allCases, id: \.self) { key in
                    Button { press(key) } label: {
                        Text(key.label.lowercased())
                            .font(Theme.mono(.footnote, .semibold))
                            .foregroundStyle(atDialog && (key == .enter || key == .esc) ? Theme.orange : Theme.soft)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 8)
                            .overlay(RoundedRectangle(cornerRadius: 6)
                                .stroke(atDialog && (key == .enter || key == .esc) ? Theme.orange.opacity(0.7) : Theme.faint, lineWidth: 1))
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(key.label)
                }
            }
            if let hint = Self.hint(atDialog: atDialog) {
                Text(hint).font(Theme.mono(.caption2)).foregroundStyle(Theme.soft).multilineTextAlignment(.center)
            }
            if let last {
                Text("\(last.key.label): \(last.result.words)")
                    .font(Theme.mono(.caption2))
                    .foregroundStyle(last.result.outcome == .delivered ? Theme.dim : Theme.red)
            }
        }
        .padding(.horizontal, 16)
        .padding(.top, 4).padding(.bottom, 8)
        .background(Theme.bar)
    }
}
