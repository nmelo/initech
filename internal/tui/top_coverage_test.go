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
		panes:       toPaneViews(panes),
		layoutState: DefaultLayoutState(nil),
		top:         topModal{active: true, data: entries, cacheTime: time.Now()},
	}
	return tui, s
}

// readAllScreen reads the full screen text as rows.
func readAllScreen(s tcell.SimulationScreen) []string {
	sw, sh := s.Size()
	rows := make([]string, sh)
	for y := 0; y < sh; y++ {
		var b strings.Builder
		for x := 0; x < sw; x++ {
			c, _, _ := s.Get(x, y)
			b.WriteString(c)
		}
		rows[y] = b.String()
	}
	return rows
}

// findRow returns the first row containing substr, or -1.
func findRow(rows []string, substr string) int {
	for i, r := range rows {
		if strings.Contains(r, substr) {
			return i
		}
	}
	return -1
}

// ── renderTop ───────────────────────────────────────────────────────

func TestRenderTop_TitleVisible(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", PID: 123, Status: "running", Command: "claude"},
	})
	tui.renderTop()

	rows := readAllScreen(s)
	if findRow(rows, "initech top") < 0 {
		t.Error("title 'initech top' not found on screen")
	}
}

func TestRenderTop_TitleGreenWhenRunning(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", PID: 100, Status: "running"},
	})
	if lp, ok := tui.panes[0].(*Pane); ok {
		lp.mu.Lock()
		lp.alive = true
		lp.lastOutputTime = time.Now()
		lp.mu.Unlock()
	}
	tui.renderTop()

	rows := readAllScreen(s)
	titleRow := findRow(rows, "initech top")
	if titleRow < 0 {
		t.Fatal("title not found")
	}
	// Find the 'i' of 'initech' and check its bg color.
	sw, _ := s.Size()
	for x := 0; x < sw; x++ {
		c, style, _ := s.Get(x, titleRow)
		if c == "i" {
			_, bg, _ := style.Decompose()
			if bg == tcell.ColorDarkGreen {
				return // pass
			}
		}
	}
	t.Error("title bg with running agent should be DarkGreen")
}

func TestRenderTop_TitleBlueWhenAllIdle(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", PID: 100, Status: "idle"},
	})
	if lp, ok := tui.panes[0].(*Pane); ok {
		lp.mu.Lock()
		lp.alive = true
		lp.activity = StateIdle
		lp.lastOutputTime = time.Time{}
		lp.mu.Unlock()
	}
	tui.renderTop()

	rows := readAllScreen(s)
	titleRow := findRow(rows, "initech top")
	if titleRow < 0 {
		t.Fatal("title not found")
	}
	sw, _ := s.Size()
	for x := 0; x < sw; x++ {
		c, style, _ := s.Get(x, titleRow)
		if c == "i" {
			_, bg, _ := style.Decompose()
			if bg == tcell.ColorDodgerBlue {
				return
			}
		}
	}
	t.Error("title bg with all idle should be DodgerBlue")
}

func TestRenderTop_HeaderRow(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", Status: "idle"},
	})
	tui.renderTop()

	rows := readAllScreen(s)
	for _, hdr := range []string{"AGENT", "PID", "PROCESS", "COMMAND", "RSS", "STATUS"} {
		if findRow(rows, hdr) < 0 {
			t.Errorf("header %q not found on screen", hdr)
		}
	}
}

func TestRenderTop_AgentRowRendered(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "super", PID: 100, Status: "running", Command: "claude --continue", RSS: 2048, Comm: "claude"},
	})
	tui.top.selected = -1
	tui.renderTop()

	rows := readAllScreen(s)
	if findRow(rows, "super") < 0 {
		t.Error("agent 'super' not found on screen")
	}
	if findRow(rows, "100") < 0 {
		t.Error("PID '100' not found on screen")
	}
	if findRow(rows, "2 MB") < 0 {
		t.Error("RSS '2 MB' not found on screen")
	}
}

func TestRenderTop_DeadStyleApplied(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", Status: "dead"},
	})
	tui.top.selected = -1
	tui.renderTop()

	rows := readAllScreen(s)
	agentRow := findRow(rows, "eng1")
	if agentRow < 0 {
		t.Fatal("agent row not found")
	}
	sw, _ := s.Size()
	for x := 0; x < sw; x++ {
		c, style, _ := s.Get(x, agentRow)
		if c == "e" {
			fg, _, _ := style.Decompose()
			if fg == tcell.ColorRed {
				return
			}
		}
	}
	t.Error("dead row fg should be Red")
}

func TestRenderTop_SuspendedStyleApplied(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng2", Status: "suspended"},
	})
	tui.top.selected = -1
	tui.renderTop()

	rows := readAllScreen(s)
	agentRow := findRow(rows, "eng2")
	if agentRow < 0 {
		t.Fatal("agent row not found")
	}
	sw, _ := s.Size()
	for x := 0; x < sw; x++ {
		c, style, _ := s.Get(x, agentRow)
		if c == "e" {
			fg, _, _ := style.Decompose()
			if fg == tcell.ColorDodgerBlue {
				return
			}
		}
	}
	t.Error("suspended row fg should be DodgerBlue")
}

