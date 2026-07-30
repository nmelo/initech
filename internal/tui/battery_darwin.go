//go:build darwin

package tui

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// batteryPercentRe extracts the battery percentage from pmset -g batt
// output, independent of the charging-status text that follows it.
//
// ini-eqdw: percent and charging status used to be captured by ONE regex
// requiring both in the same match. When pmset emitted a status word this
// package didn't recognize ("AC attached; not charging" -- macOS's own
// battery-health throttling state, not a plain charging or discharging
// state), the whole match failed and the percent -- a reliable,
// always-present fact -- disappeared along with the unreliable status word.
// Percent is now parsed on its own so an unrecognized status can never hide
// it again.
var batteryPercentRe = regexp.MustCompile(`(\d+)%`)

// batteryChargingWords are the pmset status words that positively confirm
// active charging. "charged" is included: pmset uses it for a full battery
// still on AC power, and the status bar has always treated "topped off and
// plugged in" the same as "actively charging" (both green).
//
// Deliberately NOT grown to chase every status string Apple might emit
// (ini-eqdw) -- that enumeration bet already lost once. Any status text
// that isn't exactly one of these three words leaves charging=false: the
// bar still shows the percent, just without the green "charging" styling.
// That degradation is the fix, not a gap -- see readBattery's doc comment.
var batteryChargingWords = map[string]bool{
	"charging":         true,
	"charged":          true,
	"finishing charge": true,
}

// readBattery queries macOS battery state via pmset.
// Returns percent (0-100), whether charging, and whether a battery exists.
func readBattery() (percent int, charging bool, hasBattery bool) {
	out, err := exec.Command("pmset", "-g", "batt").Output()
	if err != nil {
		return 0, false, false
	}
	return parseBatteryOutput(string(out))
}

// parseBatteryOutput parses pmset -g batt's text output. Split out from
// readBattery so the parsing logic is testable against fixture strings
// without mocking exec.Command.
//
// Percent and charging are two independent facts about the same line of
// output, deliberately parsed separately (ini-eqdw). The percent regex is
// matched against the InternalBattery line ONLY, not the full multi-line
// output -- qa2 caught that matching the full text lets a percent on an
// earlier line (a second device, a UPS, some future pmset addition) be
// mistaken for the battery's own percent. If the InternalBattery line has
// no percent, hasBattery is false, since there is truly no percent to show.
// Charging is then classified separately (also scoped to that line) by
// exact-matching each semicolon-delimited field against
// batteryChargingWords. A field like "not charging" contains the substring
// "charging" but is never an exact trimmed field equal to "charging", so it
// correctly does not count -- that's what fixes the original bug, not a
// broader word list. If no field matches, charging degrades to false: a
// battery indicator that's wrong about "currently charging" (a false green)
// is worse than one that shows the percent without asserting a charging
// state it can't confirm.
func parseBatteryOutput(text string) (percent int, charging bool, hasBattery bool) {
	batteryLine := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "InternalBattery") {
			batteryLine = line
			break
		}
	}
	if batteryLine == "" {
		return 0, false, false // Desktop Mac, no battery.
	}
	pctMatch := batteryPercentRe.FindStringSubmatch(batteryLine)
	if len(pctMatch) < 2 {
		return 0, false, false
	}
	pct, err := strconv.Atoi(pctMatch[1])
	if err != nil {
		return 0, false, false
	}
	return pct, isChargingField(batteryLine), true
}

// isChargingField reports whether text contains a semicolon-delimited field
// that exactly matches one of batteryChargingWords. Matching whole trimmed
// fields, not substrings, is what keeps "not charging" -- which contains
// the substring "charging" -- from being misread as a charging state.
func isChargingField(text string) bool {
	for _, field := range strings.Split(text, ";") {
		if batteryChargingWords[strings.TrimSpace(field)] {
			return true
		}
	}
	return false
}
