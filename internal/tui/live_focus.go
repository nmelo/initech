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

// liveFocusState is the state of an active live focus split. Not in
// LayoutState, for the reason focusSplitSnapshot gives; what reaches
// layout.yaml is derived from it by persistableLayout (ini-aa8a).
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
	// No save here: the main loop's saveIfSplitMoved writes the split, as it
	// does for its exit and its drop to the Focus split.
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
//
// A pinned agent that is suspended is left out too (ini-x0x4): a pin holds a
// tile for an agent that can act, and a suspended one shows a frozen pane.
// Leaving it out of the input, rather than unpinning it in the engine, is
// what makes it leave on the next tick (the engine drops a missing pane at
// once, past hold and one-change-per-tick) and come back on the tick after
// it wakes (the pinned add path). The pin store is never touched, and plain
// live mode reads liveTickInputs, so its fixed pinned slots are unchanged.
func (t *TUI) liveFocusInputs() []PaneView {
	panes, pinned := t.liveTickInputs()
	held := t.layoutState.Focused
	out := panes[:0:0]
	for _, p := range panes {
		k := agentKey(p)
		if k == held {
			continue
		}
		if _, isPinned := pinned[k]; isPinned && p.IsSuspended() {
			continue
		}
		out = append(out, p)
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
	t.syncLiveFocusPins()
	t.layoutState.RightSet = t.liveFocus.engine.TickAuto(t.liveFocusInputs(), now)
}

// syncLiveFocusPins hands the split's engine the live pins (ini-sbhq).
//
// One store, re-derived every tick the way live mode does it (applyLayout,
// tui.go): the global LivePinned intersected with this window. So a pin from
// the panel, from the pin command, or carried in from live mode all reach
// the right grid with no wiring of their own. Entry needs no call of its own:
// toggleLiveFocusSplit ends in applyLayout, whose tick lands here before the
// first frame (a sync in seedLiveFocus was an equivalent mutant -- removing
// it changed nothing -- so it is not there). TickAuto then treats a pinned agent as always wanted and
// adds it at once, whatever its state -- except suspended, which
// liveFocusInputs leaves out of the input (ini-x0x4).
//
// The focused pane and hidden agents need no rule here: liveFocusInputs
// leaves both out of the engine's input, and the engine only places agents
// it is given -- so a focused pinned agent stays left and a hidden one is not
// drawn.
func (t *TUI) syncLiveFocusPins() {
	_, pinned := t.liveTickInputs()
	t.liveFocus.engine.Pinned = pinned
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

// persistableLayout is the layout to write to layout.yaml. While a split is
// on, mode and grid are the layout UNDERNEATH it (its snapshot, the one its
// second press restores), never Layout2Col, and Split and Held name the split
// and the pane on its left, so a restart puts the split back over that layout
// (ini-aa8a). Every save goes through here, so a hide, an Option+z or an order
// change writes the same thing. The :main preset is Layout2Col with no
// snapshot and is written as itself: mode main, no split.
func (t *TUI) persistableLayout() LayoutState {
	ls := t.layoutState
	ls.Split, ls.Held = "", ""
	var under *focusSplitSnapshot
	switch {
	case t.liveFocus != nil:
		under, ls.Split = &t.liveFocus.prev, splitLive
	case ls.Mode == Layout2Col && t.focusSplitPrev != nil:
		under, ls.Split = t.focusSplitPrev, splitFocus
	}
	if under != nil {
		ls.Held = ls.Focused
		ls.Mode = under.mode
		ls.GridCols = under.gridCols
		ls.GridRows = under.gridRows
		ls.GridExplicit = under.gridExplicit
		ls.LiveAuto = under.liveAuto
	}
	return ls
}

// restoreSavedSplit puts back the split layout.yaml says this window was in
// (ini-aa8a). LoadLayout has already applied the layout underneath; this
// presses the same toggle the key does, with the saved pane held on the left,
// so the live split seeds its right grid from the agents working now and the
// Focus split shows every other pane. The held pane must be one of this
// window's panes, matched by its exact key, and not hidden; a suspended pane
// is still a pane. Anything else leaves the layout underneath standing, with
// one WARN naming the agent: never a split with an empty left column.
func (t *TUI) restoreSavedSplit(split, held string) {
	if split == "" {
		return
	}
	why := ""
	switch {
	case split != splitLive && split != splitFocus:
		why = "unknown split"
	case held == "":
		why = "no held agent saved"
	case !t.hasPaneKey(held):
		why = "the held agent is not in this window"
	case t.layoutState.Hidden[held]:
		why = "the held agent is hidden"
	case split == splitFocus && t.layoutState.Mode == Layout2Col:
		why = "the layout underneath is already two columns"
	case t.visibleCountFromState() <= 1:
		why = "only one agent is visible"
	}
	if why != "" {
		LogWarn("layout", "saved split not restored; opening the layout underneath",
			"split", split, "held", held, "why", why)
		return
	}
	t.layoutState.Focused = held
	if split == splitLive {
		t.toggleLiveFocusSplit()
	} else {
		t.toggleFocusSplit()
	}
	LogInfo("layout", "saved split restored", "split", split, "held", held)
}

// hasPaneKey reports whether this window has a pane with exactly this key.
func (t *TUI) hasPaneKey(key string) bool {
	for _, p := range t.panes {
		if agentKey(p) == key {
			return true
		}
	}
	return false
}
