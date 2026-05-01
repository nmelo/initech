//go:build windows

package tui

// readBattery on Windows is a no-op. Server VMs typically have no battery,
// and we don't need battery-aware pacing for the wintest pane use case.
func readBattery() (percent int, charging bool, hasBattery bool) {
	return 0, false, false
}
