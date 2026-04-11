package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruncateTitle(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 80, "short"},
		{"exactly 10", 10, "exactly 10"},
		{"this is a very long title that exceeds the limit", 20, "this is a very lo..."},
		{"", 80, ""},
	}
	for _, tc := range tests {
		got := truncateTitle(tc.input, tc.maxLen)
		if got != tc.want {
			t.Errorf("truncateTitle(%q, %d) = %q, want %q", tc.input, tc.maxLen, got, tc.want)
		}
	}
}

func TestBdShowTitle_ParsesJSON(t *testing.T) {
	dir := t.TempDir()
	fakeBd := filepath.Join(dir, "bd")
	script := `#!/bin/sh
echo '[{"id":"ini-abc","title":"Fix the bug","status":"open"}]'
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	title, err := bdShowTitle("ini-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "Fix the bug" {
		t.Errorf("title = %q, want %q", title, "Fix the bug")
	}
}

func TestBdShowTitle_NotFound(t *testing.T) {
	dir := t.TempDir()
	fakeBd := filepath.Join(dir, "bd")
	script := `#!/bin/sh
echo "error: bead not found" >&2
exit 1
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	_, err := bdShowTitle("ini-nonexistent")
	if err == nil {
		t.Fatal("expected error for missing bead")
	}
}

func TestAssignCommand_RequiresArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"assign"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestAssignCommand_AcceptsSingleBead(t *testing.T) {
	// Verify cobra accepts 2 args (agent + 1 bead).
	rootCmd.SetArgs([]string{"assign", "eng1", "ini-abc"})
	defer rootCmd.SetArgs(nil)
	// Will fail on bd/IPC but shouldn't fail on arg validation.
	err := rootCmd.Execute()
	if err != nil && strings.Contains(err.Error(), "accepts") {
		t.Fatalf("should accept 2 args, got: %v", err)
	}
}

func TestAssignCommand_AcceptsMultipleBeads(t *testing.T) {
	// Verify cobra accepts 4 args (agent + 3 beads).
	rootCmd.SetArgs([]string{"assign", "eng1", "ini-abc", "ini-def", "ini-ghi"})
	defer rootCmd.SetArgs(nil)
	// Will fail on bd/IPC but shouldn't fail on arg validation.
	err := rootCmd.Execute()
	if err != nil && strings.Contains(err.Error(), "accepts") {
		t.Fatalf("should accept 4 args, got: %v", err)
	}
}

func TestBuildDispatchMessage_SingleBead(t *testing.T) {
	successes := []assignResult{{id: "ini-abc", title: "Fix the bug"}}
	msg := buildDispatchMessage(successes, "")
	if !strings.Contains(msg, "ini-abc: Fix the bug") {
		t.Errorf("single bead dispatch missing title: %q", msg)
	}
	if !strings.Contains(msg, "Read bd show ini-abc") {
		t.Errorf("single bead dispatch missing bd show hint: %q", msg)
	}
}

func TestBuildDispatchMessage_MultipleBeads(t *testing.T) {
	successes := []assignResult{
		{id: "ini-abc", title: "Fix auth"},
		{id: "ini-def", title: "Add tests"},
		{id: "ini-ghi", title: "Update docs"},
	}
	msg := buildDispatchMessage(successes, "")
	if !strings.Contains(msg, "Assigned 3 beads") {
		t.Errorf("multi-bead dispatch missing count: %q", msg)
	}
	if !strings.Contains(msg, "- ini-abc: Fix auth") {
		t.Errorf("multi-bead dispatch missing first bead: %q", msg)
	}
	if !strings.Contains(msg, "- ini-ghi: Update docs") {
		t.Errorf("multi-bead dispatch missing third bead: %q", msg)
	}
}

func TestBuildDispatchMessage_TruncatesAt5(t *testing.T) {
	successes := make([]assignResult, 7)
	for i := range successes {
		successes[i] = assignResult{id: "ini-" + string(rune('a'+i)), title: "Task"}
	}
	msg := buildDispatchMessage(successes, "")
	if !strings.Contains(msg, "... and 2 more") {
		t.Errorf("expected truncation message in: %q", msg)
	}
}

func TestBuildDispatchMessage_WithCustomMessage(t *testing.T) {
	successes := []assignResult{{id: "ini-abc", title: "Fix the bug"}}
	msg := buildDispatchMessage(successes, "Focus on edge cases.")
	if !strings.Contains(msg, "Focus on edge cases.") {
		t.Errorf("custom message not appended: %q", msg)
	}
}
