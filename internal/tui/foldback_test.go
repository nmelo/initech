//go:build !windows

// foldback_test.go covers ini-9ka.7: when a secondary window disconnects --
// cleanly or by crashing -- its agents render in window 1 within one render
// cycle, and reattaching hands them back exactly.
//
// The mechanism under test is a render-time PREDICATE, not a disconnect
// handler. Fold-back mutates rendering only; the assignment store is never
// written during it (AC 3), which is why reattach restores exactly and why
// there is no intermediate state where an agent belongs to no window (AC 5).
package tui

import (
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// foldbackFixture builds the two inputs the predicate reads: an assignment
// store (eng2's model, ini-9ka.4) and the layout-owned agent->group mapping.
// Assignment deliberately takes groupOf as a parameter rather than reading
// layout state, so the two concerns stay independent.
func foldbackFixture(t *testing.T) (*WindowAssignment, map[string]string, []string) {
	t.Helper()
	root := t.TempDir() // no unix socket here, so t.TempDir() is safe
	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatalf("LoadAssignment: %v", err)
	}
	if err := mustAssignWriter(t, a).MoveGroup("eng", "window2"); err != nil {
		t.Fatalf("MoveGroup: %v", err)
	}
	groupOf := map[string]string{
		"eng1":  "eng",
		"eng2":  "eng",
		"super": "core",
		"pm":    "core",
	}
	allGroups := []string{"core", "eng"}
	return a, groupOf, allGroups
}

// TestFoldback_AgentOnConnectedWindowIsNotRenderedByWindowOne is the NEGATIVE
// CONTROL for both detection paths, and the half that must be red before this
// bead's changes: today window 1 renders every pane unconditionally, with no
// notion of another window owning one. Fold-back only means something if
// window 1 declines to render an agent while its assigned window is alive --
// otherwise "it appeared in window 1" proves nothing, because it never left.
func TestFoldback_AgentOnConnectedWindowIsNotRenderedByWindowOne(t *testing.T) {
	a, groupOf, _ := foldbackFixture(t)
	connected := map[string]bool{"window2": true}

	if rendersInWindow("eng1", WindowOne, a, groupOf, connected) {
		t.Error("window 1 rendered an agent assigned to a CONNECTED window2; that agent belongs to window 2 and would be duplicated on both monitors")
	}
	// The agents genuinely on window 1 must still render.
	if !rendersInWindow("super", WindowOne, a, groupOf, connected) {
		t.Error("window 1 stopped rendering its own agent")
	}
}

// TestFoldback_DisconnectedWindowsAgentsRenderInWindowOne is the fold-back
// consequence itself, shared by both detection paths: once window 2 is no
// longer connected, its agents render in window 1.
func TestFoldback_DisconnectedWindowsAgentsRenderInWindowOne(t *testing.T) {
	a, groupOf, _ := foldbackFixture(t)
	connected := map[string]bool{} // window2 gone -- close or crash, same state

	if !rendersInWindow("eng1", WindowOne, a, groupOf, connected) {
		t.Error("agent assigned to a disconnected window2 did not fold back into window 1; it would be running invisible, which is the state this epic exists to prevent")
	}
}

// TestFoldback_EveryAgentRendersInExactlyOneWindow is AC 5 as an invariant
// rather than a timing test: at any instant -- window 2 connected or not --
// every agent is rendered by exactly one window. Because fold-back is a pure
// predicate over (assignment, liveness) with no mutation sequence, there is no
// intermediate state to catch it in; this asserts that directly for both
// liveness states.
func TestFoldback_EveryAgentRendersInExactlyOneWindow(t *testing.T) {
	a, groupOf, _ := foldbackFixture(t)
	windows := []string{WindowOne, "window2"}

	for _, connected := range []map[string]bool{
		{"window2": true}, // both windows up
		{},                // window2 gone
	} {
		for agent := range groupOf {
			count := 0
			for _, w := range windows {
				if rendersInWindow(agent, w, a, groupOf, connected) {
					count++
				}
			}
			if count != 1 {
				t.Errorf("agent %q rendered by %d windows (want exactly 1) with connected=%v -- neither-window means it runs invisible, both-windows means it is duplicated", agent, count, connected)
			}
		}
	}
}

