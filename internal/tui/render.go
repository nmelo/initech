package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

func (t *TUI) render() {
	t.renderCount++
	if t.renderCount <= 5 || t.renderCount%150 == 0 {
		LogInfo("render", "enter", "frame", t.renderCount, "plan_panes", len(t.plan.Panes))
	}
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
		if lp, ok := p.(*Pane); ok {
			lp.updateActivity()
		}
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

	if t.reorder.active {
		t.renderReorder()
		s.Show()
		return
	}

	if t.agents.active {
		t.renderAgents()
		s.Show()
		return
	}

	// Draw panes from the render plan. No visibility checks needed.
	for i, pr := range t.plan.Panes {
		LogDebug("render", "drawing pane", "frame", t.renderCount, "idx", i, "name", pr.Pane.Name(), "host", pr.Pane.Host())
		sel := t.selectionForPane(pr.Pane)
		pr.Pane.Render(s, pr.Focused, pr.Dimmed, pr.Index, sel)
		LogDebug("render", "pane done", "frame", t.renderCount, "idx", i, "name", pr.Pane.Name())
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

	// Welcome overlay on first launch (centered, auto-dismisses).
	if t.welcome.active {
		t.renderWelcome()
	}

	// Toast notifications (skip during command modal to avoid overlap).
	if !t.cmd.active {
		t.renderNotifications()
	}

	// Persistent status bar at the bottom of the screen.
	t.renderStatusBar()

	s.Show()

	// Stamp the watchdog heartbeat so it knows we're alive.
	t.lastRenderAt.Store(time.Now().UnixNano())

	if t.renderCount <= 5 || t.renderCount%150 == 0 {
		LogInfo("render", "screen.Show done", "frame", t.renderCount)
	}
}

// selectionForPane returns the selection state for a given pane.
func (t *TUI) selectionForPane(p PaneView) Selection {
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

// renderReorder draws the full-screen reorder modal.
func (t *TUI) renderReorder() {
	s := t.screen
	sw, sh := s.Size()

	titleStyle := tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorBlack).Bold(true)
	normalStyle := tcell.StyleDefault.Foreground(tcell.ColorSilver)
	cursorStyle := tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite)
	movingStyle := tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorWhite).Bold(true)
	hiddenStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)
	helpStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)

	// Title bar (full-width, centered like other modals).
	title := " Reorder agents "
	for x := 0; x < sw; x++ {
		s.SetContent(x, 0, ' ', nil, titleStyle)
	}
	titleStart := (sw - len([]rune(title))) / 2
	if titleStart < 0 {
		titleStart = 0
	}
	for i, ch := range title {
		if titleStart+i < sw {
			s.SetContent(titleStart+i, 0, ch, nil, titleStyle)
		}
	}

	// Help text.
	help := "  Enter: pick/drop   j/k: move   Space: confirm   Esc: cancel"
	for i, ch := range help {
		if i < sw {
			s.SetContent(i, 2, ch, nil, helpStyle)
		}
	}

	// Item list.
	y := 4
	for i, name := range t.reorder.items {
		if y >= sh-1 {
			break
		}

		style := normalStyle
		marker := "  "
		if i == t.reorder.cursor {
			if t.reorder.moving {
				style = movingStyle
				marker = "> "
			} else {
				style = cursorStyle
			}
		}

		// Tag for hidden/suspended panes.
		tag := ""
		if p := t.findPaneByName(name); p != nil {
			if t.layoutState.Hidden[name] {
				tag = " [h]"
				if i != t.reorder.cursor {
					style = hiddenStyle
				}
			}
			if p.IsSuspended() {
				tag = " [susp]"
			}
		}

		line := fmt.Sprintf("%s%2d. %s%s", marker, i+1, name, tag)

		// Fill the row background for cursor/moving rows.
		if i == t.reorder.cursor {
			for x := 0; x < sw; x++ {
				s.SetContent(x, y, ' ', nil, style)
			}
		}

		for j, ch := range line {
			if 2+j < sw {
				s.SetContent(2+j, y, ch, nil, style)
			}
		}
		y++
	}
}

