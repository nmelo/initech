package tui

import (
	"testing"
	"time"
)

// TestUpdateActivity_RunningDuringLongToolExecution is the regression test for
// ini-1ss. Claude regularly has 90-150s gaps between JSONL writes during active
// work (make test, git push, extended thinking). The old 5-second timeout caused
// agents to show idle mid-work.
func TestUpdateActivity_RunningDuringLongToolExecution(t *testing.T) {
	p := &Pane{}
	// Simulate: last entry was "progress" (tool just called), 10s ago.
	// With the old 5s timeout this would be idle. With 120s it must be running.
	p.lastJsonlType = "progress"
	p.lastJsonlTime = time.Now().Add(-10 * time.Second)
	p.updateActivity()
	if p.activity != StateRunning {
		t.Errorf("activity = %v after 10s gap, want StateRunning (tool in flight)", p.activity)
	}
}

func TestUpdateActivity_RunningDuringExtendedThinking(t *testing.T) {
	p := &Pane{}
	// Simulate: last entry was "user" (tool_result received), 60s ago.
	// Claude is still generating its response.
	p.lastJsonlType = "user"
	p.lastJsonlTime = time.Now().Add(-60 * time.Second)
	p.updateActivity()
	if p.activity != StateRunning {
		t.Errorf("activity = %v after 60s gap, want StateRunning (LLM generating)", p.activity)
	}
}

func TestUpdateActivity_IdleAfterTimeout(t *testing.T) {
	p := &Pane{}
	// After jsonlIdleTimeout+1s with no new entries, should go idle.
	p.lastJsonlType = "assistant"
	p.lastJsonlTime = time.Now().Add(-(jsonlIdleTimeout + time.Second))
	p.updateActivity()
	if p.activity != StateIdle {
		t.Errorf("activity = %v after timeout, want StateIdle", p.activity)
	}
}

func TestUpdateActivity_SystemTypeImmediatelyIdle(t *testing.T) {
	p := &Pane{}
	// system entries appear at turn end and should immediately show idle.
	p.lastJsonlType = "system"
	p.lastJsonlTime = time.Now()
	p.updateActivity()
	if p.activity != StateIdle {
		t.Errorf("activity = %v for system type, want StateIdle immediately", p.activity)
	}
}

func TestUpdateActivity_AgentColorImmediatelyIdle(t *testing.T) {
	p := &Pane{}
	p.lastJsonlType = "agent-color"
	p.lastJsonlTime = time.Now()
	p.updateActivity()
	if p.activity != StateIdle {
		t.Errorf("activity = %v for agent-color type, want StateIdle immediately", p.activity)
	}
}

func TestUpdateActivity_LastPromptImmediatelyIdle(t *testing.T) {
	p := &Pane{}
	p.lastJsonlType = "last-prompt"
	p.lastJsonlTime = time.Now()
	p.updateActivity()
	if p.activity != StateIdle {
		t.Errorf("activity = %v for last-prompt type, want StateIdle immediately", p.activity)
	}
}

func TestUpdateActivity_RecentAssistantIsRunning(t *testing.T) {
	p := &Pane{}
	p.lastJsonlType = "assistant"
	p.lastJsonlTime = time.Now().Add(-1 * time.Second)
	p.updateActivity()
	if p.activity != StateRunning {
		t.Errorf("activity = %v after 1s, want StateRunning", p.activity)
	}
}
