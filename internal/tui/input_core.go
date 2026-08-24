package tui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
)

func (t *TUI) handleKey(ev *tcell.EventKey) bool {
	// ini-4pk Phase 0: capture what the outer terminal hands tcell for each
	// key event. Goal is to confirm whether Shift+Enter arrives as a
	// distinguishable event today (mod=Shift on KeyEnter) or as plain Enter
	// (mod=0). DEBUG-level so it only fires under `initech --verbose` and
	// imposes zero cost in normal runs. Remove or downgrade once Phase 1
	// proceeds (or stays here forever if Phase 1 doesn't proceed — it's
	// inert at default level).
	LogDebug("input", "key event",
		"key", ev.Key(),
		"key_name", ev.Name(),
		"rune", fmt.Sprintf("%q", ev.Rune()),
		"mod", ev.Modifiers(),
		"mod_shift", ev.Modifiers()&tcell.ModShift != 0,
		"mod_ctrl", ev.Modifiers()&tcell.ModCtrl != 0,
		"mod_alt", ev.Modifiers()&tcell.ModAlt != 0)

	// Consent modal owns the keyboard while open: it is a question about
	// writing files inside the operator's agents, so no keystroke may reach
	// the fleet beneath it or be mistaken for an answer (ini-2x8.6). Ahead of
	// the welcome overlay, which dismisses on ANY key -- the two must not both
	// consume one keypress.
	if t.handleAttentionConsentKey(ev) {
		return false
	}

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

	// Agents modal intercepts all input when active.
	if t.agents.active {
		return t.handleAgentsKey(ev)
	}

	// Quick grid/live dimension popup intercepts all input when active.
	if t.quickGrid.active {
		return t.handleQuickGridKey(ev)
	}

	// Command modal intercepts all input when active.
	if t.cmd.active {
		return t.handleCmdKey(ev)
	}

	// Clear any lingering error message on next keypress.
	t.cmd.error = ""

	// Backtick opens the command modal -- IN THE MAIN WINDOW ONLY (ini-fn77).
	//
	// In a child window it is SWALLOWED: no modal, no notice, and NOT
	// forwarded to the focused agent. Forwarding was considered and rejected
	// by the operator: it would let a window-2 user type a backtick into a
	// composer, which a window-1 user cannot do, so the two windows would
	// disagree about what the key means.
	if ev.Key() == tcell.KeyRune && ev.Rune() == '`' && ev.Modifiers() == 0 {
		if !t.isFleetAuthority() {
			return false
		}
		t.cmd.active = true
		t.cmd.buf = t.cmd.buf[:0]
		t.cmd.cursor = 0
		t.cmd.error = ""
		return false
	}

	// Alt-key combos are TUI shortcuts.
	if ev.Modifiers()&tcell.ModAlt != 0 {
		// Shift+Alt+digit (1-7) applies the slot's preset in LIVE mode
		// (ini-era4). Must be intercepted BEFORE the static Alt+digit switch
		// below: that switch gates on ModAlt, which is also set for Shift+Alt,
		// so without this guard a shifted press would misfire the static
		// preset (violating the fail-safe requirement). A shifted non-digit or
		// out-of-range rune falls through and never fires a preset.
		if ev.Modifiers()&tcell.ModShift != 0 && ev.Key() == tcell.KeyRune {
			if r := ev.Rune(); r >= '1' && r <= '7' {
				t.applyLayoutPresetLive(int(r - '1'))
				return false
			}
		}
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
			case '1', '2', '3', '4', '5', '6', '7':
				// Layout shortcut slots are config-driven (ini-lkww): each
				// maps to a preset resolved from initech.yaml at startup,
				// defaulting (ini-dxjk) to focus/2x1/3x1/4x1/3x2/4x2/live —
				// panel-count-consistent (Option+N draws N panels for N=1..4).
				t.applyLayoutPreset(int(ev.Rune() - '1'))
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
			case 'f':
				t.toggleFocusSplit()
				return false
			case 'a':
				if t.agents.active {
					t.agents.active = false
				} else {
					t.openAgentsModal()
				}
				return false
			case 'g':
				t.openQuickGrid(false)
				return false
			case 'l':
				t.openQuickGrid(true)
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

	// Everything else goes to the focused pane -- unless that pane is
	// SUSPENDED, in which case the keystroke is the wake gesture and is
	// CONSUMED, never delivered (ini-zffi).
	//
	// The gesture spends itself on the state change, the same principle as
	// ini-pzx0's focus-click decision. Delivering it as well would forge input
	// the operator never aimed at the agent: they pressed a key at a parked
	// pane to bring it back, not to type into whatever prompt the respawned
	// process happens to show. This return is what makes "consumed" true --
	// the key cannot reach SendKey on this branch, rather than being skipped
	// by a condition further down that a later edit could reorder.
	if fp := t.focusedPane(); fp != nil {
		if p, ok := fp.(*Pane); ok && p.IsSuspended() {
			t.wakeSuspendedPaneFromKeystroke(p)
			return false
		}
		fp.SendKey(ev)
	}
	return false
}

// wakeSuspendedPaneFromKeystroke resumes a pane the operator pressed a key at.
//
// Off the main goroutine: resumePane blocks for the full respawn (~1.1s
// measured on real Claude, ini-g7fl), and holding the input path for that long
// would freeze the display mid-gesture. The spec accepts the synchronous wake
// but the UI must stay alive while it happens, so the pane shows it is waking
// and the operator sees the gesture land immediately.
func (t *TUI) wakeSuspendedPaneFromKeystroke(p *Pane) {
	p.mu.Lock()
	if p.waking {
		p.mu.Unlock()
		return // Already waking from an earlier keystroke; further keys are still consumed.
	}
	p.waking = true
	p.mu.Unlock()

	t.safeGo(func() {
		defer func() {
			p.mu.Lock()
			p.waking = false
			p.mu.Unlock()
			t.fireOnWakeComplete()
		}()
		if err := t.resumePane(p, "keystroke"); err != nil {
			// Inherits g7fl: the queue survives, and the failure is loud
			// rather than a pane that silently stays parked.
			LogError("resource", "keystroke wake failed", "agent", p.Name(), "err", err)
			t.noticeWakeFailed(p.Name(), err)
		}
	})
}

func (t *TUI) handleResize() {
	t.screen.Sync()
	t.applyLayout()
}

func (t *TUI) cycleFocus(delta int) {
	// In live mode, cycle through rendered panes only (skips evicted panes
	// that are not on screen). In other modes, cycle all non-hidden panes.
	if t.layoutState.Mode == LayoutLive && len(t.plan.Panes) > 0 {
		n := len(t.plan.Panes)
		cur := 0
		for i, pr := range t.plan.Panes {
			if agentKey(pr.Pane) == t.layoutState.Focused {
				cur = i
				break
			}
		}
		next := (cur + delta + n) % n
		t.layoutState.Focused = agentKey(t.plan.Panes[next].Pane)
		t.applyLayout()
		return
	}
	// Fallback: cycle t.panes, skip hidden.
	n := len(t.panes)
	if n == 0 {
		return
	}
	cur := 0
	for i, p := range t.panes {
		if agentKey(p) == t.layoutState.Focused {
			cur = i
			break
		}
	}
	next := cur
	for i := 0; i < n; i++ {
		next = (next + delta + n) % n
		if !t.layoutState.Hidden[agentKey(t.panes[next])] {
			t.layoutState.Focused = agentKey(t.panes[next])
			t.applyLayout()
			return
		}
	}
}

// findPaneByName returns the pane with the given name, or nil.
func (t *TUI) findPaneByName(name string) PaneView {
	// First try exact paneKey match (handles "workbench:eng1" for remote).
	for _, p := range t.panes {
		if agentKey(p) == name {
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
		if !t.layoutState.Hidden[agentKey(p)] {
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
	// The deferred queue is taken UNDER THE SAME LOCK that serialises against
	// an in-flight send, and before Close, so nothing can enqueue onto a pane
	// that is about to stop existing (ini-1z6i).
	fp.sendMu.Lock()
	carried := fp.DrainQueue()
	fp.Close()
	fp.sendMu.Unlock()

	p, err := NewPane(fp.cfg, rows, cols)
	if err != nil {
		// The mail is in hand and the replacement never came up. Putting it
		// back is the whole reason this branch exists: returning here with
		// `carried` on the stack is the same silent discard the bead is about.
		restoreQueueAfterFailedRestart(fp, carried)
		return err
	}
	p.eventCh = t.agentEvents
	t.wireSuspendResume(p)
	p.safeGo = t.safeGo
	p.protected = fp.protected
	// Carried BEFORE Start so the maintenance tick can find the mail the
	// moment the pane is up. Nothing here delivers: the modal latch does not
	// survive the Pane, so the tick's drain is unblocked by construction.
	giveQueueToRestartedPane(p, carried)
	p.Start()
	t.panes[idx] = p
	t.applyLayout()
	return nil
}