// renderWelcome draws a centered overlay with the top keybindings on first launch.
func (t *TUI) renderWelcome() {
	s := t.screen
	sw, sh := s.Size()

	lines := []string{
		"Welcome to initech",
		"",
		"  `  (backtick)    Open command bar",
		"  Alt+Left/Right   Switch panes",
		"  Alt+z            Zoom focused pane",
		"  Alt+s            Toggle status overlay",
		"  ?                Full help reference",
		"",
		"HINT: ask super how this project works",
		"",
		"Press any key to dismiss",
	}

	boxW := 44
	boxH := len(lines) + 2 // 1 padding top + bottom
	startX := (sw - boxW) / 2
	startY := (sh - boxH) / 2
	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}

	bgStyle := tcell.StyleDefault.Background(tcell.NewRGBColor(20, 20, 20)).Foreground(tcell.ColorSilver)
	titleStyle := bgStyle.Foreground(tcell.ColorDodgerBlue).Bold(true)
	hintStyle := bgStyle.Foreground(tcell.ColorYellow)
	dimStyle := bgStyle.Foreground(tcell.ColorGray)

	// Draw box background.
	for y := startY; y < startY+boxH && y < sh; y++ {
		for x := startX; x < startX+boxW && x < sw; x++ {
			s.SetContent(x, y, ' ', nil, bgStyle)
		}
	}

	// Draw lines.
	const hintLineIdx = 8 // "HINT: ask super..." line
	for i, line := range lines {
		y := startY + 1 + i
		if y >= sh {
			break
		}
		style := bgStyle
		if i == 0 {
			style = titleStyle
		} else if i == hintLineIdx {
			style = hintStyle
		} else if i == len(lines)-1 {
			style = dimStyle
		}
		for j, ch := range line {
			x := startX + 2 + j
			if x < startX+boxW-1 && x < sw {
				s.SetContent(x, y, ch, nil, style)
			}
		}
	}
}

// renderStatusBar draws the persistent 1-line bar at the bottom of the screen.
// Content varies by state: confirmation prompt, command input, error message,
// or default keyboard hints.
func (t *TUI) renderStatusBar() {
	if t.cmd.active {
		t.renderCmdLine()
	} else if t.cmd.error != "" {
		t.renderCmdError()
	} else {
		t.renderHints()
	}
}

// statusBarItem is a single element in the status bar (text + style).
type statusBarItem struct {
	text  string
	style tcell.Style
}

// statusBarBuilder collects left- and right-aligned items and renders them
// onto a tcell screen row with dim middle-dot separators between right items.
// Right items are laid out right-to-left; left items fill remaining space.
type statusBarBuilder struct {
	barStyle tcell.Style
	sepStyle tcell.Style
	left     []statusBarItem // Rendered left-to-right from x=1.
	right    []statusBarItem // Rendered left-to-right, positioned flush-right.
}

// newStatusBarBuilder creates a builder with the standard bar background.
func newStatusBarBuilder() *statusBarBuilder {
	bar := tcell.StyleDefault.Background(tcell.NewRGBColor(30, 30, 30)).Foreground(tcell.ColorGray)
	return &statusBarBuilder{
		barStyle: bar,
		sepStyle: bar.Foreground(tcell.NewRGBColor(70, 70, 70)),
	}
}

// addRight appends an item to the right side of the status bar.
func (b *statusBarBuilder) addRight(text string, style tcell.Style) {
	b.right = append(b.right, statusBarItem{text: text, style: style})
}

// addLeft appends an item to the left side of the status bar.
func (b *statusBarBuilder) addLeft(text string, style tcell.Style) {
	b.left = append(b.left, statusBarItem{text: text, style: style})
}

const sepWidth = 3 // " · "

// render draws all items onto screen row y. Right items are drawn first
// (they have priority), then left items fill remaining space with truncation.
func (b *statusBarBuilder) render(s tcell.Screen, y int) {
	sw, _ := s.Size()

	// Clear the row.
	for x := 0; x < sw; x++ {
		s.SetContent(x, y, ' ', nil, b.barStyle)
	}

	// Calculate total width needed by right items (with separators between them).
	rightW := 0
	for i, item := range b.right {
		rightW += len([]rune(item.text))
		if i > 0 {
			rightW += sepWidth
		}
	}

	// Draw right items from their computed start position.
	rightStart := sw - rightW - 1
	if rightStart < 0 {
		rightStart = 0
	}
	x := rightStart
	for i, item := range b.right {
		if i > 0 {
			x = b.drawSep(s, x, y, sw)
		}
		for _, ch := range item.text {
			if x >= 0 && x < sw {
				s.SetContent(x, y, ch, nil, item.style)
			}
			x++
		}
	}

	// Draw left items, truncating if they would overlap the right block.
	maxLeft := rightStart - 3 // gap between left and right
	if maxLeft < 1 {
		return
	}
	lx := 1
	for _, item := range b.left {
		remaining := maxLeft - (lx - 1)
		if remaining < 10 {
			break
		}
		runes := []rune(item.text)
		if len(runes) > remaining {
			runes = append(runes[:remaining-1], '\u2026')
		}
		for _, ch := range runes {
			if lx < sw {
				s.SetContent(lx, y, ch, nil, item.style)
			}
			lx++
		}
	}
}

