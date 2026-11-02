import Combine
import Foundation

/// PaneRef names one pane across rigs.
public struct PaneRef: Hashable, Sendable {
    public var rig: String
    public var pane: String

    public init(rig: String, pane: String) {
        self.rig = rig
        self.pane = pane
    }
}

/// PaneRow is one line of the home list or the Rigs view.
public struct PaneRow: Identifiable, Equatable, Sendable {
    public var ref: PaneRef
    /// name is the name he gave the pane; the pane's own name when unpinned.
    public var name: String
    public var state: String?
    public var banner: String?
    public var contextPercent: Int?
    public var bead: String?
    public var screen: [String]?
    /// gone is true when the rig is not running or no longer has this pane.
    public var gone: Bool

    public var id: PaneRef { ref }

    /// stateLabel is the state in the words the row shows.
    public var stateLabel: String {
        if gone { return "gone" }
        if let banner, banner != "none", !banner.isEmpty { return banner }
        switch state {
        case "running": return "working"
        case "waiting": return "waiting at a prompt"
        case "pending": return "pending input"
        case nil: return "unknown"
        case let s?: return s
        }
    }

    /// atDialog is true when initech reports a dialog or banner for the pane:
    /// the pane view then shows its last screen lines above the key strip.
    public var atDialog: Bool {
        if let banner, banner != "none", !banner.isEmpty { return true }
        return state == "waiting" || state == "pending"
    }
}

/// HomeModel holds the pinned list and the status of every running rig, and
/// keeps both current from the change feed.
@MainActor
public final class HomeModel: ObservableObject {
    @Published public private(set) var pins: [Pin] = []
    @Published public private(set) var rigs: [RigStatus] = []
    @Published public private(set) var problem: String?
    /// needsPairing says the Mac refused this phone's token or certificate.
    @Published public private(set) var needsPairing = false
    /// waiting holds each pane's newest item from the blockers list, set at
    /// once by a waiting_item event and read again when the list changes.
    @Published public private(set) var waiting: [PaneRef: String] = [:]
    /// selection is the open pane: the sidebar's choice on iPad, the pushed
    /// pane on iPhone. Any view holding the model opens a pane with open(_:).
    @Published public var selection: PaneRef?

    public let service: Service
    /// listeners get every feed event before the home list does: the
    /// Blockers and Progress views while they are open.
    public var listeners: [String: any FeedListener] = [:]
    private var feedTask: Task<Void, Never>?

    public init(service: Service) { self.service = service }

    /// showsNoPinsLine says the list loaded and holds nothing; a list that
    /// failed to load says nothing about what is pinned.
    public var showsNoPinsLine: Bool { pinsLoaded && pins.isEmpty }
    private var pinsLoaded = false

    /// rows is the home list: his pins in his order, each joined to its status.
    public var rows: [PaneRow] {
        pins.sorted { $0.order < $1.order }.map { pin in
            let ref = PaneRef(rig: pin.rig, pane: pin.pane)
            guard let a = agent(ref) else {
                return PaneRow(ref: ref, name: pin.name, gone: true)
            }
            return row(ref, name: pin.name, a)
        }
    }

    /// rigRows is the Rigs view: every running rig with the panes he has not pinned.
    public var rigRows: [(rig: String, panes: [PaneRow])] {
        let pinned = Set(pins.map { PaneRef(rig: $0.rig, pane: $0.pane) })
        return rigs.map { r in
            (r.rig, r.agents.compactMap { a in
                let ref = PaneRef(rig: r.rig, pane: a.role)
                return pinned.contains(ref) ? nil : row(ref, name: a.role, a)
            })
        }
    }

    /// open shows a pane, pinned or not.
    public func open(_ ref: PaneRef) { selection = ref }

    public func row(for ref: PaneRef) -> PaneRow? {
        if let pin = pins.first(where: { $0.rig == ref.rig && $0.pane == ref.pane }) {
            return rows.first { $0.ref == ref } ?? PaneRow(ref: ref, name: pin.name, gone: true)
        }
        return agent(ref).map { row(ref, name: ref.pane, $0) }
    }

    private func agent(_ ref: PaneRef) -> AgentStatus? {
        rigs.first { $0.rig == ref.rig }?.agents.first { $0.role == ref.pane && $0.alive }
    }

    private func row(_ ref: PaneRef, name: String, _ a: AgentStatus) -> PaneRow {
        PaneRow(ref: ref, name: name, state: a.state, banner: a.banner, contextPercent: a.contextPercent,
                bead: a.beads?.first, screen: a.screen, gone: false)
    }

    // MARK: loading

    /// reload reads the pins, the status of every running rig and what waits.
    public func reload() async {
        do {
            async let p = service.pins()
            async let s = service.status()
            let (pins, status) = try await (p, s)
            self.pins = pins
            self.rigs = status.rigs
            pinsLoaded = true
            problem = nil
        } catch {
            note(error)
        }
        await refreshWaiting()
    }

    /// refreshWaiting takes what waits from the blockers list. A read that
    /// fails says nothing about an item leaving, so the rows stay as they are;
    /// the Blockers view reports the failure.
    private func refreshWaiting() async {
        guard let lists = try? await blockersSource.blockers() else { return }
        var next: [PaneRef: String] = [:]
        for rig in lists {
            for item in rig.waiting { next[PaneRef(rig: rig.rig, pane: item.pane)] = item.text }
        }
        waiting = next
    }

