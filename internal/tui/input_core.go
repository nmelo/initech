package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

func (t *TUI) handleKey(ev *tcell.EventKey) bool {
	// Welcome overlay: dismiss on any keypress.
	if t.welcome.active {
		t.welcome.active = false
		return false
	}

	// Help modal intercepts all input when active.
	if t.help.active {
		return t.handleHelpKey(ev)
	}

	// Event log modal intercepts all input when active.
	if t.eventLogM.active {
		return t.handleEventLogKey(ev)
	}

	// Top modal intercepts all input when active.
	if t.top.active {
		return t.handleTopKey(ev)
	}

	// Reorder modal intercepts all input when active.
	if t.reorder.active {
		return t.handleReorderKey(ev)
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
			case 'u':
				// Manual update check (bypass 24h cache).
				t.checkForUpdate()
				return false
			case 'q':
				// Open confirmation instead of quitting immediately.
				// Alt+q is one key away from Alt+z and Alt+s; accidental
				// presses must not silently kill all agents.
				t.cmd.active = true
				t.cmd.pendingConfirm = "quit"
				t.cmd.confirmMsg = "Quit will stop all agents. Enter to confirm, Esc to cancel."
				t.cmd.confirmExpiry = time.Now().Add(10 * time.Second)
				return false
			}
		}
	}

	// Everything else goes to the focused pane.
	if fp := t.focusedPane(); fp != nil {
		fp.SendKey(ev)
	}
	return false
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
		if paneKey(p) == t.layoutState.Focused {
			cur = i
			break
		}
	}
	// Skip hidden panes. Try every pane at most once.
	next := cur
	for i := 0; i < n; i++ {
		next = (next + delta + n) % n
		if !t.layoutState.Hidden[paneKey(t.panes[next])] {
			t.layoutState.Focused = paneKey(t.panes[next])
			t.applyLayout()
			return
		}
	}
}

// findPaneByName returns the pane with the given name, or nil.
func (t *TUI) findPaneByName(name string) PaneView {
	// First try exact paneKey match (handles "workbench:eng1" for remote).
	for _, p := range t.panes {
		if paneKey(p) == name {
			return p
		}
	}
	// Fall back to bare Name match for IPC commands that use short names.
	for _, p := range t.panes {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// visibleCountFromState returns the number of visible panes based on layoutState.
func (t *TUI) visibleCountFromState() int {
	n := 0
	for _, p := range t.panes {
		if !t.layoutState.Hidden[paneKey(p)] {
			n++
		}
	}
	return n
}

// restartFocused kills the focused pane's process and starts a new one.
func (t *TUI) restartFocused() error {
	fp := t.focusedPane()
	if fp == nil {
		return fmt.Errorf("no pane focused")
	}
	local, ok := fp.(*Pane)
	if !ok {
		return fmt.Errorf("restart not supported for remote panes")
	}
	return t.restartPane(local)
}

// restartByName finds the named pane and restarts it.
func (t *TUI) restartByName(name string) error {
	p := t.findPaneByName(name)
	if p == nil {
		return fmt.Errorf("unknown agent %q", name)
	}
	local, ok := p.(*Pane)
	if !ok {
		return fmt.Errorf("restart not supported for remote panes")
	}
	return t.restartPane(local)
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
	// Serialize with any in-flight IPC send before closing.
	// Without this lock, handleIPCSend may be mid-sleep inside its retry loop
	// (holding sendMu) while Close() tears down the PTY underneath it.
	fp.sendMu.Lock()
	fp.Close()
	fp.sendMu.Unlock()

	p, err := NewPane(fp.cfg, rows, cols)
	if err != nil {
		return err
	}
	p.eventCh = t.agentEvents
	p.safeGo = t.safeGo
	p.pinned = fp.pinned
	p.Start()
	t.panes[idx] = p
	t.applyLayout()
	return nil
}

// copyToClipboard writes text to the system clipboard using the appropriate
// tool for the current OS. Returns a status message suitable for t.cmd.error.
// On macOS uses pbcopy; on Linux tries wl-copy (Wayland) then xclip (X11).
// If no clipboard tool is available the copy is silently skipped and the
// caller message indicates the text remains visible in the patrol view.
func copyToClipboard(text string) string {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		} else if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		}
	}
	if cmd == nil {
		return "patrol: clipboard unavailable (no pbcopy/wl-copy/xclip found)"
	}
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return "patrol: " + err.Error()
	}
	return "patrol: copied to clipboard"
}
