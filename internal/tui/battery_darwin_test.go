//go:build darwin

package tui

import "testing"

// TestParseBatteryOutput_RealPmsetStrings is a regression test for ini-eqdw:
// "80%; AC attached; not charging" (macOS's battery-health throttling
// state) broke the combined percent+status regex, hiding the percent along
// with the unrecognized status word. Table-driven over real pmset -g batt
// output shapes, including every status word the old regex already handled
// (to prove those aren't regressed) plus the exact string that broke this.
func TestParseBatteryOutput_RealPmsetStrings(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		wantPct     int
		wantCharge  bool
		wantHasBatt bool
	}{
		{
			name: "discharging",
			text: "Now drawing from 'Battery Power'\n" +
				" -InternalBattery-0 (id=35717219)\t66%; discharging; 3:45 remaining present: true\n",
			wantPct:     66,
			wantCharge:  false,
			wantHasBatt: true,
		},
		{
			name: "charging",
			text: "Now drawing from 'AC Power'\n" +
				" -InternalBattery-0 (id=35717219)\t45%; charging; 1:23 remaining present: true\n",
			wantPct:     45,
			wantCharge:  true,
			wantHasBatt: true,
		},
		{
			name: "charged",
			text: "Now drawing from 'AC Power'\n" +
				" -InternalBattery-0 (id=35717219)\t100%; charged; 0:00 remaining present: true\n",
			wantPct:     100,
			wantCharge:  true,
			wantHasBatt: true,
		},
		{
			name: "finishing charge",
			text: "Now drawing from 'AC Power'\n" +
				" -InternalBattery-0 (id=35717219)\t95%; finishing charge; 0:10 remaining present: true\n",
			wantPct:     95,
			wantCharge:  true,
			wantHasBatt: true,
		},
		{
			// The exact string from ini-eqdw's report on the operator's
			// machine. Before the fix: FindStringSubmatch on the combined
			// regex returned fewer than 3 groups (no recognized status
			// word), so hasBattery was false and the percent never showed.
			name: "AC attached; not charging (ini-eqdw)",
			text: "Now drawing from 'AC Power'\n" +
				" -InternalBattery-0 (id=35717219)\t80%; AC attached; not charging present: true\n",
			wantPct:     80,
			wantCharge:  false,
			wantHasBatt: true,
		},
		{
			// The actual guardrail: an invented status word that is not,
			// and will never be, in batteryChargingWords. This fails if
			// percent and charging are ever recoupled into one match,
			// regardless of what the unrecognized word says.
			name: "deliberately unrecognized status word",
			text: "Now drawing from 'AC Power'\n" +
				" -InternalBattery-0 (id=35717219)\t72%; optimized battery charging paused; present: true\n",
			wantPct:     72,
			wantCharge:  false,
			wantHasBatt: true,
		},
		{
			// qa2's adversarial fixture, failing ini-eqdw's first QA pass:
			// a percent on a line BEFORE the InternalBattery line. When
			// batteryPercentRe was matched against the full multi-line
			// text instead of the InternalBattery line alone, this string
			// produced pct=55 (the OTHER device's percent) with
			// hasBattery=true -- a silent wrong number, not a caught
			// failure. The fix scopes both regexes to the InternalBattery
			// line only. This is the guardrail against widening that scope
			// back to the full text.
			name: "percent on an earlier unrelated line (qa2)",
			text: "Now drawing from 'AC Power'\n" +
				"Some Other Device: 55%\n" +
				" -InternalBattery-0 (id=35717219)\t80%; AC attached; not charging present: true\n",
			wantPct:     80,
			wantCharge:  false,
			wantHasBatt: true,
		},
		{
			name:        "desktop Mac, no battery",
			text:        "Now drawing from 'AC Power'\n",
			wantPct:     0,
			wantCharge:  false,
			wantHasBatt: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pct, charging, hasBattery := parseBatteryOutput(tt.text)
			if hasBattery != tt.wantHasBatt {
				t.Errorf("hasBattery = %v, want %v", hasBattery, tt.wantHasBatt)
			}
			if !tt.wantHasBatt {
				return // pct/charging are meaningless when hasBattery is false.
			}
			if pct != tt.wantPct {
				t.Errorf("percent = %d, want %d", pct, tt.wantPct)
			}
			if charging != tt.wantCharge {
				t.Errorf("charging = %v, want %v", charging, tt.wantCharge)
			}
		})
	}
}

// TestIsChargingField_NotChargingIsNotSubstringMatched guards the specific
// mechanism the fix relies on: "not charging" contains the substring
// "charging" but must never be read as a charging=true signal. A regex or
// strings.Contains check on the raw text (instead of exact-matching
// trimmed, semicolon-delimited fields) would get this wrong.
func TestIsChargingField_NotChargingIsNotSubstringMatched(t *testing.T) {
	if isChargingField("80%; AC attached; not charging present: true") {
		t.Error("isChargingField matched \"not charging\" as a charging state")
	}
	if !isChargingField("45%; charging; 1:23 remaining") {
		t.Error("isChargingField failed to match a real \"charging\" field")
	}
}
