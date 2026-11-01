import SwiftUI

/// PaneView is one pane as a conversation: replies as text, tool runs as one
/// line that expands, his messages on the right, the footer at the bottom of
/// each reply.
struct PaneView: View {
    @StateObject var model: PaneModel
    @ObservedObject var home: HomeModel

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
                            MessageView(message: m).id(m.id)
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
        }
        .navigationTitle(row?.name ?? model.ref.pane)
        .navigationBarTitleDisplayMode(.inline)
        .task {
            await model.load()
            model.startFeed()
        }
        .onDisappear { model.stopFeed() }
    }
}

/// MessageView draws one message in its shape.
struct MessageView: View {
    let message: Message
    @State private var expanded = false

    var body: some View {
        switch message.kind {
        case .nelson:
            VStack(alignment: .trailing, spacing: 4) {
                Text(message.text)
                    .padding(12)
                    .background(.quaternary, in: RoundedRectangle(cornerRadius: 18))
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
        case .system:
            Text(message.text).font(.caption).foregroundStyle(.secondary)
                .frame(maxWidth: .infinity)
        }
    }
}
