package tui

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

func (t *TUI) handleKey(ev *tcell.EventKey) bool {
	// Event log modal intercepts all input when active.
	if t.eventLogM.active {
		return t.handleEventLogKey(ev)
	}

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
				// Overlay toggle is deliberately not persisted. It's a
				// lightweight view preference (like scrollback position),
				// not a structural layout change. Always starts visible.
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
	// Confirmation state: pending destructive command waiting for Enter.
	if t.cmd.pendingConfirm != "" {
		// Auto-expire if the operator walks away.
		if time.Now().After(t.cmd.confirmExpiry) {
			t.cmd.pendingConfirm = ""
			t.cmd.confirmMsg = ""
			t.cmd.active = false
			return false
		}
		switch ev.Key() {
		case tcell.KeyEnter:
			return t.executeConfirmed()
		case tcell.KeyEscape, tcell.KeyCtrlC:
			t.cmd.pendingConfirm = ""
			t.cmd.confirmMsg = ""
			t.cmd.active = false
			t.cmd.buf = t.cmd.buf[:0]
			return false
		default:
			// Any other key cancels the confirmation.
			t.cmd.pendingConfirm = ""
			t.cmd.confirmMsg = ""
			t.cmd.buf = t.cmd.buf[:0]
			return false
		}
	}

	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.cmd.active = false
		t.cmd.buf = t.cmd.buf[:0]
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		return false
	case tcell.KeyEnter:
		cmd := strings.TrimSpace(string(t.cmd.buf))
		t.cmd.active = false
		t.cmd.buf = t.cmd.buf[:0]
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		return t.execCmd(cmd)
	case tcell.KeyTab:
		t.tabComplete()
		return false
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(t.cmd.buf) > 0 {
			t.cmd.buf = t.cmd.buf[:len(t.cmd.buf)-1]
		}
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		return false
	case tcell.KeyRune:
		// Backtick while empty closes the modal.
		if ev.Rune() == '`' && len(t.cmd.buf) == 0 {
			t.cmd.active = false
			t.cmd.tabBuf = ""
			t.cmd.tabHint = ""
			return false
		}
		t.cmd.buf = append(t.cmd.buf, ev.Rune())
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		return false
	}
	return false
}

// executeConfirmed executes a confirmed destructive command. Called after the
// operator presses Enter a second time to confirm a pending destructive action.
func (t *TUI) executeConfirmed() bool {
	pending := t.cmd.pendingConfirm
	t.cmd.pendingConfirm = ""
	t.cmd.confirmMsg = ""
	t.cmd.active = false
	t.cmd.buf = t.cmd.buf[:0]

	parts := strings.Fields(pending)
	if len(parts) == 0 {
		return false
	}
	switch parts[0] {
	case "quit":
		return true
	case "remove", "rm":
		if len(parts) >= 2 {
			if err := t.removePane(parts[1]); err != nil {
				t.cmd.error = "remove: " + err.Error()
			}
		}
	case "restart":
		if len(parts) >= 2 {
			if err := t.restartByName(parts[1]); err != nil {
				t.cmd.error = fmt.Sprintf("restart failed: %v", err)
			}
		}
	}
	return false
}

// tabComplete handles Tab keypresses in the command modal, completing agent
// names in-place. Single match: complete + trailing space. Multiple matches:
// complete to longest common prefix; second Tab shows all matches as a hint.
func (t *TUI) tabComplete() {
	buf := string(t.cmd.buf)
	parts := strings.Fields(buf)
	trailingSpace := len(buf) > 0 && buf[len(buf)-1] == ' '

	if len(parts) == 0 {
		return
	}

	cmd := parts[0]

	// Only agent-name commands are tab-completed.
	switch cmd {
	case "focus", "hide", "show", "view", "remove", "rm", "restart", "r":
		// Fall through to completion logic.
	default:
		return
	}

	// Determine the partial argument being completed.
	var partial string
	if trailingSpace {
		partial = ""
	} else if len(parts) > 1 {
		partial = parts[len(parts)-1]
	} else {
		// Only the command is typed with no trailing space; nothing to complete yet.
		return
	}

	candidates := t.completionCandidates(cmd)

	var matches []string
	for _, c := range candidates {
		if strings.HasPrefix(c, partial) {
			matches = append(matches, c)
		}
	}

	if len(matches) == 0 {
		return
	}

	// Build the prefix up to (but not including) the partial argument.
	var argPrefix string
	if trailingSpace {
		argPrefix = buf
	} else if len(parts) > 1 {
		argPrefix = strings.Join(parts[:len(parts)-1], " ") + " "
	}

	if len(matches) == 1 {
		// Unambiguous: complete with trailing space.
		t.cmd.buf = []rune(argPrefix + matches[0] + " ")
		t.cmd.tabBuf = ""
		t.cmd.tabHint = ""
		return
	}

	// Multiple matches: complete to longest common prefix.
	lcp := longestCommonPrefix(matches)

	if lcp != partial {
		// Advance the partial to the LCP.
		t.cmd.buf = []rune(argPrefix + lcp)
		t.cmd.tabBuf = string(t.cmd.buf)
		t.cmd.tabHint = ""
		return
	}

	// LCP equals the partial: we're at maximum prefix. Show or refresh the hint.
	t.cmd.tabBuf = buf
	t.cmd.tabHint = strings.Join(matches, "  ")
}

