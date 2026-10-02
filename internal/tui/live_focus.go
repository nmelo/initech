// live_focus.go implements Option+Shift+F, the live focus split (ini-92wm,
// pm/specs/live-focus-split.md option A): the focused pane held in the Focus
// split's left column, and on the right only the agents working right now,
// updating by itself as they start and stop.
//
// It composes two things that already exist rather than adding a mode:
// the Focus split's Layout2Col (focus_split.go) supplies the layout, and a
// LiveEngine in auto mode supplies who is on the right, with live's own hold
// and one-change-per-tick rules. The only thing new is the engine's Eligible
// predicate, set here to the overlay dot's "running" signal.
package tui

import "time"

// liveFocusState is the session-only state of an active live focus split.
// Deliberately not in LayoutState, for the reason focusSplitSnapshot gives:
// LayoutState persists and this mode never does.
type liveFocusState struct {
	// prev is the layout to restore on exit — the layout active before the
	// operator entered, or before Option+f when entered from a Focus split.
	prev focusSplitSnapshot
	// engine decides who is on the right. Its own instance, so the live
	// mode engine (t.liveEngine) is left dormant and resumes untouched when
	// the operator exits back into Live.
	engine *LiveEngine
}

// liveFocusNoneWorking is drawn in the empty right region (rule 8). Never a
// blank region: a blank 60% with no explanation reads as a broken layout.
const liveFocusNoneWorking = "no agent is working"

// paneIsWorking is the right grid's membership test: the activity state the
// overlay dot shows. One definition of "working" in the product, not two.
func paneIsWorking(p PaneView) bool { return p.Activity() == StateRunning }

// toggleLiveFocusSplit implements Option+Shift+F.
//
// Entry, by what is active now:
//   - Live focus split: exit, restoring the snapshot.
//   - Focus split (Option+f): the right grid becomes live and the left pane
//     stays; the Focus split's own snapshot is adopted, so exit restores the
//     layout from before Option+f.
//   - Live: snapshot the live layout; t.liveEngine stays as it is (dormant,
//     exactly as Option+f leaves it), so exit resumes live with no re-init.
//   - Anything else: snapshot it, as Option+f does.
//
// A one-pane fleet is a no-op, as Option+f is.
func (t *TUI) toggleLiveFocusSplit() {
	if t.liveFocus != nil {
		t.exitLiveFocusSplit()
		return
	}
	if t.visibleCountFromState() <= 1 {
		return
	}
	var prev focusSplitSnapshot
	if t.layoutState.Mode == Layout2Col && t.focusSplitPrev != nil {
		prev = *t.focusSplitPrev
		t.focusSplitPrev = nil
	} else {
		prev = t.layoutSnapshot()
	}
	var roles []string
	if t.project != nil {
		roles = t.project.Roles
	}
	engine := NewLiveEngine(0, nil, roles)
	engine.Eligible = paneIsWorking
	t.liveFocus = &liveFocusState{prev: prev, engine: engine}

	t.layoutState.Mode = Layout2Col
	t.layoutState.GridExplicit = false
	t.layoutState.Zoomed = false
	t.layoutState.RightSetActive = true
	t.seedLiveFocus(time.Now())
	// No save: this mode is session-only, and saveLayoutIfConfigured writes
	// the snapshot in its place should anything else save meanwhile.
	t.applyLayout()
}

// exitLiveFocusSplit restores the snapshot and drops the mode.
func (t *TUI) exitLiveFocusSplit() {
	lf := t.liveFocus
	t.clearLiveFocus()
	t.restoreLayoutSnapshot(lf.prev)
	t.applyLayout()
}

// clearLiveFocus drops the mode's state without touching the layout. Used
// by every path that leaves the mode some other way (Option+N, Shift+Option+N,
// Option+f, or anything else that changed the mode under it).
func (t *TUI) clearLiveFocus() {
	t.liveFocus = nil
	t.layoutState.RightSetActive = false
	t.layoutState.RightSet = nil
}

