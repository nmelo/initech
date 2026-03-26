package tui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
)

func (t *TUI) render() {
	s := t.screen

	// Detect dimension changes (font resize, window resize).
	w, h := s.Size()
	if w != t.lastW || h != t.lastH {
		t.lastW = w
		t.lastH = h
		t.applyLayout()
	}

	// Refresh activity state for all panes before rendering.
	// updateActivity checks PTY output recency under a lock; cost is negligible.
	for _, p := range t.panes {
		p.updateActivity()
	}

	s.Clear()

	if t.help.active {
		// Full-screen help reference card replaces pane rendering.
		t.renderHelp()
		s.Show()
		return
	}

	if t.eventLogM.active {
		// Full-screen event log replaces pane rendering.
		t.renderEventLog()
		s.Show()
		return
	}

	if t.top.active {
		// Full-screen activity monitor replaces pane rendering.
		t.renderTop()
		s.Show()
		return
	}

	// Draw panes from the render plan. No visibility checks needed.
	for _, pr := range t.plan.Panes {
		sel := t.selectionForPane(pr.Pane)
		pr.Pane.Render(s, pr.Focused, pr.Dimmed, pr.Index, sel)
	}

	// Draw dividers from the render plan.
	divStyle := tcell.StyleDefault.Foreground(tcell.ColorBlack)
	for _, d := range t.plan.Dividers {
		if d.Vertical {
			for y := d.Y; y < d.Y+d.Len; y++ {
				s.SetContent(d.X, y, '\u2502', nil, divStyle)
			}
		}
	}

	if t.layoutState.Overlay {
		t.renderOverlay()
	}

	// Toast notifications (skip during command modal to avoid overlap).
	if !t.cmd.active {
		t.renderNotifications()
	}

	// Command modal or error message at the bottom.
	if t.cmd.active {
		t.renderCmdLine()
	} else if t.cmd.error != "" {
		t.renderCmdError()
	}

	s.Show()
}


// selectionFor returns the selection state for a given pane index.
func (t *TUI) selectionFor(paneIdx int) Selection {
	if !t.sel.active || t.sel.pane != paneIdx {
		return Selection{}
	}
	return Selection{
		Active: true,
		StartX: t.sel.startX, StartY: t.sel.startY,
		EndX: t.sel.endX, EndY: t.sel.endY,
	}
}

// selectionForPane returns the selection state for a given pane.
func (t *TUI) selectionForPane(p *Pane) Selection {
	if !t.sel.active {
		return Selection{}
	}
	if t.sel.pane >= 0 && t.sel.pane < len(t.panes) && t.panes[t.sel.pane] == p {
		return Selection{
			Active: true,
			StartX: t.sel.startX, StartY: t.sel.startY,
			EndX: t.sel.endX, EndY: t.sel.endY,
		}
	}
	return Selection{}
}



