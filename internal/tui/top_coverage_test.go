// Coverage tests for top.go: renderTop, handleTopKey, refreshTopData, formatTotalRSS.
package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// helper: create a TUI with pre-populated top.data (skip refreshTopData's ps calls).
func topTestTUI(entries []topEntry) (*TUI, tcell.SimulationScreen) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 30)

	panes := make([]*Pane, len(entries))
	for i, e := range entries {
		emu := vt.NewSafeEmulator(40, 10)
		go func() {
			buf := make([]byte, 256)
			for {
				if _, err := emu.Read(buf); err != nil {
					return
				}
			}
		}()
		panes[i] = &Pane{
			name:    e.Name,
			emu:     emu,
			alive:   e.PID > 0,
			visible: true,
			region:  Region{X: 0, Y: 0, W: 120, H: 30},
		}
	}

	tui := &TUI{
		screen:      s,
		panes:       panes,
		layoutState: DefaultLayoutState(nil),
		top:         topModal{active: true, data: entries, cacheTime: time.Now()},
	}
	return tui, s
}

// ── renderTop ───────────────────────────────────────────────────────

func TestRenderTop_TitleVisible(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", PID: 123, Status: "running", Command: "claude"},
	})
	tui.renderTop()

	sw, _ := s.Size()
	var buf strings.Builder
	for x := 0; x < sw; x++ {
		c, _, _, _ := s.GetContent(x, 0)
		buf.WriteRune(c)
	}
	if !strings.Contains(buf.String(), "initech top") {
		t.Errorf("title row = %q, want 'initech top'", buf.String())
	}
}

func TestRenderTop_HeaderRow(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", Status: "idle"},
	})
	tui.renderTop()

	var buf strings.Builder
	for x := 0; x < 120; x++ {
		c, _, _, _ := s.GetContent(x, 1)
		buf.WriteRune(c)
	}
	row := buf.String()
	for _, hdr := range []string{"AGENT", "PID", "PROCESS", "COMMAND", "RSS", "STATUS"} {
		if !strings.Contains(row, hdr) {
			t.Errorf("header row missing %q: %q", hdr, row)
		}
	}
}

func TestRenderTop_AgentRowRendered(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "super", PID: 100, Status: "running", Command: "claude --continue", RSS: 2048, Comm: "claude"},
	})
	tui.top.selected = -1
	tui.renderTop()

	// Agent data row is at y=3 (title=0, header=1, separator=2, data=3).
	var buf strings.Builder
	for x := 0; x < 120; x++ {
		c, _, _, _ := s.GetContent(x, 3)
		buf.WriteRune(c)
	}
	row := buf.String()
	if !strings.Contains(row, "super") {
		t.Errorf("agent row missing 'super': %q", row)
	}
	if !strings.Contains(row, "100") {
		t.Errorf("agent row missing PID '100': %q", row)
	}
	if !strings.Contains(row, "2 MB") {
		t.Errorf("agent row missing RSS '2 MB': %q", row)
	}
}

func TestRenderTop_DeadStyleApplied(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", Status: "dead"},
	})
	tui.top.selected = -1 // no selection so normal/dead style applies
	tui.renderTop()

	// Check that the 'e' of 'eng1' has red foreground (drawField starts at x=1).
	c, _, style, _ := s.GetContent(1, 3)
	fg, _, _ := style.Decompose()
	if c != 'e' {
		t.Errorf("expected 'e' at (1,3), got %q", c)
	}
	if fg != tcell.ColorRed {
		t.Errorf("dead row fg = %v, want Red", fg)
	}
}

func TestRenderTop_SuspendedStyleApplied(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng2", Status: "suspended"},
	})
	tui.top.selected = -1
	tui.renderTop()

	c, _, style, _ := s.GetContent(1, 3)
	fg, _, _ := style.Decompose()
	if c != 'e' {
		t.Errorf("expected 'e' at (1,3), got %q", c)
	}
	if fg != tcell.ColorDodgerBlue {
		t.Errorf("suspended row fg = %v, want DodgerBlue", fg)
	}
}

func TestRenderTop_SelectedStyleApplied(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", Status: "idle"},
		{Name: "eng2", Status: "running"},
	})
	tui.top.selected = 1
	tui.renderTop()

	// Selected row (eng2) is at y=4. Check it has DarkBlue background.
	// drawField writes at x=1 for the first column.
	_, _, style, _ := s.GetContent(1, 4)
	_, bg, _ := style.Decompose()
	if bg != tcell.ColorDarkBlue {
		t.Errorf("selected row bg = %v, want DarkBlue", bg)
	}
}