// freezeLiveFocusToFocusSplit implements Option+f inside the mode: drop to
// the static Focus split with the right grid frozen to the set it has now
// (spec rule 4). The snapshot moves to focusSplitPrev, so the next Option+f
// restores the layout from before the operator entered at all.
func (t *TUI) freezeLiveFocusToFocusSplit() {
	lf := t.liveFocus
	t.liveFocus = nil
	snap := lf.prev
	t.focusSplitPrev = &snap
	// RightSet and RightSetActive stay: that is the frozen set. They are
	// cleared when this Focus split exits, or when the mode changes.
	t.applyLayout()
}

// seedLiveFocus fills the right grid with the panes working at entry, each
// on a fresh hold, so entering does not fill it one pane per tick.
func (t *TUI) seedLiveFocus(now time.Time) {
	panes := t.liveFocusInputs()
	var names []string
	for _, p := range panes {
		if paneIsWorking(p) {
			names = append(names, agentKey(p))
		}
	}
	e := t.liveFocus.engine
	e.Slots = names
	e.holdUntil = make([]time.Time, len(names))
	for i := range names {
		e.holdUntil[i] = now.Add(liveHoldDuration)
	}
	t.layoutState.RightSet = e.TickAuto(panes, now)
}

// liveFocusInputs is the right grid's universe: the live tick's own input
// (this window's panes, hidden ones excluded) minus the held pane. Excluding
// a pane from the input is how live removes it at once, so a pane hidden
// mid-mode leaves on the next frame, not after the hold (rule 7).
func (t *TUI) liveFocusInputs() []PaneView {
	panes, _ := t.liveTickInputs()
	held := t.layoutState.Focused
	out := panes[:0:0]
	for _, p := range panes {
		if agentKey(p) != held {
			out = append(out, p)
		}
	}
	return out
}

// tickLiveFocus advances the right grid one engine tick. Called from
// applyLayout, which the run loop invokes every second while the mode is on.
// If the layout mode changed under the live focus split by a path that did
// not clear it, the mode is dropped here rather than left half-on.
func (t *TUI) tickLiveFocus(now time.Time) {
	if t.liveFocus == nil {
		return
	}
	if t.layoutState.Mode != Layout2Col {
		t.clearLiveFocus()
		return
	}
	t.layoutState.RightSet = t.liveFocus.engine.TickAuto(t.liveFocusInputs(), now)
}

// layoutSnapshot captures the value-typed layout fields Option+f restores.
func (t *TUI) layoutSnapshot() focusSplitSnapshot {
	return focusSplitSnapshot{
		mode:         t.layoutState.Mode,
		gridCols:     t.layoutState.GridCols,
		gridRows:     t.layoutState.GridRows,
		gridExplicit: t.layoutState.GridExplicit,
		zoomed:       t.layoutState.Zoomed,
		liveAuto:     t.layoutState.LiveAuto,
		focused:      t.layoutState.Focused,
	}
}

// restoreLayoutSnapshot writes a snapshot back into layoutState.
func (t *TUI) restoreLayoutSnapshot(prev focusSplitSnapshot) {
	t.layoutState.Mode = prev.mode
	t.layoutState.GridCols = prev.gridCols
	t.layoutState.GridRows = prev.gridRows
	t.layoutState.GridExplicit = prev.gridExplicit
	t.layoutState.Zoomed = prev.zoomed
	t.layoutState.LiveAuto = prev.liveAuto
	t.layoutState.Focused = prev.focused
}

// persistableLayout is the layout to write to layout.yaml. While the live
// focus split is on, that is the SNAPSHOT's mode and grid, not Layout2Col:
// the mode is session-only (AC 6), and gating this here means no save from
// any path — a hide, an Option+z, an order change — can write it, rather than
// relying on this file never calling save itself.
func (t *TUI) persistableLayout() LayoutState {
	ls := t.layoutState
	if t.liveFocus != nil {
		prev := t.liveFocus.prev
		ls.Mode = prev.mode
		ls.GridCols = prev.gridCols
		ls.GridRows = prev.gridRows
		ls.GridExplicit = prev.gridExplicit
		ls.LiveAuto = prev.liveAuto
	}
	return ls
}
