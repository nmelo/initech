import Foundation

/// PaneModel is one open pane: its conversation, paged back on demand and
/// kept current from the feed.
@MainActor
final class PaneModel: ObservableObject {
    @Published private(set) var messages: [Message] = []
    @Published private(set) var startReached = false
    @Published private(set) var loaded = false
    @Published private(set) var problem: String?

    let service: Service
    let ref: PaneRef
    private var before: String?
    private var feedTask: Task<Void, Never>?
    static let pageSize = 50

    init(service: Service, ref: PaneRef) {
        self.service = service
        self.ref = ref
    }

    /// load reads the newest page of the transcript.
    func load() async {
        do {
            let page = try await service.conversation(rig: ref.rig, pane: ref.pane, before: nil, limit: Self.pageSize)
            messages = page.messages
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

    /// apply appends this pane's new transcript entries.
    func apply(_ e: FeedEvent) {
        guard case let .transcript(rig, pane, m) = e, rig == ref.rig, pane == ref.pane,
              !messages.contains(where: { $0.id == m.id }) else { return }
        messages.append(m)
    }
}
