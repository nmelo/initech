package cmd

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// isolateFromProject chdir to a temp dir with a minimal initech.yaml (no
// announce_url/webhook_url). config.Discover finds it, but announce/webhook
// calls bail out immediately because the URLs are empty.
func isolateFromProject(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	cfg := fmt.Sprintf("project: test\nroot: %s\nroles:\n  - eng1\n", dir)
	os.WriteFile(dir+"/initech.yaml", []byte(cfg), 0644)
	orig, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(orig) })
}

// stubBdFns overrides all bd function vars with stubs, isolates from the real
// project (prevents announce/webhook calls), and restores on cleanup.
func stubBdFns(t *testing.T) {
	t.Helper()
	isolateFromProject(t)
	origShow := bdShowBeadFn
	origUpdate := bdUpdateStatusFn
	origComment := bdCommentAddFn
	origTitle := bdShowTitleFn
	origClaim := bdUpdateClaimFn
	t.Cleanup(func() {
		bdShowBeadFn = origShow
		bdUpdateStatusFn = origUpdate
		bdCommentAddFn = origComment
		bdShowTitleFn = origTitle
		bdUpdateClaimFn = origClaim
	})
	bdShowBeadFn = func(id string) (string, string, error) { return id, "", nil }
	bdUpdateStatusFn = func(id, status string) error { return nil }
	bdCommentAddFn = func(id, author, comment string) error { return nil }
	bdShowTitleFn = func(id string) (string, error) { return id, nil }
	bdUpdateClaimFn = func(id, agent string) error { return nil }
}

// resetDeliverFlags resets the package-level flag vars to defaults.
func resetDeliverFlags(t *testing.T) {
	t.Helper()
	deliverFail = false
	deliverPass = false
	deliverReason = ""
	deliverTo = "super"
	deliverMessage = ""
	t.Cleanup(func() {
		deliverFail = false
		deliverPass = false
		deliverReason = ""
		deliverTo = "super"
		deliverMessage = ""
	})
}

func TestRunDeliver_PassSuccess(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, error) {
		return "Fix the login bug", "eng1", nil
	}
	var updatedStatus string
	bdUpdateStatusFn = func(id, status string) error {
		updatedStatus = status
		return nil
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"deliver", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updatedStatus != "ready_for_qa" {
		t.Errorf("expected status update to ready_for_qa, got %q", updatedStatus)
	}
	if !strings.Contains(stderr.String(), "delivered ini-abc (ready for QA)") {
		t.Errorf("stderr = %q, want confirmation message", stderr.String())
	}
}

func TestRunDeliver_FailMode(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, error) {
		return "Fix the bug", "eng1", nil
	}
	var commentText string
	bdCommentAddFn = func(id, author, comment string) error {
		commentText = comment
		return nil
	}
	var statusCalled bool
	bdUpdateStatusFn = func(id, status string) error {
		statusCalled = true
		return nil
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"deliver", "ini-abc", "--fail", "--reason", "tests broken"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if statusCalled {
		t.Error("fail mode should NOT call bdUpdateStatus")
	}
	if !strings.Contains(commentText, "FAILED: tests broken") {
		t.Errorf("comment = %q, want FAILED reason", commentText)
	}
	if !strings.Contains(stderr.String(), "FAILED: tests broken") {
		t.Errorf("stderr = %q, want FAILED confirmation", stderr.String())
	}
}

func TestRunDeliver_FailAndPassConflict(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"deliver", "ini-abc", "--fail", "--pass"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for --fail --pass conflict")
	}
	if !strings.Contains(err.Error(), "cannot specify both") {
		t.Errorf("error = %q, want 'cannot specify both'", err.Error())
	}
}

func TestRunDeliver_BeadNotFound(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, error) {
		return "", "", fmt.Errorf("bead %s not found", id)
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"deliver", "ini-nope"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing bead")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want 'not found'", err.Error())
	}
}

func TestRunDeliver_StatusUpdateError(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, error) {
		return "Fix it", "eng1", nil
	}
	bdUpdateStatusFn = func(id, status string) error {
		return fmt.Errorf("bd update failed: permission denied")
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"deliver", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when bd update fails")
	}
	if !strings.Contains(err.Error(), "bd update failed") {
		t.Errorf("error = %q, want 'bd update failed'", err.Error())
	}
}

func TestRunDeliver_AssigneeMismatchWarning(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, error) {
		return "Fix it", "eng2", nil
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"deliver", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr.String(), "assigned to eng2, you are eng1") {
		t.Errorf("stderr = %q, want assignee mismatch warning", stderr.String())
	}
}

func TestRunDeliver_NoAgent(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, error) {
		return "Fix it", "", nil
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "")

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"deliver", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr.String(), "INITECH_AGENT not set") {
		t.Errorf("stderr = %q, want INITECH_AGENT warning", stderr.String())
	}
}

func TestRunDeliver_CustomMessage(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, error) {
		return "Fix it", "eng1", nil
	}

	// Capture what gets sent to IPC to verify message appears in report.
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"deliver", "ini-abc", "-m", "also fixed lint"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr.String(), "delivered ini-abc") {
		t.Errorf("stderr = %q, want delivery confirmation", stderr.String())
	}
}

func TestRunDeliver_CrossMachineRecipient(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, error) {
		return "Fix it", "eng1", nil
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"deliver", "ini-abc", "--to", "workbench:super"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr.String(), "workbench:super") {
		t.Errorf("stderr = %q, want cross-machine recipient in output", stderr.String())
	}
}

func TestAgentOrUnknown(t *testing.T) {
	if got := agentOrUnknown(""); got != "unknown" {
		t.Errorf("agentOrUnknown('') = %q, want 'unknown'", got)
	}
	if got := agentOrUnknown("eng1"); got != "eng1" {
		t.Errorf("agentOrUnknown('eng1') = %q, want 'eng1'", got)
	}
}
