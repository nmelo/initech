// agents.go implements the agent management modal: a grouped 2-D grid
// (ini-2rc) laid out as function bands (core/eng/qa/...), replacing the
// earlier flat list. Rendered as a centered, content-sized floating box over
// the live TUI. Opened via backtick+agents command or Alt+a shortcut.
//
// Actions apply immediately and persist through the layout save path.
// Keybindings: arrows (move/grab), Space (toggle visibility), Enter
// (grab/drop for reorder, including across groups), p (toggle pin),
// P (toggle protect), / (search), g (create group), A (reveal all),
// R (reset to config order).
//
// Grid layout, navigation, search, and group data live in agents_grid.go;
// this file owns key routing and the action helpers that mutate pane/layout
// state (shared with whatever came before this bead's grid rework).
package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

// openAgentsModal initializes and opens the agent management modal.
func (t *TUI) openAgentsModal() {
	t.agents.active = true
	t.agents.selected = 0
	t.agents.moving = false
	t.agents.error = ""
	t.agents.searching = false
	t.agents.searchBuf = nil
	t.agents.creatingGroup = false
	t.agents.groupNameBuf = nil
	t.ensureGroups(true)
}

// closeAgentsModal exits the modal, pruning any band left empty by a grab
// (spec: "a group empty when the modal closes is removed").
func (t *TUI) closeAgentsModal() {
	t.agents.moving = false
	t.agents.searching = false
	t.agents.searchBuf = nil
	t.agents.creatingGroup = false
	t.agents.groupNameBuf = nil
	t.agents.active = false
	t.agentsPruneEmptyGroups()
}

// handleAgentsKey processes key events while the agents modal is open.
func (t *TUI) handleAgentsKey(ev *tcell.EventKey) bool {
	// Alt+a toggles the modal closed.
	if ev.Modifiers()&tcell.ModAlt != 0 && ev.Key() == tcell.KeyRune && ev.Rune() == 'a' {
		t.closeAgentsModal()
		return false
	}

	n := len(t.panes)
	if n == 0 {
		t.agents.active = false
		return false
	}

	// Clear stale error on any keypress.
	t.agents.error = ""

	if t.agents.creatingGroup {
		return t.handleAgentsGroupNameKey(ev)
	}
	if t.agents.searching {
		return t.handleAgentsSearchKey(ev)
	}

	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		if t.agents.moving {
			t.agents.moving = false
			return false
		}
		t.closeAgentsModal()
		return false

	case tcell.KeyEnter:
		t.agents.moving = !t.agents.moving
		return false

	case tcell.KeyLeft:
		t.agentsMoveH(-1)
		return false

	case tcell.KeyRight:
		t.agentsMoveH(1)
		return false

	case tcell.KeyUp:
		t.agentsMoveV(t.agentsCurrentCells(), -1)
		return false

	case tcell.KeyDown:
		t.agentsMoveV(t.agentsCurrentCells(), 1)
		return false

	case tcell.KeyRune:
		switch ev.Rune() {
		case '/':
			t.agents.searching = true
			t.agents.searchBuf = nil
			t.agents.moving = false
			t.agents.preSearchSelected = t.agents.selected
			return false
		case 'g':
			t.agents.creatingGroup = true
			t.agents.groupNameBuf = nil
			t.agents.moving = false
			return false
		case 'q', '`':
			t.closeAgentsModal()
			return false
		case ' ':
			t.agentsToggleVisibility()
			return false
		case 'p':
			t.agentsToggleLivePin()
			return false
		case 'P':
			t.agentsToggleProtected()
			return false
		case 'A':
			t.agentsRevealAll()
			return false
		case 'R':
			t.agentsResetOrder()
			return false
		default:
			// Number keys 0-9: pin selected agent to that live slot.
			if t.layoutState.Mode == LayoutLive && ev.Rune() >= '0' && ev.Rune() <= '9' {
				t.agentsLivePin(int(ev.Rune() - '0'))
				return false
			}
		}
	}
	return false
}

// agentsCurrentCells computes the grid cell layout for the current screen
// size, using agentsGridBoxDims -- the SAME box-geometry function
// renderAgentsGrid calls, not a separately hand-derived approximation. Two
// independently-computed formulas for "where is this cell" is exactly the
// kind of drift that produced a real bug during this bead's own
// development (moveV's nearest-column x-distance disagreeing with what was
// actually on screen); sharing one function eliminates the drift by
// construction. Called fresh on every navigation keypress -- never cached --
// so a resize or a group edit between renders can't leave navigation
// reading a stale layout (the same discipline the search no-matches fix
// requires).
func (t *TUI) agentsCurrentCells() []gridCell {
	sw, sh := t.screen.Size()
	t.ensureGroups(false)
	members := t.agentsGroupMembers()
	box := agentsGridBoxDims(members, t.layoutState.Groups, sw, sh, t.agents.searching || t.agents.creatingGroup)
	return agentsGridLayoutCells(members, t.layoutState.Groups, box.innerX, box.startY+box.searchRows, box.perRow)
}

