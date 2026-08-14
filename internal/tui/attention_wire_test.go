package tui

// Waiting state crossing the wire (ini-35ak).
//
// FIXTURE FIDELITY IS THE POINT OF THIS FILE, not an aside (AC3). The defect
// this bead closes was invisible for every release because the tests that
// covered the attention walk held LOCAL panes: waitingRows and shouldChime
// both do p.(waitingPane) and silently skip anything that does not implement
// it, and *Pane always did. A local-pane fixture cannot fail the way the
// product failed, so every discriminating test below holds a REMOTE pane.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// remoteWaiting builds a viewer-side remote pane in a known waiting state.
func remoteWaiting(name string, ws WaitingState) *RemotePane {
	rp := &RemotePane{name: name, host: WindowOnePeerName, alive: true}
	rp.ApplyWaiting(ws)
	return rp
}

// waitingNow is the state window 1 would send for an agent that started
// waiting `age` ago.
func waitingNow(age time.Duration, preview string) WaitingState {
	return WaitingState{
		Waiting:     true,
		SinceMillis: time.Now().Add(-age).UnixMilli(),
		Preview:     preview,
	}
}

// TestRemotePane_ImplementsWaitingPane is the structural cell: the REAL type
// must satisfy the interface the attention walk gates on.
//
// Asserted on *RemotePane itself rather than on a stand-in, because a test
// double that implements waitingPane proves only that the test double does.
// That substitution is exactly how the gap survived: the ini-9isx unit test
// passed on local-pane fixtures while the product had never once listed a
// remote agent.
func TestRemotePane_ImplementsWaitingPane(t *testing.T) {
	var pv PaneView = &RemotePane{name: "eng1", host: WindowOnePeerName}
	wp, ok := pv.(waitingPane)
	if !ok {
		t.Fatal("*RemotePane does not implement waitingPane, so waitingRows and " +
			"shouldChime skip every remote pane BEFORE scoping is consulted -- which is " +
			"the bug: a viewer can never list or chime a window-1 agent")
	}
	if waiting, since, preview := wp.WaitingInput(); waiting || !since.IsZero() || preview != "" {
		t.Errorf("a fresh remote pane reports waiting=%v since=%v preview=%q; it must "+
			"report nothing until window 1 says otherwise -- a viewer cannot see the "+
			"agent's PTY and must never invent attention state", waiting, since, preview)
	}
}

// TestWaitingRows_ListsRemoteAgents is AC1's list half, on the fixture class
// that exposed the gap.
func TestWaitingRows_ListsRemoteAgents(t *testing.T) {
	tui := newTestTUI()
	tui.windowID = "window-2"
	tui.panes = []PaneView{
		remoteWaiting("eng1", waitingNow(45*time.Second, "Continue? (y/n)")),
		&RemotePane{name: "eng2", host: WindowOnePeerName, alive: true},
	}

	rows := tui.waitingRows()
	if len(rows) != 1 {
		t.Fatalf("waitingRows returned %d rows, want 1: a window-1 agent waiting on the "+
			"operator is invisible from monitor 2, which is the whole promise of the "+
			"attention system", len(rows))
	}
	if rows[0].Name != "eng1" {
		t.Errorf("row names %q, want eng1", rows[0].Name)
	}
	if rows[0].Preview != "Continue? (y/n)" {
		t.Errorf("row preview is %q; the operator needs to know WHAT is being asked, "+
			"not merely that something is", rows[0].Preview)
	}
	if rows[0].Since.IsZero() {
		t.Error("row has no start time; the list orders by it and shows a duration, so a " +
			"zero would sort wrong and read as waiting forever")
	}
}

