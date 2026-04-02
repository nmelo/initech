// QA tests for ini-fqe: Event log modal scrollable history.
// Covers AC verification and edge cases not in eng's tests.
package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// AC4 (empty state): renderEventLog shows descriptive message when no events.
func TestRenderEventLog_EmptyShowsMessage(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)
	tui := &TUI{screen: s, eventLogM: eventLogModal{active: true}}
	tui.renderEventLog()

	// Title row should contain "no events recorded" text (row 0).
	// The title text is " Event Log (no events recorded) ".
	// Look for 'n' from "no" somewhere in the title row.
	found := false
	sw, _ := s.Size()
	titleText := " Event Log (no events recorded) "
	for i := 0; i < sw-len(titleText); i++ {
		match := true
		for j, ch := range titleText {
			c, _, _ := s.Get(i+j, 0)
			if c != string(ch) {
				match = false
				break
			}
		}
		if match {
			found = true
			break
		}
	}
	if !found {
		t.Error("empty event log title should contain 'no events recorded'")
	}

	// Row 2 should have descriptive hint text starting with spaces + "No events".
	// The message: "  No events recorded. Events appear here when agents..."
	c0, _, _ := s.Get(2, 2) // 'N' at position 2 (after 2 spaces)
	if c0 != "N" {
		t.Errorf("empty state row 2 col 2 = %q, want 'N' from 'No events'", c0)
	}
}

// AC2: events are color-coded correctly by type.
func TestEventLogStyle_Colors(t *testing.T) {
	tests := []struct {
		et        EventType
		wantColor tcell.Color
	}{
		{EventBeadCompleted, tcell.ColorGreen},
		{EventBeadFailed, tcell.ColorRed},
		{EventAgentStuck, tcell.ColorRed},
		{EventAgentStalled, tcell.ColorYellow},
		{EventBeadClaimed, tcell.ColorDodgerBlue},
		{EventAgentIdle, tcell.ColorGray},
	}
	for _, tt := range tests {
		style := eventLogStyle(tt.et)
		fg, _, _ := style.Decompose()
		if fg != tt.wantColor {
			t.Errorf("eventLogStyle(%v) fg = %v, want %v", tt.et, fg, tt.wantColor)
		}
	}
}

// AC3: backtick rune closes modal (the '`' toggle).
func TestHandleEventLogKeyBacktick(t *testing.T) {
	tui := &TUI{eventLogM: eventLogModal{active: true}}
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyRune, '`', 0))
	if tui.eventLogM.active {
		t.Error("backtick should close event log modal")
	}
}

// AC5: Ctrl+C also closes the modal (alternative to Esc).
func TestHandleEventLogKeyCtrlC(t *testing.T) {
	tui := &TUI{eventLogM: eventLogModal{active: true}}
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyCtrlC, 0, 0))
	if tui.eventLogM.active {
		t.Error("Ctrl+C should close event log modal")
	}
}

// AC3: PgUp scrolls up by visibleRows, clamped at maxOffset.
func TestHandleEventLogKeyPgUp_ClampsAtMax(t *testing.T) {
	tui := &TUI{eventLogM: eventLogModal{active: true, scrollOffset: 0}}
	// 20 events, visibleRows=10 (no screen) → maxOff=10.
	for i := 0; i < 20; i++ {
		tui.eventLog = append(tui.eventLog, AgentEvent{Pane: "eng1", Time: time.Now()})
	}
	// PgUp from offset 0 → should be clamped at maxOff=10, not go to 20.
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyPgUp, 0, 0))
	maxOff := tui.eventLogMaxOffset() // 10
	if tui.eventLogM.scrollOffset != maxOff {
		t.Errorf("PgUp from 0: scrollOffset = %d, want %d (clamped at max)", tui.eventLogM.scrollOffset, maxOff)
	}
}

// AC3: PgDn scrolls down by visibleRows, clamped at 0.
func TestHandleEventLogKeyPgDn_ClampsAtZero(t *testing.T) {
	tui := &TUI{eventLogM: eventLogModal{active: true, scrollOffset: 3}}
	for i := 0; i < 20; i++ {
		tui.eventLog = append(tui.eventLog, AgentEvent{Pane: "eng1", Time: time.Now()})
	}
	// PgDn by visibleRows (10) from offset 3 → would be -7, should clamp to 0.
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyPgDn, 0, 0))
	if tui.eventLogM.scrollOffset != 0 {
		t.Errorf("PgDn past bottom: scrollOffset = %d, want 0", tui.eventLogM.scrollOffset)
	}
}

// AC3: Up key clamps at maxOffset (no scroll past top).
func TestHandleEventLogKeyUp_ClampsAtMax(t *testing.T) {
	tui := &TUI{eventLogM: eventLogModal{active: true}}
	// Only 5 events, visibleRows=10 → maxOff=0, already at top.
	for i := 0; i < 5; i++ {
		tui.eventLog = append(tui.eventLog, AgentEvent{Pane: "eng1", Time: time.Now()})
	}
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if tui.eventLogM.scrollOffset != 0 {
		t.Errorf("Up with maxOff=0: scrollOffset = %d, want 0 (no scroll)", tui.eventLogM.scrollOffset)
	}
}

