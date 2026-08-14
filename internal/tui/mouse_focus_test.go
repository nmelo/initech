package tui

// mouse_focus_test.go — regressions for ini-pzx0 (focus first, everywhere).
//
// THE BUG: a forwarded click landing on a real permission dialog's option row
// ANSWERS it — dialog gone, tool call executed, no keystroke involved. Since
// initech forwards clicks to the pane under the cursor, an operator clicking a
// pane merely to look at it could approve something they never read. Measured
// N=2 on real Claude Code 2.1.232.
//
// THE RULE (Nelson's decision): the first click on an UNFOCUSED pane focuses it
// and nothing else; click again to act. A deliberate behaviour change, not only
// a protection — it publishes as the two-click rule.
//
// THE FIXTURE TRAP, which is this bead's own headline and applies to every test
// below: the click is INERT on Claude's fresh-workspace trust dialog and LIVE
// on its permission prompt. Two real option-pickers, opposite answers. A test
// written against the trust dialog proves nothing here, so these use
// renderPermissionPrompt — the rows captured verbatim from the live rig.
//
// AND THE TRAP UNDER THAT ONE: the emulator DROPS every mouse event unless the
// child has enabled mouse reporting. Without the DECSET below, every assertion
// of the form "no bytes reached the child" passes for the wrong reason, on a
// mouse mode that was never on. That is why TestPZX0_FocusedPaneClickStillActs
// is not merely a symmetric case — it is this file's positive control, and if
// it ever fails the rest of the file is measuring nothing.

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// pzx0Rig is a two-pane TUI driven through the real handleMouse entry point,
// with a tap on everything pane "eng1" writes toward its child.
type pzx0Rig struct {
	t      *TUI
	target *Pane
	mu     sync.Mutex
	toPTY  []byte // Bytes the pane emitted toward its child.
}

// newPZX0Rig builds a TUI whose FIRST pane ("eng1") shows an open permission
// dialog, with mouse reporting enabled exactly as a real Claude child enables
// it.
//
// The tap is the EMULATOR'S OUTPUT STREAM, not a PTY. That stream is precisely
// what the pane forwards to its child (responseLoop drains it to the ptmx), so
// reading it answers "what would the child have received" without needing a
// child. The first cut of this rig did use a real PTY and called Pane.Start(),
// and hung in Close(): a hand-built Pane has no cmd, so goWg.Wait() waited on
// goroutines whose shutdown depends on one. The simpler fixture is also the
// more direct measurement -- it taps the thing under test rather than a
// downstream copy of it.
func newPZX0Rig(t *testing.T) *pzx0Rig {
	t.Helper()

	emu := vt.NewSafeEmulator(80, 24)
	// Mouse reporting as Claude Code turns it on: SGR extended (1006) plus
	// button-event tracking (1002). Without this the emulator silently drops
	// every SendMouse and this whole file becomes a tautology.
	_, _ = emu.Write([]byte("\x1b[?1002h\x1b[?1006h"))
	renderPermissionPrompt(emu)

	target := &Pane{
		name:    "eng1",
		emu:     emu,
		alive:   true,
		visible: true,
		region:  Region{X: 0, Y: 0, W: 60, H: 20},
	}
	other := &Pane{
		name:    "eng2",
		emu:     vt.NewSafeEmulator(80, 24),
		alive:   true,
		visible: true,
		region:  Region{X: 60, Y: 0, W: 60, H: 20},
	}

	names := []string{"eng1", "eng2"}
	ls := DefaultLayoutState(names)
	views := []PaneView{target, other}
	tui := &TUI{panes: views, layoutState: ls, lastW: 120, lastH: 40}
	tui.plan = computeLayout(ls, views, 120, 40)

	r := &pzx0Rig{t: tui, target: target}
	go func() {
		buf := make([]byte, 512)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				r.mu.Lock()
				r.toPTY = append(r.toPTY, buf[:n]...)
				r.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { emu.Close() })
	return r
}

func (r *pzx0Rig) childSaw() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.toPTY)
}

func (r *pzx0Rig) reset() {
	r.mu.Lock()
	r.toPTY = nil
	r.mu.Unlock()
}

