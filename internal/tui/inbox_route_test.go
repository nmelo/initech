package tui

// Tests for the any-window inbox (ini-3wkl.7).
//
// THE CHILD TALKS TO WINDOW 1 THROUGH THE REAL HANDLER. Every routed act here
// crosses a net.Pipe into Daemon.handleControlStream and comes back as a real
// ControlResp, because the claim is about the ROUTE, not about a function
// called with the right arguments. A fake that swallowed the command would
// pass every assertion below while the product wrote nothing.

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
)

func routeWindowOne(t *testing.T, root string) *TUI {
	t.Helper()
	return inboxFixtureFor(t).track(&TUI{
		projectRoot: root,
		windowID:    WindowOne,
		project:     &config.Project{Name: "rig", Root: root},
	})
}

func routeChild(t *testing.T, root string) *TUI {
	t.Helper()
	return inboxFixtureFor(t).track(&TUI{
		projectRoot: root,
		windowID:    "window-2",
		project:     &config.Project{Name: "rig", Root: root, PeerName: "window-2"},
	})
}

// linkToWindowOne gives a child the control path a real viewer has: a
// RemotePane carrying a ControlMux whose far end is window 1's own daemon
// handler, wired to window 1's applyInboxCmd exactly as startWindowServer
// wires it in production. The fixture owns the stream goroutine (ini-yxlh).
func linkToWindowOne(t *testing.T, child, w1 *TUI) {
	t.Helper()
	inboxFixtureFor(t).linkDaemon(child, &Daemon{onInboxCmd: w1.applyInboxCmd})
}

func routePost(t *testing.T, w1 *TUI, agent, body, def string) InboxPostResult {
	t.Helper()
	it, err := w1.inboxState().Post(
		InboxItem{Agent: agent, Body: body, DefaultText: def}, "run1")
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return it
}

func inboxBytes(t *testing.T, root string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".initech", "inbox.yaml"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read inbox: %v", err)
	}
	return b
}

// ── AC 1: a child's acts mutate the store through window 1 ───────────

func TestInboxRoute_ReplyFromAChildWindowIsWrittenByWindowOne(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	item := routePost(t, w1, "eng2", "which schema?", "")

	child := routeChild(t, root)
	linkToWindowOne(t, child, w1)
	// LOADED BEFORE THE ACT. Without this line the child's first read happens
	// after window 1 wrote, so its view is fresh whether or not routeInboxCmd
	// refreshes -- a surviving mutant found that.
	child.inboxState()

	if err := child.inboxAct(inboxOpReply, item.ID, "use the v2 schema"); err != nil {
		t.Fatalf("a child could not reply: %v", err)
	}

	got, ok := w1.inboxState().Item(item.ID)
	if !ok {
		t.Fatal("the item vanished from window 1's store")
	}
	if got.State != InboxAnswered {
		t.Errorf("state is %q, want answered -- the child's reply did not reach the writer", got.State)
	}
	if got.ReplyText == "" || !contains(got.ReplyText, "use the v2 schema") {
		t.Errorf("stored reply is %q; the operator's words must be what window 1 wrote", got.ReplyText)
	}
	// NO DOORBELL IS WIRED HERE, so this is the child's own post-act refresh.
	// A window that asked for a change and still shows the pre-change state
	// invites the operator to answer the same item twice.
	if mine, _ := child.inboxState().Item(item.ID); mine.State != InboxAnswered {
		t.Errorf("the replying window still shows the item as %q after window 1 accepted the "+
			"reply; it must re-read without waiting for a doorbell that may be dropped", mine.State)
	}
}