    /// startFeed applies feed events until stop; a reset reloads everything.
    public func startFeed() {
        guard feedTask == nil else { return }
        let stream = service.feed()
        feedTask = Task { [weak self] in
            for await e in stream { await self?.apply(e) }
        }
    }

    public func stopFeed() {
        feedTask?.cancel()
        feedTask = nil
    }

    /// apply folds one feed event into the model.
    public func apply(_ e: FeedEvent) async {
        for l in listeners.values { await l.apply(e) }
        switch e {
        case let .paneState(rig, pane, _, to, banner):
            guard let ri = rigs.firstIndex(where: { $0.rig == rig }),
                  let ai = rigs[ri].agents.firstIndex(where: { $0.role == pane }) else {
                // A pane or rig the list has not seen yet: read status again.
                await refreshStatus()
                return
            }
            rigs[ri].agents[ai].state = to
            rigs[ri].agents[ai].banner = banner
            if !(rigs[ri].agents[ai].atDialog) { rigs[ri].agents[ai].screen = nil }
            // The screen lines come with status, not with the event.
            if rigs[ri].agents[ai].atDialog { await refreshStatus() }
        case let .waitingItem(rig, pane, text):
            waiting[PaneRef(rig: rig, pane: pane)] = text
        case .stuck:
            await refreshStatus()
        case .blockers:
            await refreshWaiting()
        case .transcript:
            break // the open pane's own model takes these
        case .reset:
            await reload()
        }
    }

    private func refreshStatus() async {
        do { rigs = try await service.status().rigs } catch { note(error) }
    }

    // MARK: editing the pinned list (stored on the Mac)

    public func pin(_ ref: PaneRef) async {
        guard !pins.contains(where: { $0.rig == ref.rig && $0.pane == ref.pane }) else { return }
        var next = pins.sorted { $0.order < $1.order }
        next.append(Pin(rig: ref.rig, pane: ref.pane, name: ref.pane, order: 0))
        await save(next)
    }

    public func unpin(_ ref: PaneRef) async {
        await save(pins.sorted { $0.order < $1.order }.filter { !($0.rig == ref.rig && $0.pane == ref.pane) })
    }

    public func rename(_ ref: PaneRef, to name: String) async {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        await save(pins.sorted { $0.order < $1.order }.map {
            var p = $0
            if p.rig == ref.rig && p.pane == ref.pane { p.name = trimmed }
            return p
        })
    }

    /// move reorders like List's onMove, over the rows as shown.
    public func move(from: IndexSet, to: Int) async {
        await save(pins.sorted { $0.order < $1.order }.moved(fromOffsets: from, toOffset: to))
    }

    /// save renumbers the order and replaces the list on the Mac. The screen
    /// changes only once the Mac has it, so what he sees is what is stored.
    private func save(_ ordered: [Pin]) async {
        let next = ordered.enumerated().map { i, p in
            var p = p
            p.order = i
            return p
        }
        do {
            try await service.savePins(next)
            pins = next
            problem = nil
        } catch {
            note(error)
        }
    }

    /// note shows a failure, and logs it; a cancel (the task that asked was
    /// cancelled) is not one and shows nothing.
    private func note(_ error: Error) {
        if error is CancellationError { return }
        Log.shown(error, screen: "home")
        switch error as? ServiceError {
        case .unauthorized?:
            needsPairing = true
            problem = "The Mac refused this phone's token. Pair again."
        case .certificateRefused?:
            needsPairing = true
            problem = "The Mac's certificate is not the one this phone paired with. Pair again."
        default:
            problem = "The Mac did not answer: \(error)"
        }
    }
}

extension AgentStatus {
    public var atDialog: Bool {
        if let banner, banner != "none", !banner.isEmpty { return true }
        return state == "waiting" || state == "pending"
    }
}

/// FeedListener is a model that HomeModel hands every feed event to, before
/// it applies the event to the home list.
@MainActor
public protocol FeedListener: AnyObject {
    func apply(_ e: FeedEvent) async
}

extension BlockersModel: FeedListener {}
extension ProgressModel: FeedListener {}

/// Unserved answers for a service that does not serve the blockers or the
/// progress endpoint, so the view shows why it is empty instead of nothing.
struct Unserved: BlockersSource, ProgressSource {
    struct Missing: Error, CustomStringConvertible {
        var description: String { "this service does not serve it" }
    }
    func blockers() async throws -> [RigBlockers] { throw Missing() }
    func progress() async throws -> [ProgressReport] { throw Missing() }
}

extension HomeModel {
    /// blockersSource is the service as the blockers list reads it.
    public var blockersSource: BlockersSource { service as? BlockersSource ?? Unserved() }
    /// progressSource is the service as the progress view reads it.
    public var progressSource: ProgressSource { service as? ProgressSource ?? Unserved() }

    /// openBlocker opens the pane a blocker row names.
    public func openBlocker(rig: String, pane: String) { open(PaneRef(rig: rig, pane: pane)) }
}

extension Array {
    /// moved is the list after List's onMove, as SwiftUI's
    /// move(fromOffsets:toOffset:) reorders it (the package has no SwiftUI):
    /// the elements at source, in order, go just before the one that was at
    /// destination. MoveParityTests checks it against SwiftUI's.
    func moved(fromOffsets source: IndexSet, toOffset destination: Int) -> [Element] {
        let kept = indices.filter { !source.contains($0) }
        let before = kept.filter { $0 < destination }.map { self[$0] }
        let after = kept.filter { $0 >= destination }.map { self[$0] }
        return before + source.map { self[$0] } + after
    }
}