// TestWaitingRows_ClearTravelsToo is AC2. Both edges, and the clear is the one
// that rots quietly: a stale row survives an answered dialog and sends the
// operator to a monitor where nothing is waiting.
func TestWaitingRows_ClearTravelsToo(t *testing.T) {
	rp := remoteWaiting("eng1", waitingNow(10*time.Second, "Proceed?"))
	tui := newTestTUI()
	tui.windowID = "window-2"
	tui.panes = []PaneView{rp}

	if len(tui.waitingRows()) != 1 {
		t.Fatal("precondition failed: the raise edge did not land, so the clear assertion " +
			"below would pass without testing anything")
	}

	// The operator answers on window 1; window 1 broadcasts the new state.
	rp.ApplyWaiting(WaitingState{})

	if rows := tui.waitingRows(); len(rows) != 0 {
		t.Errorf("after the wait cleared, window 2 still lists %d row(s) (%q): the question "+
			"has been answered and the viewer is still pointing at it", len(rows), rows[0].Name)
	}
}

// TestWaitingRows_NotConsultedByDisplayScoping is AC4 as a literal cell.
//
// The viewer here owns NOTHING -- window 1 holds every agent -- which is
// exactly the state where a scoped list would show nothing. Attention is
// never scoped: the walk is over t.panes, not the window's rendered set.
func TestWaitingRows_NotConsultedByDisplayScoping(t *testing.T) {
	tui := newTestTUI()
	tui.windowID = "window-2"
	tui.panes = []PaneView{remoteWaiting("eng1", waitingNow(5*time.Second, "y/n"))}
	// Served ownership says window 1 owns it, so window 2 renders no panes.
	tui.applyServedPaneOwnership(map[string]string{"eng1": WindowOne})

	if got := len(tui.visiblePanesForWindow()); got != 0 {
		t.Fatalf("precondition: window 2 plans %d pane(s), want 0 -- this cell is only "+
			"discriminating while the agent is NOT displayed here", got)
	}
	if rows := tui.waitingRows(); len(rows) != 1 {
		t.Fatalf("waitingRows returned %d rows for an agent this window does not display, "+
			"want 1.\n\nDisplay scoping must never reach into attention: an agent waiting "+
			"on the operator has to be discoverable from every window, whichever monitor "+
			"it happens to render on.", len(rows))
	}
}

// TestWaitingStateOf_ReadsLocalPanes covers the send side: what window 1 puts
// on the wire is what its own attention walk sees.
func TestWaitingStateOf_ReadsLocalPanes(t *testing.T) {
	p := testPane("eng1")
	if ws := waitingStateOf(p); ws.Waiting {
		t.Error("an idle pane reports waiting on the wire")
	}

	started := time.Now().Add(-30 * time.Second)
	p.waitingSince = started
	p.waitingPreview = "Overwrite? (y/n)"

	ws := waitingStateOf(p)
	if !ws.Waiting {
		t.Fatal("a waiting local pane reports nothing on the wire, so no viewer can learn it")
	}
	if ws.Preview != "Overwrite? (y/n)" {
		t.Errorf("preview crossed as %q", ws.Preview)
	}
	if got := ws.Since(); got.Unix() != started.Unix() {
		t.Errorf("wait start crossed as %v, want %v -- chime bookkeeping compares this "+
			"instant to decide whether a wait is NEW, so a drifting value re-announces "+
			"the same question", got, started)
	}
}

// TestWaitingState_SurvivesTheWireFlat pins the encoding. The state is
// embedded so its keys stay flat next to the fields that already travel; a
// nested object would be a second shape for one concept.
func TestWaitingState_SurvivesTheWireFlat(t *testing.T) {
	in := AgentStatus{
		Name:         "eng1",
		Alive:        true,
		WaitingState: WaitingState{Waiting: true, SinceMillis: 1755200000000, Preview: "y/n"},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"waiting", "waiting_since_ms", "waiting_preview"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("wire form has no %q key: %s", key, data)
		}
	}

	var out AgentStatus
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if out.WaitingState != in.WaitingState {
		t.Errorf("round trip changed the state: %+v -> %+v", in.WaitingState, out.WaitingState)
	}
}

