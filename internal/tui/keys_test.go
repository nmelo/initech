package tui

import (
	"runtime"
	"strings"
	"testing"
)

func TestModKeyMatchesPlatform(t *testing.T) {
	switch runtime.GOOS {
	case "darwin":
		if modKey != "Opt" {
			t.Errorf("modKey = %q on darwin, want Opt", modKey)
		}
	default:
		if modKey != "Alt" {
			t.Errorf("modKey = %q on %s, want Alt", modKey, runtime.GOOS)
		}
	}
}

func TestHelpLinesUseModKey(t *testing.T) {
	lines := getHelpLines()
	found := false
	for _, line := range lines {
		if strings.Contains(line, modKey+"+") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("help lines should contain %q+ shortcuts", modKey)
	}
	for _, line := range lines {
		if strings.Contains(line, "Alt+") && modKey != "Alt" {
			t.Errorf("help line still contains hardcoded Alt+: %q", line)
		}
	}
}

func TestStatusTipsUseModKey(t *testing.T) {
	tips := getStatusTips()
	for _, tip := range tips {
		if strings.Contains(tip, "Alt+") && modKey != "Alt" {
			t.Errorf("status tip still contains hardcoded Alt+: %q", tip)
		}
	}
}