// optionRowY is the screen row of the dialog's "❯ 1. Yes" line, in TUI
// coordinates: the fixture paints it at emulator row 22 (1-based), and pane
// content starts one row below the activity bar (ini-yah).
const pzx0OptionRowY = 22

// press and release drive the REAL handleMouse, the same entry point tcell
// calls. Nothing here reaches into the swallow flag: the rule is observed
// through the child's bytes, never through the implementation's opinion.
func (r *pzx0Rig) press(x, y int) {
	r.t.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	time.Sleep(150 * time.Millisecond)
}

func (r *pzx0Rig) release(x, y int) {
	r.t.handleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	time.Sleep(150 * time.Millisecond)
}

// focusEng1 makes the target pane the focused one, so a following click is a
// SECOND click by the operator's model of the rule.
func (r *pzx0Rig) focusEng1() {
	r.t.layoutState.Focused = agentKey(r.target)
	r.t.plan = computeLayout(r.t.layoutState, r.t.panes, 120, 40)
}

// TestPZX0_FocusedPaneClickStillActs is AC item 3 AND this file's positive
// control. A deliberate click on an already-focused pane must still reach the
// child: that is intended operator action, and the whole point of "focus first"
// is that the second click works.
func TestPZX0_FocusedPaneClickStillActs(t *testing.T) {
	r := newPZX0Rig(t)
	r.focusEng1()
	r.reset()

	r.press(4, pzx0OptionRowY)
	pressSaw := r.childSaw()
	r.release(4, pzx0OptionRowY)
	releaseSaw := r.childSaw()

	if !strings.Contains(pressSaw, "\x1b[<0;") {
		t.Fatalf("a click on an ALREADY-FOCUSED pane did not reach the child. Either the rule is "+
			"over-suppressing (the operator can never act on a pane) or mouse reporting is off in "+
			"this fixture, in which case every other test in this file is vacuous. child saw %q",
			pressSaw)
	}
	if !strings.Contains(releaseSaw[len(pressSaw):], "\x1b[<0;") {
		t.Errorf("the press forwarded but the RELEASE did not: a press without its release is the "+
			"ini-82k mismatched-pair defect. child saw %q after press", releaseSaw[len(pressSaw):])
	}
}

// TestPZX0_UnfocusedPaneClickOnlyFocuses is AC item 1, on the permission-prompt
// fixture: the dialog must still be open and the child must have seen nothing.
func TestPZX0_UnfocusedPaneClickOnlyFocuses(t *testing.T) {
	r := newPZX0Rig(t)
	r.t.layoutState.Focused = agentKey(r.t.panes[1]) // eng2 focused; target is not
	r.reset()

	r.press(4, pzx0OptionRowY)

	if got := r.childSaw(); got != "" {
		t.Errorf("the first click on an UNFOCUSED pane reached the child. On a real permission "+
			"dialog that click answers the prompt and runs the tool call the operator never read. "+
			"child saw %q", got)
	}
	if r.t.layoutState.Focused != agentKey(r.target) {
		t.Errorf("the click did not focus the pane; focus-first costs the operator click-to-focus, "+
			"which AC item 4 forbids. focused=%q", r.t.layoutState.Focused)
	}
	if !paneShowsModalOnScreen(r.target) {
		t.Error("the permission dialog is no longer on screen -- the fixture was answered or lost, " +
			"and this test would pass for the wrong reason")
	}
}

// TestPZX0_SwallowedClickIsSwallowedWholePair is AC item 2, and it is the
// sharpest item on the bead. The release arrives AFTER focus has already
// landed, so any implementation that re-tests "is this pane focused" per event
// forwards this release alone -- an orphan into the child, the ini-82k
// mismatched-pair defect inverted. Press and release are asserted SEPARATELY so
// a half-swallow is distinguishable from a full one.
func TestPZX0_SwallowedClickIsSwallowedWholePair(t *testing.T) {
	r := newPZX0Rig(t)
	r.t.layoutState.Focused = agentKey(r.t.panes[1])
	r.reset()

	r.press(4, pzx0OptionRowY)
	afterPress := r.childSaw()
	if afterPress != "" {
		t.Fatalf("precondition: the press should have been swallowed; child saw %q", afterPress)
	}

	// Focus has now landed on the target pane. A per-event focus check would
	// answer "focused" here and let the release through.
	if r.t.layoutState.Focused != agentKey(r.target) {
		t.Fatal("precondition: focus should have moved to the target pane")
	}
	r.release(4, pzx0OptionRowY)

	if got := r.childSaw(); got != "" {
		t.Errorf("the press was swallowed but the RELEASE was forwarded. The child receives a "+
			"release with no matching press -- a client that validates press/release identity "+
			"before completing a click sees an orphan event (ini-82k, from the other side). "+
			"child saw %q", got)
	}
}