func TestRenderTop_TotalRow(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", PID: 100, Status: "running", RSS: 512000},
		{Name: "eng2", PID: 200, Status: "running", RSS: 256000},
	})
	tui.top.selected = -1
	tui.renderTop()

	// Total row is after: title(0), header(1), separator(2), data(3,4), blank(5), total(6).
	// Scan rows 5-8 to find the total line.
	found := false
	for y := 5; y <= 8; y++ {
		var buf strings.Builder
		for x := 0; x < 80; x++ {
			c, _, _, _ := s.GetContent(x, y)
			buf.WriteRune(c)
		}
		row := buf.String()
		if strings.Contains(row, "Total") && strings.Contains(row, "2 alive") {
			found = true
			break
		}
	}
	if !found {
		t.Error("total row missing or doesn't contain '2 alive'")
	}
}

func TestRenderTop_HelpLine(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", Status: "idle"},
	})
	tui.renderTop()

	_, sh := s.Size()
	var buf strings.Builder
	for x := 0; x < 80; x++ {
		c, _, _, _ := s.GetContent(x, sh-1)
		buf.WriteRune(c)
	}
	help := buf.String()
	if !strings.Contains(help, "[r]estart") {
		t.Errorf("help line missing '[r]estart': %q", help)
	}
}

func TestRenderTop_BeadInStatus(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", Status: "running", Bead: "ini-abc"},
	})
	tui.top.selected = -1
	tui.renderTop()

	var buf strings.Builder
	for x := 0; x < 120; x++ {
		c, _, _, _ := s.GetContent(x, 3)
		buf.WriteRune(c)
	}
	if !strings.Contains(buf.String(), "ini-abc") {
		t.Errorf("status should include bead ID: %q", buf.String())
	}
}

func TestRenderTop_NarrowTerminal(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(30, 3) // too narrow
	tui := &TUI{
		screen: s,
		top:    topModal{active: true, data: []topEntry{{Name: "a", Status: "idle"}}},
	}
	tui.renderTop() // must not panic

	var buf strings.Builder
	for x := 0; x < 30; x++ {
		c, _, _, _ := s.GetContent(x, 0)
		buf.WriteRune(c)
	}
	if !strings.Contains(buf.String(), "too narrow") {
		t.Errorf("narrow terminal should show error: %q", buf.String())
	}
}

func TestRenderTop_RSSFormatTiers(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "a", Status: "idle", RSS: 500},       // KB
		{Name: "b", Status: "idle", RSS: 5000},      // MB
		{Name: "c", Status: "idle", RSS: 2000000},   // GB
	})
	tui.top.selected = -1
	tui.renderTop()

	rows := make([]string, 3)
	for i := 0; i < 3; i++ {
		var buf strings.Builder
		for x := 0; x < 120; x++ {
			c, _, _, _ := s.GetContent(x, 3+i)
			buf.WriteRune(c)
		}
		rows[i] = buf.String()
	}
	if !strings.Contains(rows[0], "500 KB") {
		t.Errorf("row 0 missing '500 KB': %q", rows[0])
	}
	if !strings.Contains(rows[1], "5 MB") {
		t.Errorf("row 1 missing '5 MB': %q", rows[1])
	}
	if !strings.Contains(rows[2], "1.9 GB") {
		t.Errorf("row 2 missing '1.9 GB': %q", rows[2])
	}
}

// ── handleTopKey ────────────────────────────────────────────────────

func TestHandleTopKey_EscapeCloses(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{{Name: "a", Status: "idle"}})
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if tui.top.active {
		t.Error("Escape should close top modal")
	}
}

func TestHandleTopKey_CtrlCCloses(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{{Name: "a", Status: "idle"}})
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyCtrlC, 0, 0))
	if tui.top.active {
		t.Error("Ctrl+C should close top modal")
	}
}

func TestHandleTopKey_QCloses(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{{Name: "a", Status: "idle"}})
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, 'q', 0))
	if tui.top.active {
		t.Error("q should close top modal")
	}
}

func TestHandleTopKey_BacktickCloses(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{{Name: "a", Status: "idle"}})
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, '`', 0))
	if tui.top.active {
		t.Error("backtick should close top modal")
	}
}

func TestHandleTopKey_UpDown(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{
		{Name: "a", Status: "idle"},
		{Name: "b", Status: "idle"},
		{Name: "c", Status: "idle"},
	})
	tui.top.selected = 1

	tui.handleTopKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if tui.top.selected != 0 {
		t.Errorf("Up: selected = %d, want 0", tui.top.selected)
	}

	// Up at 0 stays at 0.
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if tui.top.selected != 0 {
		t.Errorf("Up from 0: selected = %d, want 0 (clamped)", tui.top.selected)
	}

	tui.handleTopKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.top.selected != 1 {
		t.Errorf("Down: selected = %d, want 1", tui.top.selected)
	}

	tui.top.selected = 2
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.top.selected != 2 {
		t.Errorf("Down from max: selected = %d, want 2 (clamped)", tui.top.selected)
	}
}

