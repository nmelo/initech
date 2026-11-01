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
    private var before: Int64?
    /// feedTask lives as long as the model: a view's disappear is not the
    /// pane closing (a pane opened again disappears and stays on screen while
    /// the push runs), so the feed stops when the model goes, in deinit.
    nonisolated(unsafe) private var feedTask: Task<Void, Never>?
    /// rereading and rereadingOlder say a cancelled read is being read once
    /// more, so a second cancel in a row stops there instead of looping.
    private var rereading = false
    private var rereadingOlder = false
    private var sentCount = 0
    static let pageSize = 50

    init(service: Service, ref: PaneRef) {
        self.service = service
        self.ref = ref
    }

    deinit { feedTask?.cancel() }

    /// open starts the feed, then reads the newest page: in that order, so an
    /// entry written between the two is not missed. The read runs in a task of
    /// its own: the view's task is cancelled when a pane opened a second time
    /// passes through the push, and a cancelled read left the pane empty.
    func open() async {
        startFeed()
        await Task { await self.load() }.value
    }

    /// load reads the newest page of the transcript and merges it with what is
    /// held. When the page overlaps what is held, the older entries before it
    /// stay and so does the paging back, so a reader keeps their place; what
    /// is held and not on the page (a feed entry newer than the page, a message
    /// he just sent) stays after it. With no overlap the page replaces the held
    /// entries: more than a page was missed, and the place cannot be kept.
    func load() async { await load(afterReset: false) }

    /// load(afterReset:) is load; after a feed reset with no overlap, what is
    /// held is stale, so only what he sent and the transcript has not shown
    /// yet stays after the page.
    private func load(afterReset: Bool) async {
        do {
            let page = try await service.conversation(rig: ref.rig, pane: ref.pane, before: nil, limit: Self.pageSize)
            page.messages.forEach { dropSentCopy(of: $0) }
            let onPage = Set(page.messages.map(\.id))
            if let first = page.messages.first?.id, let i = messages.firstIndex(where: { $0.id == first }) {
                messages = Array(messages[..<i]) + page.messages + messages[i...].filter { !onPage.contains($0.id) }
            } else {
                let kept = afterReset ? messages.filter { $0.id.hasPrefix("local-") } : messages
                messages = page.messages + kept.filter { !onPage.contains($0.id) }
                before = page.before
                startReached = page.startReached
            }
            loaded = true
            problem = nil
        } catch is CancellationError {
            // Not a failure: the task that asked was cancelled. Nothing shows;
            // while this task is live the page is read once more.
            guard !rereading, !Task.isCancelled else { return }
            rereading = true
            defer { rereading = false }
            await load()
        } catch {
            show("Could not read the conversation: \(error)", error)
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
        } catch is CancellationError {
            guard !rereadingOlder, !Task.isCancelled else { return } // the button stays; a tap reads again
            rereadingOlder = true
            defer { rereadingOlder = false }
            await loadOlder()
        } catch {
            show("Could not read older messages: \(error)", error)
        }
    }

    /// show puts a failure on the pane and its domain and code in the device log.
    private func show(_ line: String, _ error: Error) {
        problem = line
        Log.shown(error, screen: "pane")
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
        } catch ServiceError.status(let code, let words?) {
            verdicts[local.id] = SendResult(verdict: "not_delivered", words: words)
            Log.shown(ServiceError.status(code, words), screen: "pane")
        } catch {
            verdicts[local.id] = SendResult(verdict: "not_delivered", words: "The Mac did not answer: \(error)")
            Log.shown(error, screen: "pane")
        }
    }

    /// press sends one key of the key strip to the pane.
    func press(_ key: PaneKey) async {
        do {
            lastKey = (key, try await service.key(rig: ref.rig, pane: ref.pane, key: key))
        } catch ServiceError.status(let code, let words?) {
            lastKey = (key, SendResult(verdict: "not_delivered", words: words))
            Log.shown(ServiceError.status(code, words), screen: "pane")
        } catch {
            lastKey = (key, SendResult(verdict: "not_delivered", words: "The Mac did not answer: \(error)"))
            Log.shown(error, screen: "pane")
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
    /// window: the newest page is read again and merged in (see load), so the
    /// reader keeps their place.
    func apply(_ e: FeedEvent) {
        if case .reset = e {
            Task { await load(afterReset: true) }
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