// agentsLivePin pins the selected agent to the given slot index in live mode.
func (t *TUI) agentsLivePin(slot int) {
	if t.agents.selected < 0 || t.agents.selected >= len(t.panes) {
		return
	}
	totalSlots := t.layoutState.GridCols * t.layoutState.GridRows
	if slot >= totalSlots {
		t.agents.error = fmt.Sprintf("slot %d does not exist (grid has %d slots)", slot, totalSlots)
		return
	}
	if t.layoutState.LivePinned == nil {
		t.layoutState.LivePinned = make(map[string]int)
	}
	name := paneKey(t.panes[t.agents.selected])
	// Remove any existing pin on this slot.
	for k, v := range t.layoutState.LivePinned {
		if v == slot {
			delete(t.layoutState.LivePinned, k)
		}
	}
	t.layoutState.LivePinned[name] = slot
	if t.liveEngine != nil {
		t.liveEngine.Pinned = t.layoutState.LivePinned
	}
	t.applyLayout()
	t.saveLayoutIfConfigured()
}

// handleAgentsSearchKey processes keys while in search mode. The grid DIMS
// non-matches in place rather than filtering rows out (spec: the spatial
// layout is what's being navigated, so it must not reflow under the query).
// Space (hide) is intercepted before the generic printable-rune case so it
// acts on the selection instead of typing into the query; p is NOT
// intercepted, since names contain the letter p and typing must win there.
func (t *TUI) handleAgentsSearchKey(ev *tcell.EventKey) bool {
	cells := t.agentsCurrentCells()
	switch {
	case ev.Key() == tcell.KeyEscape:
		t.agents.searching = false
		t.agents.searchBuf = nil
		t.agents.selected = t.agents.preSearchSelected // Esc restores pre-search selection.
		return false

	case ev.Key() == tcell.KeyEnter:
		t.agents.searching = false
		t.agents.searchBuf = nil // keep the selection the search reached
		return false

	case ev.Key() == tcell.KeyBackspace || ev.Key() == tcell.KeyBackspace2:
		if len(t.agents.searchBuf) == 0 {
			t.agents.searching = false
			t.agents.selected = t.agents.preSearchSelected // same as Esc from an empty query.
			return false
		}
		t.agents.searchBuf = t.agents.searchBuf[:len(t.agents.searchBuf)-1]
		t.agentsEnsureMatchSelected(cells)
		return false

	case ev.Key() == tcell.KeyLeft || ev.Key() == tcell.KeyUp:
		t.agentsMatchNav(cells, -1)
		return false

	case ev.Key() == tcell.KeyRight || ev.Key() == tcell.KeyDown:
		t.agentsMatchNav(cells, 1)
		return false

	case ev.Rune() == ' ':
		t.agentsToggleVisibility()
		return false

	case ev.Key() == tcell.KeyRune && ev.Rune() != 0 && unicode.IsPrint(ev.Rune()):
		t.agents.searchBuf = append(t.agents.searchBuf, ev.Rune())
		t.agentsEnsureMatchSelected(cells)
		return false
	}
	return false
}

// handleAgentsGroupNameKey processes keys while the 'g' create-group prompt
// is open. A blank/whitespace-only name is rejected silently on Enter
// (prompt stays open) rather than creating an unlabeled band.
func (t *TUI) handleAgentsGroupNameKey(ev *tcell.EventKey) bool {
	switch {
	case ev.Key() == tcell.KeyEscape:
		t.agents.creatingGroup = false
		t.agents.groupNameBuf = nil
		return false

	case ev.Key() == tcell.KeyEnter:
		name := strings.TrimSpace(string(t.agents.groupNameBuf))
		if name == "" {
			t.agents.error = "group name cannot be empty"
			return false
		}
		t.agentsCreateGroup(name)
		t.agents.creatingGroup = false
		t.agents.groupNameBuf = nil
		return false

	case ev.Key() == tcell.KeyBackspace || ev.Key() == tcell.KeyBackspace2:
		if len(t.agents.groupNameBuf) > 0 {
			t.agents.groupNameBuf = t.agents.groupNameBuf[:len(t.agents.groupNameBuf)-1]
		}
		return false

	case ev.Key() == tcell.KeyRune && ev.Rune() != 0 && unicode.IsPrint(ev.Rune()):
		t.agents.groupNameBuf = append(t.agents.groupNameBuf, ev.Rune())
		return false
	}
	return false
}

// agentsToggleVisibility toggles hidden state for the selected pane.
// Blocks hiding the last visible pane.
func (t *TUI) agentsToggleVisibility() {
	if t.agents.selected < 0 || t.agents.selected >= len(t.panes) {
		return
	}
	name := paneKey(t.panes[t.agents.selected])
	if !t.toggleHidden(name) {
		t.agents.error = "cannot hide last visible pane"
	}
}

// toggleHidden toggles hidden state for the named pane. Returns false if
// the toggle was blocked (hiding the last visible pane). Used by both the
// agents modal and overlay dot click.
func (t *TUI) toggleHidden(name string) bool {
	if t.layoutState.Hidden[name] {
		delete(t.layoutState.Hidden, name)
	} else {
		if t.visibleCountFromState() <= 1 {
			return false
		}
		if t.layoutState.Hidden == nil {
			t.layoutState.Hidden = make(map[string]bool)
		}
		t.layoutState.Hidden[name] = true
	}
	t.recalcGrid(false)
	t.applyLayout()
	t.saveLayoutIfConfigured()
	return true
}

