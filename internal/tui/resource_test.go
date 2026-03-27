package tui

import (
	"os"
	"testing"
)

func TestResourceEnabled_Default(t *testing.T) {
	tui := &TUI{}
	if tui.ResourceEnabled() {
		t.Error("ResourceEnabled should be false by default")
	}
}

func TestResourceEnabled_WhenSet(t *testing.T) {
	tui := &TUI{autoSuspend: true}
	if !tui.ResourceEnabled() {
		t.Error("ResourceEnabled should be true when autoSuspend is set")
	}
}

func TestPressureThreshold_Default(t *testing.T) {
	tui := &TUI{}
	if got := tui.PressureThreshold(); got != 85 {
		t.Errorf("PressureThreshold() = %d, want 85", got)
	}
}

func TestPressureThreshold_Custom(t *testing.T) {
	tui := &TUI{pressureThreshold: 70}
	if got := tui.PressureThreshold(); got != 70 {
		t.Errorf("PressureThreshold() = %d, want 70", got)
	}
}

func TestPollPaneRSS_InvalidPID(t *testing.T) {
	if rss := pollPaneRSS(0); rss != 0 {
		t.Errorf("pollPaneRSS(0) = %d, want 0", rss)
	}
	if rss := pollPaneRSS(-1); rss != 0 {
		t.Errorf("pollPaneRSS(-1) = %d, want 0", rss)
	}
}

func TestPollPaneRSS_CurrentProcess(t *testing.T) {
	pid := os.Getpid()
	rss := pollPaneRSS(pid)
	if rss <= 0 {
		t.Errorf("pollPaneRSS(self=%d) = %d, want > 0", pid, rss)
	}
}

func TestPollPaneRSS_DeadProcess(t *testing.T) {
	// PID 99999999 should not exist.
	rss := pollPaneRSS(99999999)
	if rss != 0 {
		t.Errorf("pollPaneRSS(dead) = %d, want 0", rss)
	}
}

func TestPaneMemoryRSS_DefaultZero(t *testing.T) {
	p := &Pane{}
	if rss := p.MemoryRSS(); rss != 0 {
		t.Errorf("MemoryRSS() = %d, want 0", rss)
	}
}

func TestPaneMemoryRSS_SetAndGet(t *testing.T) {
	p := &Pane{}
	p.setMemoryRSS(123456)
	if rss := p.MemoryRSS(); rss != 123456 {
		t.Errorf("MemoryRSS() = %d, want 123456", rss)
	}
}

func TestPaneMemoryRSS_ResetToZero(t *testing.T) {
	p := &Pane{}
	p.setMemoryRSS(50000)
	p.setMemoryRSS(0)
	if rss := p.MemoryRSS(); rss != 0 {
		t.Errorf("MemoryRSS() = %d, want 0 after reset", rss)
	}
}

func TestSystemMemoryTotal(t *testing.T) {
	total, err := systemMemoryTotal()
	if err != nil {
		t.Fatalf("systemMemoryTotal() error: %v", err)
	}
	// Any real machine should have at least 512 MB (524288 KB).
	if total < 524288 {
		t.Errorf("systemMemoryTotal() = %d KB, suspiciously low", total)
	}
}

func TestSystemMemoryAvail(t *testing.T) {
	avail, err := systemMemoryAvail()
	if err != nil {
		t.Fatalf("systemMemoryAvail() error: %v", err)
	}
	// Should have at least some available memory.
	if avail <= 0 {
		t.Errorf("systemMemoryAvail() = %d KB, want > 0", avail)
	}
}

func TestSystemMemoryAvailable_Accessor(t *testing.T) {
	tui := &TUI{systemMemAvail: 4096}
	if got := tui.SystemMemoryAvailable(); got != 4096 {
		t.Errorf("SystemMemoryAvailable() = %d, want 4096", got)
	}
}

func TestSystemMemoryTotal_Accessor(t *testing.T) {
	tui := &TUI{systemMemTotal: 8192}
	if got := tui.SystemMemoryTotal(); got != 8192 {
		t.Errorf("SystemMemoryTotal() = %d, want 8192", got)
	}
}

func TestPollAllRSS_Integration(t *testing.T) {
	// Create a TUI with ipcCh=nil so runOnMain executes directly.
	tui := &TUI{
		quitCh: make(chan struct{}),
	}
	// Create a pane with the current process PID (no real PTY needed).
	p := &Pane{pid: os.Getpid()}
	tui.panes = []*Pane{p}

	tui.pollAllRSS()

	if rss := p.MemoryRSS(); rss <= 0 {
		t.Errorf("after pollAllRSS, pane RSS = %d, want > 0", rss)
	}
	if avail := tui.SystemMemoryAvailable(); avail <= 0 {
		t.Errorf("after pollAllRSS, systemMemAvail = %d, want > 0", avail)
	}
}

func TestMonitorNotStartedWhenDisabled(t *testing.T) {
	// Verify the gate: when autoSuspend is false, startMemoryMonitor
	// should not be called (tested via the Run() integration path).
	// This test just checks the boolean gate directly.
	tui := &TUI{autoSuspend: false}
	if tui.ResourceEnabled() {
		t.Error("resource should not be enabled when autoSuspend is false")
	}
}
