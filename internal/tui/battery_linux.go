//go:build linux

package tui

import (
	"os"
	"strconv"
	"strings"
)

// readBattery queries Linux battery state from sysfs.
// Returns percent (0-100), whether charging, and whether a battery exists.
func readBattery() (percent int, charging bool, hasBattery bool) {
	data, err := os.ReadFile("/sys/class/power_supply/BAT0/capacity")
	if err != nil {
		return 0, false, false // No battery or not accessible.
	}
	pct, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false, false
	}
	status, err := os.ReadFile("/sys/class/power_supply/BAT0/status")
	if err != nil {
		return pct, false, true
	}
	s := strings.TrimSpace(string(status))
	isCharging := s == "Charging" || s == "Full"
	return pct, isCharging, true
}
