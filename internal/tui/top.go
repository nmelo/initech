package tui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Top modal box dimensions.
const (
	topBoxW = 100
	topBoxH = 30
)

// refreshTopData queries ps for each pane and caches the result.
//
// The cached rows are what the modal draws, so t.top.selected indexes this
// snapshot — not the live t.panes. Every rebuild re-anchors the highlight to
// the agent it was on by identity rather than by position, so a removal above
// the selection cannot slide the highlight onto a different agent (ini-6gjg).
func (t *TUI) refreshTopData() {
	if time.Since(t.top.cacheTime) < 2*time.Second && len(t.top.data) > 0 {
		return
	}
	prevKey := t.topSelectedKey()
	entries := make([]topEntry, len(t.panes))
	for i, pv := range t.panes {
		pk := paneKey(pv)
		e := topEntry{
			Name:   pv.Name(),
			Key:    pk,
			Bead:   pv.BeadID(),
			Status: pv.Activity().String(),
		}
		if t.layoutState.Hidden[pk] {
			e.Status += " [hidden]"
		}
		if lp, ok := pv.(*Pane); ok {
			e.Command = strings.Join(lp.cfg.Command, " ")
			if lp.pid > 0 {
				e.PID = lp.pid
				e.RSS = processTreeRSS(lp.pid)
				out, err := exec.Command("ps", "-o", "comm=", "-p",
					fmt.Sprintf("%d", e.PID)).Output()
				if err == nil {
					comm := strings.TrimSpace(string(out))
					if comm != "" {
						e.Comm = filepath.Base(comm)
					}
				}
			}
		}
		entries[i] = e
	}
	t.top.data = entries
	t.top.cacheTime = time.Now()
	t.topResync(prevKey)
}

// topSelectedKey returns the identity of the agent on the highlighted row —
// the row the operator can actually see. Empty when the selection falls
// outside the rendered snapshot, which is the caller's signal that there is no
// visible agent to act on.
func (t *TUI) topSelectedKey() string {
	if t.top.selected < 0 || t.top.selected >= len(t.top.data) {
		return ""
	}
	return t.top.data[t.top.selected].Key
}

// topResync re-points the highlight at prevKey's row in the freshly rebuilt
// snapshot so the selection follows the agent across a pane-set change instead
// of staying on a position that now belongs to someone else. Clamps into range
// when that agent is gone.
func (t *TUI) topResync(prevKey string) {
	n := len(t.top.data)
	if n == 0 {
		t.top.selected = 0
		t.top.scrollOffset = 0
		return
	}
	if prevKey != "" {
		for i, e := range t.top.data {
			if e.Key == prevKey {
				t.top.selected = i
				break
			}
		}
	}
	if t.top.selected >= n {
		t.top.selected = n - 1
	}
	if t.top.selected < 0 {
		t.top.selected = 0
	}
	if t.top.scrollOffset < 0 || t.top.scrollOffset >= n {
		t.top.scrollOffset = 0
	}
}

// topResolveSelected maps the highlighted row to an index into the live
// t.panes by pane identity rather than by position.
//
// The modal draws from a snapshot up to 2s old, so the row the operator sees
// and the live slice can disagree after a removal (t.panes shrinks) or a peer
// update (reorderPanes permutes t.panes without changing its length). A
// positional lookup then resolves to an agent that was never highlighted.
// Every destructive action goes through here; ok=false means the highlighted
// agent has no live counterpart and the caller must refuse rather than act on
// whichever pane inherited the index (ini-6gjg). The returned index comes from
// ranging over t.panes, so it is in range by construction.
func (t *TUI) topResolveSelected() (int, bool) {
	key := t.topSelectedKey()
	if key == "" {
		return -1, false
	}
	for i, pv := range t.panes {
		if paneKey(pv) == key {
			return i, true
		}
	}
	return -1, false
}

// topRefuseAction reports a destructive action that could not be tied to a
// live agent and invalidates the cache so the next render redraws from live
// state. The operator gets told nothing happened and sees the corrected list,
// rather than a silent no-op against a row that no longer exists.
func (t *TUI) topRefuseAction(verb string) {
	name := "selected agent"
	if t.top.selected >= 0 && t.top.selected < len(t.top.data) {
		if n := t.top.data[t.top.selected].Name; n != "" {
			name = n
		}
	}
	t.cmd.error = fmt.Sprintf("%s refused: %s is no longer running (list refreshed)", verb, name)
	t.top.cacheTime = time.Time{}
}

