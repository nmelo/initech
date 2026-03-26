package tui

import (
	"testing"
	"time"
)

func TestUpdateActivity_RunningWhenRecentOutput(t *testing.T) {
	p := &Pane{alive: true}
	p.lastOutputTime = time.Now().Add(-500 * time.Millisecond)
	p.updateActivity()
	if p.activity != StateRunning {
		t.Errorf("activity = %v after 500ms gap, want StateRunning (output is recent)", p.activity)
	}
}

func TestUpdateActivity_RunningJustUnderThreshold(t *testing.T) {
	p := &Pane{alive: true}
	p.lastOutputTime = time.Now().Add(-(ptyIdleTimeout - 100*time.Millisecond))
	p.updateActivity()
	if p.activity != StateRunning {
		t.Errorf("activity = %v just under ptyIdleTimeout, want StateRunning", p.activity)
	}
}

func TestUpdateActivity_IdleAfterThreshold(t *testing.T) {
	p := &Pane{alive: true}
	p.lastOutputTime = time.Now().Add(-(ptyIdleTimeout + time.Second))
	p.updateActivity()
	if p.activity != StateIdle {
		t.Errorf("activity = %v after ptyIdleTimeout+1s, want StateIdle", p.activity)
	}
}

func TestUpdateActivity_IdleWhenNoOutputYet(t *testing.T) {
	p := &Pane{alive: true}
	// lastOutputTime is zero value — no output ever received.
	p.updateActivity()
	if p.activity != StateIdle {
		t.Errorf("activity = %v with zero lastOutputTime, want StateIdle", p.activity)
	}
}

func TestUpdateActivity_IdleWhenDead(t *testing.T) {
	// ini-a1e.29: dead panes show StateDead (red filled dot),
	// distinct from StateIdle (gray hollow circle).
	p := &Pane{alive: false}
	// Even with a very recent lastOutputTime, dead pane must be StateDead.
	p.lastOutputTime = time.Now()
	p.updateActivity()
	if p.activity != StateDead {
		t.Errorf("activity = %v for dead pane, want StateDead", p.activity)
	}
}

func TestUpdateActivity_TransitionRunningToIdle(t *testing.T) {
	p := &Pane{alive: true}
	// Simulate active agent: recent output.
	p.lastOutputTime = time.Now().Add(-100 * time.Millisecond)
	p.updateActivity()
	if p.activity != StateRunning {
		t.Fatalf("activity = %v, want StateRunning", p.activity)
	}
	// Simulate idle agent: output is stale.
	p.lastOutputTime = time.Now().Add(-(ptyIdleTimeout + time.Second))
	p.updateActivity()
	if p.activity != StateIdle {
		t.Errorf("activity = %v after stale output, want StateIdle", p.activity)
	}
}