// TestFoldback_ReattachRestoresWithoutTouchingAssignment is AC 3's honest
// restore. The assignment store must read back byte-identical after a
// disconnect/reattach round trip: fold-back is a rendering change, so nothing
// should have been written. If fold-back called MoveGroup, this is where it
// would show up -- the agent would be permanently on window 1 and reattach
// would restore nothing.
func TestFoldback_ReattachRestoresWithoutTouchingAssignment(t *testing.T) {
	a, groupOf, allGroups := foldbackFixture(t)

	before := a.WindowOfGroup("eng")
	beforeGroups := append([]string(nil), a.GroupsForWindow("window2", allGroups)...)

	// Disconnect, render a frame's worth of decisions, reattach.
	for _, connected := range []map[string]bool{{}, {"window2": true}} {
		for agent := range groupOf {
			_ = rendersInWindow(agent, WindowOne, a, groupOf, connected)
			_ = rendersInWindow(agent, "window2", a, groupOf, connected)
		}
	}

	if got := a.WindowOfGroup("eng"); got != before {
		t.Errorf("assignment changed across a fold-back/reattach cycle: %q -> %q (fold-back must mutate rendering only)", before, got)
	}
	got := a.GroupsForWindow("window2", allGroups)
	if len(got) != len(beforeGroups) {
		t.Fatalf("window2's assigned groups changed: %v -> %v", beforeGroups, got)
	}
	for i := range got {
		if got[i] != beforeGroups[i] {
			t.Errorf("window2's assigned groups changed: %v -> %v", beforeGroups, got)
		}
	}

	// And the reattached window renders its own agents again.
	if !rendersInWindow("eng1", "window2", a, groupOf, map[string]bool{"window2": true}) {
		t.Error("reattached window2 does not render its own agent")
	}
	if rendersInWindow("eng1", WindowOne, a, groupOf, map[string]bool{"window2": true}) {
		t.Error("window 1 still renders window2's agent after reattach; the fold-back did not release it")
	}
}

// TestFoldback_OnlyWindowOneAbsorbsDisconnectedAgents pins the direction: a
// third window must not also pick up window 2's orphans, or a two-monitor
// operator would see them twice. Fold-back is specified as "into window 1".
func TestFoldback_OnlyWindowOneAbsorbsDisconnectedAgents(t *testing.T) {
	a, groupOf, _ := foldbackFixture(t)
	connected := map[string]bool{"window3": true} // window2 gone, window3 up

	if rendersInWindow("eng1", "window3", a, groupOf, connected) {
		t.Error("window3 absorbed a disconnected window's agent; only window 1 folds back, otherwise the agent appears on two monitors")
	}
	if !rendersInWindow("eng1", WindowOne, a, groupOf, connected) {
		t.Error("window 1 did not absorb the disconnected window's agent")
	}
}

// --- Detection paths: two mechanisms, one consequence -----------------------
//
// Both tests below drive the SAME assertion (the agent folds back into window
// 1) through genuinely different disconnects, which is the reason this bead
// was not split. There is no goodbye message in the protocol -- verified, not
// assumed: handleControlStream has no bye/detach action -- so "not reusing the
// goodbye-message code path" is structural. Detection is transport-level in
// both cases, and the crash test proves it works when the client sends nothing
// graceful whatsoever.

// foldbackServerFixture starts a real window server exposing one agent
// assigned to window2, and returns the pieces the assertions need.
func foldbackServerFixture(t *testing.T) (*windowServer, string, *WindowAssignment, map[string]string) {
	t.Helper()
	root := t.TempDir()
	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatalf("LoadAssignment: %v", err)
	}
	if err := mustAssignWriter(t, a).MoveGroup("eng", "window2"); err != nil {
		t.Fatalf("MoveGroup: %v", err)
	}
	groupOf := map[string]string{"eng1": "eng", "super": "core"}

	panes := []*Pane{windowServerTestPane("eng1"), windowServerTestPane("super")}
	ws, addr := startTestWindowServer(t, panes)
	return ws, addr, a, groupOf
}

