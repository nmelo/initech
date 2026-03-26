package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

func (t *TUI) handleKey(ev *tcell.EventKey) bool {
	// Top modal intercepts all input when active.
	if t.top.active {
		return t.handleTopKey(ev)
	}

	// Command modal intercepts all input when active.
	if t.cmd.active {
		return t.handleCmdKey(ev)
	}

	// Clear any lingering error message on next keypress.
	t.cmd.error = ""

	// Backtick opens the command modal.
	if ev.Key() == tcell.KeyRune && ev.Rune() == '`' && ev.Modifiers() == 0 {
		t.cmd.active = true
		t.cmd.buf = t.cmd.buf[:0]
		t.cmd.error = ""
		return false
	}

	// Alt-key combos are TUI shortcuts.
	if ev.Modifiers()&tcell.ModAlt != 0 {
		switch ev.Key() {
		case tcell.KeyLeft:
			t.cycleFocus(-1)
			return false
		case tcell.KeyRight:
			t.cycleFocus(1)
			return false
		case tcell.KeyUp:
			t.cycleFocus(-1)
			return false
		case tcell.KeyDown:
			t.cycleFocus(1)
			return false
		case tcell.KeyRune:
			switch ev.Rune() {
			case '1':
				t.layoutState.Mode = LayoutFocus
				t.layoutState.Zoomed = false
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case '2':
				t.layoutState.Mode = LayoutGrid
				t.layoutState.GridCols = 2
				t.layoutState.GridRows = 2
				t.layoutState.Zoomed = false
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case '3':
				t.layoutState.Mode = LayoutGrid
				t.layoutState.GridCols = 3
				t.layoutState.GridRows = 3
				t.layoutState.Zoomed = false
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case '4':
				t.layoutState.Mode = Layout2Col
				t.layoutState.Zoomed = false
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case 's':
				t.layoutState.Overlay = !t.layoutState.Overlay
				return false
			case 'z':
				t.layoutState.Zoomed = !t.layoutState.Zoomed
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case 'q':
				return true
			}
		}
	}

	// Everything else goes to the focused pane.
	if fp := t.focusedPane(); fp != nil {
		fp.SendKey(ev)
	}
	return false
}

// handleCmdKey processes key events while the command modal is open.
func (t *TUI) handleCmdKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.cmd.active = false
		t.cmd.buf = t.cmd.buf[:0]
		return false
	case tcell.KeyEnter:
		cmd := strings.TrimSpace(string(t.cmd.buf))
		t.cmd.active = false
		t.cmd.buf = t.cmd.buf[:0]
		return t.execCmd(cmd)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(t.cmd.buf) > 0 {
			t.cmd.buf = t.cmd.buf[:len(t.cmd.buf)-1]
		}
		return false
	case tcell.KeyRune:
		// Backtick while empty closes the modal.
		if ev.Rune() == '`' && len(t.cmd.buf) == 0 {
			t.cmd.active = false
			return false
		}
		t.cmd.buf = append(t.cmd.buf, ev.Rune())
		return false
	}
	return false
}