// renderCmdLine draws the command input bar at the bottom of the screen.
// If a destructive command is pending confirmation, it renders a yellow
// confirmation prompt instead of the normal input.
// If tab completion matches are available, it renders a hint line one row above.
func (t *TUI) renderCmdLine() {
	s := t.screen
	sw, sh := s.Size()
	y := sh - 1

	// Confirmation prompt: replace normal input with a yellow warning bar.
	if t.cmd.pendingConfirm != "" {
		confirmStyle := tcell.StyleDefault.Background(tcell.ColorOlive).Foreground(tcell.ColorWhite)
		for x := 0; x < sw; x++ {
			s.SetContent(x, y, ' ', nil, confirmStyle)
		}
		// Append a live countdown so the operator knows how long they have.
		remaining := time.Until(t.cmd.confirmExpiry)
		secs := int(remaining.Seconds())
		if secs < 0 {
			secs = 0
		}
		countdown := fmt.Sprintf(" (%ds)", secs)
		msg := []rune(" " + t.cmd.confirmMsg + countdown)
		for i, ch := range msg {
			if i < sw {
				s.SetContent(i, y, ch, nil, confirmStyle)
			}
		}
		return
	}

	// Background for the entire line.
	bgStyle := tcell.StyleDefault.Background(tcell.ColorDarkSlateGray)
	for x := 0; x < sw; x++ {
		s.SetContent(x, y, ' ', nil, bgStyle)
	}

	// Prompt.
	promptStyle := bgStyle.Foreground(tcell.ColorYellow).Bold(true)
	s.SetContent(0, y, '>', nil, promptStyle)
	s.SetContent(1, y, ' ', nil, bgStyle)

	// Input text.
	textStyle := bgStyle.Foreground(tcell.ColorWhite)
	for i, ch := range t.cmd.buf {
		if 2+i < sw {
			s.SetContent(2+i, y, ch, nil, textStyle)
		}
	}

	// Cursor.
	cursorPos := 2 + len(t.cmd.buf)
	if cursorPos < sw {
		cursorStyle := tcell.StyleDefault.Background(tcell.ColorWhite).Foreground(tcell.ColorBlack)
		s.SetContent(cursorPos, y, ' ', nil, cursorStyle)
	}

	// Hint text on the right.
	hint := "Enter:run  Esc:cancel  ?:help"
	hintStyle := bgStyle.Foreground(tcell.ColorGray)
	hintStart := sw - len(hint) - 1
	if hintStart > cursorPos+2 {
		for i, ch := range hint {
			s.SetContent(hintStart+i, y, ch, nil, hintStyle)
		}
	}

	// Tab completion hint: draw a dimmed hint bar one row above the input.
	if t.cmd.tabHint != "" && sh >= 2 {
		hintY := sh - 2
		tabHintStyle := tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorGray)
		for x := 0; x < sw; x++ {
			s.SetContent(x, hintY, ' ', nil, tabHintStyle)
		}
		label := []rune("  " + t.cmd.tabHint)
		for i, ch := range label {
			if i < sw {
				s.SetContent(i, hintY, ch, nil, tabHintStyle)
			}
		}
	}
}

// renderCmdError draws an error message at the bottom of the screen.
func (t *TUI) renderCmdError() {
	s := t.screen
	sw, sh := s.Size()
	if sw < 5 {
		return // Too narrow to render error without slice-bounds panic (ini-a1e.6).
	}
	y := sh - 1

	errStyle := tcell.StyleDefault.Background(tcell.ColorDarkRed).Foreground(tcell.ColorWhite)
	for x := 0; x < sw; x++ {
		s.SetContent(x, y, ' ', nil, errStyle)
	}
	msg := []rune(" " + t.cmd.error)
	if len(msg) > sw-1 {
		msg = append(msg[:sw-4], '.', '.', '.')
	}
	for i, ch := range msg {
		s.SetContent(i, y, ch, nil, errStyle)
	}
}

// renderNotifications draws active toast notifications at the bottom-right,
// stacking upward. Skipped during top modal or command modal.
func (t *TUI) renderNotifications() {
	if len(t.notifications) == 0 {
		return
	}
	s := t.screen
	sw, sh := s.Size()

	// Too narrow to render toasts.
	if sw < 30 {
		return
	}

	// Stack from the bottom-right, above the command/error bar.
	// Reserve 1 row at the bottom for the command bar.
	baseY := sh - 2
	maxW := 50
	if maxW > sw-2 {
		maxW = sw - 2
	}

	for i := len(t.notifications) - 1; i >= 0; i-- {
		n := t.notifications[i]
		row := baseY - (len(t.notifications) - 1 - i)
		if row < 1 {
			break // Off the top of the screen.
		}

		// Format: "[agent] detail"
		text := fmt.Sprintf("[%s] %s", n.event.Pane, n.event.Detail)
		runes := []rune(text)
		if len(runes) > maxW-2 {
			runes = append(runes[:maxW-3], '\u2026')
		}
		toastW := len(runes) + 2 // 1 char padding on each side.
		x := sw - toastW - 1

		// Background color by event type.
		var style tcell.Style
		switch n.event.Type {
		case EventBeadCompleted:
			style = tcell.StyleDefault.Background(tcell.ColorDarkGreen).Foreground(tcell.ColorBlack)
		case EventBeadClaimed:
			style = tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorWhite)
		case EventBeadFailed, EventAgentStuck:
			style = tcell.StyleDefault.Background(tcell.ColorDarkRed).Foreground(tcell.ColorWhite)
		case EventAgentStalled:
			style = tcell.StyleDefault.Background(tcell.ColorDarkOrange).Foreground(tcell.ColorBlack)
		case EventAgentIdle:
			style = tcell.StyleDefault.Background(tcell.ColorDimGray).Foreground(tcell.ColorWhite)
		default:
			style = tcell.StyleDefault.Background(tcell.ColorDimGray).Foreground(tcell.ColorWhite)
		}

		// Draw toast background.
		for dx := 0; dx < toastW; dx++ {
			s.SetContent(x+dx, row, ' ', nil, style)
		}
		// Draw text with 1-char left padding.
		for j, ch := range runes {
			s.SetContent(x+1+j, row, ch, nil, style)
		}
	}
}

