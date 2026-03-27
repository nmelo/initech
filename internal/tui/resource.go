// Package tui resource management.
//
// resource.go is the home for all resource-aware agent lifecycle code:
// memory monitoring, auto-suspend policy, and resume-on-message. All of this
// is gated behind the autoSuspend bool on the TUI struct.
//
// When autoSuspend is false (the default), nothing in this file runs. The
// memory monitor goroutine is never started, the suspend policy never checks,
// and agents are never automatically suspended or resumed.
package tui

// ResourceEnabled reports whether resource-aware auto-suspend is active for
// this TUI instance. All resource management code should check this gate
// before taking any action.
func (t *TUI) ResourceEnabled() bool {
	return t.autoSuspend
}

// PressureThreshold returns the configured RSS percentage above which agents
// may be auto-suspended. Returns 85 (the default) when not explicitly set.
func (t *TUI) PressureThreshold() int {
	if t.pressureThreshold > 0 {
		return t.pressureThreshold
	}
	return 85
}