// TestPZX0_SwallowedDragIsSwallowedThroughItsRelease covers the drag clause of
// AC item 2: a gesture that begins with a swallowed press stays swallowed for
// its motion as well, so the child cannot receive a motion-then-release pair
// whose press it never saw.
func TestPZX0_SwallowedDragIsSwallowedThroughItsRelease(t *testing.T) {
	r := newPZX0Rig(t)
	r.t.layoutState.Focused = agentKey(r.t.panes[1])
	r.reset()

	r.press(4, pzx0OptionRowY)
	// Drag two rows down, then release somewhere else entirely.
	r.t.handleMouse(tcell.NewEventMouse(10, pzx0OptionRowY+1, tcell.Button1, tcell.ModNone))
	time.Sleep(150 * time.Millisecond)
	if got := r.childSaw(); got != "" {
		t.Errorf("drag motion forwarded after a swallowed press; child saw %q", got)
	}
	r.release(10, pzx0OptionRowY+1)
	if got := r.childSaw(); got != "" {
		t.Errorf("the drag's release forwarded after a swallowed press; child saw %q", got)
	}
}

// TestPZX0_SecondClickActsAfterFocusingClick is the operator's own model of the
// rule end to end -- "click again to act" -- and the guard against a swallow
// flag that is set and never cleared, which would make every click inert.
func TestPZX0_SecondClickActsAfterFocusingClick(t *testing.T) {
	r := newPZX0Rig(t)
	r.t.layoutState.Focused = agentKey(r.t.panes[1])
	r.reset()

	// First gesture: focuses only.
	r.press(4, pzx0OptionRowY)
	r.release(4, pzx0OptionRowY)
	if got := r.childSaw(); got != "" {
		t.Fatalf("precondition: first gesture should be fully swallowed; child saw %q", got)
	}

	// Second gesture on the now-focused pane: acts.
	r.press(4, pzx0OptionRowY)
	if got := r.childSaw(); !strings.Contains(got, "\x1b[<0;") {
		t.Errorf("the SECOND click did not reach the child. The operator was told 'click again to "+
			"act'; a swallow flag that is never cleared makes the pane permanently unclickable. "+
			"child saw %q", got)
	}
}

// TestPZX0_WheelOverUnfocusedPaneIsUnaffected pins the untouched half of AC
// item 4. The wheel cases never consult the gesture state, and the v2.5.1
// scroll-the-pane-under-the-cursor rule stands; this fails if a future edit
// routes wheel events through the click-swallow path.
func TestPZX0_WheelOverUnfocusedPaneIsUnaffected(t *testing.T) {
	r := newPZX0Rig(t)
	r.t.layoutState.Focused = agentKey(r.t.panes[1])
	// Alt-screen so the wheel FORWARDS to the child (ini-i3v) rather than
	// mutating scrollOffset -- the observable case.
	_, _ = r.target.emu.Write([]byte("\x1b[?1049h"))
	r.reset()

	r.t.handleMouse(tcell.NewEventMouse(4, pzx0OptionRowY, tcell.WheelUp, tcell.ModNone))
	time.Sleep(150 * time.Millisecond)

	if got := r.childSaw(); got == "" {
		t.Errorf("the wheel over an unfocused alt-screen pane reached the child with nothing. " +
			"focus-first governs CLICKS; scrolling to read a pane you have not focused is exactly " +
			"the v2.5.1 behaviour it must not cost.")
	}
}