// TestBroadcastAgentStatusChanges_SingleWindowIsUntouched is AC5. A session
// with no second window must behave exactly as it did before this feature
// existed -- including doing no work per frame.
//
// lint:test-name-allow verifies a no-op contract with an assertion on the
// state that would change if the no-op broke.
func TestBroadcastAgentStatusChanges_SingleWindowIsUntouched(t *testing.T) {
	tui := newTestTUI()
	p := testPane("eng1")
	p.waitingSince = time.Now()
	tui.panes = []PaneView{p}
	tui.windowSrv = nil // single-window session

	tui.broadcastAgentStatusChanges()

	if tui.agentStatus != nil {
		t.Errorf("a single-window session built status bookkeeping (%d entries); with "+
			"nobody attached there is nothing to tell and nothing to remember",
			len(tui.agentStatus))
	}
}

// TestAttentionChimes_OncePerHost is pm's ruling as a literal cell
// (2026-08-14, canon amended cb1cbb2): on ONE HOST the sight travels to every
// window and the sound rings once.
//
// The two halves are asserted together on purpose. Splitting them lets a
// change satisfy one and quietly break the other, and the pairing IS the rule:
// window 2 must list the agent (it is the operator's only way to see WHICH
// agent is asking from that monitor) and must not ring (the windows share
// speakers, so a second chime is a duplicate noise, not new information).
//
// Cross-machine windows are a different question and a different bead
// (ini-tagj): there the second window has its own speakers and the question is
// silent there no matter what window 1 does.
func TestAttentionChimes_OncePerHost(t *testing.T) {
	now := time.Now()

	// WINDOW 1, session owner: rings once for a chime-grade wait.
	owner, ownerChimes := chimeTUI(waitingPaneAt("eng1", WaitingTierChime, 20*time.Second))
	if fired := owner.attentionChimes(now); fired != 1 || ownerChimes.n != 1 {
		t.Errorf("window 1 fired=%d chimes=%d for a new wait, want 1 and 1; if the host "+
			"never rings, the operator must be LOOKING at a monitor to learn a question "+
			"exists, which is the failure the attention system exists to prevent",
			fired, ownerChimes.n)
	}

	// WINDOW 2, viewer: lists the same wait, and stays silent.
	//
	// Built through chimeTUI so sound is genuinely CONFIGURED and a working
	// chimer is in place. Asserting silence on a viewer with no chimer would
	// pass for the wrong reason -- it would be measuring the fixture's missing
	// sound rather than the once-per-host rule.
	viewer, viewerChimes := chimeTUI()
	viewer.windowID = "window-2"
	viewer.panes = []PaneView{remoteWaiting("eng1", waitingNow(20*time.Second, "y/n"))}

	if rows := viewer.waitingRows(); len(rows) != 1 {
		t.Fatalf("window 2 lists %d waiting agent(s), want 1 -- the sight must travel to "+
			"every window even though the sound does not", len(rows))
	}
	if fired := viewer.attentionChimes(now); fired != 0 || viewerChimes.n != 0 {
		t.Errorf("window 2 fired=%d chimes=%d; on one host the windows share speakers, so "+
			"a viewer chime is a duplicate of window 1's, not a second notification "+
			"(pm ruling 2026-08-14; cross-machine is ini-tagj)", fired, viewerChimes.n)
	}

	// AND THE GATE ITSELF, because the assertion above does not reach it.
	//
	// Found by mutation: deleting the window-1-only gate in attentionChimes
	// left the check above green. A viewer's remote panes are silent for a
	// SECOND reason -- the chime loop requires tieredWaitingPane and
	// RemotePane does not implement it, since the tier does not travel the
	// wire. So the realistic fixture cannot distinguish "the gate holds" from
	// "remote panes have no tier", and a test that cannot tell those apart
	// would report the rule as enforced while the enforcement was accidental.
	//
	// This arm puts a chime-GRADE pane in a viewer, which is synthetic today
	// and is exactly the point: it is the only fixture in which everything
	// except the gate would ring. It also guards ini-tagj -- the moment tier
	// travels, this gate becomes the only thing standing between a viewer and
	// a duplicate chime on the same host.
	gated, gatedChimes := chimeTUI(waitingPaneAt("eng2", WaitingTierChime, 20*time.Second))
	gated.windowID = "window-2"
	if fired := gated.attentionChimes(now); fired != 0 || gatedChimes.n != 0 {
		t.Errorf("a viewer holding a chime-grade wait fired=%d chimes=%d, want 0 and 0: "+
			"the once-per-host rule is enforced by the window gate, not by remote panes "+
			"happening to lack a tier", fired, gatedChimes.n)
	}
}

