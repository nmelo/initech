import Foundation

/// PaneModel is one open pane: its conversation, paged back on demand, kept
/// current from the feed, and what he sends into it with the send path's verdict.
@MainActor
final class PaneModel: ObservableObject {
    @Published private(set) var messages: [Message] = []
    @Published private(set) var startReached = false
    @Published private(set) var loaded = false
    @Published private(set) var problem: String?
    /// verdicts holds the send verdict for each message he sent from here.
    @Published private(set) var verdicts: [String: SendResult] = [:]
    /// lastKey is the newest key-strip verdict, shown under the strip.
    @Published private(set) var lastKey: (key: PaneKey, result: SendResult)?

    let service: Service
    let ref: PaneRef
    private var before: String?
    private var feedTask: Task<Void, Never>?
    private var sentCount = 0
    static let pageSize = 50

    init(service: Service, ref: PaneRef) {
        self.service = service
        self.ref = ref
    }

    /// open starts the feed, then reads the newest page: in that order, so an
    /// entry written between the two is not missed.
    func open() async {
        startFeed()
        await load()
    }

    /// load reads the newest page of the transcript. What it already holds
    /// and the page does not carry yet (a feed entry newer than the page, a
    /// message he just sent) stays after the page.
    func load() async {
        do {
            let page = try await service.conversation(rig: ref.rig, pane: ref.pane, before: nil, limit: Self.pageSize)
            page.messages.forEach { dropSentCopy(of: $0) }
            let onPage = Set(page.messages.map(\.id))
            messages = page.messages + messages.filter { !onPage.contains($0.id) }
            before = page.before
            startReached = page.startReached
            loaded = true
            problem = nil
        } catch {
            problem = "Could not read the conversation: \(error)"
        }
    }

    /// loadOlder prepends the next older page; nothing once the start is reached.
    func loadOlder() async {
        guard loaded, !startReached else { return }
        do {
            let page = try await service.conversation(rig: ref.rig, pane: ref.pane, before: before, limit: Self.pageSize)
            let known = Set(messages.map(\.id))
            messages = page.messages.filter { !known.contains($0.id) } + messages
            before = page.before
            startReached = page.startReached
        } catch {
            problem = "Could not read older messages: \(error)"
        }
    }

    /// send types text into the pane through the service and puts the verdict on the message.
    func send(_ text: String) async {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        sentCount += 1
        let local = Message(id: "local-\(sentCount)", at: nil, kind: .nelson, text: trimmed, footer: nil, tool: nil)
        messages.append(local)
        do {
            verdicts[local.id] = try await service.send(rig: ref.rig, pane: ref.pane, text: trimmed)
        } catch ServiceError.status(_, let words?) {
            verdicts[local.id] = SendResult(verdict: "not_delivered", words: words)
        } catch {
            verdicts[local.id] = SendResult(verdict: "not_delivered", words: "The Mac did not answer: \(error)")
        }
    }

    /// press sends one key of the key strip to the pane.
    func press(_ key: PaneKey) async {
        do {
            lastKey = (key, try await service.key(rig: ref.rig, pane: ref.pane, key: key))
        } catch ServiceError.status(_, let words?) {
            lastKey = (key, SendResult(verdict: "not_delivered", words: words))
        } catch {
            lastKey = (key, SendResult(verdict: "not_delivered", words: "The Mac did not answer: \(error)"))
        }
    }

    func startFeed() {
        guard feedTask == nil else { return }
        let stream = service.feed()
        feedTask = Task { [weak self] in
            for await e in stream { self?.apply(e) }
        }
    }

    func stopFeed() {
        feedTask?.cancel()
        feedTask = nil
    }

    /// apply appends this pane's new transcript entries. His own message coming
    /// back from the transcript replaces the copy shown when he sent it, and
    /// keeps its verdict. A reset means entries were missed past the replay
    /// window: the conversation is read again from its newest page, keeping
    /// only what he sent that the transcript has not shown yet.
    func apply(_ e: FeedEvent) {
        if case .reset = e {
            messages = messages.filter { $0.id.hasPrefix("local-") }
            startReached = false
            Task { await load() }
            return
        }
        guard case let .transcript(rig, pane, m) = e, rig == ref.rig, pane == ref.pane,
              !messages.contains(where: { $0.id == m.id }) else { return }
        dropSentCopy(of: m)
        messages.append(m)
    }

    /// dropSentCopy removes the copy shown when he sent this message, once the
    /// transcript carries it; the verdict moves to the transcript's entry.
    private func dropSentCopy(of m: Message) {
        guard m.kind == .nelson,
              let i = messages.firstIndex(where: { $0.id.hasPrefix("local-") && $0.text == m.text }) else { return }
        verdicts[m.id] = verdicts.removeValue(forKey: messages[i].id)
        messages.remove(at: i)
    }
}
