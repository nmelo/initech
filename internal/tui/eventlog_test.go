package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ── Event log append and retention ───────────────────────────────────

func TestEventLogAppendAndCap(t *testing.T) {
	tui := &TUI{}
	// Add 110 events (10 more than the cap).
	for i := 0; i < 110; i++ {
		tui.handleAgentEvent(AgentEvent{
			Type:   EventBeadClaimed,
			Pane:   "eng1",
			Detail: fmt.Sprintf("event %d", i),
			Time:   time.Now(),
		})
	}
	if len(tui.eventLog) != maxEventLog {
		t.Errorf("eventLog len = %d, want %d (cap)", len(tui.eventLog), maxEventLog)
	}
	// Oldest events should be dropped; last event should be event 109.
	last := tui.eventLog[len(tui.eventLog)-1]
	if last.Detail != "event 109" {
		t.Errorf("last event = %q, want 'event 109'", last.Detail)
	}
}

func TestEventLogRetentionPrunesOldEvents(t *testing.T) {
	tui := &TUI{}
	old := AgentEvent{
		Type:   EventBeadClaimed,
		Pane:   "eng1",
		Detail: "old event",
		Time:   time.Now().Add(-2 * eventLogRetention),
	}
	recent := AgentEvent{
		Type:   EventBeadCompleted,
		Pane:   "eng1",
		Detail: "recent event",
		Time:   time.Now(),
	}
	tui.eventLog = append(tui.eventLog, old, recent)
	tui.pruneEventLog()

	if len(tui.eventLog) != 1 {
		t.Fatalf("eventLog len = %d, want 1", len(tui.eventLog))
	}
	if tui.eventLog[0].Detail != "recent event" {
		t.Errorf("surviving event = %q, want 'recent event'", tui.eventLog[0].Detail)
	}
}

func TestEventLogRetentionKeepsFreshEvents(t *testing.T) {
	tui := &TUI{}
	for i := 0; i < 5; i++ {
		tui.handleAgentEvent(AgentEvent{
			Type:   EventBeadClaimed,
			Pane:   "eng1",
			Detail: fmt.Sprintf("event %d", i),
			Time:   time.Now(),
		})
	}
	if len(tui.eventLog) != 5 {
		t.Errorf("eventLog len = %d, want 5", len(tui.eventLog))
	}
}

// notifications are still capped at maxNotifications independent of eventLog
func TestEventLogAndNotificationsAreIndependent(t *testing.T) {
	tui := &TUI{}
	for i := 0; i < 10; i++ {
		tui.handleAgentEvent(AgentEvent{
			Type:   EventBeadClaimed,
			Pane:   "eng1",
			Detail: fmt.Sprintf("event %d", i),
		})
	}
	if len(tui.notifications) != maxNotifications {
		t.Errorf("notifications = %d, want %d", len(tui.notifications), maxNotifications)
	}
	if len(tui.eventLog) != 10 {
		t.Errorf("eventLog = %d, want 10", len(tui.eventLog))
	}
}

// ── Scroll helpers ────────────────────────────────────────────────────

func TestEventLogMaxOffset(t *testing.T) {
	tui := &TUI{}
	// No screen, so visibleRows = 10 (fallback).
	// With 5 events and 10 visible rows: max offset = 0.
	for i := 0; i < 5; i++ {
		tui.eventLog = append(tui.eventLog, AgentEvent{Pane: "eng1", Time: time.Now()})
	}
	if got := tui.eventLogMaxOffset(); got != 0 {
		t.Errorf("maxOffset = %d, want 0 (fewer events than visible rows)", got)
	}

	// With 20 events and 10 visible rows: max offset = 10.
	tui.eventLog = nil
	for i := 0; i < 20; i++ {
		tui.eventLog = append(tui.eventLog, AgentEvent{Pane: "eng1", Time: time.Now()})
	}
	if got := tui.eventLogMaxOffset(); got != 10 {
		t.Errorf("maxOffset = %d, want 10", got)
	}
}

// ── Key handling ──────────────────────────────────────────────────────

func TestHandleEventLogKeyEscape(t *testing.T) {
	tui := &TUI{eventLogM: eventLogModal{active: true}}
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if tui.eventLogM.active {
		t.Error("Esc should close event log modal")
	}
}

func TestHandleEventLogKeyQRune(t *testing.T) {
	tui := &TUI{eventLogM: eventLogModal{active: true}}
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyRune, 'q', 0))
	if tui.eventLogM.active {
		t.Error("q should close event log modal")
	}
}