// waitForFoldback polls the predicate until eng1 renders in window 1, or the
// budget expires. The budget is the DETECTION latency allowance, not a
// convenience sleep: on loopback a dead process's socket closes immediately,
// so this normally returns on the first poll. A generous ceiling here would
// hide a regression that pushed detection past one render cycle.
func waitForFoldback(t *testing.T, ws *windowServer, a *WindowAssignment, groupOf map[string]string) time.Duration {
	t.Helper()
	const budget = 2 * time.Second
	start := time.Now()
	for time.Since(start) < budget {
		if rendersInWindow("eng1", WindowOne, a, groupOf, ws.connectedWindows()) {
			return time.Since(start)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("agent did not fold back into window 1 within %v of the window going away", budget)
	return 0
}

// TestFoldback_CleanCloseFoldsAgentIntoWindowOne is detection path 1: the
// window exits gracefully and its connection closes.
func TestFoldback_CleanCloseFoldsAgentIntoWindowOne(t *testing.T) {
	ws, addr, a, groupOf := foldbackServerFixture(t)

	session, ctrl, _ := dialWindow(t, addr, "window2")
	waitForClients(t, ws, 1)

	// While attached, window 1 must NOT be rendering window2's agent -- this
	// is the half that is red without the predicate, and it is what makes the
	// fold-back assertion below mean something.
	if rendersInWindow("eng1", WindowOne, a, groupOf, ws.connectedWindows()) {
		t.Fatal("window 1 rendered window2's agent while window2 was attached")
	}

	ctrl.Close()
	session.Close()

	took := waitForFoldback(t, ws, a, groupOf)
	t.Logf("clean close -> fold-back detected in %v", took)
}

// TestFoldback_CrashFoldsAgentIntoWindowOne is detection path 2, and the one
// that matters most: a REAL attached client process is killed with SIGKILL, so
// it sends nothing graceful -- no goodbye, no close, no unwind. Detection can
// only come from the transport noticing the connection died.
//
// The client is a re-invocation of this test binary (helper-process pattern)
// rather than an in-process goroutine, because an in-process "crash" cannot be
// SIGKILLed and would close its socket through Go's normal cleanup -- which is
// the clean-close path wearing a crash costume, and would prove nothing the
// test above does not already prove.
func TestFoldback_CrashFoldsAgentIntoWindowOne(t *testing.T) {
	ws, addr, a, groupOf := foldbackServerFixture(t)

	helper := exec.Command(os.Args[0], "-test.run=TestFoldbackHelperAttachAndIdle")
	helper.Env = append(os.Environ(), "INITECH_FOLDBACK_HELPER=1", "INITECH_FOLDBACK_ADDR="+addr)
	if err := helper.Start(); err != nil {
		t.Fatalf("start helper client: %v", err)
	}
	defer func() { _ = helper.Process.Kill() }()

	waitForClients(t, ws, 1)
	if rendersInWindow("eng1", WindowOne, a, groupOf, ws.connectedWindows()) {
		t.Fatal("window 1 rendered window2's agent while the helper window was attached")
	}

	// SIGKILL: no signal handlers run, no deferred close, no goodbye.
	if err := helper.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL helper: %v", err)
	}

	took := waitForFoldback(t, ws, a, groupOf)
	t.Logf("SIGKILL -> fold-back detected in %v", took)
}

// TestFoldbackHelperAttachAndIdle is the helper process for the crash test:
// it attaches as window2 and idles until killed. It is a no-op in a normal
// test run.
//
// lint:test-name-allow helper process entry point, not an assertion-bearing test
func TestFoldbackHelperAttachAndIdle(t *testing.T) {
	if os.Getenv("INITECH_FOLDBACK_HELPER") != "1" {
		t.Skip("helper process for TestFoldback_CrashFoldsAgentIntoWindowOne")
	}
	addr := os.Getenv("INITECH_FOLDBACK_ADDR")
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("helper dial: %v", err)
	}
	session, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("helper yamux: %v", err)
	}
	ctrl, err := session.Open()
	if err != nil {
		t.Fatalf("helper open ctrl: %v", err)
	}
	if err := writeJSON(ctrl, HelloMsg{Action: "hello", Version: 1, PeerName: "window2"}); err != nil {
		t.Fatalf("helper hello: %v", err)
	}
	// Single reader on the control stream, per the IPCScanner double-reader
	// trap: read the handshake reply here and nowhere else.
	scanner := NewIPCScanner(ctrl)
	if !scanner.Scan() {
		t.Fatal("helper got no hello_ok")
	}
	// Idle until SIGKILL arrives.
	time.Sleep(60 * time.Second)
}

