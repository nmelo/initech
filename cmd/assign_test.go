package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
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
	skipWindows(t)
	dir := t.TempDir()
	fakeBd := filepath.Join(dir, "bd")
	script := `#!/bin/sh
echo '[{"id":"ini-abc","title":"Fix the bug","status":"open"}]'
`
	if err := os.WriteFile(fakeBd, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	title, err := bdShowTitleImpl("ini-abc")
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

	_, err := bdShowTitleImpl("ini-nonexistent")
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

// resetAssignFlags resets the package-level flag vars to defaults.
func resetAssignFlags(t *testing.T) {
	t.Helper()
	assignMessage = ""
	t.Cleanup(func() { assignMessage = "" })
}

func TestRunAssign_SingleBeadSuccess(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)

	bdShowTitleFn = func(id string) (string, error) { return "Fix the bug", nil }
	var claimedID, claimedAgent string
	bdUpdateClaimFn = func(id, agent string) error {
		claimedID = id
		claimedAgent = agent
		return nil
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"assign", "eng1", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claimedID != "ini-abc" {
		t.Errorf("claimed ID = %q, want ini-abc", claimedID)
	}
	if claimedAgent != "eng1" {
		t.Errorf("claimed agent = %q, want eng1", claimedAgent)
	}
	if !strings.Contains(stderr.String(), "assigned 1 bead(s) to eng1") {
		t.Errorf("stderr = %q, want assignment confirmation", stderr.String())
	}
}

func TestRunAssign_MultiBeadSuccess(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)

	bdShowTitleFn = func(id string) (string, error) { return "Task " + id, nil }

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"assign", "eng1", "ini-a", "ini-b", "ini-c"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr.String(), "assigned 3 bead(s)") {
		t.Errorf("stderr = %q, want 3 beads assigned", stderr.String())
	}
}

func TestRunAssign_PartialFailure(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)

	bdShowTitleFn = func(id string) (string, error) {
		if id == "ini-bad" {
			return "", fmt.Errorf("bead %s not found", id)
		}
		return "Task " + id, nil
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"assign", "eng1", "ini-ok", "ini-bad"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("partial failure should succeed (1 of 2): %v", err)
	}
	if !strings.Contains(stderr.String(), "assigned 1 bead(s)") {
		t.Errorf("stderr = %q, want 1 bead assigned", stderr.String())
	}
	if !strings.Contains(stderr.String(), "failed: ini-bad") {
		t.Errorf("stderr = %q, want failure listed", stderr.String())
	}
}

func TestRunAssign_AllFail(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)

	bdShowTitleFn = func(id string) (string, error) {
		return "", fmt.Errorf("bead %s not found", id)
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"assign", "eng1", "ini-a", "ini-b"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when all beads fail")
	}
	if !strings.Contains(err.Error(), "no beads could be assigned") {
		t.Errorf("error = %q, want 'no beads could be assigned'", err.Error())
	}
}

func TestRunAssign_DeduplicatesBeads(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)

	var claimCount int
	bdShowTitleFn = func(id string) (string, error) { return "Task", nil }
	bdUpdateClaimFn = func(id, agent string) error {
		claimCount++
		return nil
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"assign", "eng1", "ini-abc", "ini-abc", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claimCount != 1 {
		t.Errorf("claimed %d times, want 1 (dedup)", claimCount)
	}
}

func TestRunAssign_CrossMachineAgent(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)

	bdShowTitleFn = func(id string) (string, error) { return "Task", nil }

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"assign", "workbench:eng1", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr.String(), "assigned 1 bead(s) to eng1") {
		t.Errorf("stderr = %q, want agent name without host prefix", stderr.String())
	}
}

func TestRunAssign_ClaimFailure(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)

	bdShowTitleFn = func(id string) (string, error) { return "Task", nil }
	bdUpdateClaimFn = func(id, agent string) error {
		return fmt.Errorf("bd update failed: locked")
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"assign", "eng1", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when claim fails")
	}
}

func TestRunAssign_IPCDispatchFailure(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)

	bdShowTitleFn = func(id string) (string, error) { return "Task", nil }

	// IPC returns error on send.
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: false, Error: "agent not found"})
	t.Setenv("INITECH_SOCKET", sockPath)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"assign", "eng1", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when IPC dispatch fails")
	}
	if !strings.Contains(err.Error(), "dispatch failed") {
		t.Errorf("error = %q, want 'dispatch failed'", err.Error())
	}
}