func TestHandleEventLogKeyScrollUp(t *testing.T) {
	tui := &TUI{eventLogM: eventLogModal{active: true}}
	// Add enough events to allow scrolling (more than visibleRows=10).
	for i := 0; i < 20; i++ {
		tui.eventLog = append(tui.eventLog, AgentEvent{Pane: "eng1", Time: time.Now()})
	}
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if tui.eventLogM.scrollOffset != 1 {
		t.Errorf("scrollOffset after Up = %d, want 1", tui.eventLogM.scrollOffset)
	}
	// k rune also scrolls up.
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyRune, 'k', 0))
	if tui.eventLogM.scrollOffset != 2 {
		t.Errorf("scrollOffset after k = %d, want 2", tui.eventLogM.scrollOffset)
	}
}

func TestHandleEventLogKeyScrollDown(t *testing.T) {
	tui := &TUI{eventLogM: eventLogModal{active: true, scrollOffset: 5}}
	for i := 0; i < 20; i++ {
		tui.eventLog = append(tui.eventLog, AgentEvent{Pane: "eng1", Time: time.Now()})
	}
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.eventLogM.scrollOffset != 4 {
		t.Errorf("scrollOffset after Down = %d, want 4", tui.eventLogM.scrollOffset)
	}
	// j rune also scrolls down.
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyRune, 'j', 0))
	if tui.eventLogM.scrollOffset != 3 {
		t.Errorf("scrollOffset after j = %d, want 3", tui.eventLogM.scrollOffset)
	}
}

func TestHandleEventLogKeyScrollClampAtZero(t *testing.T) {
	tui := &TUI{eventLogM: eventLogModal{active: true, scrollOffset: 0}}
	for i := 0; i < 5; i++ {
		tui.eventLog = append(tui.eventLog, AgentEvent{Pane: "eng1", Time: time.Now()})
	}
	// Scroll down when already at bottom: should stay at 0.
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.eventLogM.scrollOffset != 0 {
		t.Errorf("scrollOffset should stay 0, got %d", tui.eventLogM.scrollOffset)
	}
}

func TestHandleEventLogKeyHomeEnd(t *testing.T) {
	tui := &TUI{eventLogM: eventLogModal{active: true, scrollOffset: 3}}
	for i := 0; i < 20; i++ {
		tui.eventLog = append(tui.eventLog, AgentEvent{Pane: "eng1", Time: time.Now()})
	}
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyHome, 0, 0))
	maxOff := tui.eventLogMaxOffset()
	if tui.eventLogM.scrollOffset != maxOff {
		t.Errorf("after Home: scrollOffset = %d, want %d (max)", tui.eventLogM.scrollOffset, maxOff)
	}
	tui.handleEventLogKey(tcell.NewEventKey(tcell.KeyEnd, 0, 0))
	if tui.eventLogM.scrollOffset != 0 {
		t.Errorf("after End: scrollOffset = %d, want 0", tui.eventLogM.scrollOffset)
	}
}

// ── Render smoke tests ────────────────────────────────────────────────

func TestRenderEventLogEmptyNoPanic(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)
	tui := &TUI{
		screen:    s,
		eventLogM: eventLogModal{active: true},
	}
	// Should not panic with empty event log.
	tui.renderEventLog()
}

func TestRenderEventLogWithEventsNoPanic(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)
	tui := &TUI{
		screen:    s,
		eventLogM: eventLogModal{active: true},
	}
	for _, typ := range []EventType{EventBeadCompleted, EventBeadClaimed, EventBeadFailed, EventAgentStalled, EventAgentStuck, EventAgentIdle} {
		tui.eventLog = append(tui.eventLog, AgentEvent{
			Type:   typ,
			Pane:   "eng1",
			Detail: typ.String() + " detail",
			Time:   time.Now(),
		})
	}
	// Should not panic.
	tui.renderEventLog()
}

// ── execCmd integration ───────────────────────────────────────────────

func TestExecCmdLogOpensModal(t *testing.T) {
	tui := &TUI{}
	tui.execCmd("log")
	if !tui.eventLogM.active {
		t.Error("'log' command should open event log modal")
	}
}

func TestExecCmdEventsOpensModal(t *testing.T) {
	tui := &TUI{}
	tui.execCmd("events")
	if !tui.eventLogM.active {
		t.Error("'events' command should open event log modal")
	}
}

func TestExecCmdLogResetsScrollOffset(t *testing.T) {
	tui := &TUI{eventLogM: eventLogModal{scrollOffset: 42}}
	tui.execCmd("log")
	if tui.eventLogM.scrollOffset != 0 {
		t.Errorf("'log' command should reset scrollOffset to 0, got %d", tui.eventLogM.scrollOffset)
	}
}

// ── eventLogStyle ─────────────────────────────────────────────────────

func TestEventLogStyleCoverage(t *testing.T) {
	// Just ensure no panic for all event types.
	types := []EventType{
		EventBeadCompleted, EventBeadClaimed, EventBeadFailed,
		EventAgentStalled, EventAgentStuck, EventAgentIdle,
		EventType(99),
	}
	for _, et := range types {
		_ = eventLogStyle(et)
	}
}
