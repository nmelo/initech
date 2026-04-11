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
func (t *TUI) refreshTopData() {
	if time.Since(t.top.cacheTime) < 2*time.Second && len(t.top.data) > 0 {
		return
	}
	entries := make([]topEntry, len(t.panes))
	for i, pv := range t.panes {
		pk := paneKey(pv)
		e := topEntry{
			Name:   pv.Name(),
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
		if t.top.selected < len(t.panes)-1 {
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
			if t.top.selected >= 0 && t.top.selected < len(t.panes) {
				p, ok := t.panes[t.top.selected].(*Pane)
				if !ok {
					return false
				}
				idx := t.top.selected
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
			if t.top.selected >= 0 && t.top.selected < len(t.panes) {
				p, ok := t.panes[t.top.selected].(*Pane)
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
}

// renderTop draws the floating activity monitor modal.
func (t *TUI) renderTop() {
	t.refreshTopData()
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