// TestWindowLivenessTracker_ReportsTransitionsNotSteadyState covers the notice
// half of the AC. The notice must fire on the CHANGE, not on every frame the
// window stays gone -- a fold-back that re-announced itself 30 times a second
// would bury the session in notifications and make the real event unreadable.
func TestWindowLivenessTracker_ReportsTransitionsNotSteadyState(t *testing.T) {
	tr := newWindowLivenessTracker()

	// First observation: windows already attached are not arrivals.
	if gone, ret := tr.observe(map[string]bool{"window2": true}); len(gone) != 0 || len(ret) != 0 {
		t.Errorf("first observation reported transitions gone=%v returned=%v; already-attached windows have not just arrived", gone, ret)
	}

	// Steady state: no transitions.
	if gone, ret := tr.observe(map[string]bool{"window2": true}); len(gone) != 0 || len(ret) != 0 {
		t.Errorf("steady state reported transitions gone=%v returned=%v", gone, ret)
	}

	// Window leaves: reported once.
	gone, ret := tr.observe(map[string]bool{})
	if len(gone) != 1 || gone[0] != "window2" || len(ret) != 0 {
		t.Errorf("departure = gone:%v returned:%v, want gone:[window2]", gone, ret)
	}
	// Still gone: not reported again.
	if gone, ret := tr.observe(map[string]bool{}); len(gone) != 0 || len(ret) != 0 {
		t.Errorf("a window that is still gone re-reported: gone=%v returned=%v", gone, ret)
	}

	// Window returns: reported once.
	gone, ret = tr.observe(map[string]bool{"window2": true})
	if len(ret) != 1 || ret[0] != "window2" || len(gone) != 0 {
		t.Errorf("return = gone:%v returned:%v, want returned:[window2]", gone, ret)
	}
}

// TestWindowLivenessTracker_MultipleWindowsReportedDeterministically guards
// notice ordering: with more than one window changing in the same frame, the
// output must not vary with map iteration order, or the notice text would
// shuffle between otherwise-identical runs.
func TestWindowLivenessTracker_MultipleWindowsReportedDeterministically(t *testing.T) {
	tr := newWindowLivenessTracker()
	tr.observe(map[string]bool{"window2": true, "window3": true, "window4": true})

	gone, _ := tr.observe(map[string]bool{"window3": true})
	if len(gone) != 2 || gone[0] != "window2" || gone[1] != "window4" {
		t.Errorf("gone = %v, want sorted [window2 window4]", gone)
	}
}

// TestFoldedBackAgents_ListsOnlyOrphansInCallerOrder covers what the notice
// names: the agents window 1 is currently covering for. Order follows the
// caller's agent list so the notice is stable frame to frame.
func TestFoldedBackAgents_ListsOnlyOrphansInCallerOrder(t *testing.T) {
	a, groupOf, _ := foldbackFixture(t)
	keys := []string{"super", "eng1", "pm", "eng2"}

	got := foldedBackAgents(keys, a, groupOf, map[string]bool{})
	if len(got) != 2 || got[0] != "eng1" || got[1] != "eng2" {
		t.Errorf("folded back = %v, want [eng1 eng2] in caller order", got)
	}

	// Nothing folded back while the window is attached.
	if got := foldedBackAgents(keys, a, groupOf, map[string]bool{"window2": true}); len(got) != 0 {
		t.Errorf("folded back = %v while window2 attached, want none", got)
	}
}

// --- Production wiring (ini-9ka.6) -----------------------------------------
//
// qa1's .7 verdict flagged that rendersInWindow and the notice events had no
// non-test consumers. These assert the REAL render path consults the predicate
// and the notices actually raise, rather than the predicate being correct in
// isolation while nothing calls it.

