import SwiftUI

/// PaneView is one pane as a conversation: replies as text, tool runs as one
/// line that expands, his messages on the right, the footer at the bottom of
/// each reply, and below the input box the key strip.
struct PaneView: View {
    @StateObject var model: PaneModel
    @ObservedObject var home: HomeModel
    @State private var draft = ""

    var row: PaneRow? { home.row(for: model.ref) }

    var body: some View {
        VStack(spacing: 0) {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 14) {
                        if model.loaded && !model.startReached {
                            ProgressView().frame(maxWidth: .infinity)
                                .task { await model.loadOlder() }
                        }
                        if model.loaded && model.messages.isEmpty {
                            Text("Nothing in this pane's conversation yet.")
                                .foregroundStyle(.secondary).frame(maxWidth: .infinity)
                        }
                        ForEach(model.messages) { m in
                            MessageView(message: m, verdict: model.verdicts[m.id]).id(m.id)
                        }
                    }
                    .padding()
                }
                .onChange(of: model.messages.last?.id) { _, last in
                    if let last { withAnimation { proxy.scrollTo(last, anchor: .bottom) } }
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
        .onDisappear { model.stopFeed() }
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
