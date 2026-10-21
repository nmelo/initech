// live_list.go implements Option+Shift+L, the live list split (ini-3g39,
// pm/specs/live-list-split.md): the focused pane held in the Focus split's
// left column, and on the right a grid of fixed size, four boxes across by
// a row count the operator sets, holding the agents working right now in
// the Agents panel's order.
//
// It composes what the live focus split already has: the same LiveEngine in
// auto mode with the same Eligible predicate and the same inputs decides WHO
// is in. Two things are new: liveListPlace decides WHERE (panel order, pinned
// agents counted first, an agent in a box keeps it while the grid is full), and
// calcLiveListSplit lays out the boxes without looking at how many are
// filled, so no box ever changes size while the mode is on.
package tui

import (
	"fmt"
	"sort"
	"time"
)

const (
	// liveListDefaultCols is the column count until the operator sets one
	// with grid CxR, and what a saved list without list_cols means (ini-3o4b).
	liveListDefaultCols = 4
	// liveListMaxCols bounds the columns of `grid CxR`.
	liveListMaxCols = 6
	// liveListDefaultRows is the row count until the operator sets one, and
	// what a saved list without list_rows means.
	liveListDefaultRows = 3
	// liveListMaxRows bounds `grid 4xR`.
	liveListMaxRows = 6
)

// liveListState is the state of an active live list split, nil when off.
type liveListState struct {
	// prev is the layout to restore on exit: the one before the operator
	// entered, or before the first split when entered from another split.
	prev focusSplitSnapshot
	// engine decides who is in. Its own instance, configured as the live
	// focus split's is.
	engine *LiveEngine
	// shown is the right grid as last placed, in box order.
	shown []string
}

// toggleLiveListSplit implements Option+Shift+L and the `list` command.
//
// Entry, by what is active now:
//   - Live list split: exit, restoring the snapshot.
//   - Live focus split or Focus split: keep the held pane and adopt that
//     split's snapshot, so exit restores the layout from before the first
//     split.
//   - Anything else, Live included: snapshot it. t.liveEngine stays dormant,
//     as the other splits leave it.
//
// A one-pane fleet is a no-op, as for the other splits.
func (t *TUI) toggleLiveListSplit() {
	if t.liveList != nil {
		t.exitLiveListSplit()
		return
	}
	if t.visibleCountFromState() <= 1 {
		return
	}
	var prev focusSplitSnapshot
	switch {
	case t.liveFocus != nil:
		prev = t.liveFocus.prev
		t.clearLiveFocus()
	case t.layoutState.Mode == Layout2Col && t.focusSplitPrev != nil:
		prev = *t.focusSplitPrev
		t.focusSplitPrev = nil
	default:
		prev = t.layoutSnapshot()
	}
	var roles []string
	if t.project != nil {
		roles = t.project.Roles
	}
	engine := NewLiveEngine(0, nil, roles)
	engine.Eligible = paneIsWorking
	t.liveList = &liveListState{prev: prev, engine: engine}
	if t.listRows < 1 {
		t.listRows = liveListDefaultRows
	}
	if t.listCols < 1 {
		t.listCols = liveListDefaultCols
	}

	t.layoutState.Mode = Layout2Col
	t.layoutState.GridExplicit = false
	t.layoutState.Zoomed = false
	t.layoutState.RightSetActive = true
	t.layoutState.ListRows = t.listRows
	t.layoutState.ListCols = t.listCols
	t.seedLiveList(time.Now())
	t.applyLayout()
}

// exitLiveListSplit restores the snapshot and drops the mode.
func (t *TUI) exitLiveListSplit() {
	ll := t.liveList
	t.clearLiveList()
	t.restoreLayoutSnapshot(ll.prev)
	t.applyLayout()
}

// clearLiveList drops the mode's state without touching the layout. Used by
// every path that leaves the mode some other way (Option+N, Shift+Option+N,
// a mode change under it). A no-op when the mode is off, so it never clears
// the live focus split's right set.
func (t *TUI) clearLiveList() {
	if t.liveList == nil {
		return
	}
	t.liveList = nil
	t.layoutState.RightSetActive = false
	t.layoutState.RightSet = nil
	t.layoutState.ListRows = 0
	t.layoutState.ListCols = 0
}