// topReconcile invalidates the top modal's cached rows when the pane set
// changes, so a removed agent is not drawn for the rest of the cache window.
// Call it wherever t.panes changes, alongside agentsReconcile. No-op when the
// modal is closed (state is reset on open).
//
// This narrows the stale window; it does not carry the correctness. Actions
// resolve their target by identity (topResolveSelected), which is what
// guarantees a destructive action can never hit an agent the operator did not
// see (ini-6gjg).
func (t *TUI) topReconcile() {
	if !t.top.active {
		return
	}
	t.top.cacheTime = time.Time{}
}

// handleTopKey handles input while the top modal is active.
func (t *TUI) handleTopKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.top.active = false
		return false
	case tcell.KeyUp:
		if t.top.selected > 0 {
			t.top.selected--
			t.topEnsureVisible()
		}
		return false
	case tcell.KeyDown:
		// Bounded by the rendered snapshot, not the live slice: selected
		// indexes t.top.data, which is what the operator is looking at.
		if t.top.selected < len(t.top.data)-1 {
			t.top.selected++
			t.topEnsureVisible()
		}
		return false
	case tcell.KeyRune:
		switch ev.Rune() {
		case '`':
			t.top.active = false
			return false
		case 'r':
			// Resolve by identity: the agent on the highlighted row, never the
			// pane that happens to sit at that index right now (ini-6gjg).
			if idx, resolved := t.topResolveSelected(); !resolved {
				t.topRefuseAction("restart")
			} else if idx >= 0 && idx < len(t.panes) {
				p, ok := t.panes[idx].(*Pane)
				if !ok {
					return false
				}
				cols := p.Emulator().Width()
				rows := p.Emulator().Height()
				if cols < 10 {
					cols = 80
				}
				if rows < 2 {
					rows = 24
				}
				p.sendMu.Lock()
				p.Close()
				p.sendMu.Unlock()
				np, err := NewPane(p.cfg, rows, cols)
				if err != nil {
					t.cmd.error = fmt.Sprintf("restart %s: %v", p.Name(), err)
				} else {
					np.eventCh = t.agentEvents
					np.safeGo = t.safeGo
					np.protected = p.protected
					np.Start()
					t.panes[idx] = np
					t.applyLayout()
				}
				t.top.cacheTime = time.Time{}
			}
			return false
		case 'k':
			// Same identity resolution as restart: kill destroys in-flight
			// work, so a stale index must refuse, not redirect (ini-6gjg).
			if idx, resolved := t.topResolveSelected(); !resolved {
				t.topRefuseAction("kill")
			} else if idx >= 0 && idx < len(t.panes) {
				p, ok := t.panes[idx].(*Pane)
				if !ok {
					return false
				}
				if p.cmd != nil && p.cmd.Process != nil {
					p.cmd.Process.Kill()
				}
				p.mu.Lock()
				p.alive = false
				p.mu.Unlock()
				t.top.cacheTime = time.Time{}
			}
			return false
		case 'q':
			t.top.active = false
			return false
		}
	}
	return false
}

// topVisibleRows returns how many data rows fit inside the modal box,
// excluding header (2 rows: header + separator), summary (1), and help (1).
func (t *TUI) topVisibleRows() int {
	_, sh := t.screen.Size()
	boxH := topBoxH
	if boxH > sh-4 {
		boxH = sh - 4
	}
	if boxH < 8 {
		boxH = 8
	}
	// Interior rows: boxH - 2 (border) - 2 (header+sep) - 1 (summary) - 1 (help) = boxH - 6
	vis := boxH - 6
	if vis < 1 {
		vis = 1
	}
	return vis
}

// topEnsureVisible adjusts scrollOffset so the selected row is in the viewport.
func (t *TUI) topEnsureVisible() {
	vis := t.topVisibleRows()
	if t.top.selected < t.top.scrollOffset {
		t.top.scrollOffset = t.top.selected
	}
	if t.top.selected >= t.top.scrollOffset+vis {
		t.top.scrollOffset = t.top.selected - vis + 1
	}
	// selected == -1 means nothing is highlighted; scrolling to meet it would
	// drive the offset negative and index t.top.data out of range.
	if t.top.scrollOffset < 0 {
		t.top.scrollOffset = 0
	}
}