// execCmd parses and executes a command string. Returns true if the TUI should quit.
func (t *TUI) execCmd(cmd string) bool {
	if cmd == "" {
		return false
	}

	parts := strings.Fields(cmd)
	switch parts[0] {
	case "grid":
		if len(parts) < 2 {
			visCount := t.visibleCountFromState()
			c, r := autoGrid(visCount)
			t.layoutState.Mode = LayoutGrid
			t.layoutState.GridCols = c
			t.layoutState.GridRows = r
			t.layoutState.Zoomed = false
			t.applyLayout()
			t.saveLayoutIfConfigured()
			return false
		}
		visCount := t.visibleCountFromState()
		cols, rows, ok := parseGrid(parts[1], visCount)
		if !ok {
			t.cmd.error = fmt.Sprintf("invalid grid %q, use CxR or just C (e.g. 3x3, 4)", parts[1])
			return false
		}
		t.layoutState.Mode = LayoutGrid
		t.layoutState.GridCols = cols
		t.layoutState.GridRows = rows
		t.layoutState.Zoomed = false
		t.applyLayout()
		t.saveLayoutIfConfigured()

	case "focus":
		if len(parts) < 2 {
			t.layoutState.Mode = LayoutFocus
			t.layoutState.Zoomed = false
			t.applyLayout()
			t.saveLayoutIfConfigured()
			return false
		}
		name := parts[1]
		if t.findPaneByName(name) == nil {
			t.cmd.error = fmt.Sprintf("unknown agent %q", name)
			return false
		}
		t.layoutState.Focused = name
		t.layoutState.Mode = LayoutFocus
		t.layoutState.Zoomed = false
		t.applyLayout()
		t.saveLayoutIfConfigured()

	case "zoom":
		t.layoutState.Zoomed = !t.layoutState.Zoomed
		t.applyLayout()
		t.saveLayoutIfConfigured()

	case "panel":
		t.layoutState.Overlay = !t.layoutState.Overlay

	case "main":
		t.layoutState.Mode = Layout2Col
		t.layoutState.Zoomed = false
		t.applyLayout()
		t.saveLayoutIfConfigured()

	case "show":
		if len(parts) < 2 {
			t.cmd.error = "usage: show <name> or show all"
			return false
		}
		if parts[1] == "all" {
			t.layoutState.Hidden = make(map[string]bool)
			t.autoRecalcGrid()
			t.saveLayoutIfConfigured()
			return false
		}
		if t.findPaneByName(parts[1]) == nil {
			t.cmd.error = fmt.Sprintf("unknown agent %q", parts[1])
			return false
		}
		delete(t.layoutState.Hidden, parts[1])
		t.autoRecalcGrid()
		t.saveLayoutIfConfigured()

	case "hide":
		if len(parts) < 2 {
			t.cmd.error = "usage: hide <name>"
			return false
		}
		if parts[1] == "all" {
			t.cmd.error = "cannot hide all panes"
			return false
		}
		if t.findPaneByName(parts[1]) == nil {
			t.cmd.error = fmt.Sprintf("unknown agent %q", parts[1])
			return false
		}
		if t.layoutState.Hidden[parts[1]] {
			return false // Already hidden.
		}
		if t.visibleCountFromState() <= 1 {
			t.cmd.error = "cannot hide last visible pane"
			return false
		}
		if t.layoutState.Hidden == nil {
			t.layoutState.Hidden = make(map[string]bool)
		}
		t.layoutState.Hidden[parts[1]] = true
		t.autoRecalcGrid()
		t.saveLayoutIfConfigured()

	case "view":
		if len(parts) < 2 {
			t.cmd.error = "usage: view <name1> [name2] ..."
			return false
		}
		for _, name := range parts[1:] {
			if t.findPaneByName(name) == nil {
				t.cmd.error = fmt.Sprintf("unknown agent %q", name)
				return false
			}
		}
		show := make(map[string]bool, len(parts)-1)
		for _, name := range parts[1:] {
			show[name] = true
		}
		// Check that at least one pane will be visible.
		visCount := 0
		for _, p := range t.panes {
			if show[p.name] {
				visCount++
			}
		}
		if visCount == 0 {
			t.cmd.error = "view must include at least one valid pane"
			return false
		}
		hidden := make(map[string]bool)
		for _, p := range t.panes {
			if !show[p.name] {
				hidden[p.name] = true
			}
		}
		t.layoutState.Hidden = hidden
		t.autoRecalcGrid()
		t.saveLayoutIfConfigured()

	case "layout":
		if len(parts) < 2 {
			t.cmd.error = "usage: layout reset"
			return false
		}
		switch parts[1] {
		case "reset":
			if t.projectRoot != "" {
				DeleteLayout(t.projectRoot)
			}
			names := make([]string, len(t.panes))
			for i, p := range t.panes {
				names[i] = p.name
			}
			t.layoutState = DefaultLayoutState(names)
			t.applyLayout()
		default:
			t.cmd.error = fmt.Sprintf("unknown layout subcommand %q", parts[1])
		}

	case "restart", "r":
		if err := t.restartFocused(); err != nil {
			t.cmd.error = fmt.Sprintf("restart failed: %v", err)
		}

	case "top", "ps":
		t.top.active = true
		t.top.selected = 0
		t.top.cacheTime = time.Time{} // Force fresh data.

	case "quit", "q":
		return true

	default:
		t.cmd.error = fmt.Sprintf("unknown command %q", parts[0])
	}
	return false
}

// parseGrid parses "CxR" or just "C" (auto-calculating rows from numPanes).
func parseGrid(s string, numPanes int) (cols, rows int, ok bool) {
	s = strings.ToLower(s)
	if strings.Contains(s, "x") {
		parts := strings.SplitN(s, "x", 2)
		if len(parts) != 2 {
			return 0, 0, false
		}
		c, err1 := strconv.Atoi(parts[0])
		r, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || c < 1 || r < 1 || c > 10 || r > 10 {
			return 0, 0, false
		}
		return c, r, true
	}
	// Just a column count; auto-calculate rows.
	c, err := strconv.Atoi(s)
	if err != nil || c < 1 || c > 10 {
		return 0, 0, false
	}
	r := (numPanes + c - 1) / c
	if r < 1 {
		r = 1
	}
	return c, r, true
}

// findPaneByName returns the pane with the given name, or nil.
func (t *TUI) findPaneByName(name string) *Pane {
	for _, p := range t.panes {
		if p.name == name {
			return p
		}
	}
	return nil
}

// visibleCountFromState returns the number of visible panes based on layoutState.
func (t *TUI) visibleCountFromState() int {
	n := 0
	for _, p := range t.panes {
		if !t.layoutState.Hidden[p.name] {
			n++
		}
	}
	return n
}

// autoRecalcGrid recalculates grid dimensions for the current visible count
// and applies the layout.
func (t *TUI) autoRecalcGrid() {
	if t.layoutState.Mode == LayoutGrid {
		c, r := autoGrid(t.visibleCountFromState())
		t.layoutState.GridCols = c
		t.layoutState.GridRows = r
	}
	t.applyLayout()
}

func (t *TUI) handleResize() {
	t.screen.Sync()
	t.applyLayout()
}

func (t *TUI) cycleFocus(delta int) {
	n := len(t.panes)
	if n == 0 {
		return
	}
	// Find current focused index.
	cur := 0
	for i, p := range t.panes {
		if p.name == t.layoutState.Focused {
			cur = i
			break
		}
	}
	// Skip hidden panes. Try every pane at most once.
	next := cur
	for i := 0; i < n; i++ {
		next = (next + delta + n) % n
		if !t.layoutState.Hidden[t.panes[next].name] {
			t.layoutState.Focused = t.panes[next].name
			t.applyLayout()
			return
		}
	}
}


// restartFocused kills the focused pane's process and starts a new one.
func (t *TUI) restartFocused() error {
	fp := t.focusedPane()
	if fp == nil {
		return fmt.Errorf("no pane focused")
	}
	idx := -1
	for i, p := range t.panes {
		if p == fp {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("focused pane not found")
	}
	cols := fp.emu.Width()
	rows := fp.emu.Height()
	if cols < 10 {
		cols = 80
	}
	if rows < 2 {
		rows = 24
	}
	fp.Close()

	p, err := NewPane(fp.cfg, rows, cols)
	if err != nil {
		return err
	}
	t.panes[idx] = p
	t.applyLayout()
	return nil
}
