package tui

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	iexec "github.com/nmelo/initech/internal/exec"
)

// fakeBeads returns a JSON array of N fake bead objects.
func fakeBeads(n int) string {
	items := make([]map[string]string, n)
	for i := range items {
		items[i] = map[string]string{"id": "ini-test", "title": "test"}
	}
	b, _ := json.Marshal(items)
	return string(b)
}

func TestQueryBdReady_ReturnsCount(t *testing.T) {
	runner := &iexec.FakeRunner{Output: fakeBeads(3)}
	got := queryBdReady(runner)
	if got != 3 {
		t.Errorf("queryBdReady = %d, want 3", got)
	}
	if len(runner.Calls) != 1 || runner.Calls[0] != "|bd ready --json" {
		t.Errorf("unexpected calls: %v", runner.Calls)
	}
}

func TestQueryBdReady_Zero(t *testing.T) {
	runner := &iexec.FakeRunner{Output: fakeBeads(0)}
	got := queryBdReady(runner)
	if got != 0 {
		t.Errorf("queryBdReady = %d, want 0 for empty array", got)
	}
}

func TestQueryBdReady_Error(t *testing.T) {
	runner := &iexec.FakeRunner{Output: "", Err: fmt.Errorf("bd not found")}
	got := queryBdReady(runner)
	if got != 0 {
		t.Errorf("queryBdReady = %d, want 0 on error", got)
	}
}

func TestQueryBdReady_InvalidJSON(t *testing.T) {
	runner := &iexec.FakeRunner{Output: "not json"}
	got := queryBdReady(runner)
	if got != 0 {
		t.Errorf("queryBdReady = %d, want 0 on invalid JSON", got)
	}
}

func TestIdleAgentsWithoutBead(t *testing.T) {
	t.Helper()
	tui := &TUI{
		panes: []*Pane{
			{name: "eng1", alive: true, activity: StateIdle, beadID: ""},       // idle, no bead -> included
			{name: "eng2", alive: true, activity: StateRunning, beadID: ""},    // running -> excluded
			{name: "eng3", alive: true, activity: StateIdle, beadID: "ini-x.1"}, // has bead -> excluded
			{name: "eng4", alive: false, activity: StateIdle, beadID: ""},      // dead -> excluded
		},
	}
	got := tui.idleAgentsWithoutBead()
	if len(got) != 1 || got[0] != "eng1" {
		t.Errorf("idleAgentsWithoutBead = %v, want [eng1]", got)
	}
}

func TestIdleAgentsWithoutBead_AllBusy(t *testing.T) {
	tui := &TUI{
		panes: []*Pane{
			{name: "eng1", alive: true, activity: StateRunning},
			{name: "eng2", alive: true, activity: StateRunning},
		},
	}
	got := tui.idleAgentsWithoutBead()
	if len(got) != 0 {
		t.Errorf("idleAgentsWithoutBead = %v, want empty", got)
	}
}

func TestWatchBacklog_EmitsEventForIdleAgent(t *testing.T) {
	quitCh := make(chan struct{})
	tui := &TUI{
		quitCh:      quitCh,
		agentEvents: make(chan AgentEvent, 8),
		panes: []*Pane{
			{name: "eng1", alive: true, activity: StateIdle, beadID: ""},
		},
	}

	runner := &iexec.FakeRunner{Output: fakeBeads(2)}

	// Run one check cycle directly (bypassing the 2-min ticker).
	idle := tui.idleAgentsWithoutBead()
	if len(idle) != 1 {
		t.Fatalf("expected 1 idle agent, got %v", idle)
	}
	readyCount := queryBdReady(runner)
	if readyCount != 2 {
		t.Fatalf("expected 2 ready beads, got %d", readyCount)
	}
	// Simulate what watchBacklog does.
	notified := make(map[string]bool)
	for _, name := range idle {
		if !notified[name] {
			notified[name] = true
			EmitEvent(tui.agentEvents, AgentEvent{
				Type:   EventAgentIdle,
				Pane:   name,
				Detail: name + ": idle, 2 bead(s) ready",
				Time:   time.Now(),
			})
		}
	}

	if len(tui.agentEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(tui.agentEvents))
	}
	ev := <-tui.agentEvents
	if ev.Type != EventAgentIdle || ev.Pane != "eng1" {
		t.Errorf("event = {%v %q}, want EventAgentIdle/eng1", ev.Type, ev.Pane)
	}
}

func TestWatchBacklog_NoEventWhenNoReadyBeads(t *testing.T) {
	tui := &TUI{
		agentEvents: make(chan AgentEvent, 8),
		panes: []*Pane{
			{name: "eng1", alive: true, activity: StateIdle, beadID: ""},
		},
	}
	runner := &iexec.FakeRunner{Output: fakeBeads(0)}

	idle := tui.idleAgentsWithoutBead()
	readyCount := queryBdReady(runner)
	if len(idle) > 0 && readyCount > 0 {
		t.Fatal("should not reach emit: either no idle or no ready")
	}
	if len(tui.agentEvents) != 0 {
		t.Errorf("expected no events, got %d", len(tui.agentEvents))
	}
}

func TestWatchBacklog_NoEventWhenNoIdleAgents(t *testing.T) {
	tui := &TUI{
		agentEvents: make(chan AgentEvent, 8),
		panes: []*Pane{
			{name: "eng1", alive: true, activity: StateRunning, beadID: "ini-x.1"},
		},
	}
	runner := &iexec.FakeRunner{Output: fakeBeads(3)}

	idle := tui.idleAgentsWithoutBead()
	if len(idle) != 0 {
		t.Fatalf("expected no idle agents, got %v", idle)
	}
	_ = queryBdReady(runner)
	if len(tui.agentEvents) != 0 {
		t.Errorf("expected no events, got %d", len(tui.agentEvents))
	}
}

func TestWatchBacklog_Dedup(t *testing.T) {
	// Same agent should only get one event per idle episode.
	notified := make(map[string]bool)
	idle := []string{"eng1"}
	readyCount := 2

	evCount := 0
	emit := func(name string) {
		if !notified[name] {
			notified[name] = true
			evCount++
		}
	}

	// First check: emit.
	for _, name := range idle {
		if readyCount > 0 {
			emit(name)
		}
	}
	// Second check (same idle state): no re-emit.
	for _, name := range idle {
		if readyCount > 0 {
			emit(name)
		}
	}
	if evCount != 1 {
		t.Errorf("dedup: emitted %d events, want 1", evCount)
	}

	// Agent gains a bead, dedup clears.
	idle = []string{} // eng1 no longer idle
	for name := range notified {
		stillIdle := false
		for _, n := range idle {
			if n == name {
				stillIdle = true
				break
			}
		}
		if !stillIdle {
			delete(notified, name)
		}
	}
	// Now eng1 goes idle again — should emit again.
	idle = []string{"eng1"}
	for _, name := range idle {
		if readyCount > 0 {
			emit(name)
		}
	}
	if evCount != 2 {
		t.Errorf("after reset: emitted %d total events, want 2", evCount)
	}
}
