import InitechCore
import SwiftUI

/// PaneView is one pane as a conversation, in the terminal look: replies as
/// text, a run of tool calls as one line that opens, his messages as prompt
/// lines, the footer at the bottom of each reply, and the input line below.
struct PaneView: View {
    @StateObject var model: PaneModel
    @ObservedObject var home: HomeModel
    /// back names the screen the way back returns to: the bar's screen he opened the pane from.
    var back = "pinned"
    @State private var draft = ""
    @State private var follow = FollowState()
    @State private var loadingOlder = false
    /// visible is the entries on screen, at least half in view.
    @State private var visible: Set<String> = []
    /// end is the id of the conversation's true end.
    static let end = "pane-end"
    /// compact is the pane stacked over the list, where it needs a way back.
    @Environment(\.horizontalSizeClass) private var sizeClass

    var row: PaneRow? { home.row(for: model.ref) }
    /// screenTail is the pane's last screen lines while it sits at something.
    var screenTail: [String]? {
        #if DEBUG
        if let sample = UITestHooks.screenLines { return sample }
        #endif
        guard let row, row.atDialog else { return nil }
        return row.screen
    }
    /// sampleScreen is true when those lines are the debug sample, not the pane's own.
    var sampleScreen: Bool {
        #if DEBUG
        return UITestHooks.screenLines != nil
        #else
        return false
        #endif
    }
    /// strip is the pane's list strip, as its agent set it.
    var strip: [String] {
        #if DEBUG
        if let sample = UITestHooks.strip { return sample }
        #endif
        return row?.strip ?? []
    }
    /// sampleStrip is true when the strip shown is the debug sample, not the pane's own.
    var sampleStrip: Bool {
        #if DEBUG
        return UITestHooks.strip != nil
        #else
        return false
        #endif
    }
    /// rows is the conversation as shown: each run of tool calls joined into one row.
    var rows: [ConversationRow] { ConversationRow.group(model.messages) }