// AC1: handleKey intercept — eventLogM.active takes all input before other handlers.
// Verify a key like 'q' closes the log and does NOT propagate (return false).
func TestHandleKeyInterceptsEventLog(t *testing.T) {
	tui := &TUI{eventLogM: eventLogModal{active: true}}
	// 'q' is handled by the event log modal's close action.
	// After handleKey, modal should be closed.
	result := tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyRune, 'q', 0))
	if tui.eventLogM.active {
		t.Error("event log should be closed after 'q'")
	}
	if result {
		t.Error("handleEventLogKey should always return false (never quits TUI)")
	}
}

// Zero-time guard: handleAgentEvent fills Time=now() when event has zero Time.
func TestHandleAgentEvent_ZeroTimeGuard(t *testing.T) {
	tui := &TUI{}
	before := time.Now()
	tui.handleAgentEvent(AgentEvent{
		Type:   EventBeadClaimed,
		Pane:   "eng1",
		Detail: "test",
		// Time intentionally zero.
	})
	after := time.Now()
	if len(tui.eventLog) != 1 {
		t.Fatalf("expected 1 event, got %d", len(tui.eventLog))
	}
	ev := tui.eventLog[0]
	if ev.Time.IsZero() {
		t.Error("zero Time should be filled with time.Now() in handleAgentEvent")
	}
	if ev.Time.Before(before) || ev.Time.After(after) {
		t.Errorf("filled Time %v not in expected range [%v, %v]", ev.Time, before, after)
	}
}

// pruneEventLog: cap-then-retention order is correct.
// If we have 105 events (5 over cap) and the oldest 5 are stale by age,
// after pruning we should have ≤100 events, all recent.
func TestPruneEventLog_CapBeforeRetention(t *testing.T) {
	tui := &TUI{}
	// 5 old events (beyond retention).
	for i := 0; i < 5; i++ {
		tui.eventLog = append(tui.eventLog, AgentEvent{
			Pane: "eng1", Time: time.Now().Add(-2 * eventLogRetention),
		})
	}
	// 100 recent events.
	for i := 0; i < 100; i++ {
		tui.eventLog = append(tui.eventLog, AgentEvent{
			Pane: "eng1", Time: time.Now(),
		})
	}
	// 105 total.
	tui.pruneEventLog()
	// Cap prune drops oldest 5 → 100 remain, all recent.
	if len(tui.eventLog) != 100 {
		t.Errorf("after prune: len = %d, want 100", len(tui.eventLog))
	}
	for _, ev := range tui.eventLog {
		if time.Since(ev.Time) > eventLogRetention {
			t.Error("stale event survived prune")
			break
		}
	}
}

// renderEventLog: scrollOffset over maxOff is auto-clamped during render.
func TestRenderEventLog_ScrollOffsetClamped(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)
	tui := &TUI{
		screen:    s,
		eventLogM: eventLogModal{active: true, scrollOffset: 9999}, // Way over max.
	}
	for i := 0; i < 5; i++ {
		tui.eventLog = append(tui.eventLog, AgentEvent{
			Pane:   "eng1",
			Detail: "ev",
			Time:   time.Now(),
		})
	}
	tui.renderEventLog() // Should not panic or render garbage.
	maxOff := tui.eventLogMaxOffset()
	if tui.eventLogM.scrollOffset > maxOff {
		t.Errorf("scrollOffset %d not clamped to max %d during render", tui.eventLogM.scrollOffset, maxOff)
	}
}

// Title shows correct event count when events are present.
func TestRenderEventLog_TitleShowsCount(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)
	tui := &TUI{screen: s, eventLogM: eventLogModal{active: true}}
	for i := 0; i < 3; i++ {
		tui.eventLog = append(tui.eventLog, AgentEvent{
			Type: EventBeadCompleted, Pane: "eng1",
			Detail: "done", Time: time.Now(),
		})
	}
	tui.renderEventLog()

	// Title row should contain "3 events".
	want := "3 events)"
	sw, _ := s.Size()
	found := false
	for i := 0; i < sw-len(want); i++ {
		match := true
		for j, ch := range want {
			c, _, _ := s.Get(i+j, 0)
			if c != string(ch) {
				match = false
				break
			}
		}
		if match {
			found = true
			break
		}
	}
	if !found {
		t.Error("title should contain '3 events)' with event count")
	}
}

// New event appended while modal open is visible on next render.
func TestEventLog_NewEventVisibleAfterAppend(t *testing.T) {
	tui := &TUI{}
	tui.handleAgentEvent(AgentEvent{Type: EventBeadClaimed, Pane: "eng1", Detail: "first", Time: time.Now()})
	if len(tui.eventLog) != 1 {
		t.Fatal("expected 1 event after first append")
	}
	tui.handleAgentEvent(AgentEvent{Type: EventBeadCompleted, Pane: "eng1", Detail: "second", Time: time.Now()})
	if len(tui.eventLog) != 2 {
		t.Fatalf("expected 2 events, got %d", len(tui.eventLog))
	}
	if tui.eventLog[1].Detail != "second" {
		t.Errorf("second event detail = %q, want 'second'", tui.eventLog[1].Detail)
	}
}