// renderTop draws the floating activity monitor modal.
func (t *TUI) renderTop() {
	t.refreshTopData()
	// Keep the highlight on screen. A rebuild can move the selection (an agent
	// above it was removed) and leave scrollOffset pointing elsewhere; drawing
	// a selection the operator cannot see would put a destructive action on an
	// invisible target (ini-6gjg).
	t.topEnsureVisible()
	s := t.screen
	sw, sh := s.Size()

	boxW := topBoxW
	boxH := topBoxH
	if boxW > sw-4 {
		boxW = sw - 4
	}
	if boxH > sh-4 {
		boxH = sh - 4
	}
	if boxW < 40 || boxH < 8 {
		drawField(s, 0, 0, sw, "Terminal too small for top", tcell.StyleDefault.Foreground(tcell.ColorRed))
		return
	}

	startX := (sw - boxW) / 2
	startY := (sh - boxH) / 2
	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}

	bgStyle := tcell.StyleDefault.Background(tcell.NewRGBColor(20, 20, 20)).Foreground(tcell.ColorSilver)
	borderStyle := bgStyle.Foreground(tcell.ColorGray)
	headerStyle := bgStyle.Bold(true).Foreground(tcell.ColorWhite)
	normalStyle := bgStyle.Foreground(tcell.ColorSilver)
	deadStyle := bgStyle.Foreground(tcell.ColorRed)
	suspendedStyle := bgStyle.Foreground(tcell.ColorDodgerBlue)
	selectedStyle := tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite)
	totalStyle := bgStyle.Foreground(tcell.ColorYellow).Bold(true)
	helpStyle := bgStyle.Foreground(tcell.ColorGray)
	scrollStyle := bgStyle.Foreground(tcell.ColorDodgerBlue)

	// Draw opaque background.
	for y := startY; y < startY+boxH && y < sh; y++ {
		for x := startX; x < startX+boxW && x < sw; x++ {
			s.SetContent(x, y, ' ', nil, bgStyle)
		}
	}

	// Draw border.
	s.SetContent(startX, startY, '\u250c', nil, borderStyle)
	s.SetContent(startX+boxW-1, startY, '\u2510', nil, borderStyle)
	s.SetContent(startX, startY+boxH-1, '\u2514', nil, borderStyle)
	s.SetContent(startX+boxW-1, startY+boxH-1, '\u2518', nil, borderStyle)
	for x := startX + 1; x < startX+boxW-1 && x < sw; x++ {
		s.SetContent(x, startY, '\u2500', nil, borderStyle)
		s.SetContent(x, startY+boxH-1, '\u2500', nil, borderStyle)
	}
	for y := startY + 1; y < startY+boxH-1 && y < sh; y++ {
		s.SetContent(startX, y, '\u2502', nil, borderStyle)
		s.SetContent(startX+boxW-1, y, '\u2502', nil, borderStyle)
	}

	innerW := boxW - 2
	innerX := startX + 1

	drawLine := func(y int, text string, style tcell.Style) {
		runes := []rune(text)
		for i := 0; i < len(runes) && i < innerW; i++ {
			s.SetContent(innerX+i, y, runes[i], nil, style)
		}
	}

	fillRow := func(y int, style tcell.Style) {
		for x := innerX; x < innerX+innerW; x++ {
			s.SetContent(x, y, ' ', nil, style)
		}
	}

	// Title centered in top border. Green when running, blue when idle.
	anyRunning := false
	for _, pv := range t.panes {
		if pv.Activity() == StateRunning {
			anyRunning = true
			break
		}
	}
	titleText := " initech top "
	titleBg := tcell.ColorDodgerBlue
	if anyRunning {
		titleBg = tcell.ColorDarkGreen
	}
	titleStyle := tcell.StyleDefault.Background(titleBg).Foreground(tcell.ColorBlack).Bold(true)
	titleStart := startX + (boxW-len([]rune(titleText)))/2
	if titleStart < startX+1 {
		titleStart = startX + 1
	}
	for i, ch := range titleText {
		if titleStart+i < startX+boxW-1 {
			s.SetContent(titleStart+i, startY, ch, nil, titleStyle)
		}
	}

	// Column widths (fit within innerW).
	nameW := 10
	pidW := 7
	commW := 10
	rssW := 9
	statusW := 18
	cmdW := innerW - nameW - pidW - commW - rssW - statusW - 5 // 5 spaces between cols
	if cmdW < 8 {
		cmdW = 8
	}

	drawRow := func(row int, style tcell.Style, name, pid, comm, cmd, rss, status string) {
		x := innerX
		drawField(s, x, row, nameW, name, style)
		x += nameW + 1
		drawField(s, x, row, pidW, pid, style)
		x += pidW + 1
		drawField(s, x, row, commW, comm, style)
		x += commW + 1
		drawField(s, x, row, cmdW, cmd, style)
		x += cmdW + 1
		drawField(s, x, row, rssW, rss, style)
		x += rssW + 1
		drawField(s, x, row, statusW, status, style)
	}

	iy := startY + 1

	// Header row.
	drawRow(iy, headerStyle, "AGENT", "PID", "PROCESS", "COMMAND", "RSS", "STATUS")
	iy++
	// Separator.
	for x := innerX; x < innerX+innerW; x++ {
		s.SetContent(x, iy, '\u2500', nil, borderStyle)
	}
	iy++

	// Data rows with scrolling.
	visRows := boxH - 6 // header(1) + sep(1) + summary(1) + help(1) + borders(2)
	if visRows < 1 {
		visRows = 1
	}

	// Scroll indicators.
	if t.top.scrollOffset > 0 {
		drawLine(iy-1, string('\u25b2'), scrollStyle) // up arrow on separator line
	}

	var totalRSS int64
	for _, e := range t.top.data {
		if e.RSS > 0 {
			totalRSS += e.RSS
		}
	}

	dataLen := len(t.top.data)
	for vi := 0; vi < visRows && vi+t.top.scrollOffset < dataLen; vi++ {
		i := vi + t.top.scrollOffset
		e := t.top.data[i]
		style := normalStyle
		if e.Status == StateSuspended.String() || strings.HasPrefix(e.Status, StateSuspended.String()+" ") {
			style = suspendedStyle
		} else if e.Status == StateDead.String() || strings.HasPrefix(e.Status, StateDead.String()+" ") {
			style = deadStyle
		}
		if i == t.top.selected {
			fillRow(iy+vi, selectedStyle)
			style = selectedStyle
		}

		pid := "-"
		if e.PID > 0 {
			pid = fmt.Sprintf("%d", e.PID)
		}
		comm := e.Comm
		if comm == "" {
			comm = "-"
		}
		cmd := e.Command
		if cmd == "" {
			cmd = "-"
		}
		rss := "-"
		if e.RSS > 0 {
			if e.RSS > 1048576 {
				rss = fmt.Sprintf("%.1f GB", float64(e.RSS)/1048576)
			} else if e.RSS > 1024 {
				rss = fmt.Sprintf("%.0f MB", float64(e.RSS)/1024)
			} else {
				rss = fmt.Sprintf("%d KB", e.RSS)
			}
		}
		status := e.Status
		if status == "" {
			status = "-"
		}
		drawRow(iy+vi, style, e.Name, pid, comm, cmd, rss, status)
	}

	// Down scroll indicator.
	if t.top.scrollOffset+visRows < dataLen {
		drawLine(iy+visRows-1, string('\u25bc'), scrollStyle) // down arrow on last data row
	}

	// Summary row.
	summaryY := iy + visRows
	totalStr := "-"
	if totalRSS > 0 {
		totalStr = formatTotalRSS(totalRSS)
	}
	alive := 0
	dead := 0
	for _, e := range t.top.data {
		if e.PID > 0 {
			alive++
		} else {
			dead++
		}
	}
	summary := fmt.Sprintf("Total: %s (%d alive, %d dead)", totalStr, alive, dead)
	drawLine(summaryY, summary, totalStyle)

	// Help line.
	helpY := summaryY + 1
	help := "[r]estart  [k]ill  [q/Esc] close"
	drawLine(helpY, help, helpStyle)
}

// formatTotalRSS formats a total RSS value in KB to a human-readable string.
// Mirrors the per-entry RSS formatting tiers (GB / MB / KB) so small totals
// display as "512 KB" rather than "0 MB".
func formatTotalRSS(kb int64) string {
	if kb > 1048576 {
		return fmt.Sprintf("%.1f GB", float64(kb)/1048576)
	} else if kb >= 1024 {
		return fmt.Sprintf("%.0f MB", float64(kb)/1024)
	}
	return fmt.Sprintf("%d KB", kb)
}