// drawSep writes a dim " · " separator at position x and returns the new x.
func (b *statusBarBuilder) drawSep(s tcell.Screen, x, y, sw int) int {
	if x >= 0 && x < sw {
		s.SetContent(x, y, ' ', nil, b.barStyle)
	}
	x++
	if x >= 0 && x < sw {
		s.SetContent(x, y, '\u00b7', nil, b.sepStyle)
	}
	x++
	if x >= 0 && x < sw {
		s.SetContent(x, y, ' ', nil, b.barStyle)
	}
	return x + 1
}

// renderHints draws the status bar with a cycling tip on the left and
// keyboard shortcuts on the right. Subtle dark background with dim text.
func (t *TUI) renderHints() {
	_, sh := t.screen.Size()
	b := newStatusBarBuilder()

	// Left: cycling tip.
	b.addLeft(statusTips[t.tipIndex%len(statusTips)], b.barStyle)

	// Right items are added in display order (left-to-right within the right block).

	// Pending timers.
	if t.timers != nil {
		if pending := t.timers.Pending(); pending > 0 {
			b.addRight(fmt.Sprintf("%d pending", pending), b.barStyle)
		}
	}

	// Update available.
	if t.updateAvailable != "" {
		b.addRight("v"+t.updateAvailable+" available", b.barStyle.Foreground(tcell.ColorYellow))
	}

	// Quota.
	if t.quotaPercent >= 0 {
		quotaStyle := b.barStyle
		if t.quotaPercent >= 95 {
			quotaStyle = b.barStyle.Foreground(tcell.ColorRed)
		} else if t.quotaPercent >= 80 {
			quotaStyle = b.barStyle.Foreground(tcell.ColorYellow)
		}
		b.addRight(fmt.Sprintf("Q:%d%%", t.quotaPercent), quotaStyle)
	}

	// Battery.
	if t.batteryPercent >= 0 {
		battStr := fmt.Sprintf("%d%%", t.batteryPercent)
		battStyle := b.barStyle
		if t.batteryCharging {
			battStr += " +"
			battStyle = b.barStyle.Foreground(tcell.ColorGreen)
		} else if t.batteryPercent < 10 {
			battStyle = b.barStyle.Foreground(tcell.ColorRed)
		} else if t.batteryPercent < 20 {
			battStyle = b.barStyle.Foreground(tcell.ColorYellow)
		}
		b.addRight(battStr, battStyle)
	}

	// Keyboard shortcuts.
	b.addRight("`:cmd  Alt+z:zoom  Alt+s:overlay  ?:help", b.barStyle)

	// Clock (rightmost).
	b.addRight(time.Now().Format("15:04"), b.barStyle)

	b.render(t.screen, sh-1)
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
		confirmStyle := tcell.StyleDefault.Background(tcell.NewRGBColor(0, 0, 0)).Foreground(tcell.ColorYellow).Bold(true)
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

	// Hint line one row above the input (shared by tab completion and fuzzy suggestions).
	// Tab completion takes priority; fuzzy suggestions show when tab hint is empty.
	hintText := t.cmd.tabHint
	if hintText == "" && len(t.cmd.suggestions) > 0 {
		hintText = strings.Join(t.cmd.suggestions, "  ")
	}
	if hintText != "" && sh >= 3 {
		hintY := sh - 2
		tabHintStyle := tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorGray)
		for x := 0; x < sw; x++ {
			s.SetContent(x, hintY, ' ', nil, tabHintStyle)
		}
		label := []rune("  " + hintText)
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

		// Colors by event type: gutter accent color + body style.
		var gutterColor tcell.Color
		var style tcell.Style
		switch n.event.Type {
		case EventBeadCompleted:
			gutterColor = tcell.ColorGreen
			style = tcell.StyleDefault.Background(tcell.ColorDarkGreen).Foreground(tcell.ColorBlack)
		case EventBeadClaimed:
			gutterColor = tcell.ColorDodgerBlue
			style = tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorWhite)
		case EventBeadFailed, EventAgentStuck:
			gutterColor = tcell.ColorRed
			style = tcell.StyleDefault.Background(tcell.ColorDarkRed).Foreground(tcell.ColorWhite)
		case EventAgentStalled:
			gutterColor = tcell.ColorOrange
			style = tcell.StyleDefault.Background(tcell.ColorDarkOrange).Foreground(tcell.ColorBlack)
		case EventAgentIdle:
			gutterColor = tcell.ColorGray
			style = tcell.StyleDefault.Background(tcell.ColorDimGray).Foreground(tcell.ColorWhite)
		default:
			gutterColor = tcell.ColorGray
			style = tcell.StyleDefault.Background(tcell.ColorDimGray).Foreground(tcell.ColorWhite)
		}

		// Draw toast: colored gutter block on left, then body.
		gutterStyle := tcell.StyleDefault.Background(gutterColor).Foreground(gutterColor)
		s.SetContent(x, row, '\u2588', nil, gutterStyle) // full block gutter
		for dx := 1; dx < toastW; dx++ {
			s.SetContent(x+dx, row, ' ', nil, style)
		}
		// Draw text after the gutter (1-char left padding from body start).
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
		vis := !t.layoutState.Hidden[paneKey(p)]
		act := p.Activity()
		bead := p.BeadID()
		// Build status: combine activity and bead ID as "running (ini-sx5)".
		status := act.String()
		if bead != "" {
			status = fmt.Sprintf("%s (%s)", act.String(), bead)
		}
		pin := t.layoutState.Pinned[paneKey(p)]
		remote := p.Host() != ""
		displayName := p.Name()
		if remote {
			displayName = p.Host() + ":" + p.Name()
		}
		agents[i] = AgentInfo{Name: displayName, Status: status, Activity: act, Visible: vis, Pinned: pin, Remote: remote}
		nameLen := len(displayName)
		if remote {
			nameLen += 4 // " [R]"
		}
		if pin {
			nameLen += 4 // " [P]"
		}
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

	overlayBg := tcell.NewRGBColor(20, 25, 40)
	bgStyle := tcell.StyleDefault.Background(overlayBg)
	borderStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(overlayBg)

	// Top border with title (rounded corners).
	s.SetContent(px, py, '\u256d', nil, borderStyle)
	title := " Agents "
	if t.projectName != "" {
		title = " Agents (" + t.projectName + ") "
	}
	for i := 1; i < panelW-1; i++ {
		ch := '\u2500'
		if i-1 < len(title) {
			ch = rune(title[i-1])
		}
		s.SetContent(px+i, py, ch, nil, borderStyle)
	}
	s.SetContent(px+panelW-1, py, '\u256e', nil, borderStyle)

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
			dotColor = tcell.ColorGray
		case StateDead:
			dotColor = tcell.ColorRed
		case StateSuspended:
			dot = '\u25cb' // Hollow dot, same as idle but blue.
			dotColor = tcell.ColorDodgerBlue
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
		// Remote marker.
		if a.Remote {
			remoteStyle := bgStyle.Foreground(tcell.ColorTeal)
			for _, ch := range " [R]" {
				if col < px+panelW-1 {
					s.SetContent(col, row, ch, nil, remoteStyle)
				}
				col++
			}
		}
		// Pin marker.
		if a.Pinned {
			pinStyle := bgStyle.Foreground(tcell.ColorCornflowerBlue)
			for _, ch := range " [P]" {
				if col < px+panelW-1 {
					s.SetContent(col, row, ch, nil, pinStyle)
				}
				col++
			}
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
	s.SetContent(px, botRow, '\u2570', nil, borderStyle)
	for i := 1; i < panelW-1; i++ {
		s.SetContent(px+i, botRow, '\u2500', nil, borderStyle)
	}
	s.SetContent(px+panelW-1, botRow, '\u256f', nil, borderStyle)
}
