package tui

import (
	"testing"
	"time"
)

// TestClose_IdlePaneIsInstant pins the busy-only grace boundary: an IDLE
// pane's Close must not pay any grace window — the quit-tax fight (ini-ap3i)
// was won by removing exactly this kind of unconditional wait.
func TestClose_IdlePaneIsInstant(t *testing.T) {
	p, err := NewPane(PaneConfig{Name: "idle", Command: []string{"sh", "-c", "sleep 60"}}, 10, 40)
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	// Default activity is not StateRunning unless detection promotes it;
	// force idle to pin the boundary.
	p.mu.Lock()
	p.activity = StateIdle
	p.mu.Unlock()
	start := time.Now()
	p.Close()
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("idle Close took %v — the grace window leaked onto idle panes", d)
	}
}

// TestClose_RunningPaneGetsTermGrace: a RUNNING pane's child that traps
// SIGTERM and exits promptly is reaped by the grace, not the kill.
func TestClose_RunningPaneGetsTermGrace(t *testing.T) {
	p, err := NewPane(PaneConfig{Name: "busy", Command: []string{"sh", "-c", "trap 'exit 0' TERM; while :; do sleep 0.1; done"}}, 10, 40)
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	time.Sleep(200 * time.Millisecond) // let the trap install
	p.mu.Lock()
	p.activity = StateRunning
	p.mu.Unlock()
	start := time.Now()
	p.Close()
	d := time.Since(start)
	if d > 1900*time.Millisecond {
		t.Fatalf("running Close took %v — the TERM grace did not reap a cooperative child", d)
	}
}