// handLiveListToLiveFocus implements Option+Shift+F inside the list: the
// live focus split takes over with the same held pane and the list's
// snapshot. The snapshot is handed over as a Focus split's, which
// toggleLiveFocusSplit already adopts, so the live focus split's own code is
// not touched.
func (t *TUI) handLiveListToLiveFocus() {
	prev := t.liveList.prev
	t.clearLiveList()
	t.focusSplitPrev = &prev
	t.toggleLiveFocusSplit()
}

// seedLiveList fills the grid with the panes working at entry, each on a
// fresh hold, so entering does not fill it one box per tick. The same seed
// the live focus split does, on this mode's engine.
func (t *TUI) seedLiveList(now time.Time) {
	panes := t.liveFocusInputs()
	var names []string
	for _, p := range panes {
		if paneIsWorking(p) {
			names = append(names, agentKey(p))
		}
	}
	e := t.liveList.engine
	e.Slots = names
	e.holdUntil = make([]time.Time, len(names))
	for i := range names {
		e.holdUntil[i] = now.Add(liveHoldDuration)
	}
	_, pinned := t.liveTickInputs()
	e.Pinned = pinned
	t.placeLiveList(e.TickAuto(panes, now), pinned)
}

// tickLiveList advances the grid one engine tick, then places the result.
// Called from applyLayout, which the run loop invokes every second while the
// mode is on. A mode changed under it drops it here rather than leave it
// half-on.
func (t *TUI) tickLiveList(now time.Time) {
	if t.liveList == nil {
		return
	}
	if t.layoutState.Mode != Layout2Col {
		t.clearLiveList()
		return
	}
	// One pin store, re-derived every tick, as the live focus split does it.
	_, pinned := t.liveTickInputs()
	t.liveList.engine.Pinned = pinned
	t.placeLiveList(t.liveList.engine.TickAuto(t.liveFocusInputs(), now), pinned)
}

// placeLiveList puts the engine's set into boxes.
func (t *TUI) placeLiveList(in []string, pinned map[string]int) {
	shown := liveListPlace(in, t.agentsPanelOrder(), pinned, t.liveList.shown, t.listCols*t.listRows)
	t.liveList.shown = shown
	t.layoutState.RightSet = shown
	t.layoutState.ListRows = t.listRows
	t.layoutState.ListCols = t.listCols
}

// liveListPlace decides which of the engine's agents get a box, and in what
// order the boxes read: the Agents panel's order, pinned agents included in
// their own place (spec acceptance 3: a pinned agent "appears in its order
// position").
//
// Pinned agents are COUNTED first: they always get a box. An agent already
// in a box keeps it while the grid is full; a newcomer waits until a box
// frees, and then the first waiting agent in panel order takes it (spec: "the
// rest wait and come in as boxes free up"). When pins, or fewer rows, leave
// more agents than boxes, the unpinned agents last in the order wait. The
// engine decides who is in, never where: its own order is not used.
func liveListPlace(in, panelOrder []string, pinned map[string]int, prev []string, boxes int) []string {
	rank := make(map[string]int, len(panelOrder))
	for i, k := range panelOrder {
		if _, dup := rank[k]; !dup {
			rank[k] = i
		}
	}
	// An agent the panel does not list sorts after every one it does, in the
	// engine's order: a stable fallback, never a crash.
	for i, k := range in {
		if _, ok := rank[k]; !ok {
			rank[k] = len(panelOrder) + i
		}
	}
	isPinned := func(k string) bool { _, ok := pinned[k]; return ok }
	// byPanel is the reading order of the boxes.
	byPanel := func(s []string) {
		sort.SliceStable(s, func(i, j int) bool { return rank[s[i]] < rank[s[j]] })
	}
	// byPriority is who keeps a box when there are too few: pinned first.
	byPriority := func(s []string) {
		sort.SliceStable(s, func(i, j int) bool {
			if pi, pj := isPinned(s[i]), isPinned(s[j]); pi != pj {
				return pi
			}
			return rank[s[i]] < rank[s[j]]
		})
	}
	candidates := append([]string(nil), in...)
	byPriority(candidates)
	inSet := make(map[string]bool, len(in))
	for _, k := range in {
		inSet[k] = true
	}

	shown := make([]string, 0, boxes)
	taken := make(map[string]bool, boxes)
	add := func(k string) {
		if !taken[k] {
			taken[k] = true
			shown = append(shown, k)
		}
	}
	for _, k := range prev {
		if inSet[k] {
			add(k)
		}
	}
	for _, k := range candidates {
		if isPinned(k) {
			add(k)
		}
	}
	byPriority(shown)
	if len(shown) > boxes {
		for _, k := range shown[boxes:] {
			delete(taken, k)
		}
		shown = shown[:boxes]
	}
	for _, k := range candidates {
		if len(shown) >= boxes {
			break
		}
		add(k)
	}
	byPanel(shown)
	return shown
}