// TestApplyLayout_ExcludesPanesOwnedByAConnectedWindow proves the production
// layout path -- not a test helper -- filters by assignment. If applyLayout
// stopped calling the predicate, this is what would catch it.
func TestApplyLayout_ExcludesPanesOwnedByAConnectedWindow(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1")
	root := t.TempDir()
	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatalf("LoadAssignment: %v", err)
	}
	if err := mustAssignWriter(t, a).MoveGroup("eng", "window-2"); err != nil {
		t.Fatalf("MoveGroup: %v", err)
	}
	tui.assignment = a
	tui.windowID = WindowOne
	tui.liveness = newWindowLivenessTracker()
	tui.layoutState.GroupOf = map[string]string{"super": "core", "eng1": "eng"}

	// window-2 attached: window 1 must not render eng1.
	tui.windowSrv = fakeWindowServerWith("window-2")
	tui.applyLayout()
	if planHasPane(tui, "eng1") {
		t.Error("applyLayout included a pane owned by a CONNECTED window-2; the real render path is not consulting the assignment predicate")
	}
	if !planHasPane(tui, "super") {
		t.Error("applyLayout dropped window 1's own pane")
	}

	// window-2 gone: eng1 folds back into the plan.
	tui.windowSrv = fakeWindowServerWith()
	tui.applyLayout()
	if !planHasPane(tui, "eng1") {
		t.Error("applyLayout did not fold a disconnected window's pane back into window 1; the agent would render nowhere")
	}
}

// TestApplyLayout_SingleWindowSessionRendersEveryPane is the zero-change
// guard for the wiring: with no assignment loaded (every ordinary session),
// the filter must be a pass-through, not a new code path with the same
// intended result.
func TestApplyLayout_SingleWindowSessionRendersEveryPane(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1")
	if tui.assignment != nil {
		t.Fatal("precondition: a plain test TUI must have no assignment store")
	}
	tui.applyLayout()
	for _, name := range []string{"super", "eng1"} {
		if !planHasPane(tui, name) {
			t.Errorf("single-window session dropped pane %q", name)
		}
	}
}

// TestNoticeWindowTransitions_RaisesFoldbackAndRestoreEvents proves the
// notices actually fire through the TUI's own event channel -- the AC's "with
// a notice", which qa1 flagged as unconsumed after .7.
func TestNoticeWindowTransitions_RaisesFoldbackAndRestoreEvents(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1")
	root := t.TempDir()
	a, _ := LoadAssignment(root, WindowOne)
	tui.assignment = a
	tui.windowID = WindowOne
	tui.liveness = newWindowLivenessTracker()
	tui.agentEvents = make(chan AgentEvent, 8)

	// Baseline with window-2 present, then it vanishes.
	tui.windowSrv = fakeWindowServerWith("window-2")
	tui.noticeWindowTransitions()
	tui.windowSrv = fakeWindowServerWith()
	tui.noticeWindowTransitions()

	ev := drainOneEvent(t, tui.agentEvents)
	if ev.Type != EventWindowFoldback {
		t.Errorf("event type = %v, want EventWindowFoldback", ev.Type)
	}
	if !strings.Contains(ev.Detail, "window-2") {
		t.Errorf("fold-back notice does not name the window: %q", ev.Detail)
	}
	if ev.Pane != "" {
		t.Errorf("fold-back notice is attached to pane %q; it is session-level and must render in every window", ev.Pane)
	}

	// And back again.
	tui.windowSrv = fakeWindowServerWith("window-2")
	tui.noticeWindowTransitions()
	ev = drainOneEvent(t, tui.agentEvents)
	if ev.Type != EventWindowRestored {
		t.Errorf("event type = %v, want EventWindowRestored", ev.Type)
	}
}

// fakeWindowServerWith builds a windowServer whose connected set is exactly
// the given peers, without opening a socket.
func fakeWindowServerWith(peers ...string) *windowServer {
	d := &Daemon{clients: make(map[string]net.Conn)}
	for _, p := range peers {
		d.clients[p] = nil
	}
	return &windowServer{daemon: d}
}

func planHasPane(t *TUI, name string) bool {
	for _, pr := range t.plan.Panes {
		if pr.Pane.Name() == name {
			return true
		}
	}
	return false
}

func drainOneEvent(t *testing.T, ch chan AgentEvent) AgentEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no event raised")
		return AgentEvent{}
	}
}
