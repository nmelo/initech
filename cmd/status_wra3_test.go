package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-wra.3: a pane that has worked fifteen minutes with no bead reads
// "no bead" in initech status's Bead column; bd's answer still wins.
func TestStatus_UnattributedWorkReadsNoBead(t *testing.T) {
	plain := jvrsPane("eng2")
	plain.Activity = "running"
	marked := jvrsPane("eng3")
	marked.Activity = "running"
	marked.Unattributed = true
	held := jvrsPane("eng1", "ini-p") // bd holds ini-p for eng1
	held.Unattributed = true
	rows := jvrsStatus(t, plain, marked, held)

	if !strings.Contains(rows["eng3"], "no bead") {
		t.Errorf("eng3, 15 minutes of work with no bead: row %q, want \"no bead\"", rows["eng3"])
	}
	if strings.Contains(rows["eng2"], "no bead") {
		t.Errorf("eng2, not marked: row %q, want no \"no bead\"", rows["eng2"])
	}
	if strings.Contains(rows["eng1"], "no bead") || !strings.Contains(rows["eng1"], "ini-p") {
		t.Errorf("eng1, bd holds ini-p: row %q, want ini-p and no \"no bead\"", rows["eng1"])
	}
}

// The list reply's unattributed field is read.
func TestStatus_ReadsTheUnattributedField(t *testing.T) {
	var pi paneInfo
	if err := json.Unmarshal([]byte(`{"name":"eng1","unattributed":true}`), &pi); err != nil || !pi.Unattributed {
		t.Fatalf("unattributed not read: %+v %v", pi, err)
	}
	_ = tui.PaneInfo{Unattributed: true}
}
