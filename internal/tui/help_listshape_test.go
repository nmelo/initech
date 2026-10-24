package tui

import (
	"strings"
	"testing"
)

// The live list split's shape is the operator's (grid CxR, ini-3o4b): no help
// line may still call it a fixed four-wide grid.
func TestHelp_TheLiveListLinesNameTheOperatorsShape(t *testing.T) {
	var list []string
	for _, l := range getHelpLines() {
		if strings.Contains(l, "Live list split") {
			list = append(list, l)
		}
	}
	if len(list) < 2 {
		t.Fatalf("found %d help lines for the live list split, want the key and the command", len(list))
	}
	for _, l := range list {
		if strings.Contains(l, "4-wide") || strings.Contains(l, "4xR") {
			t.Errorf("help line still names a fixed four-column grid: %q", strings.TrimSpace(l))
		}
	}
}
