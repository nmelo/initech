package tui

import (
	"fmt"
	"testing"
)

// mergedStatusStr replicates the logic in renderTop for computing the display
// status string from a topEntry. Extracted here to allow unit testing without
// a live screen.
func mergedStatusStr(e topEntry) string {
	status := e.Status
	if e.Bead != "" {
		status = fmt.Sprintf("%s (%s)", e.Status, e.Bead)
	}
	if status == "" {
		status = "-"
	}
	return status
}

func TestMergedStatus_NoBead(t *testing.T) {
	e := topEntry{Status: "running"}
	got := mergedStatusStr(e)
	if got != "running" {
		t.Errorf("mergedStatusStr = %q, want %q", got, "running")
	}
}

func TestMergedStatus_WithBead(t *testing.T) {
	e := topEntry{Status: "running", Bead: "ini-sx5"}
	got := mergedStatusStr(e)
	want := "running (ini-sx5)"
	if got != want {
		t.Errorf("mergedStatusStr = %q, want %q", got, want)
	}
}

func TestMergedStatus_IdleNoBead(t *testing.T) {
	e := topEntry{Status: "idle"}
	got := mergedStatusStr(e)
	if got != "idle" {
		t.Errorf("mergedStatusStr = %q, want %q", got, "idle")
	}
}

func TestMergedStatus_DeadNoBead(t *testing.T) {
	e := topEntry{Status: "dead"}
	got := mergedStatusStr(e)
	if got != "dead" {
		t.Errorf("mergedStatusStr = %q, want %q", got, "dead")
	}
}

func TestMergedStatus_EmptyStatus(t *testing.T) {
	e := topEntry{}
	got := mergedStatusStr(e)
	if got != "-" {
		t.Errorf("mergedStatusStr = %q, want %q", got, "-")
	}
}

func TestMergedStatus_EmptyStatusWithBead(t *testing.T) {
	// If status is somehow empty but bead is set, still combine.
	e := topEntry{Status: "", Bead: "ini-abc"}
	got := mergedStatusStr(e)
	want := " (ini-abc)"
	if got != want {
		t.Errorf("mergedStatusStr = %q, want %q", got, want)
	}
}

func TestMergedStatus_HiddenAnnotation(t *testing.T) {
	// Hidden annotation is appended to Status before mergedStatusStr is called.
	e := topEntry{Status: "idle [hidden]"}
	got := mergedStatusStr(e)
	if got != "idle [hidden]" {
		t.Errorf("mergedStatusStr = %q, want %q", got, "idle [hidden]")
	}
}

func TestMergedStatus_HiddenWithBead(t *testing.T) {
	e := topEntry{Status: "running [hidden]", Bead: "ini-abc"}
	got := mergedStatusStr(e)
	want := "running [hidden] (ini-abc)"
	if got != want {
		t.Errorf("mergedStatusStr = %q, want %q", got, want)
	}
}

// TestOverlayStatusCombinesActivityAndBead verifies the logic used in
// renderOverlay to build the Status string for AgentInfo.
func TestOverlayStatusCombinesActivityAndBead(t *testing.T) {
	tests := []struct {
		activity string
		bead     string
		want     string
	}{
		{"running", "ini-sx5", "running (ini-sx5)"},
		{"idle", "", "idle"},
		{"dead", "", "dead"},
		{"running", "", "running"},
		{"idle", "ini-abc", "idle (ini-abc)"},
	}
	for _, tc := range tests {
		// Replicate renderOverlay logic.
		status := tc.activity
		if tc.bead != "" {
			status = fmt.Sprintf("%s (%s)", tc.activity, tc.bead)
		}
		if status != tc.want {
			t.Errorf("overlay status for activity=%q bead=%q = %q, want %q",
				tc.activity, tc.bead, status, tc.want)
		}
	}
}