// agentsPanelOrder is the Agents panel's order of this fleet's agent keys:
// band by band, each band's members in pane order. Read off the panel's own
// walk (agentsGridWalk, one band per row), so the list cannot read in an
// order the panel does not show.
func (t *TUI) agentsPanelOrder() []string {
	t.ensureGroups(false)
	tiersActive := t.agentsTiersActive()
	// The assignment store is read only when tiers draw: agentsAssignment
	// caches it on the TUI, and a single-window session keeps none.
	var assign *WindowAssignment
	if tiersActive {
		assign = t.agentsAssignment()
	}
	geo := agentsGridWalk(t.agentsGroupMembers(), t.agentsTierGroups(assign, tiersActive), tiersActive, 0, 0, 1)
	out := make([]string, 0, len(geo.cells))
	for _, c := range geo.cells {
		if c.paneIdx >= 0 && c.paneIdx < len(t.panes) {
			out = append(out, agentKey(t.panes[c.paneIdx]))
		}
	}
	return out
}

// calcLiveListSplit lays out the live list split: the Focus split's 40% left
// column, and on the right a fixed cols x rows grid. It returns 1+cols*rows
// regions whatever the number of agents: region 0 is the held pane's, the
// rest are the boxes in reading order. It deliberately does not take the
// count, so no number of agents can resize a box.
func calcLiveListSplit(cols, rows, screenW, screenH int) []Region {
	if rows < 1 {
		rows = liveListDefaultRows
	}
	if cols < 1 {
		cols = liveListDefaultCols
	}
	leftW := screenW * 40 / 100
	leftOwnedW := leftW - 1 // the gutter column calcMainVertical reserves
	if leftOwnedW < 1 {
		leftOwnedW = 1
	}
	boxes := cols * rows
	regions := make([]Region, 0, 1+boxes)
	regions = append(regions, Region{X: 0, Y: 0, W: leftOwnedW, H: screenH})
	for _, r := range gridRegions(cols, rows, boxes, screenW-leftW, screenH, nil, nil) {
		r.X += leftW
		regions = append(regions, r)
	}
	return regions
}

// cmdListGrid is `grid CxR` inside the live list split: C columns and R rows,
// each 1 to 6 (ini-3o4b). Anything else leaves the grid as it is and says
// why. The shape is kept for the session and saved with the split.
func (t *TUI) cmdListGrid(arg string) bool {
	var cols, rows int
	var rest string
	n, _ := fmt.Sscanf(arg+" end", "%dx%d %s", &cols, &rows, &rest)
	if n != 3 || rest != "end" || cols < 1 || cols > liveListMaxCols || rows < 1 || rows > liveListMaxRows {
		t.cmd.error = fmt.Sprintf("invalid grid %q for the live list: use CxR with C and R from 1 to %d", arg, liveListMaxRows)
		return false
	}
	t.listCols, t.listRows = cols, rows
	t.layoutState.ListCols, t.layoutState.ListRows = cols, rows
	t.applyLayout()
	t.saveLayoutIfConfigured()
	return false
}