func TestInboxRoute_DismissAndSeenFromAChildWindowReachWindowOne(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	seen := routePost(t, w1, "eng2", "first", "")
	dismissed := routePost(t, w1, "eng3", "second", "")

	child := routeChild(t, root)
	linkToWindowOne(t, child, w1)

	if err := child.inboxAct(inboxOpSeen, seen.ID, ""); err != nil {
		t.Fatalf("seen: %v", err)
	}
	if err := child.inboxAct(inboxOpDismiss, dismissed.ID, ""); err != nil {
		t.Fatalf("dismiss: %v", err)
	}

	if got, _ := w1.inboxState().Item(seen.ID); got.State != InboxSeen {
		t.Errorf("seen item is %q on window 1, want seen", got.State)
	}
	if got, _ := w1.inboxState().Item(dismissed.ID); got.State != InboxDismissed {
		t.Errorf("dismissed item is %q on window 1, want dismissed", got.State)
	}
}

func TestInboxRoute_AcceptFromAChildWindowStoresTheDefaultAsTheReply(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	item := routePost(t, w1, "eng2", "ship it?", "ship at 5pm")

	child := routeChild(t, root)
	linkToWindowOne(t, child, w1)

	if err := child.inboxAct(inboxOpAccept, item.ID, ""); err != nil {
		t.Fatalf("accept: %v", err)
	}
	got, _ := w1.inboxState().Item(item.ID)
	if !contains(got.ReplyText, "ship at 5pm") {
		t.Errorf("accept stored %q; the default must be written as the reply text, which is "+
			"the whole reason accept goes through the ordinary write", got.ReplyText)
	}
}

// A CHILD'S OWN STORE STAYS REFUSED. The routing is what makes the act
// possible; the chokepoint is what makes improvising impossible. Both, not
// either.
func TestInboxRoute_AChildStoreStillRefusesADirectWrite(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	item := routePost(t, w1, "eng2", "q", "")

	child := routeChild(t, root)
	if err := child.inboxState().Transition(item.ID, InboxSeen, actorOperator); err == nil {
		t.Error("a child's store accepted a direct write; the routing would then be optional, " +
			"and any future seam could bypass it")
	}
}

// ── AC 2: every window lists it, every window counts it ──────────────

func TestInboxRoute_APostReachesAChildsListAndCount(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	child := routeChild(t, root)

	if got := child.inboxUnreadForBadge(); got != 0 {
		t.Fatalf("child starts with count %d, want 0", got)
	}
	routePost(t, w1, "eng2", "blocked on the schema", "")

	child.refreshInboxIfFollower()

	if got := child.inboxUnreadForBadge(); got != 1 {
		t.Errorf("the child's corner count is %d, want 1 -- attention is never scoped (§288)", got)
	}
	items := inboxListFor(child.inboxPanelStore())
	if len(items) != 1 || items[0].Agent != "eng2" {
		t.Errorf("the child's list is %+v, want the one posted item", items)
	}
}

// The doorbell itself: a change on window 1 puts inbox_changed on every
// attached control stream, and it is NOT a session notice.
func TestInboxRoute_AChangeRingsItsOwnDoorbell(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)

	windowSide, childSide := net.Pipe()
	defer windowSide.Close()
	defer childSide.Close()
	d := &Daemon{}
	d.ctrlConns = append(d.ctrlConns, windowSide)
	w1.windowSrv = &windowServer{daemon: d}

	got := make(chan ControlResp, 1)
	go func() {
		var resp ControlResp
		if err := json.NewDecoder(childSide).Decode(&resp); err == nil {
			got <- resp
		}
	}()

	routePost(t, w1, "eng2", "blocked", "")

	select {
	case resp := <-got:
		// THE LITERAL, NOT THE CONSTANT. Comparing against inboxChangedAction
		// would pass if the constant itself were changed to "session_notice",
		// because sender and assertion would move together.
		if resp.Action != "inbox_changed" || resp.Action == sessionNoticeAction {
			t.Errorf("the doorbell is %q, want %q. A post riding session_notice would make "+
				"every follower re-read three stores and re-plan its layout",
				resp.Action, inboxChangedAction)
		}
		if resp.Text != "" {
			t.Errorf("the doorbell carries a payload (%q); it exists to say READ THE STORE, "+
				"and a payload is a second source of truth to get out of order", resp.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no doorbell after a post: every child window's count is now stale until its " +
			"cadence fires, which is the failure the cadence bounds rather than the design")
	}
}