// agentsToggleLivePin toggles the live mode slot pin for the selected agent.
// If the agent is already live-pinned, removes the pin. If not, pins it to
// its current slot (or the first available slot). Only active in live mode.
func (t *TUI) agentsToggleLivePin() {
	if t.layoutState.Mode != LayoutLive {
		t.agents.error = "live pin requires live mode"
		return
	}
	if t.agents.selected < 0 || t.agents.selected >= len(t.panes) {
		return
	}
	pk := paneKey(t.panes[t.agents.selected])

	if t.layoutState.LivePinned == nil {
		t.layoutState.LivePinned = make(map[string]int)
	}

	if _, pinned := t.layoutState.LivePinned[pk]; pinned {
		// Unpin: remove from LivePinned.
		delete(t.layoutState.LivePinned, pk)
	} else {
		// Pin: find current slot in LiveSlots, or use first available.
		slot := -1
		for i, sn := range t.layoutState.LiveSlots {
			if sn == pk {
				slot = i
				break
			}
		}
		if slot < 0 {
			numSlots := t.layoutState.GridCols * t.layoutState.GridRows
			occupied := make(map[int]bool, len(t.layoutState.LivePinned))
			for _, v := range t.layoutState.LivePinned {
				occupied[v] = true
			}
			for i := 0; i < numSlots; i++ {
				if !occupied[i] {
					slot = i
					break
				}
			}
			if slot < 0 {
				t.agents.error = "all live slots are pinned"
				return
			}
		}
		// Clear any existing occupant of this slot from LivePinned.
		for k, v := range t.layoutState.LivePinned {
			if v == slot {
				delete(t.layoutState.LivePinned, k)
			}
		}
		t.layoutState.LivePinned[pk] = slot
	}
	if t.liveEngine != nil {
		t.liveEngine.Pinned = t.layoutState.LivePinned
	}
	t.applyLayout()
	t.saveLayoutIfConfigured()
}

// agentsToggleProtected toggles the auto-suspend protection for the selected pane.
func (t *TUI) agentsToggleProtected() {
	if t.agents.selected < 0 || t.agents.selected >= len(t.panes) {
		return
	}
	name := paneKey(t.panes[t.agents.selected])

	if t.layoutState.Protected == nil {
		t.layoutState.Protected = make(map[string]bool)
	}
	if t.layoutState.Protected[name] {
		delete(t.layoutState.Protected, name)
		if lp, ok := t.panes[t.agents.selected].(*Pane); ok {
			lp.SetProtected(false)
		}
	} else {
		t.layoutState.Protected[name] = true
		if lp, ok := t.panes[t.agents.selected].(*Pane); ok {
			lp.SetProtected(true)
		}
	}
	t.saveLayoutIfConfigured()
}

// agentsRevealAll unhides all agents.
func (t *TUI) agentsRevealAll() {
	t.layoutState.Hidden = make(map[string]bool)
	t.recalcGrid(false)
	t.applyLayout()
	t.saveLayoutIfConfigured()
}

// agentsResetOrder resets pane order to the config-declared role order
// from initech.yaml. Falls back to current order if no config is available.
func (t *TUI) agentsResetOrder() {
	if t.project == nil || len(t.project.Roles) == 0 {
		t.agents.error = "no config role order available"
		return
	}
	t.layoutState.Order = make([]string, len(t.project.Roles))
	copy(t.layoutState.Order, t.project.Roles)
	reorderPanes(t.panes, t.layoutState.Order)
	t.applyLayout()
	t.saveLayoutIfConfigured()
	// Reset selection after reorder.
	t.agents.selected = 0
	t.agents.moving = false
}

// agentsReconcile re-derives the agents-modal selection state against the
// current t.panes. Call it whenever the pane set changes (removal, peer
// update) so a stale selected index can't crash the render loop or move
// actions (ini-w7ym / ini-t3ov). No-op when the modal is closed (state is
// reset on open).
func (t *TUI) agentsReconcile() {
	if !t.agents.active {
		return
	}
	n := len(t.panes)
	if n == 0 {
		t.agents.selected = 0
		t.agents.moving = false
		return
	}
	if t.agents.selected >= n {
		t.agents.selected = n - 1
	}
	if t.agents.selected < 0 {
		t.agents.selected = 0
	}
	t.ensureGroups(false)
}

// agentsPersistOrder snapshots the current pane order into layoutState and persists.
func (t *TUI) agentsPersistOrder() {
	order := make([]string, len(t.panes))
	for i, p := range t.panes {
		order[i] = paneKey(p)
	}
	t.layoutState.Order = order
	t.applyLayout()
	t.saveLayoutIfConfigured()
}

// renderAgents draws the centered floating agent management modal.
func (t *TUI) renderAgents() {
	t.renderAgentsGrid()
}
