import XCTest
@testable import InitechApp

@MainActor
final class PaneModelTests: XCTestCase {
    var server: FixtureServer!
    var service: HTTPService!
    let ref = PaneRef(rig: "initech", pane: "eng1")

    override func setUp() async throws {
        server = try FixtureServer()
        try await server.start()
        service = HTTPService(pairing: server.pairing, retryDelay: .milliseconds(50))
    }

    override func tearDown() { server.stop() }

    func testConversation_ScrollsBackToTheTranscriptStart() async {
        let all = (1...120).map { Fx.msg("m\($0)", $0 % 3 == 0 ? .tool : .agent, "entry \($0)") }
        server.setTranscript(rig: ref.rig, pane: ref.pane, all, pageSize: PaneModel.pageSize)
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        XCTAssertEqual(pane.messages.count, 50)
        XCTAssertEqual(pane.messages.last?.id, "m120", "the newest page comes first")
        for _ in 0..<10 where !pane.startReached { await pane.loadOlder() }
        XCTAssertEqual(pane.messages, all)
        await pane.loadOlder()
        XCTAssertEqual(pane.messages.count, 120, "nothing past the start")
    }

    func testConversation_KeepsTheShapes() async {
        let msgs = [Fx.msg("1", .nelson, "status?"),
                    Fx.msg("2", .tool, "Bash", tool: .init(name: "Bash", summary: "git log -1", detail: "abc123 fix")),
                    Fx.msg("3", .agent, "All landed.", footer: ["▶ Ship it?"])]
        server.setTranscript(rig: ref.rig, pane: ref.pane, msgs, pageSize: 50)
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        XCTAssertEqual(pane.messages, msgs)
    }

    func testEmptyTranscript_LoadsWithNothing() async {
        let pane = PaneModel(service: service, ref: PaneRef(rig: "initech", pane: "fresh"))
        await pane.load()
        XCTAssertTrue(pane.loaded)
        XCTAssertTrue(pane.messages.isEmpty)
        XCTAssertTrue(pane.startReached)
    }

    func testSend_PutsTheVerdictOnTheMessage() async {
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        await pane.send("first")
        server.setSendVerdict(rig: ref.rig, pane: ref.pane, SendResult(verdict: "deferred", words: "deferred to eng1 — target has a modal open; will deliver when it closes"))
        await pane.send("second")
        server.setSendVerdict(rig: ref.rig, pane: ref.pane, SendResult(verdict: "not_delivered", words: "eng1 is not running"))
        await pane.send("third")
        XCTAssertEqual(pane.messages.map(\.text), ["first", "second", "third"])
        XCTAssertEqual(pane.messages.map { pane.verdicts[$0.id]?.outcome }, [.delivered, .held, .notDelivered])
        XCTAssertEqual(pane.verdicts[pane.messages[2].id]?.words, "eng1 is not running")
    }

    func testSend_WhenTheMacDoesNotAnswer_SaysNotDelivered() async {
        let pane = PaneModel(service: service, ref: ref)
        server.stop()
        await pane.send("hello")
        XCTAssertEqual(pane.verdicts[pane.messages[0].id]?.outcome, .notDelivered)
    }

    func testSend_IgnoresABlankMessage() async {
        let pane = PaneModel(service: service, ref: ref)
        await pane.send("  \n ")
        XCTAssertTrue(pane.messages.isEmpty)
        XCTAssertFalse(server.requests.contains { $0.path.hasPrefix("/v1/send") })
    }