// ── AC 3: a post must not re-plan any window's layout ────────────────

// layoutFingerprint is every byte of the arrangement: the plan the renderer
// draws from, and each pane's own region.
func layoutFingerprint(t *TUI) string {
	out := ""
	for _, pr := range t.plan.Panes {
		out += pr.Pane.Name() + ":" + regionText(pr.Region) + "\n"
	}
	out += "screen:" + itoa(t.plan.ScreenW) + "x" + itoa(t.plan.ScreenH) + "\n"
	for _, p := range t.panes {
		out += "pane:" + p.Name() + ":" + regionText(p.GetRegion()) + "\n"
	}
	return out
}

func regionText(r Region) string {
	return itoa(r.X) + "," + itoa(r.Y) + "," + itoa(r.W) + "," + itoa(r.H)
}

func TestInboxRoute_ADoorbellDoesNotReplanTheLayout(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)

	child := routeChild(t, root)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen: %v", err)
	}
	screen.SetSize(120, 40)
	child.screen = screen
	child.panes = []PaneView{
		&mockPaneView{name: "eng1", host: "window1", alive: true},
		&mockPaneView{name: "eng2", host: "window1", alive: true},
	}
	child.layoutState, _ = LoadLayout(root, nil)
	child.ensureGroups(false)
	// SERVED OWNERSHIP, or the plan is EMPTY. A child renders only the panes
	// window 1 serves it and derives nothing (ini-x5ob); an unserved child's
	// plan has zero panes, and a zero-pane plan cannot show panes moving. The
	// first draft compared two empty plans and called it byte-identical.
	child.applyServedPaneOwnership(map[string]string{"eng1": "window-2", "eng2": "window-2"})
	child.recalcGrid(true)
	child.applyLayout()
	if n := len(child.plan.Panes); n != 2 {
		t.Fatalf("setup: the child's plan has %d panes, want 2 -- an arrangement with nothing "+
			"in it cannot show that a post moved nothing", n)
	}

	before := layoutFingerprint(child)
	logs := captureLogs(t)

	routePost(t, w1, "eng2", "blocked on the schema", "")
	// Exactly what the doorbell callback does.
	child.refreshInboxIfFollower()

	// THE RE-PLAN ITSELF, NOT ONLY ITS RESULT. A re-plan over unchanged inputs
	// yields the same arrangement, so the fingerprint below cannot see one --
	// a surviving mutant (recalcGrid + applyLayout inside the refresh) proved
	// it. eng2's finding was that a post RE-PLANS every follower, which is a
	// live-mode tick and a render-state rewrite on every post, visible or not.
	// applyLayout logs every run at INFO, so its absence is directly measured.
	// The MEASURED record text: LogInfo prefixes the component, so the line is
	// msg="[applyLayout] layout applied". A first draft searched for
	// msg="layout applied", which never occurs -- an assertion that could not
	// fail, found when the re-plan mutant survived it and a probe showed why.
	if strings.Contains(logs.String(), `[applyLayout] layout applied`) {
		t.Errorf("handling the inbox doorbell ran applyLayout. The inbox has its own doorbell " +
			"precisely so a post never re-plans a window; that is the session notice's job, " +
			"and only for a change to the session's shape")
	}

	if after := layoutFingerprint(child); after != before {
		t.Errorf(`A POST RE-PLANNED A CHILD WINDOW'S LAYOUT.

The inbox has its own doorbell precisely so a post does not do what a session
notice does: re-read three stores, re-project, recalcGrid, applyLayout. An
agent posting an item must never move the operator's panes.

before:
%s
after:
%s`, before, after)
	}
	if got := child.inboxUnreadForBadge(); got != 1 {
		t.Errorf("count is %d after the refresh, want 1: the refresh must still do its own job", got)
	}
}

