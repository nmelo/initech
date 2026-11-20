//go:build !windows

package tui

// x5ob_composed_test.go is ini-x5ob's composed leg: a REAL window server, a
// REAL peer connection, and the operator's exact timeline replayed against
// them -- reattach, then a move, with no further reattach to heal it.
//
// The in-process cells prove the rules; this proves the WIRE carries them.
// Ownership that is computed correctly and never arrives is the bug this bead
// started as, so "window 1 decided" and "window 2 was told" are separate
// claims and get separate evidence.
//
// '//go:build !windows' matches the rest of the window-server suites, whose
// TCP accept hangs the tui package on the Windows leg (run 31651424793).

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ownershipRecorder captures what a viewer is served, in order, so a test can
// assert on the SET it was told to render rather than on a count.
type ownershipRecorder struct {
	mu   sync.Mutex
	last map[string]string
	n    int
}

func (r *ownershipRecorder) record(owner map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = owner
	r.n++
}

func (r *ownershipRecorder) mine(windowID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for k, w := range r.last {
		if w == windowID {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func (r *ownershipRecorder) serves() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// TestX5obComposed_MoveAfterReattachReachesTheViewer replays the operator's
// timeline over a real connection.
func TestX5obComposed_MoveAfterReattachReachesTheViewer(t *testing.T) {
	rec := &ownershipRecorder{}
	// Window 1's authority state: eng1 belongs to a group assigned to window-2.
	root := t.TempDir()
	seedX5obStores(t, root)
	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatalf("LoadAssignment: %v", err)
	}
	w, ok := a.Writer()
	if !ok {
		t.Fatal("no writer")
	}

	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true},
		&mockPaneView{name: "super", alive: true},
	}
	groupOf := map[string]string{"eng1": "core", "super": "eng"}

	// A server wired with a REAL ownership provider, as production wires it
	// from the TUI. The shared test helper passes nil, which would leave the
	// handshake half of this test asserting against a server that has no
	// ownership to serve -- green for the wrong reason.
	ws, cleanup, err := startWindowServer(
		testWindowProject("127.0.0.1:0"), "test",
		[]*Pane{windowServerTestPane("eng1"), windowServerTestPane("super")},
		func(f func()) { go f() }, nil,
		func(string) map[string]string {
			return computePaneOwnership(panes, a, groupOf, map[string]bool{"window-2": true})
		}, nil)
	if err != nil {
		t.Fatalf("startWindowServer: %v", err)
	}
	t.Cleanup(cleanup)
	addr := ws.Addr()

	// FIRST ATTACH, then a disconnect and REATTACH -- the operator's 11:57
	// cycle. The viewer must be served at each handshake, which is what makes
	// a move made afterwards land without any further reattach.
	for cycle := 0; cycle < 2; cycle++ {
		session, ctrl, hello := dialWindow(t, addr, "window-2")
		if hello.Action != "hello_ok" {
			t.Fatalf("cycle %d: handshake failed: %+v", cycle, hello)
		}
		if hello.Owner != nil {
			rec.record(hello.Owner)
		}
		_ = ctrl
		session.Close()
	}
	handshakeServes := rec.serves()

	// THE MOVE, after the reattach, made on window 1 through the real seam.
	if err := w.MoveGroup("core", "window-2"); err != nil {
		t.Fatalf("move: %v", err)
	}
	owner := computePaneOwnership(panes, a, groupOf, map[string]bool{"window-2": true})

	// Window 1 serves the decision over the real broadcaster.
	session, ctrl, _ := dialWindow(t, addr, "window-2")
	defer session.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := NewIPCScanner(ctrl)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && scanner.Scan() {
			var ev ControlResp
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				continue
			}
			if ev.Action == paneOwnershipAction {
				rec.record(ev.Owner)
				return
			}
		}
	}()
	time.Sleep(100 * time.Millisecond) // let the reader attach
	ws.broadcastPaneOwnership(owner)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the viewer never received an ownership broadcast after a window-1 move -- " +
			"this is the operator's incident: the decision was made and never arrived")
	}

	if got := rec.mine("window-2"); got != "eng1" {
		t.Errorf(`the viewer was served %q, want "eng1".

Asserted as a SET: this bug's own measurement trap is that a stale membership
and a correct one can have the same size.`, got)
	}
	if handshakeServes == 0 {
		t.Error("no ownership was served at the handshake; a window that attaches and owns " +
			"nothing yet would render nothing until some unrelated change moved the map")
	}
}