    var body: some View {
        VStack(spacing: 0) {
            ScrollViewReader { proxy in
                ScrollView {
                  VStack(spacing: 0) {
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
                        // A run of tool calls is one row, so the rows, not the messages,
                        // are what is listed, followed and counted below.
                        ForEach(rows) { row in
                            switch row.kind {
                            case .message(let m): MessageView(message: m, verdict: model.verdicts[m.id]).id(row.id)
                            case .tools(let run): ToolRunView(tools: run).id(row.id)
                            }
                        }
                    }
                    .scrollTargetLayout()
                    .padding()
                    // The true end of the conversation, below the last row and its
                    // padding: what "the bottom" scrolls to. Scrolling to the last row
                    // itself stopped at its lower edge, 17 points short (measured), and
                    // left that much to scroll.
                    Color.clear.frame(height: 1).id(Self.end)
                  }
                }
                // A pane opens on its newest entry. No scroll position is held
                // by id: a held id went stale when an older page came in, and the
                // next entry then moved the view to it. Without one, an entry
                // added below never moves the view.
                .defaultScrollAnchor(.bottom, for: .initialOffset)
                // When the room for the conversation shrinks (the keyboard coming up,
                // the message growing to more lines) and he was at the end, it stays
                // at the end: the last lines move up with the input line and are never
                // left under the keyboard. Away from the end, his place is kept.
                .onScrollGeometryChange(for: [CGFloat].self) { g in
                    [g.containerSize.height, g.contentSize.height - g.contentOffset.y - g.containerSize.height - g.contentInsets.top]
                } action: { old, now in
                    if now[0] < old[0] - 1, old[1] < 40 { proxy.scrollTo(Self.end, anchor: .bottom) }
                }
                // He is at the bottom when the newest entry is in view; reaching
                // it, by a tap or by hand, clears the new-below line.
                .onScrollTargetVisibilityChange(idType: String.self, threshold: 0.5) { ids in
                    visible = Set(ids)
                    if let last = rows.last?.id, visible.contains(last) {
                        follow.sawBottom(last)
                    } else if !ids.isEmpty {
                        follow.leftBottom()
                    }
                }
                // A new entry follows him only if the entry before it was in
                // view, or it is his own message; away from the bottom the view
                // stays put and the new-below line counts what came after the
                // last entry he saw.
                .onChange(of: rows.last?.id) { old, last in
                    guard let last else { return }
                    guard let old else {
                        proxy.scrollTo(Self.end, anchor: .bottom)
                        follow.sawBottom(last)
                        return
                    }
                    let ids = rows.map(\.id)
                    let own = model.messages.last?.kind == .nelson
                    // Before the first scroll nothing has reported what is in
                    // view; the pane opened at the bottom, so that stands.
                    let wasAtBottom = visible.isEmpty ? follow.atBottom : visible.contains(old)
                    if follow.arrived(ids, previous: old, wasAtBottom: wasAtBottom, own: own) == .follow {
                        withAnimation { proxy.scrollTo(Self.end, anchor: .bottom) }
                    }
                }
                .overlay(alignment: .bottom) {
                    if let line = follow.markerText(rows.map(\.id)) {
                        Button(line) {
                            if let last = rows.last?.id {
                                withAnimation { proxy.scrollTo(Self.end, anchor: .bottom) }
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
            if let screen = screenTail, !screen.isEmpty {
                // A pane with text left unsent shows that text alone, cut from its screen;
                // when the cut is not certain, and at a dialog or banner, the screen as it is.
                if row?.state == "pending", let unsent = ScreenTail.unsent(screen) {
                    ScreenLines(title: "unsent in its input box", lines: unsent, sample: sampleScreen)
                } else {
                    ScreenLines(title: "on its screen now", lines: screen, sample: sampleScreen)
                }
            }
            if !strip.isEmpty { StripView(items: strip, sample: sampleStrip) }
            InputBar(draft: $draft) { text in Task { await model.send(text) } }
        }
        .background(Theme.bg.ignoresSafeArea())
        // The pane draws its own header, in the list's style, so the system's bar
        // and its round back button are hidden. The title stays for VoiceOver.
        .navigationTitle(row?.name ?? model.ref.pane)
        .toolbar(.hidden, for: .navigationBar)
        // Hiding the bar turns off the system's swipe back from the left edge; stacked, it is turned back on.
        .background { if sizeClass == .compact { BackSwipe().frame(width: 0, height: 0) } }
        .safeAreaInset(edge: .top, spacing: 0) { header }
        .task {
            await model.open()
        }
    }
}

extension PaneView {
    /// header is the pane's prompt line. Stacked over the list (iPhone) it
    /// starts with the way back, "‹ pinned" or "‹ rigs"; beside the list (iPad) the list is
    /// already there. Under it, the pane's state and where it runs; at the right,
    /// its context.
    var header: some View {
        HStack(alignment: .center, spacing: 0) {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 0) {
                    if sizeClass == .compact {
                        Button { home.selection = nil } label: {
                            Text("‹ \(back)").foregroundStyle(Theme.red)
                                .padding(.vertical, 8).contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Back")
                        .accessibilityIdentifier("BackButton")
                        Text(" ❯ ").foregroundStyle(Theme.dim)
                    } else {
                        Text("❯ ").foregroundStyle(Theme.dim)
                    }
                    Text((row?.name ?? model.ref.pane).lowercased()).fontWeight(.bold).foregroundStyle(Theme.bright)
                        .lineLimit(1)
                    Rectangle().fill(Theme.red).frame(width: 9, height: 17).padding(.leading, 6)
                        .accessibilityHidden(true)
                }
                .font(Theme.mono(.subheadline))
                Text(subtitle).font(Theme.mono(.caption2)).foregroundStyle(Theme.dim).lineLimit(1)
            }
            Spacer(minLength: 10)
            if let pct = row?.contextPercent {
                ContextBarView(percent: pct)
                Text("\(pct)%").font(Theme.mono(.caption)).foregroundStyle(Theme.soft)
                    .frame(minWidth: 36, alignment: .trailing)
            }
        }
        .padding(.horizontal, 20).padding(.vertical, 7)
        .frame(minHeight: Theme.headerHeight)
        .background(Theme.bg)
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
    }

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
            ToolRunView(tools: [message])
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

/// ToolRunView is a run of tool calls as one quiet line, the way Claude Code
/// writes it: "Read 2 files, ran 5 shell commands", the counts in bold. A tap
/// opens it: one call shows what it ran and what came back; several show a
/// line each, and each of those opens in turn.
struct ToolRunView: View {
    let tools: [Message]
    @State private var expanded = false
    /// open is the calls of a longer run whose result is showing.
    @State private var open: Set<String> = []

    private var names: [String] { tools.map { $0.tool?.name ?? "" } }

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            Button { expanded.toggle() } label: {
                ToolSummary.parts(names).reduce(Text("")) { line, part in
                    line + Text(part.text).fontWeight(part.number ? .bold : .regular)
                        .foregroundColor(part.number ? Theme.soft : Theme.dim)
                }
                .font(Theme.mono(.footnote))
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(tools.count == 1 ? "Tool run \(names[0])" : "Tool runs: \(ToolSummary.text(names))")
            if expanded {
                ForEach(tools) { call in
                    if tools.count == 1 {
                        what(call)
                        result(call)
                    } else {
                        Button { open.formSymmetricDifference([call.id]) } label: {
                            HStack(alignment: .firstTextBaseline, spacing: 7) {
                                Text(open.contains(call.id) ? "▾" : "▸").foregroundStyle(Theme.dim)
                                what(call)
                            }
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Tool run \(call.tool?.name ?? "")")
                        if open.contains(call.id) { result(call) }
                    }
                }
            }
        }
    }

    /// what is one call: the tool, then what it was asked.
    private func what(_ call: Message) -> some View {
        (Text(call.tool?.name ?? "tool").fontWeight(.semibold).foregroundColor(Theme.soft)
            + Text("  " + (call.tool?.summary ?? call.text)).foregroundColor(Theme.dim))
            .font(Theme.mono(.caption)).lineLimit(2)
            .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// result is what came back from one call.
    private func result(_ call: Message) -> some View {
        Text(call.tool?.detail ?? call.text)
            .font(Theme.mono(.caption))
            .foregroundStyle(Theme.soft)
            .textSelection(.enabled)
            .padding(9)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Theme.bar, in: RoundedRectangle(cornerRadius: 6))
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

/// StripView is the pane's list strip, as initech draws it at the bottom of a
/// pane on the Mac: a band with one line per item, each marked ▶, in the
/// agent's order. A long item wraps under its text; a long list scrolls.
struct StripView: View {
    let items: [String]
    /// sample marks the items as made-up text from the debug option, so they are
    /// never read as what the pane's agent really set.
    var sample = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 7) {
                if sample {
                    Text("SAMPLE TEXT, NOT THIS PANE'S LIST").font(Theme.mono(.caption2, .bold)).foregroundStyle(Theme.red)
                }
                ForEach(Array(items.enumerated()), id: \.offset) { _, item in
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        Text("▶").font(Theme.mono(.caption2)).foregroundStyle(Theme.orange)
                            .accessibilityHidden(true)
                        Text(item).font(Theme.mono(.footnote)).foregroundStyle(Theme.bright)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
            }
            .padding(.horizontal, 16).padding(.vertical, 10)
        }
        .scrollBounceBehavior(.basedOnSize)
        .frame(maxHeight: 190)
        .fixedSize(horizontal: false, vertical: true)
        .background(Theme.strip)
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Waiting on you")
        .accessibilityIdentifier("pane-strip")
    }
}

/// ScreenLines is the last lines of the pane's own terminal screen, shown
/// while the pane sits at something (a dialog, a banner, text left unsent in
/// its input box): a band above the message line, named so it explains
/// itself, with the lines as the terminal drew them. A long line scrolls
/// sideways and is never wrapped, so a dialog keeps its shape.
struct ScreenLines: View {
    /// title names what the lines are: "on its screen now", or "unsent in its input box".
    let title: String
    let lines: [String]
    /// sample marks the lines as made-up text from the debug option.
    var sample = false

    var body: some View {
        let tidy = ScreenTail.tidy(lines)
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Text(title).font(Theme.mono(.caption2, .bold)).foregroundStyle(Theme.orange)
                    .fixedSize()
                if sample { Text("SAMPLE TEXT").font(Theme.mono(.caption2, .bold)).foregroundStyle(Theme.red) }
                Rectangle().fill(Theme.line).frame(height: 1)
            }
            .padding(.horizontal, 16).padding(.top, 10).padding(.bottom, 6)
            ScrollView([.horizontal, .vertical]) {
                Text(tidy.joined(separator: "\n"))
                    .font(Theme.mono(.caption)).foregroundStyle(Theme.text)
                    .lineSpacing(2)
                    .fixedSize()
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 16).padding(.bottom, 10)
            }
            .defaultScrollAnchor(.bottomLeading)
            .scrollBounceBehavior(.basedOnSize, axes: [.horizontal, .vertical])
            .frame(maxHeight: 150)
            .fixedSize(horizontal: false, vertical: true)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.bar)
        .overlay(alignment: .leading) { Rectangle().fill(Theme.orange).frame(width: 3) }
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(title): \(tidy.joined(separator: ", "))")
        .accessibilityIdentifier("pane-screen")
    }
}

/// InputBar is the box he types a message into.
struct InputBar: View {
    @Binding var draft: String
    let send: (String) -> Void
    private var empty: Bool { draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    @FocusState private var typing: Bool

    var body: some View {
        HStack(alignment: .center, spacing: 9) {
            Text("❯").font(Theme.mono(.body, .bold)).foregroundStyle(Theme.red).accessibilityHidden(true)
            TextField("Message", text: $draft, prompt: Text("message").foregroundStyle(Theme.dim), axis: .vertical)
                .lineLimit(1...6)
                .font(Theme.mono(.body))
                .foregroundStyle(Theme.bright)
                .tint(Theme.red)
                .focused($typing)
                // The lowercase prompt is what the field showed as its name; it is "Message" to VoiceOver and the tests.
                .accessibilityLabel("Message")
                .accessibilityIdentifier("Message")
                #if DEBUG
                // -initech-debug-focus: the keyboard comes up by itself, so that state can be looked at from the Mac.
                .onAppear { if UITestHooks.focus { DispatchQueue.main.async { typing = true } } }
                #endif
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
        .padding(.vertical, 8)
        .frame(minHeight: Theme.barHeight)
        .background(Theme.bar.ignoresSafeArea(edges: .bottom))
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
    }
}
