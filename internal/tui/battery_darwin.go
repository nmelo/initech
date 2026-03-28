//go:build darwin

package tui

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// batteryRe matches the InternalBattery line from pmset -g batt output.
// Example: "  -InternalBattery-0 (id=...)  66%; discharging; 3:45 remaining"
var batteryRe = regexp.MustCompile(`(\d+)%;\s*(charging|discharging|charged|finishing charge)`)

// readBattery queries macOS battery state via pmset.
// Returns percent (0-100), whether charging, and whether a battery exists.
func readBattery() (percent int, charging bool, hasBattery bool) {
	out, err := exec.Command("pmset", "-g", "batt").Output()
	if err != nil {
		return 0, false, false
	}
	text := string(out)
	if !strings.Contains(text, "InternalBattery") {
		return 0, false, false // Desktop Mac, no battery.
	}
	matches := batteryRe.FindStringSubmatch(text)
	if len(matches) < 3 {
		return 0, false, false
	}
	pct, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, false, false
	}
	status := matches[2]
	isCharging := status == "charging" || status == "charged" || status == "finishing charge"
	return pct, isCharging, true
}