// TestX5obComposed_UnservedViewerAnnouncesWhyItIsEmpty asserts the ANNOUNCEMENT,
// not merely the absence of panes (pm ruling + super's AC addition,
// 2026-08-14).
//
// Absence-only would be the vacuous shape: a window that renders nothing
// passes it whether it is correctly waiting, broken, or simply not drawing.
// The visible line is the positive half, and it is the half that matters --
// a silent blank window 2 looks exactly like the vanished-pane symptom this
// bead exists to fix, and would contradict what v2.7.9 shipped about a viewer
// that cannot reach window 1 telling you.
//
// The DECIDED LITERAL is asserted, never the constant: comparing the render to
// unservedViewerHint would pass for any value of it, including a paraphrase --
// the constant-on-both-sides tautology this repo has now caught four times in
// this arc (see placement_test.go's note). If this fails on a copy change, the
// change is a re-decision and pm signs it here.
func TestX5obComposed_UnservedViewerAnnouncesWhyItIsEmpty(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.windowID = "window-2"
	// Deliberately never served: this is the window-1-absent edge.
	//
	// RE-PLAN THROUGH THE REAL PATH. The shared fixture plans every pane it
	// builds, which models a viewer whose plan holds panes it does not own --
	// a state production cannot reach, and one that would hide this very
	// regression by making the window look occupied. The plan must come from
	// visiblePanesForWindow, the same function the product uses.
	tui.plan = computeLayout(tui.layoutState, tui.visiblePanesForWindow(), 120, 40)

	if got := len(tui.visiblePanesForWindow()); got != 0 {
		t.Fatalf("an unserved viewer planned %d pane(s); it must render none rather than guess", got)
	}

	w, h := s.Size()
	tui.renderEmptyViewerHint(s, w, h)
	s.Show()

	const decidedCopy = "waiting for window 1 — it decides which agents appear here; reconnecting"
	if got := hintRow(s, w, h/2); got != decidedCopy {
		t.Errorf(`the unserved viewer's empty state does not carry the decided copy.

got:  %q
want: %q

An empty window with no explanation is visually identical to the vanished-pane
symptom this bead fixed, and contradicts the shipped claim that a viewer which
cannot reach window 1 tells you.`, got, decidedCopy)
	}

	// And the other empty state must not be claimed here: "nothing assigned to
	// you" is a different sentence and asks the operator to act, which is
	// wrong while the answer simply has not arrived.
	if strings.Contains(hintRow(s, w, h/2), "no groups assigned") {
		t.Error("the unserved viewer claimed nothing was assigned to it; that sentence is " +
			"false while it is still waiting to be told what it owns")
	}
}

// TestX5obComposed_ServedViewerDropsTheWaitingLine is the other direction: the
// announcement must clear the moment the answer arrives, or it becomes a
// permanent lie on a working window.
func TestX5obComposed_ServedViewerDropsTheWaitingLine(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.windowID = "window-2"
	tui.applyServedPaneOwnership(map[string]string{"eng1": "window-2", "eng2": "window-2"})
	tui.plan = computeLayout(tui.layoutState, tui.visiblePanesForWindow(), 120, 40)

	w, h := s.Size()
	tui.renderEmptyViewerHint(s, w, h)
	s.Show()

	if strings.Contains(hintRow(s, w, h/2), "waiting for window 1") {
		t.Error("a served viewer still shows the waiting line; the hint is derived from " +
			"ownershipServed every frame, so it must vanish the moment the answer lands")
	}
}

// hintRow reads one rendered row as text.
func hintRow(s tcell.Screen, w, y int) string {
	row := make([]rune, 0, w)
	for x := 0; x < w; x++ {
		ch, _, _, _ := s.GetContent(x, y)
		row = append(row, ch)
	}
	return strings.TrimSpace(string(row))
}
