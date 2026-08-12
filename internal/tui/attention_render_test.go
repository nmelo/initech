package tui

// attention_render_test.go covers the needs-input list as the TUI actually
// draws it, on a real SimulationScreen (ini-2x8.1).
//
// A SimulationScreen with an EXPLICIT size is required: a nil screen reports
// 0,0, which silently zeroes every width calculation and makes layout
// assertions pass for the wrong reason.

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// simTUI builds a TUI over a SimulationScreen of the given size.
func simTUI(t *testing.T, w, h int, panes ...*Pane) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatalf("init simulation screen: %v", err)
	}
	t.Cleanup(s.Fini)
	s.SetSize(w, h)

	tu := newTestTUI(panes...)
	tu.screen = s
	return tu, s
}

// screenRows renders the simulation screen's cells back into strings.
func screenRows(t *testing.T, s tcell.SimulationScreen) []string {
	t.Helper()
	cells, w, h := s.GetContents()
	rows := make([]string, 0, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			r := cells[y*w+x].Runes
			if len(r) == 0 {
				b.WriteRune(' ')
				continue
			}
			b.WriteRune(r[0])
		}
		rows = append(rows, strings.TrimRight(b.String(), " "))
	}
	return rows
}

// TestRenderAttention_DrawsNothingWhenNobodyWaits is the negative control, and
// it is the AC that matters most: a no-waiting session must render exactly as it
// did before this feature existed.
func TestRenderAttention_DrawsNothingWhenNobodyWaits(t *testing.T) {
	tu, s := simTUI(t, 100, 30, testPane("eng1"), testPane("super"))

	tu.renderAttention()
	s.Show()

	for y, row := range screenRows(t, s) {
		if row != "" {
			t.Errorf("row %d is not empty with nobody waiting: %q", y, row)
		}
	}
}

func TestRenderAttention_DrawsTheBoxWhenAnAgentWaits(t *testing.T) {
	p := testPane("super")
	p.SetWaitingInput("ship v2.6.0 now, or wait?")
	tu, s := simTUI(t, 100, 30, p)

	tu.renderAttention()
	s.Show()

	rows := screenRows(t, s)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "needs input") {
		t.Fatalf("box title missing:\n%s", joined)
	}
	if !strings.Contains(joined, "super") || !strings.Contains(joined, "ship v2.6.0 now, or wait?") {
		t.Errorf("row content missing:\n%s", joined)
	}
	// Top-left, not floating in the middle.
	if rows[0] != "" {
		t.Errorf("expected row 0 clear (box starts at y=1), got %q", rows[0])
	}
	if !strings.HasPrefix(rows[1], " ┌") {
		t.Errorf("box does not start at x=1 on row 1: %q", rows[1])
	}
}

// TestRenderAttention_ClearsWithinOneRenderCycle is the "answering the agent
// removes it with no manual dismissal" AC, at the level where it is true.
func TestRenderAttention_ClearsWithinOneRenderCycle(t *testing.T) {
	p := testPane("pm")
	p.SetWaitingInput("stripe or paypal first?")
	tu, s := simTUI(t, 100, 30, p)

	tu.renderAttention()
	s.Show()
	if !strings.Contains(strings.Join(screenRows(t, s), "\n"), "needs input") {
		t.Fatal("setup failed: box was never drawn")
	}

	// The agent is answered.
	p.ClearWaitingInput()
	s.Clear()
	tu.renderAttention()
	s.Show()

	for y, row := range screenRows(t, s) {
		if row != "" {
			t.Errorf("row %d still drawn one cycle after the agent was answered: %q", y, row)
		}
	}
}

func TestRenderAttention_ShowsEveryWaitingAgentOldestFirst(t *testing.T) {
	// Distinct names, distinct questions, distinct durations: truncation,
	// cross-wiring, and mis-sort each fail differently here.
	now := time.Now()
	super := testPane("super")
	super.SetWaitingInput("ship v2.6.0 now, or wait?")
	eng1 := testPane("eng1")
	eng1.SetWaitingInput("Bash: rm -rf build/")
	qa4 := testPane("qa4")
	qa4.SetWaitingInput("dialog detected")

	// Force distinct, known wait ages.
	super.mu.Lock()
	super.waitingSince = now.Add(-242 * time.Second)
	super.mu.Unlock()
	eng1.mu.Lock()
	eng1.waitingSince = now.Add(-45 * time.Second)
	eng1.mu.Unlock()
	qa4.mu.Lock()
	qa4.waitingSince = now.Add(-12 * time.Second)
	qa4.mu.Unlock()

	// Added to the TUI newest-first so a missing sort cannot pass by accident.
	tu, s := simTUI(t, 120, 30, qa4, eng1, super)
	tu.renderAttention()
	s.Show()

	rows := screenRows(t, s)
	var order []string
	for _, r := range rows {
		for _, name := range []string{"super", "eng1", "qa4"} {
			if strings.Contains(r, name+" ") || strings.Contains(r, name+"  ") {
				order = append(order, name)
				break
			}
		}
	}
	want := []string{"super", "eng1", "qa4"}
	if len(order) != 3 {
		t.Fatalf("expected 3 agent rows, found %v in:\n%s", order, strings.Join(rows, "\n"))
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("row order = %v, want %v (oldest wait on top)\n%s", order, want, strings.Join(rows, "\n"))
		}
	}

	joined := strings.Join(rows, "\n")
	for _, q := range []string{"ship v2.6.0 now, or wait?", "Bash: rm -rf build/", "dialog detected"} {
		if !strings.Contains(joined, q) {
			t.Errorf("question missing or cross-wired: %q not in\n%s", q, joined)
		}
	}
	for _, d := range []string{"4m02s", "45s", "12s"} {
		if !strings.Contains(joined, d) {
			t.Errorf("duration missing or cross-wired: %q not in\n%s", d, joined)
		}
	}
}

// TestRenderAttention_SurvivesAScreenTooShortForTheBox guards the clipping path:
// a fleet with more waiting agents than the terminal has rows must not panic or
// write outside the screen.
func TestRenderAttention_SurvivesAScreenTooShortForTheBox(t *testing.T) {
	var panes []*Pane
	for i := 0; i < 20; i++ {
		p := testPane("agent" + itoa(i))
		p.SetWaitingInput("question " + itoa(i) + "?")
		panes = append(panes, p)
	}
	tu, s := simTUI(t, 60, 6, panes...)

	tu.renderAttention()
	s.Show()

	rows := screenRows(t, s)
	if len(rows) != 6 {
		t.Fatalf("screen height changed: got %d rows", len(rows))
	}
	if !strings.Contains(strings.Join(rows, "\n"), "needs input") {
		t.Error("box not drawn at all on a short screen")
	}
}

// TestWaitingRows_SkipsAgentsThatAreNotWaiting keeps the collector honest: the
// list is built from state, not from "every pane we know about".
func TestWaitingRows_SkipsAgentsThatAreNotWaiting(t *testing.T) {
	waiting := testPane("super")
	waiting.SetWaitingInput("a real question")
	quiet := testPane("eng1")
	busy := testPane("eng2")

	tu, _ := simTUI(t, 100, 30, waiting, quiet, busy)

	rows := tu.waitingRows()
	if len(rows) != 1 {
		t.Fatalf("waitingRows() returned %d rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].Name != "super" {
		t.Errorf("wrong agent surfaced: %q", rows[0].Name)
	}
}