// completionCandidates returns the agent names that are valid completions for
// the given command keyword.
func (t *TUI) completionCandidates(cmd string) []string {
	switch cmd {
	case "show":
		// Hidden panes plus the special "all" keyword.
		var names []string
		for _, p := range t.panes {
			if t.layoutState.Hidden[p.name] {
				names = append(names, p.name)
			}
		}
		names = append(names, "all")
		return names
	case "hide":
		// Visible panes only.
		var names []string
		for _, p := range t.panes {
			if !t.layoutState.Hidden[p.name] {
				names = append(names, p.name)
			}
		}
		return names
	default:
		// All pane names.
		names := make([]string, len(t.panes))
		for i, p := range t.panes {
			names[i] = p.name
		}
		return names
	}
}

// longestCommonPrefix returns the longest string that is a prefix of every
// element in strs. Returns "" for an empty slice.
func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	prefix := strs[0]
	for _, s := range strs[1:] {
		for !strings.HasPrefix(s, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
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
		// Overlay toggle is deliberately not persisted (see Alt+s comment).
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
			// Delete the saved layout file and revert to defaults.
			// We deliberately don't call saveLayoutIfConfigured() here:
			// the intent is to remove persistence so the next startup
			// auto-calculates from the role count. Re-saving would
			// immediately recreate the file with default values.
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
		if len(parts) >= 2 && parts[1] != "" {
			// Named restart requires confirmation: operator must press Enter again.
			name := parts[1]
			if t.findPaneByName(name) == nil {
				t.cmd.error = fmt.Sprintf("restart: unknown agent %q", name)
				return false
			}
			t.cmd.pendingConfirm = "restart " + name
			t.cmd.confirmMsg = fmt.Sprintf("Restart %s? Context window will be lost. Enter to confirm, Esc to cancel.", name)
			t.cmd.confirmExpiry = time.Now().Add(3 * time.Second)
			t.cmd.active = true
		} else {
			// No-arg restart: restart focused pane immediately (less dangerous).
			if err := t.restartFocused(); err != nil {
				t.cmd.error = fmt.Sprintf("restart failed: %v", err)
			}
		}

	case "patrol":
		// Build patrol output and copy to clipboard for easy pasting.
		var buf strings.Builder
		for _, p := range t.panes {
			header := p.name + " (" + p.Activity().String()
			if bead := p.BeadID(); bead != "" {
				header += " | " + bead
			}
			header += ")"
			buf.WriteString("=== " + header + " ===\n")
			content := strings.TrimRight(peekContent(p, 20), "\n")
			if content == "" {
				buf.WriteString("[no recent output]\n")
			} else {
				buf.WriteString(content + "\n")
			}
			buf.WriteByte('\n')
		}
		// Copy to clipboard.
		clip := exec.Command("pbcopy")
		clip.Stdin = strings.NewReader(buf.String())
		if err := clip.Run(); err == nil {
			t.cmd.error = "patrol: copied to clipboard"
		} else {
			t.cmd.error = "patrol: " + err.Error()
		}

	case "top", "ps":
		t.cmd.error = "" // Clear stale errors so they don't reappear on close.
		t.top.active = true
		t.top.selected = 0
		t.top.cacheTime = time.Time{} // Force fresh data.

	case "add":
		if len(parts) < 2 || parts[1] == "" {
			t.cmd.error = "usage: add <name>"
		} else if err := t.addPane(parts[1]); err != nil {
			t.cmd.error = "add: " + err.Error()
		} else {
			t.cmd.error = "added " + parts[1]
		}

	case "remove", "rm":
		if len(parts) < 2 || parts[1] == "" {
			t.cmd.error = "usage: remove <name>"
		} else if t.findPaneByName(parts[1]) == nil {
			t.cmd.error = fmt.Sprintf("remove: unknown agent %q", parts[1])
		} else {
			name := parts[1]
			t.cmd.pendingConfirm = "remove " + name
			t.cmd.confirmMsg = fmt.Sprintf("Remove %s? This kills the process. Enter to confirm, Esc to cancel.", name)
			t.cmd.confirmExpiry = time.Now().Add(3 * time.Second)
			t.cmd.active = true
		}

	case "log", "events":
		t.cmd.error = ""
		t.eventLogM.active = true
		t.eventLogM.scrollOffset = 0 // Start at the bottom (latest events).

	case "quit", "q":
		t.cmd.pendingConfirm = "quit"
		t.cmd.confirmMsg = "Quit will stop all agents. Enter to confirm, Esc to cancel."
		t.cmd.confirmExpiry = time.Now().Add(3 * time.Second)
		t.cmd.active = true

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
	return t.restartPane(fp)
}

// restartByName finds the named pane and restarts it.
func (t *TUI) restartByName(name string) error {
	p := t.findPaneByName(name)
	if p == nil {
		return fmt.Errorf("unknown agent %q", name)
	}
	return t.restartPane(p)
}

// restartPane kills the given pane's process and starts a new one at the same
// index in the pane list.
func (t *TUI) restartPane(fp *Pane) error {
	idx := -1
	for i, p := range t.panes {
		if p == fp {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("pane not found")
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
	p.eventCh = t.agentEvents
	p.safeGo = t.safeGo
	p.Start()
	t.panes[idx] = p
	t.applyLayout()
	return nil
}