    func testFeed_AppendsNewEntriesForThisPaneOnly() async throws {
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        pane.startFeed()
        defer { pane.stopFeed() }
        let srv = server!
        try await waitFor { srv.openFeeds == 1 }
        server.emit("transcript", #"{"rig":"initech","pane":"other","message":{"id":"x","kind":"agent","text":"not mine"}}"#)
        server.emit("transcript", #"{"rig":"initech","pane":"eng1","message":{"id":"n1","kind":"agent","text":"new reply","footer":["▶ ok?"]}}"#)
        try await waitForMain { pane.messages.count == 1 }
        XCTAssertEqual(pane.messages.first?.text, "new reply")
        XCTAssertEqual(pane.messages.first?.footer, ["▶ ok?"])
    }

    func testAResetFromTheFeed_ReloadsTheConversation() async throws {
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        XCTAssertTrue(pane.messages.isEmpty)
        // Written while the feed was away, past its replay window.
        server.setTranscript(rig: ref.rig, pane: ref.pane, [Fx.msg("m1", .agent, "missed")], pageSize: 50)
        pane.apply(.reset)
        try await waitForMain { pane.messages.map(\.id) == ["m1"] }
    }

    func testAnEntryFromTheFeed_SurvivesALoadThatDoesNotCarryItYet() async {
        server.setTranscript(rig: ref.rig, pane: ref.pane, [Fx.msg("m1", .agent, "older")], pageSize: 50)
        let pane = PaneModel(service: service, ref: ref)
        pane.apply(.transcript(rig: ref.rig, pane: ref.pane, message: Fx.msg("n1", .agent, "newer")))
        await pane.load()
        XCTAssertEqual(pane.messages.map(\.id), ["m1", "n1"])
    }

    func testAReset_AfterPagingBack_KeepsTheOrder() async throws {
        let all = (1...120).map { Fx.msg("m\($0)", .agent, "entry \($0)") }
        server.setTranscript(rig: ref.rig, pane: ref.pane, all, pageSize: PaneModel.pageSize)
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        await pane.loadOlder()
        XCTAssertEqual(pane.messages.count, 100)
        pane.apply(.reset)
        // The newest page overlaps what is held, so the older pages stay: the
        // reader keeps their place.
        try await Task.sleep(for: .milliseconds(300))
        XCTAssertEqual(pane.messages.count, 100)
        XCTAssertEqual(pane.messages.first?.id, "m21")
        XCTAssertEqual(pane.messages.last?.id, "m120")
        XCTAssertFalse(pane.startReached)
    }

    func testAReset_KeepsWhatHeSent_UntilTheTranscriptCarriesIt() async throws {
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        await pane.send("go ahead")
        pane.apply(.reset)
        try await waitForMain { pane.loaded }
        XCTAssertEqual(pane.messages.map(\.text), ["go ahead"], "not in the transcript yet: it stays")

        server.setTranscript(rig: ref.rig, pane: ref.pane, [Fx.msg("t9", .nelson, "go ahead")], pageSize: 50)
        await pane.load()
        XCTAssertEqual(pane.messages.map(\.id), ["t9"], "the page carries it: no second copy")
        XCTAssertEqual(pane.verdicts["t9"]?.outcome, .delivered)
    }

    /// A pane opened a second time has its view's task cancelled in the push;
    /// the read must still finish, or the pane opens empty.
    func testOpen_WhenItsTaskIsCancelled_StillReadsTheConversation() async throws {
        server.setTranscript(rig: ref.rig, pane: ref.pane, (1...10).map { Fx.msg("m\($0)", .agent, "e") }, pageSize: PaneModel.pageSize)
        let pane = PaneModel(service: service, ref: ref)
        let opening = Task { await pane.open() }
        opening.cancel()
        await opening.value
        try await waitForMain { pane.loaded }
        XCTAssertEqual(pane.messages.last?.id, "m10")
        XCTAssertNil(pane.problem)
        pane.stopFeed()
    }

    /// The feed lives as long as the model: once nothing holds an opened
    /// pane, it is released, and its deinit stops the feed.
    func testAnOpenedPane_IsReleasedWhenNothingHoldsIt() async throws {
        server.setTranscript(rig: ref.rig, pane: ref.pane, (1...3).map { Fx.msg("m\($0)", .agent, "e") }, pageSize: PaneModel.pageSize)
        var pane: PaneModel? = PaneModel(service: service, ref: ref)
        await pane?.open()
        weak let held = pane
        pane = nil
        try await waitForMain { held == nil }
    }

    func testAReset_WithEntriesMissedBelow_SplicesThemInAndKeepsTheOlderPages() async throws {
        var all = (1...120).map { Fx.msg("m\($0)", .agent, "entry \($0)") }
        server.setTranscript(rig: ref.rig, pane: ref.pane, all, pageSize: PaneModel.pageSize)
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        await pane.loadOlder()
        all += (121...125).map { Fx.msg("m\($0)", .agent, "entry \($0)") }
        server.setTranscript(rig: ref.rig, pane: ref.pane, all, pageSize: PaneModel.pageSize)
        pane.apply(.reset)
        try await waitForMain { pane.messages.last?.id == "m125" }
        XCTAssertEqual(pane.messages.map(\.id), (21...125).map { "m\($0)" })
    }

    func testAReset_PastMoreThanAPage_ReplacesWhatIsHeld() async throws {
        server.setTranscript(rig: ref.rig, pane: ref.pane, (1...60).map { Fx.msg("m\($0)", .agent, "e") }, pageSize: 50)
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        server.setTranscript(rig: ref.rig, pane: ref.pane, (1...200).map { Fx.msg("m\($0)", .agent, "e") }, pageSize: 50)
        pane.apply(.reset)
        try await waitForMain { pane.messages.last?.id == "m200" }
        XCTAssertEqual(pane.messages.first?.id, "m151", "no overlap: the newest page replaces, the place is lost")
        XCTAssertFalse(pane.startReached)
    }

    func testOpen_RunsTheFeedAndLoads() async throws {
        server.setTranscript(rig: ref.rig, pane: ref.pane, [Fx.msg("m1", .agent, "hi")], pageSize: 50)
        let pane = PaneModel(service: service, ref: ref)
        await pane.open()
        defer { pane.stopFeed() }
        XCTAssertEqual(pane.messages.map(\.id), ["m1"])
        let srv = server!
        try await waitFor { srv.openFeeds == 1 }
    }

    func testHisMessageComingBackFromTheTranscript_ReplacesTheSentCopyAndKeepsItsVerdict() async {
        let pane = PaneModel(service: service, ref: ref)
        await pane.load()
        await pane.send("go ahead")
        pane.apply(.transcript(rig: ref.rig, pane: ref.pane, message: Fx.msg("t9", .nelson, "go ahead")))
        XCTAssertEqual(pane.messages.map(\.id), ["t9"])
        XCTAssertEqual(pane.verdicts["t9"]?.outcome, .delivered)
    }

    func testKeyStrip_SendsTheKeyAndShowsItsVerdict() async {
        let pane = PaneModel(service: service, ref: ref)
        for key in PaneKey.allCases {
            await pane.press(key)
            XCTAssertEqual(pane.lastKey?.key, key)
            XCTAssertEqual(pane.lastKey?.result.outcome, .delivered)
        }
        let sent = server.requests.filter { $0.path == "/v1/keys/initech/eng1" }
            .map { String(decoding: $0.body, as: UTF8.self) }
        XCTAssertEqual(sent, PaneKey.allCases.map { #"{"key":"\#($0.rawValue)"}"# })
    }

    func testKeyStrip_NotDeliveredSaysSo() async {
        server.setKeyVerdict(rig: ref.rig, pane: ref.pane, SendResult(verdict: "not_delivered", words: "eng1 is not running"))
        let pane = PaneModel(service: service, ref: ref)
        await pane.press(.enter)
        XCTAssertEqual(pane.lastKey?.result.outcome, .notDelivered)
        XCTAssertEqual(pane.lastKey?.result.words, "eng1 is not running")
    }

    func testKeyStrip_SaysWhichKeysActOnlyAtADialog() throws {
        let hint = try XCTUnwrap(KeyStrip.hint(atDialog: true))
        XCTAssertTrue(hint.contains("Enter says Yes"), hint)
        XCTAssertTrue(hint.contains("Esc says No"), hint)
        XCTAssertTrue(hint.contains("Ctrl-C do nothing"), hint)
        XCTAssertNil(KeyStrip.hint(atDialog: false))
    }

    func testPaneRow_ShowsScreenLinesOnlyAtADialogOrBanner() {
        func row(_ state: String?, _ banner: String?) -> PaneRow {
            PaneRow(ref: ref, name: "eng1", state: state, banner: banner, gone: false)
        }
        XCTAssertTrue(row("waiting", "none").atDialog)
        XCTAssertTrue(row("pending", "none").atDialog)
        XCTAssertTrue(row("idle", "login").atDialog)
        XCTAssertFalse(row("running", "none").atDialog)
        XCTAssertFalse(row("idle", nil).atDialog)
    }
}