// ── AC 4: the staleness bound ────────────────────────────────────────

// TestInboxRoute_ADroppedDoorbellCostsAtMostTheBound is the dead end this bead
// closes, asserted as the BOUND and not as the mechanism: no assertion here
// says the count must still be stale before the bound, because refreshing
// sooner is allowed and only refreshing LATER is the defect.
//
// The doorbell is never delivered: no windowSrv, so nothing is broadcast at
// all. That is the condition being measured, not a shortcut.
func TestInboxRoute_ADroppedDoorbellCostsAtMostTheBound(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	child := routeChild(t, root)

	start := time.Now()
	child.refreshInboxIfFollowerAt(start)

	routePost(t, w1, "eng2", "blocked on the schema", "")

	// THE BOUND AS STATED (pm default), NOT THE CONSTANT. Advancing the clock by
	// inboxFollowerRefresh would move with the constant: a 60s mutant advanced
	// the clock 60s and passed. Found by that surviving mutant.
	child.refreshInboxOnCadence(start.Add(30 * time.Second))

	if got := child.inboxUnreadForBadge(); got != 1 {
		t.Errorf(`A CHILD WINDOW'S COUNT IS STILL %d ONE BOUND AFTER A DROPPED DOORBELL.

Refresh-on-open alone is not enough, which is the whole reason this bead exists:
a stale count is exactly why the operator would not open the panel, so the one
seam that would fix it is the one nobody triggers.`, got)
	}
}

func TestInboxRoute_TheCadenceLeavesWindowOneAlone(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	item := routePost(t, w1, "eng2", "blocked", "")

	// Window 1's in-memory store IS the truth. A refresh there would discard
	// state it has not written yet and re-read its own file for no reason.
	w1.inboxState().Transition(item.ID, InboxSeen, actorOperator) //nolint:errcheck
	w1.refreshInboxOnCadence(time.Now().Add(time.Hour))

	if got, _ := w1.inboxState().Item(item.ID); got.State != InboxSeen {
		t.Errorf("window 1's store changed under the cadence (state %q): the authority never "+
			"reloads, the way refreshFleetIfFollower never reloads window 1", got.State)
	}
}

// ── AC 5: the panel opens on truth ───────────────────────────────────

func TestInboxRoute_OpeningThePanelRefreshesAStaleChild(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	child := routeChild(t, root)
	child.refreshInboxIfFollowerAt(time.Now()) // Child is current, and empty.

	routePost(t, w1, "eng2", "blocked on the schema", "")

	// No doorbell, no cadence: only the open.
	child.openInboxPanel()

	items := inboxListFor(child.inboxPanelStore())
	if len(items) != 1 {
		t.Errorf("the panel opened showing %d items, want 1. The cadence bounds how stale a "+
			"CLOSED window is; the moment the operator looks must not spend any of it", len(items))
	}
}

// ── AC 6: decline, never improvise ───────────────────────────────────

func TestInboxRoute_AChildThatCannotReachWindowOneDeclinesAndSaysSo(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	item := routePost(t, w1, "eng2", "which schema?", "")

	child := routeChild(t, root)
	// A viewer with panes but NO connection to window 1 -- the real shape of a
	// window whose peer link has dropped, not a TUI with nothing in it.
	child.panes = []PaneView{&mockPaneView{name: "eng1", host: "window1", alive: true}}
	before := inboxBytes(t, root)

	child.wireInboxDelivery()
	child.inbox.onReply(item.ID, "use the v2 schema")

	if child.inbox.note == "" {
		t.Error("a declined reply said nothing. §291 is decline AND say so: a silent no-op " +
			"leaves the operator believing an agent was answered")
	}
	if !contains(child.inbox.note, "not connected to window 1") {
		t.Errorf("the note is %q; it must name the reason, because the recovery differs from "+
			"every other failure", child.inbox.note)
	}
	if got := inboxBytes(t, root); string(got) != string(before) {
		t.Errorf(`A DISCONNECTED CHILD WROTE THE STORE.

§291 verbatim: a viewer that cannot reach window 1 DECLINES the mutation, it
never improvises. A local write that "syncs later" is exactly the improvisation
the canon forbids -- window 1 never re-reads, so the write would be invisible
to the only writer.

before: %s
after:  %s`, before, got)
	}
	if got, _ := w1.inboxState().Item(item.ID); got.State != InboxUnread {
		t.Errorf("window 1's item is %q; a declined act must change nothing anywhere", got.State)
	}
}