// ── Activity monitor (top) modal ─────────────────────────────────────

// topEntry holds process info for one pane.
type topEntry struct {
	Name    string
	PID     int
	Comm    string // Process name from ps.
	Command string // Launch command from config.
	RSS     int64  // Resident memory in KB.
	Bead    string // Current bead ID (empty = none).
	Status  string // running, idle, dead, hidden.
}
func drawField(s tcell.Screen, x, y, width int, text string, style tcell.Style) {
	if width <= 0 {
		return
	}
	runes := []rune(text)
	if len(runes) > width {
		runes = runes[:width-1]
		runes = append(runes, '\u2026') // ellipsis
	}
	for i, ch := range runes {
		s.SetContent(x+i, y, ch, nil, style)
	}
}

// renderOverlay draws the floating agent status panel.
func (t *TUI) renderOverlay() {
	s := t.screen

	agents := make([]AgentInfo, len(t.panes))
	maxNameLen := 0
	hiddenCount := 0
	for i, p := range t.panes {
		vis := !t.layoutState.Hidden[p.name]
		act := p.Activity()
		bead := p.BeadID()
		// Build status: show idle-with-backlog hint when idle, then combine
		// activity and bead ID as "running (ini-sx5)".
		status := act.String()
		idleBacklog := false
		backlogN := 0
		if act == StateIdle && bead == "" {
			idleBacklog = p.IdleWithBacklog()
			backlogN = p.BacklogCount()
			if idleBacklog {
				status = fmt.Sprintf("idle (%d ready)", backlogN)
			}
		}
		if bead != "" {
			status = fmt.Sprintf("%s (%s)", act.String(), bead)
		}
		agents[i] = AgentInfo{Name: p.name, Status: status, Activity: act, Visible: vis, IdleWithBacklog: idleBacklog, BacklogCount: backlogN}
		nameLen := len(p.name)
		if !vis {
			nameLen += 4 // " [h]"
			hiddenCount++
		}
		if nameLen > maxNameLen {
			maxNameLen = nameLen
		}
	}

	statusMaxLen := 7 // minimum: "running"
	for _, a := range agents {
		if len(a.Status) > statusMaxLen {
			statusMaxLen = len(a.Status)
		}
	}
	panelW := 4 + maxNameLen + 1 + statusMaxLen + 2
	// Extra row for summary line when there are hidden panes.
	summaryRow := hiddenCount > 0
	panelH := len(agents) + 2
	if summaryRow {
		panelH++
	}

	sw, sh := s.Size()
	px := sw - panelW - 1
	py := 1
	if px < 0 {
		px = 0
	}
	if px+panelW > sw {
		panelW = sw - px
	}
	if py+panelH > sh {
		panelH = sh - py
	}
	if panelW < 10 || panelH < 3 {
		return
	}

	bgStyle := tcell.StyleDefault.Background(tcell.ColorDarkBlue)
	borderStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorDarkBlue)

	// Top border with title.
	s.SetContent(px, py, '\u250c', nil, borderStyle)
	title := " Agents "
	for i := 1; i < panelW-1; i++ {
		ch := '\u2500'
		if i-1 < len(title) {
			ch = rune(title[i-1])
		}
		s.SetContent(px+i, py, ch, nil, borderStyle)
	}
	s.SetContent(px+panelW-1, py, '\u2510', nil, borderStyle)

	// Agent rows.
	for i, a := range agents {
		if i+2 >= panelH {
			break
		}
		row := py + 1 + i

		s.SetContent(px, row, '\u2502', nil, borderStyle)
		for x := px + 1; x < px+panelW-1; x++ {
			s.SetContent(x, row, ' ', nil, bgStyle)
		}

		// Status dot (color per actual activity state, not display text).
		dot := '\u25cf'
		var dotColor tcell.Color
		switch a.Activity {
		case StateRunning:
			dotColor = tcell.ColorGreen
		case StateIdle:
			dot = '\u25cb'
			if a.IdleWithBacklog {
				dotColor = tcell.ColorYellow
			} else {
				dotColor = tcell.ColorGray
			}
		case StateDead:
			dotColor = tcell.ColorRed
		default:
			dotColor = tcell.ColorGray
		}
		s.SetContent(px+2, row, dot, nil, bgStyle.Foreground(dotColor))

		// Name (dimmed for hidden panes).
		nameStyle := bgStyle.Foreground(tcell.ColorWhite)
		if a.Name == t.layoutState.Focused {
			nameStyle = bgStyle.Foreground(tcell.ColorYellow).Bold(true)
		} else if !a.Visible {
			nameStyle = bgStyle.Foreground(tcell.ColorDarkGray)
		}
		col := px + 4
		for _, ch := range a.Name {
			if col < px+panelW-1 {
				s.SetContent(col, row, ch, nil, nameStyle)
			}
			col++
		}
		// Hidden marker.
		if !a.Visible {
			markerStyle := bgStyle.Foreground(tcell.ColorDarkGray)
			for _, ch := range " [h]" {
				if col < px+panelW-1 {
					s.SetContent(col, row, ch, nil, markerStyle)
				}
				col++
			}
		}

		// Status text.
		statusStyle := bgStyle.Foreground(tcell.ColorSilver)
		if !a.Visible {
			statusStyle = bgStyle.Foreground(tcell.ColorDarkGray)
		}
		statusCol := px + 4 + maxNameLen + 1
		for j, ch := range a.Status {
			if statusCol+j < px+panelW-1 {
				s.SetContent(statusCol+j, row, ch, nil, statusStyle)
			}
		}

		s.SetContent(px+panelW-1, row, '\u2502', nil, borderStyle)
	}

	// Summary line (only when hidden panes exist).
	if summaryRow {
		sumRow := py + 1 + len(agents)
		if sumRow+1 < py+panelH {
			s.SetContent(px, sumRow, '\u2502', nil, borderStyle)
			for x := px + 1; x < px+panelW-1; x++ {
				s.SetContent(x, sumRow, ' ', nil, bgStyle)
			}
			visCount := len(agents) - hiddenCount
			summary := fmt.Sprintf(" %d visible, %d hidden", visCount, hiddenCount)
			sumStyle := bgStyle.Foreground(tcell.ColorSilver)
			for j, ch := range summary {
				if px+1+j < px+panelW-1 {
					s.SetContent(px+1+j, sumRow, ch, nil, sumStyle)
				}
			}
			s.SetContent(px+panelW-1, sumRow, '\u2502', nil, borderStyle)
		}
	}

	// Bottom border.
	botRow := py + panelH - 1
	s.SetContent(px, botRow, '\u2514', nil, borderStyle)
	for i := 1; i < panelW-1; i++ {
		s.SetContent(px+i, botRow, '\u2500', nil, borderStyle)
	}
	s.SetContent(px+panelW-1, botRow, '\u2518', nil, borderStyle)
}