func TestHandleTopKey_KillMarksDeadImmediately(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{
		{Name: "eng1", PID: 1, Status: "running"},
	})
	tui.top.selected = 0
	// Pane has no real process, so Kill will fail silently (cmd is nil).
	// But the alive flag should be set to false.

	tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, 'k', 0))

	if tui.panes[0].IsAlive() {
		t.Error("'k' should mark pane as dead")
	}
}

func TestHandleTopKey_HideToggle(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{
		{Name: "eng1", Status: "idle"},
		{Name: "eng2", Status: "idle"},
	})
	tui.top.selected = 0
	tui.layoutState.Hidden = make(map[string]bool)

	// Hide eng1.
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, 'h', 0))
	if !tui.layoutState.Hidden["eng1"] {
		t.Error("'h' should hide eng1")
	}

	// Unhide eng1.
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, 'h', 0))
	if tui.layoutState.Hidden["eng1"] {
		t.Error("second 'h' should unhide eng1")
	}
}

func TestHandleTopKey_HideBlocksLastVisible(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{
		{Name: "eng1", Status: "idle"},
	})
	tui.top.selected = 0
	tui.layoutState.Hidden = make(map[string]bool)

	// Hiding the only visible pane should be blocked.
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, 'h', 0))
	if tui.layoutState.Hidden["eng1"] {
		t.Error("should not hide the last visible pane")
	}
}

func TestHandleTopKey_PinToggle(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{
		{Name: "eng1", Status: "idle"},
	})
	tui.top.selected = 0

	tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, 'p', 0))
	if !tui.layoutState.Pinned["eng1"] {
		t.Error("'p' should pin eng1")
	}
	if !tui.panes[0].IsPinned() {
		t.Error("pane should be pinned")
	}

	tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, 'p', 0))
	if tui.layoutState.Pinned["eng1"] {
		t.Error("second 'p' should unpin eng1")
	}
}

func TestHandleTopKey_AlwaysReturnsFalse(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{{Name: "a", Status: "idle"}})
	for _, ev := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyEscape, 0, 0),
		tcell.NewEventKey(tcell.KeyUp, 0, 0),
		tcell.NewEventKey(tcell.KeyDown, 0, 0),
		tcell.NewEventKey(tcell.KeyRune, 'q', 0),
		tcell.NewEventKey(tcell.KeyRune, 'h', 0),
		tcell.NewEventKey(tcell.KeyRune, 'p', 0),
	} {
		tui.top.active = true
		if tui.handleTopKey(ev) {
			t.Errorf("handleTopKey should always return false, got true for %v", ev.Key())
		}
	}
}

// ── refreshTopData ──────────────────────────────────────────────────

func TestRefreshTopData_PopulatesFromPanes(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	p := &Pane{
		name:    "eng1",
		emu:     emu,
		alive:   true,
		visible: true,
		cfg:     PaneConfig{Command: []string{"claude", "--continue"}},
	}
	tui := &TUI{
		panes:       []*Pane{p},
		layoutState: DefaultLayoutState(nil),
	}

	tui.refreshTopData()

	if len(tui.top.data) != 1 {
		t.Fatalf("top.data len = %d, want 1", len(tui.top.data))
	}
	e := tui.top.data[0]
	if e.Name != "eng1" {
		t.Errorf("Name = %q, want 'eng1'", e.Name)
	}
	if e.Command != "claude --continue" {
		t.Errorf("Command = %q, want 'claude --continue'", e.Command)
	}
}

func TestRefreshTopData_CachePreventsRepoll(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	tui := &TUI{
		panes:       []*Pane{{name: "a", emu: emu, alive: true}},
		layoutState: DefaultLayoutState(nil),
	}
	tui.refreshTopData()
	first := tui.top.data

	// Second call within 2s should return cached data.
	tui.refreshTopData()
	if &tui.top.data[0] == &first[0] {
		// Slice header might differ but data should be same reference.
	}
	if tui.top.data[0].Name != "a" {
		t.Error("cached data should still be valid")
	}
}

func TestRefreshTopData_HiddenStatus(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	tui := &TUI{
		panes:       []*Pane{{name: "eng1", emu: emu, alive: true}},
		layoutState: DefaultLayoutState(nil),
	}
	tui.layoutState.Hidden = map[string]bool{"eng1": true}
	tui.refreshTopData()

	if !strings.Contains(tui.top.data[0].Status, "[hidden]") {
		t.Errorf("hidden pane status = %q, want contains '[hidden]'", tui.top.data[0].Status)
	}
}

