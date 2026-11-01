import Foundation

/// PaneRef names one pane across rigs.
struct PaneRef: Hashable, Sendable {
    var rig: String
    var pane: String
}

/// PaneRow is one line of the home list or the Rigs view.
struct PaneRow: Identifiable, Equatable, Sendable {
    var ref: PaneRef
    /// name is the name he gave the pane; the pane's own name when unpinned.
    var name: String
    var state: String?
    var banner: String?
    var contextPercent: Int?
    var bead: String?
    var screen: [String]?
    /// gone is true when the rig is not running or no longer has this pane.
    var gone: Bool

    var id: PaneRef { ref }

    /// stateLabel is the state in the words the row shows.
    var stateLabel: String {
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
    var atDialog: Bool {
        if let banner, banner != "none", !banner.isEmpty { return true }
        return state == "waiting" || state == "pending"
    }
}

/// HomeModel holds the pinned list and the status of every running rig, and
/// keeps both current from the change feed.
@MainActor
final class HomeModel: ObservableObject {
    @Published private(set) var pins: [Pin] = []
    @Published private(set) var rigs: [RigStatus] = []
    @Published private(set) var problem: String?
    @Published private(set) var unauthorized = false
    /// waiting holds the newest waiting item per pane from the feed.
    @Published private(set) var waiting: [PaneRef: String] = [:]
    /// selection is the open pane: the sidebar's choice on iPad, the pushed
    /// pane on iPhone. Any view holding the model opens a pane with open(_:).
    @Published var selection: PaneRef?

    let service: Service
    /// listeners get every feed event before the home list does: the
    /// Blockers and Progress views while they are open.
    var listeners: [String: any FeedListener] = [:]
    private var feedTask: Task<Void, Never>?

    init(service: Service) { self.service = service }

    /// rows is the home list: his pins in his order, each joined to its status.
    var rows: [PaneRow] {
        pins.sorted { $0.order < $1.order }.map { pin in
            let ref = PaneRef(rig: pin.rig, pane: pin.pane)
            guard let a = agent(ref) else {
                return PaneRow(ref: ref, name: pin.name, gone: true)
            }
            return row(ref, name: pin.name, a)
        }
    }

    /// rigRows is the Rigs view: every running rig with the panes he has not pinned.
    var rigRows: [(rig: String, panes: [PaneRow])] {
        let pinned = Set(pins.map { PaneRef(rig: $0.rig, pane: $0.pane) })
        return rigs.map { r in
            (r.rig, r.agents.compactMap { a in
                let ref = PaneRef(rig: r.rig, pane: a.role)
                return pinned.contains(ref) ? nil : row(ref, name: a.role, a)
            })
        }
    }

    /// open shows a pane, pinned or not.
    func open(_ ref: PaneRef) { selection = ref }

    func row(for ref: PaneRef) -> PaneRow? {
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

    /// reload reads the pins and the status of every running rig.
    func reload() async {
        do {
            async let p = service.pins()
            async let s = service.status()
            let (pins, status) = try await (p, s)
            self.pins = pins
            self.rigs = status.rigs
            problem = nil
        } catch {
            note(error)
        }
    }

    /// startFeed applies feed events until stop; a reset reloads everything.
    func startFeed() {
        guard feedTask == nil else { return }
        let stream = service.feed()
        feedTask = Task { [weak self] in
            for await e in stream { await self?.apply(e) }
        }
    }

    func stopFeed() {
        feedTask?.cancel()
        feedTask = nil
    }

    /// apply folds one feed event into the model.
    func apply(_ e: FeedEvent) async {
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

    func pin(_ ref: PaneRef) async {
        guard !pins.contains(where: { $0.rig == ref.rig && $0.pane == ref.pane }) else { return }
        var next = pins.sorted { $0.order < $1.order }
        next.append(Pin(rig: ref.rig, pane: ref.pane, name: ref.pane, order: 0))
        await save(next)
    }

    func unpin(_ ref: PaneRef) async {
        await save(pins.sorted { $0.order < $1.order }.filter { !($0.rig == ref.rig && $0.pane == ref.pane) })
    }

    func rename(_ ref: PaneRef, to name: String) async {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        await save(pins.sorted { $0.order < $1.order }.map {
            var p = $0
            if p.rig == ref.rig && p.pane == ref.pane { p.name = trimmed }
            return p
        })
    }

    /// move reorders like List's onMove, over the rows as shown.
    func move(from: IndexSet, to: Int) async {
        var next = pins.sorted { $0.order < $1.order }
        next.move(fromOffsets: from, toOffset: to)
        await save(next)
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

    private func note(_ error: Error) {
        if error as? ServiceError == .unauthorized {
            unauthorized = true
            problem = "The Mac refused this phone's token. Pair again."
        } else {
            problem = "The Mac did not answer: \(error)"
        }
    }
}

extension AgentStatus {
    var atDialog: Bool {
        if let banner, banner != "none", !banner.isEmpty { return true }
        return state == "waiting" || state == "pending"
    }
}