// TestInboxRoute_TheDismissKeyInAChildWindowRoutes drives the operator's
// actual keypress, not inboxAct: a key handler that calls the store directly
// is refused by the chokepoint and shows an error, and nothing reached
// window 1 -- which a test of inboxAct alone cannot see.
func TestInboxRoute_TheDismissKeyInAChildWindowRoutes(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	item := routePost(t, w1, "eng2", "stale question", "")

	child := routeChild(t, root)
	linkToWindowOne(t, child, w1)
	child.openInboxPanel()

	child.handleInboxKey(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone))

	if child.inbox.note != "" {
		t.Errorf("the dismiss key reported %q from a connected child", child.inbox.note)
	}
	if got, _ := w1.inboxState().Item(item.ID); got.State != InboxDismissed {
		t.Errorf("window 1's item is %q after a child pressed d; the key did not route", got.State)
	}
}

// A REFUSAL IS NOT A LOST RACE. Only "someone already resolved it" is silent;
// everything else window 1 refuses must reach the operator. Driven through a
// window 1 that is connected but owns no inbox, so the refusal is real and
// comes back over the wire.
func TestInboxRoute_ARefusalFromWindowOneIsShownNotSwallowed(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	item := routePost(t, w1, "eng2", "which schema?", "")

	child := routeChild(t, root)
	// onInboxCmd nil: a daemon that does not own the inbox. Owned by the
	// fixture like every other link (ini-yxlh).
	inboxFixtureFor(t).linkDaemon(child, &Daemon{})

	child.wireInboxDelivery()
	child.inbox.onReply(item.ID, "use the v2 schema")

	if !contains(child.inbox.note, "refused") {
		t.Errorf("window 1 refused the reply and the child's note is %q. The race-loss silence "+
			"is for ONE case; widening it to every refusal hides a failed answer", child.inbox.note)
	}
	if got, _ := w1.inboxState().Item(item.ID); got.State != InboxUnread {
		t.Errorf("a refused reply changed the item to %q", got.State)
	}
}

// A FAILURE INSIDE WINDOW 1'S OWN APPLY IS NOT A LOST RACE EITHER. The refusal
// cell above is refused before window 1 applies anything; this one reaches the
// store and fails there, which is the case a too-wide race check would
// silence. Found by a surviving mutant (every error mapped to "gone").
func TestInboxRoute_AReadOnlyStoreOnWindowOneIsShownToTheChild(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	item := routePost(t, w1, "eng2", "which schema?", "")
	// Window 1's REAL store, holding the item, turned read-only. A first draft
	// swapped in newFallbackInbox, which is EMPTY: window 1 then answered "no
	// such item", which is correctly the lost-race class and correctly silent,
	// so the cell failed on unmutated code and its "kill" measured nothing.
	// The item must exist and the WRITE must be what fails.
	ib := w1.inboxState()
	ib.mu.Lock()
	ib.readOnly = true
	ib.mu.Unlock()

	child := routeChild(t, root)
	linkToWindowOne(t, child, w1)
	child.wireInboxDelivery()
	child.inbox.onReply(item.ID, "use the v2 schema")

	if child.inbox.note == "" {
		t.Error("window 1 could not write the reply and the child said nothing. Only a lost " +
			"race is silent; a store that cannot be written means the answer went nowhere")
	}
}

// ── AC 7: first write wins ───────────────────────────────────────────