func TestRenderTop_SelectedStyleApplied(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", Status: "idle"},
		{Name: "eng2", Status: "running"},
	})
	tui.top.selected = 1
	tui.renderTop()

	rows := readAllScreen(s)
	agentRow := findRow(rows, "eng2")
	if agentRow < 0 {
		t.Fatal("selected agent row not found")
	}
	sw, _ := s.Size()
	for x := 0; x < sw; x++ {
		_, style, _ := s.Get(x, agentRow)
		_, bg, _ := style.Decompose()
		if bg == tcell.ColorDarkBlue {
			return
		}
	}
	t.Error("selected row bg should be DarkBlue")
}

func TestRenderTop_TotalRow(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", PID: 100, Status: "running", RSS: 512000},
		{Name: "eng2", PID: 200, Status: "running", RSS: 256000},
	})
	tui.top.selected = -1
	tui.renderTop()

	rows := readAllScreen(s)
	if findRow(rows, "2 alive") < 0 {
		t.Error("total row missing '2 alive'")
	}
}

func TestRenderTop_HelpLine(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", Status: "idle"},
	})
	tui.renderTop()

	rows := readAllScreen(s)
	if findRow(rows, "[r]estart") < 0 {
		t.Error("help line missing '[r]estart'")
	}
}

func TestRenderTop_BeadInStatus(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", Status: "running", Bead: "ini-abc"},
	})
	tui.top.selected = -1
	tui.renderTop()

	// Bead info no longer in top modal status column (shown in pane ribbon).
	// Just verify the agent row renders without the bead.
	rows := readAllScreen(s)
	if findRow(rows, "eng1") < 0 {
		t.Error("agent row not found")
	}
}

func TestRenderTop_NarrowTerminal(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(30, 5)
	tui := &TUI{
		screen: s,
		top:    topModal{active: true, data: []topEntry{{Name: "a", Status: "idle"}}},
	}
	tui.renderTop()

	rows := readAllScreen(s)
	if findRow(rows, "too small") < 0 {
		t.Error("narrow terminal should show 'too small' error")
	}
}

func TestRenderTop_RSSFormatTiers(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "a", Status: "idle", RSS: 500},     // KB
		{Name: "b", Status: "idle", RSS: 5000},    // MB
		{Name: "c", Status: "idle", RSS: 2000000}, // GB
	})
	tui.top.selected = -1
	tui.renderTop()

	rows := readAllScreen(s)
	if findRow(rows, "500 KB") < 0 {
		t.Error("missing '500 KB'")
	}
	if findRow(rows, "5 MB") < 0 {
		t.Error("missing '5 MB'")
	}
	if findRow(rows, "1.9 GB") < 0 {
		t.Error("missing '1.9 GB'")
	}
}

// ── handleTopKey ────────────────────────────────────────────────────

func TestHandleTopKey_EscapeCloses(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{{Name: "eng1", Status: "idle"}})
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if tui.top.active {
		t.Error("Esc should close top")
	}
}

func TestHandleTopKey_QCloses(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{{Name: "eng1", Status: "idle"}})
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone))
	if tui.top.active {
		t.Error("q should close top")
	}
}

func TestHandleTopKey_BacktickCloses(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{{Name: "eng1", Status: "idle"}})
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, '`', tcell.ModNone))
	if tui.top.active {
		t.Error("backtick should close top")
	}
}

func TestHandleTopKey_ArrowNavigation(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{
		{Name: "eng1", Status: "idle"},
		{Name: "eng2", Status: "idle"},
	})
	tui.top.selected = 0
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if tui.top.selected != 1 {
		t.Errorf("Down: selected = %d, want 1", tui.top.selected)
	}
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	if tui.top.selected != 0 {
		t.Errorf("Up: selected = %d, want 0", tui.top.selected)
	}
}

func TestHandleTopKey_ArrowBounds(t *testing.T) {
	tui, _ := topTestTUI([]topEntry{
		{Name: "eng1", Status: "idle"},
	})
	tui.top.selected = 0
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	if tui.top.selected != 0 {
		t.Errorf("Up at 0: selected = %d, want 0", tui.top.selected)
	}
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if tui.top.selected != 0 {
		t.Errorf("Down at max: selected = %d, want 0", tui.top.selected)
	}
}

// ── renderTop as floating modal ─────────────────────────────────────

func TestRenderTop_IsFloatingModal(t *testing.T) {
	tui, s := topTestTUI([]topEntry{
		{Name: "eng1", Status: "idle"},
	})
	tui.renderTop()

	// Verify the box border exists (corner characters).
	rows := readAllScreen(s)
	foundCorner := false
	for _, r := range rows {
		if strings.ContainsRune(r, '\u250c') { // top-left corner
			foundCorner = true
			break
		}
	}
	if !foundCorner {
		t.Error("floating modal missing box-drawing border")
	}
}
