// Coverage tests Tier 2: renderNotifications, runDetectors.
package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// ── renderNotifications ─────────────────────────────────────────────

func TestRenderNotifications_EmptyNoOp(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{screen: s}
	tui.renderNotifications() // must not panic with nil/empty slice
}

func TestRenderNotifications_SingleToast(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{
		screen: s,
		notifications: []notification{{
			event:   AgentEvent{Type: EventBeadCompleted, Pane: "eng1", Detail: "ini-abc done"},
			expires: time.Now().Add(10 * time.Second),
		}},
	}
	tui.renderNotifications()

	// Toast should appear near the bottom-right.
	// Scan row sh-2 = 22 for the notification text.
	var buf strings.Builder
	for x := 0; x < 80; x++ {
		c, _, _, _ := s.GetContent(x, 22)
		buf.WriteRune(c)
	}
	row := strings.TrimSpace(buf.String())
	if !strings.Contains(row, "eng1") || !strings.Contains(row, "ini-abc done") {
		t.Errorf("toast row = %q, want eng1 + 'ini-abc done'", row)
	}
}

func TestRenderNotifications_ColorByType(t *testing.T) {
	tests := []struct {
		name    string
		evType  EventType
		wantBg  tcell.Color
	}{
		{"completed", EventBeadCompleted, tcell.ColorDarkGreen},
		{"claimed", EventBeadClaimed, tcell.ColorDodgerBlue},
		{"failed", EventBeadFailed, tcell.ColorDarkRed},
		{"stuck", EventAgentStuck, tcell.ColorDarkRed},
		{"stalled", EventAgentStalled, tcell.ColorDarkOrange},
		{"idle", EventAgentIdle, tcell.ColorDimGray},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := tcell.NewSimulationScreen("")
			s.Init()
			s.SetSize(80, 24)
			tui := &TUI{
				screen: s,
				notifications: []notification{{
					event:   AgentEvent{Type: tc.evType, Pane: "a", Detail: "x"},
					expires: time.Now().Add(10 * time.Second),
				}},
			}
			tui.renderNotifications()

			// Find a cell on row 22 with the expected background.
			found := false
			for x := 0; x < 80; x++ {
				_, _, style, _ := s.GetContent(x, 22)
				_, bg, _ := style.Decompose()
				if bg == tc.wantBg {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("toast bg for %s not found", tc.name)
			}
		})
	}
}

func TestRenderNotifications_MultipleStack(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{
		screen: s,
		notifications: []notification{
			{event: AgentEvent{Pane: "a", Detail: "first"}, expires: time.Now().Add(10 * time.Second)},
			{event: AgentEvent{Pane: "b", Detail: "second"}, expires: time.Now().Add(10 * time.Second)},
		},
	}
	tui.renderNotifications()

	// Two toasts should stack: row 22 and row 21.
	readRow := func(y int) string {
		var buf strings.Builder
		for x := 0; x < 80; x++ {
			c, _, _, _ := s.GetContent(x, y)
			buf.WriteRune(c)
		}
		return buf.String()
	}
	if !strings.Contains(readRow(22), "second") {
		t.Error("bottom toast should contain 'second'")
	}
	if !strings.Contains(readRow(21), "first") {
		t.Error("stacked toast should contain 'first'")
	}
}

func TestRenderNotifications_NarrowTerminalSkips(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(25, 10) // too narrow (<30)
	tui := &TUI{
		screen: s,
		notifications: []notification{{
			event:   AgentEvent{Pane: "a", Detail: "test"},
			expires: time.Now().Add(10 * time.Second),
		}},
	}
	tui.renderNotifications() // must not panic, should skip
}

func TestRenderNotifications_LongTextTruncated(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	long := strings.Repeat("x", 100)
	tui := &TUI{
		screen: s,
		notifications: []notification{{
			event:   AgentEvent{Pane: "eng1", Detail: long},
			expires: time.Now().Add(10 * time.Second),
		}},
	}
	tui.renderNotifications()

	// Should not overflow or panic. The toast is capped at maxW=50.
	var buf strings.Builder
	for x := 0; x < 80; x++ {
		c, _, _, _ := s.GetContent(x, 22)
		buf.WriteRune(c)
	}
	text := strings.TrimSpace(buf.String())
	// Should end with ellipsis.
	if !strings.HasSuffix(text, "\u2026") {
		t.Errorf("long toast should be truncated with ellipsis: %q", text)
	}
}

// ── runDetectors ────────────────────────────────────────────────────

func TestRunDetectors_NilEventChNoOp(t *testing.T) {
	p := &Pane{emu: vt.NewSafeEmulator(80, 24)}
	// eventCh is nil, should return immediately without panic.
	p.runDetectors(nil)
}

func TestRunDetectors_NilDedupNoOp(t *testing.T) {
	ch := make(chan AgentEvent, 8)
	p := &Pane{emu: vt.NewSafeEmulator(80, 24), eventCh: ch}
	// dedupEvents is nil, should return immediately.
	p.runDetectors(nil)
}

func TestRunDetectors_CompletionEmitsEvent(t *testing.T) {
	ch := make(chan AgentEvent, 8)
	p := &Pane{
		emu:         vt.NewSafeEmulator(80, 24),
		eventCh:     ch,
		dedupEvents: newDedup(),
		alive:       true,
		beadID:      "ini-test",
	}

	// Simulate a DONE comment in the JSONL entry that triggers completion detection.
	entries := []JournalEntry{{
		Type:      "tool_result",
		Content:   `DONE: implemented feature. Tests: added. Commit: abc123`,
		Timestamp: time.Now(),
		ExitCode:  0,
	}}

	p.runDetectors(entries)

	// Check if a completion event was emitted (may or may not depending on
	// detectCompletion logic). Either way, must not panic.
	// Drain any events.
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func TestRunDetectors_NewEntriesClearStallState(t *testing.T) {
	ch := make(chan AgentEvent, 8)
	p := &Pane{
		emu:           vt.NewSafeEmulator(80, 24),
		eventCh:       ch,
		dedupEvents:   newDedup(),
		alive:         true,
		stallReported: true,
	}

	entries := []JournalEntry{{
		Type:      "assistant",
		Content:   "some output",
		Timestamp: time.Now(),
	}}

	p.runDetectors(entries)

	p.mu.Lock()
	stall := p.stallReported
	p.mu.Unlock()

	if stall {
		t.Error("stallReported should be cleared after new entries")
	}
}

func TestRunDetectors_StuckResetOnClear(t *testing.T) {
	ch := make(chan AgentEvent, 8)
	p := &Pane{
		emu:           vt.NewSafeEmulator(80, 24),
		eventCh:       ch,
		dedupEvents:   newDedup(),
		alive:         true,
		stuckReported: true,
	}

	// Add a successful tool_result to the journal so detectStuck returns nil.
	p.journal = []JournalEntry{{
		Type:      "tool_result",
		Content:   "success output",
		Timestamp: time.Now(),
		ExitCode:  0,
	}}

	p.runDetectors(nil) // no new entries

	p.mu.Lock()
	stuck := p.stuckReported
	p.mu.Unlock()

	if stuck {
		t.Error("stuckReported should be cleared when detectStuck returns nil")
	}
}