func TestInboxRoute_FirstReplyWinsAndTheLoserNeverOverwritesIt(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	item := routePost(t, w1, "eng2", "which schema?", "")

	first := routeChild(t, root)
	linkToWindowOne(t, first, w1)
	second := routeChild(t, root)
	linkToWindowOne(t, second, w1)
	second.refreshInboxIfFollower() // Both windows are looking at the open item.

	if err := first.inboxAct(inboxOpReply, item.ID, "use the v2 schema"); err != nil {
		t.Fatalf("first reply: %v", err)
	}

	second.wireInboxDelivery()
	second.inbox.onReply(item.ID, "use the v1 schema")

	if second.inbox.note != "" {
		t.Errorf(`THE LOSER OF THE RACE WAS TOLD OFF: %q.

The operator took the trade of NO "answered from another window" notice. A raw
transition error is that notice, in its worst wording -- it reads as a failure
of the operator's own keypress rather than as someone else having answered.`,
			second.inbox.note)
	}
	got, _ := w1.inboxState().Item(item.ID)
	if !contains(got.ReplyText, "v2") {
		t.Errorf("stored reply is %q, want the FIRST reply; window 1 serialises every mutation",
			got.ReplyText)
	}
	// WHEN THE ITEM DISAPPEARS, STATED. The AC says the loser's item vanishes.
	// In the built product that is gated on DELIVERY being confirmed, by C and
	// D's rule: an answered item whose delivery is unconfirmed keeps listing,
	// because its stored reply may be the only copy of the operator's answer.
	// So the loser first sees the item as answered carrying the OTHER window's
	// reply -- never its own -- and sees it go once delivery is confirmed.
	//
	// NO EXPLICIT REFRESH HERE: the lost-race path must have re-read on its own.
	// A refresh call in this spot did the mutant's missing work for it.
	items := inboxListFor(second.inboxPanelStore())
	if len(items) != 1 || items[0].State != InboxAnswered {
		t.Fatalf("the loser's view is %+v; want the item listed as answered", items)
	}
	if contains(items[0].ReplyText, "v1") {
		t.Errorf("the loser's own reply text reached the store (%q). First write wins means "+
			"the second write is not applied at all, not applied second", items[0].ReplyText)
	}

	w1.setInboxDelivery(item.ID, "eng2", InboxDelivered)
	second.refreshInboxIfFollower()
	if items := inboxListFor(second.inboxPanelStore()); len(items) != 0 {
		t.Errorf("the loser still lists the item after delivery was confirmed (%+v)", items)
	}
}

// ── the §291 hole this bead measured and closed ──────────────────────

// TestInboxRoute_AFollowerLoadNeverWritesTheStore pins a violation that
// existed before this bead: LoadInbox prunes terminal items and calls save()
// directly, and save() checks readOnly and memoryOnly but not authority --
// authority lives in mutate, which this path bypasses. Measured at 95f7bb7: a
// child window's load rewrote inbox.yaml from 189 bytes to 11. A follower
// refresh cadence would have repeated that write every 30 seconds.
func TestInboxRoute_AFollowerLoadNeverWritesTheStore(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	item := routePost(t, w1, "eng2", "done with it", "")
	if err := w1.inboxState().Transition(item.ID, InboxDismissed, actorOperator); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	before := inboxBytes(t, root)

	ib, err := LoadInbox(root, false)
	if err != nil {
		t.Fatalf("follower load: %v", err)
	}

	if got := inboxBytes(t, root); string(got) != string(before) {
		t.Errorf(`A FOLLOWER LOAD REWROTE WINDOW 1'S STORE (%d -> %d bytes).

A secondary window never writes project-root state (§291). The prune is the
authority's to persist; a follower drops the pruned items from its own view and
leaves the file alone.

before: %s
after:  %s`, len(before), len(got), before, got)
	}
	if n := len(ib.Items()); n != 0 {
		t.Errorf("the follower's own view kept %d terminal item(s); it must still prune what it "+
			"shows, it just must not write that decision to the file", n)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
