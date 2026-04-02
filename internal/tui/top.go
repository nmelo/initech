package tui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
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
		}
		return false
	case tcell.KeyDown:
		if t.top.selected < len(t.panes)-1 {
			t.top.selected++
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
					np.pinned = p.pinned
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

// renderTop draws the full-screen activity monitor table.
func (t *TUI) renderTop() {
	t.refreshTopData()
	s := t.screen
	sw, sh := s.Size()
	if sw < 40 || sh < 5 {
		drawField(s, 0, 0, sw, "Terminal too narrow for top", tcell.StyleDefault.Foreground(tcell.ColorRed))
		return
	}

	headerStyle := tcell.StyleDefault.Bold(true).Foreground(tcell.ColorWhite)
	normalStyle := tcell.StyleDefault.Foreground(tcell.ColorSilver)
	deadStyle := tcell.StyleDefault.Foreground(tcell.ColorRed)
	suspendedStyle := tcell.StyleDefault.Foreground(tcell.ColorDodgerBlue)
	selectedStyle := tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite)
	totalStyle := tcell.StyleDefault.Foreground(tcell.ColorYellow).Bold(true)
	helpStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)

	// Column widths.
	nameW := 10
	pidW := 8
	commW := 12
	rssW := 10
	statusW := 22
	cmdW := sw - nameW - pidW - commW - rssW - statusW - 6 // 6 for spacing
	if cmdW < 10 {
		cmdW = 10
	}

	// Header row.
	y := 1
	drawRow := func(row int, style tcell.Style, name, pid, comm, cmd, rss, status string) {
		x := 1
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

	// Title (centered). Green when any agent is running, blue when all idle.
	title := " initech top "
	anyRunning := false
	for _, pv := range t.panes {
		if pv.Activity() == StateRunning {
			anyRunning = true
			break
		}
	}
	bg := tcell.ColorDodgerBlue
	if anyRunning {
		bg = tcell.ColorDarkGreen
	}
	titleStyle := tcell.StyleDefault.Background(bg).Foreground(tcell.ColorBlack).Bold(true)
	titleStart := (sw - len([]rune(title))) / 2
	if titleStart < 0 {
		titleStart = 0
	}
	for i, ch := range title {
		if titleStart+i < sw {
			s.SetContent(titleStart+i, 0, ch, nil, titleStyle)
		}
	}

	drawRow(y, headerStyle, "AGENT", "PID", "PROCESS", "COMMAND", "RSS", "STATUS")
	y++
	// Separator.
	for x := 1; x < sw-1; x++ {
		s.SetContent(x, y, '\u2500', nil, tcell.StyleDefault.Foreground(tcell.ColorGray))
	}
	y++

	var totalRSS int64
	for i, e := range t.top.data {
		style := normalStyle
		if e.Status == StateSuspended.String() || strings.HasPrefix(e.Status, StateSuspended.String()+" ") {
			style = suspendedStyle
		} else if e.Status == StateDead.String() || strings.HasPrefix(e.Status, StateDead.String()+" ") {
			style = deadStyle
		}
		if i == t.top.selected {
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
			totalRSS += e.RSS
			if e.RSS > 1048576 {
				rss = fmt.Sprintf("%.1f GB", float64(e.RSS)/1048576)
			} else if e.RSS > 1024 {
				rss = fmt.Sprintf("%.0f MB", float64(e.RSS)/1024)
			} else {
				rss = fmt.Sprintf("%d KB", e.RSS)
			}
		}
		status := e.Status
		if e.Bead != "" {
			status = fmt.Sprintf("%s (%s)", e.Status, e.Bead)
		}
		if status == "" {
			status = "-"
		}
		drawRow(y, style, e.Name, pid, comm, cmd, rss, status)
		y++
		if y >= sh-3 {
			break
		}
	}

	// Total row.
	y++
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
	for i, ch := range summary {
		if 1+i < sw {
			s.SetContent(1+i, y, ch, nil, totalStyle)
		}
	}

	// Help line at bottom.
	help := "  [r]estart  [k]ill  [q/Esc] close"
	for i, ch := range help {
		if 1+i < sw {
			s.SetContent(1+i, sh-1, ch, nil, helpStyle)
		}
	}
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