// TestConnectPeer_SeedsWaitingFromTheHandshake is the LATE ATTACH case, and
// it is the one a transition-only design silently loses.
//
// If a window attaches while an agent is already waiting, no transition is
// coming: the question is on screen NOW, and the next status frame for that
// agent may be the one that CLEARS it. A viewer that only learns from
// transitions would show nothing until the operator answered elsewhere, which
// is the exact failure the bead describes -- a question waiting unseen.
//
// Covers the seeding path in connectPeer that the receive-side cells above
// cannot reach: they apply state to a pane that already exists, while this one
// asserts the pane is BORN with what the handshake carried.
func TestConnectPeer_SeedsWaitingFromTheHandshake(t *testing.T) {
	started := time.Now().Add(-90 * time.Second)
	srv := startRemotePeerServer(t, remotePeerServerConfig{
		firstResponse: HelloOKMsg{
			Action: "hello_ok", Version: 1, PeerName: "workbench",
			Agents: []AgentStatus{
				{Name: "eng1", Alive: true, WaitingState: WaitingState{
					Waiting: true, SinceMillis: started.UnixMilli(), Preview: "Continue? (y/n)",
				}},
				{Name: "eng2", Alive: true},
			},
		},
		agentNames: []string{"eng1", "eng2"},
	})

	project := &config.Project{PeerName: "local-peer", Token: "project-token"}
	pc, err := connectPeer("workbench", config.Remote{Addr: srv.addr}, project)
	if err != nil {
		t.Fatalf("connectPeer: %v", err)
	}
	defer func() {
		for _, pane := range pc.panes {
			if rp, ok := pane.(*RemotePane); ok {
				rp.Close()
			}
		}
	}()

	var seeded, quiet *RemotePane
	for _, pv := range pc.panes {
		rp, ok := pv.(*RemotePane)
		if !ok {
			continue
		}
		switch rp.Name() {
		case "eng1":
			seeded = rp
		case "eng2":
			quiet = rp
		}
	}
	if seeded == nil || quiet == nil {
		t.Fatalf("handshake produced %d pane(s); expected eng1 and eng2", len(pc.panes))
	}

	waiting, since, preview := seeded.WaitingInput()
	if !waiting {
		t.Fatal("a window attaching while eng1 was ALREADY waiting sees no wait: the " +
			"question is on screen now and no transition is coming for it, so this " +
			"window stays blind until the operator answers somewhere else")
	}
	if preview != "Continue? (y/n)" {
		t.Errorf("seeded preview is %q", preview)
	}
	if since.Unix() != started.Unix() {
		t.Errorf("seeded wait start is %v, want %v -- the duration shown to the operator "+
			"and the chime's rising-edge test both read this", since, started)
	}

	// The negative half: an agent that is NOT waiting must not be born waiting.
	if waiting, _, _ := quiet.WaitingInput(); waiting {
		t.Error("eng2 was seeded as waiting though the handshake said otherwise; a viewer " +
			"that invents attention sends the operator to a monitor with no question on it")
	}
}
