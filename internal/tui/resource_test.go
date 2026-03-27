package tui

import "testing"

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
