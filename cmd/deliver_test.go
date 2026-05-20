package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/nmelo/initech/internal/lifecycle"
	"github.com/nmelo/initech/internal/roles"
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
// Also stubs lifecycle.ConfigGetFn (ini-6e54) to return the default initech
// custom-status list, so the lifecycle walker can build a real chain without
// shelling out to bd. bdShowBeadFn defaults to returning status "in_progress"
// so deliver has a valid starting point in the chain — tests that need to
// exercise a specific state override the stub.
func stubBdFns(t *testing.T) {
	t.Helper()
	isolateFromProject(t)
	origShow := bdShowBeadFn
	origUpdate := bdUpdateStatusFn
	origComment := bdCommentAddFn
	origTitle := bdShowTitleFn
	origClaim := bdUpdateClaimFn
	origLifecycle := lifecycle.ConfigGetFn
	t.Cleanup(func() {
		bdShowBeadFn = origShow
		bdUpdateStatusFn = origUpdate
		bdCommentAddFn = origComment
		bdShowTitleFn = origTitle
		bdUpdateClaimFn = origClaim
		lifecycle.ConfigGetFn = origLifecycle
	})
	bdShowBeadFn = func(id string) (string, string, string, error) { return id, "", "in_progress", nil }
	bdUpdateStatusFn = func(id, status string) error { return nil }
	bdCommentAddFn = func(id, author, comment string) error { return nil }
	bdShowTitleFn = func(id string) (string, error) { return id, nil }
	bdUpdateClaimFn = func(id, agent string) error { return nil }
	lifecycle.ConfigGetFn = func(key string) (string, error) {
		// Default initech chain: [open, in_progress] + custom + [closed].
		return "ready_for_qa,in_qa,qa_passed,ready_to_ship", nil
	}
}

// resetDeliverFlags resets the package-level flag vars to defaults.
func resetDeliverFlags(t *testing.T) {
	t.Helper()
	deliverFail = false
	deliverPass = false
	deliverReason = ""
	deliverTo = "super"
	deliverMessage = ""
	deliverVerdict = ""
	deliverAs = ""
	t.Cleanup(func() {
		deliverFail = false
		deliverPass = false
		deliverReason = ""
		deliverTo = "super"
		deliverMessage = ""
		deliverVerdict = ""
		deliverAs = ""
	})
}

func TestRunDeliver_PassSuccess(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Fix the login bug", "eng1", "in_progress", nil
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

// TestRunDeliver_FailMode: --fail on a non-initial state walks the bead
// back one step in the lifecycle AND records a FAILED audit comment
// (ini-6e54 Q2). The pre-walker behavior (no status write on --fail) was
// removed; --fail now does both the comment and the walk-back.
func TestRunDeliver_FailMode(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Fix the bug", "eng1", "in_progress", nil
	}
	var commentText string
	bdCommentAddFn = func(id, author, comment string) error {
		commentText = comment
		return nil
	}
	var statusWritten string
	bdUpdateStatusFn = func(id, status string) error {
		statusWritten = status
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
	// --fail on in_progress walks back to open (the initial state).
	if statusWritten != "open" {
		t.Errorf("expected --fail to walk back to open, got status=%q", statusWritten)
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

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "", "", "", fmt.Errorf("bead %s not found", id)
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")

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

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Fix it", "eng1", "in_progress", nil
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

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Fix it", "eng2", "in_progress", nil
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

// TestRunDeliver_NoAgent: ini-dgt.2 changed the contract. Empty
// INITECH_AGENT (and no --as) is now a hard error, not a warning, because
// announce/report/webhook templates are role-aware and silently defaulting to
// the engineer template was the original bug.
func TestRunDeliver_NoAgent(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Fix it", "", "in_progress", nil
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "")

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"deliver", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error for missing INITECH_AGENT and --as, got nil")
	}
	if !strings.Contains(err.Error(), "cannot detect role") {
		t.Errorf("err = %q, want 'cannot detect role'", err)
	}
}

func TestRunDeliver_CustomMessage(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Fix it", "eng1", "in_progress", nil
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

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Fix it", "eng1", "in_progress", nil
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

// --- ini-dgt.2 coverage: family-aware deliver templates ---
//
// These tests close the gap noted in the triage: the original deliver_test.go
// only exercised eng1, so the engineer-template-regardless-of-role bug shipped
// with green tests. The unit tests below cover selectTemplate and
// validateDeliverFlags directly (cheap, no IPC), and the integration tests
// capture the IPC report payload to prove the wiring is correct end-to-end.

// startCapturingFakeIPC is a startFakeIPC variant that records every IPCRequest
// it receives. Returns the socket path and a pointer to the slice of received
// requests in arrival order.
func startCapturingFakeIPC(t *testing.T, resp tui.IPCResponse) (string, *[]tui.IPCRequest) {
	t.Helper()
	n := fakeIPCCounter.Add(1)
	sockPath := fmt.Sprintf("/tmp/initech-test-cap-%d-%d.sock", os.Getpid(), n)
	os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sockPath) })

	var mu sync.Mutex
	var received []tui.IPCRequest
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			var req tui.IPCRequest
			dec := json.NewDecoder(conn)
			if err := dec.Decode(&req); err == nil {
				mu.Lock()
				received = append(received, req)
				mu.Unlock()
			}
			data, _ := json.Marshal(resp)
			conn.Write(data)
			conn.Write([]byte("\n"))
			conn.Close()
		}
	}()
	return sockPath, &received
}

// runDeliverWith executes the deliver command with the given args and the
// fake-IPC + bd stubs already wired. Returns the captured report (the IPC
// "send" action body), stderr, and any error from rootCmd.Execute.
//
// Default bead state is "in_progress" so the lifecycle walker (ini-6e54)
// has a valid starting point. Tests that need a different state should
// reassign bdShowBeadFn after calling this helper.
func runDeliverWith(t *testing.T, agent, beadTitle string, args ...string) (report string, stderr string, err error) {
	t.Helper()
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return beadTitle, agent, "in_progress", nil
	}

	sockPath, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", agent)

	var stderrBuf bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderrBuf)
	rootCmd.SetArgs(append([]string{"deliver", "ini-test"}, args...))
	defer rootCmd.SetArgs(nil)

	err = rootCmd.Execute()

	for _, req := range *received {
		if req.Action == "send" {
			report = req.Text
			break
		}
	}
	return report, stderrBuf.String(), err
}

func TestRunDeliver_QA_PassVerdict(t *testing.T) {
	skipWindows(t)
	report, stderr, err := runDeliverWith(t, "qa1", "Login flow regression", "--verdict", "PASS")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(report, "PASS:") {
		t.Errorf("report should lead verdict, got %q", report)
	}
	if strings.Contains(report, "ready for QA") {
		t.Errorf("QA PASS report must not say 'ready for QA' (engineer template), got %q", report)
	}
	if !strings.Contains(report, "Login flow regression") {
		t.Errorf("report should include title, got %q", report)
	}
	if !strings.Contains(stderr, "PASS") {
		t.Errorf("stderr summary should mention PASS, got %q", stderr)
	}
}

func TestRunDeliver_QA_FailVerdict(t *testing.T) {
	skipWindows(t)
	report, _, err := runDeliverWith(t, "qa2", "Auth bug",
		"--verdict", "FAIL", "--reason", "logout broken")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(report, "FAIL:") {
		t.Errorf("QA FAIL report should lead with FAIL:, got %q", report)
	}
	if !strings.Contains(report, "logout broken") {
		t.Errorf("QA FAIL report should include reason, got %q", report)
	}
}

func TestRunDeliver_QA_FailFlagAlias(t *testing.T) {
	// --fail without --verdict for QA should be treated as --verdict FAIL,
	// so QA users coming from eng habits don't have to learn a new flag.
	skipWindows(t)
	report, _, err := runDeliverWith(t, "qa1", "Some bug",
		"--fail", "--reason", "broken")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(report, "FAIL:") {
		t.Errorf("--fail for QA should produce FAIL: template, got %q", report)
	}
}

func TestRunDeliver_QA_MissingVerdictRejected(t *testing.T) {
	// Contract test eng1 also asserts in dgt.1: QA with neither --verdict nor
	// --fail must error BEFORE any bd writes happen.
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	statusUpdated := false
	bdUpdateStatusFn = func(id, status string) error {
		statusUpdated = true
		return nil
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "qa1")

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"deliver", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for QA without --verdict, got nil")
	}
	if !strings.Contains(err.Error(), "verdict") {
		t.Errorf("err = %q, want mention of verdict", err)
	}
	if statusUpdated {
		t.Error("validation must run before bd update; status was written")
	}
}

func TestRunDeliver_Eng_VerdictRejected(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"deliver", "ini-abc", "--verdict", "PASS"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for eng + --verdict, got nil")
	}
	if !strings.Contains(err.Error(), "verdict") {
		t.Errorf("err = %q, want mention of verdict", err)
	}
}

func TestRunDeliver_Other_GenericTemplate(t *testing.T) {
	skipWindows(t)
	report, _, err := runDeliverWith(t, "pm", "Spec for live mode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(report, "delivered:") {
		t.Errorf("Other family report should say 'delivered:', got %q", report)
	}
	if strings.Contains(report, "ready for QA") {
		t.Errorf("Other family must not use eng 'ready for QA' template, got %q", report)
	}
	if strings.Contains(report, "PASS") || strings.Contains(report, "FAIL") {
		t.Errorf("Other family must not use QA verdict template, got %q", report)
	}
}

func TestRunDeliver_Other_FailTemplate(t *testing.T) {
	skipWindows(t)
	report, _, err := runDeliverWith(t, "shipper", "Release v1.21",
		"--fail", "--reason", "checksum mismatch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(report, "delivery failed:") {
		t.Errorf("Other family fail report should say 'delivery failed:', got %q", report)
	}
	if !strings.Contains(report, "checksum mismatch") {
		t.Errorf("Other family fail report should include reason, got %q", report)
	}
}

func TestRunDeliver_AsOverride(t *testing.T) {
	// --as <role> must override INITECH_AGENT for family detection. Verifies a
	// caller with INITECH_AGENT=eng1 can deliver as qa1 by passing --as.
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Some bead", "qa1", "in_qa", nil
	}

	sockPath, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1") // wrong env, should be overridden

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"deliver", "ini-abc", "--as", "qa1", "--verdict", "PASS"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report string
	for _, req := range *received {
		if req.Action == "send" {
			report = req.Text
			break
		}
	}
	if !strings.Contains(report, "PASS:") {
		t.Errorf("--as qa1 should produce QA template, got %q", report)
	}
}

func TestRunDeliver_VerdictPassConflictWithFail(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "qa1")

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"deliver", "ini-abc", "--verdict", "PASS", "--fail"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for --verdict PASS + --fail")
	}
	if !strings.Contains(err.Error(), "conflicts") {
		t.Errorf("err = %q, want 'conflicts'", err)
	}
}

func TestRunDeliver_InvalidVerdictRejected(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "qa1")

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"deliver", "ini-abc", "--verdict", "MAYBE"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid verdict")
	}
	if !strings.Contains(err.Error(), "PASS or FAIL") {
		t.Errorf("err = %q, want 'PASS or FAIL'", err)
	}
}

// TestRunDeliver_Eng_RegressionTemplates: byte-level regression check that the
// engineer template is unchanged from the pre-dgt.2 strings. This is the load-
// bearing test for Q3 (silent default for eng). If any of these strings drift,
// every workspace's CLAUDE.md and operator muscle memory breaks.
func TestRunDeliver_Eng_RegressionTemplates(t *testing.T) {
	skipWindows(t)

	t.Run("pass", func(t *testing.T) {
		report, stderr, err := runDeliverWith(t, "eng1", "Auth refactor")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "[from eng1] ini-test: Auth refactor ready for QA"
		if report != want {
			t.Errorf("report = %q\n want = %q", report, want)
		}
		if !strings.Contains(stderr, "delivered ini-test (ready for QA) -> super") {
			t.Errorf("stderr = %q, want 'delivered ini-test (ready for QA) -> super'", stderr)
		}
	})

	t.Run("fail with reason", func(t *testing.T) {
		report, _, err := runDeliverWith(t, "eng2", "Login bug",
			"--fail", "--reason", "tests broken")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "[from eng2] ini-test: Login bug FAILED: tests broken"
		if report != want {
			t.Errorf("report = %q\n want = %q", report, want)
		}
	})

	t.Run("fail no reason", func(t *testing.T) {
		report, _, err := runDeliverWith(t, "eng3", "Some bug", "--fail")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "[from eng3] ini-test: Some bug FAILED: no reason provided"
		if report != want {
			t.Errorf("report = %q\n want = %q", report, want)
		}
	})
}

// --- selectTemplate unit tests (no IPC, fast, table-driven) ---

func TestSelectTemplate(t *testing.T) {
	tests := []struct {
		name              string
		family            roles.RoleFamily
		isFail            bool
		verdict           string
		reason            string
		title             string
		agent             string
		wantKind          string
		wantRadioPrefix   string
		wantReportPrefix  string
		wantSummarySuffix string
	}{
		{
			name: "eng pass", family: roles.FamilyEng, agent: "eng1", title: "Auth refactor",
			wantKind: "agent.completed", wantRadioPrefix: "eng1 finished:",
			wantReportPrefix: "Auth refactor ready for QA", wantSummarySuffix: "ready for QA",
		},
		{
			name: "eng fail with reason", family: roles.FamilyEng, isFail: true, reason: "tests broken",
			agent: "eng2", title: "Login bug",
			wantKind: "agent.failed", wantRadioPrefix: "eng2 hit a wall: tests broken",
			wantReportPrefix: "Login bug FAILED: tests broken", wantSummarySuffix: "FAILED: tests broken",
		},
		{
			name: "eng fail no reason", family: roles.FamilyEng, isFail: true,
			agent: "eng3", title: "Bug",
			wantKind: "agent.failed", wantRadioPrefix: "eng3 hit a wall",
			wantReportPrefix: "Bug FAILED: no reason provided",
			wantSummarySuffix: "FAILED: no reason provided",
		},
		{
			name: "qa pass", family: roles.FamilyQA, verdict: "PASS",
			agent: "qa1", title: "Login regression",
			wantKind: "agent.completed", wantRadioPrefix: "qa1 PASS:",
			wantReportPrefix: "PASS:", wantSummarySuffix: "PASS",
		},
		{
			name: "qa fail", family: roles.FamilyQA, isFail: true, verdict: "FAIL", reason: "logout broken",
			agent: "qa2", title: "Auth bug",
			wantKind: "agent.failed", wantRadioPrefix: "qa2 FAIL:",
			wantReportPrefix: "FAIL:", wantSummarySuffix: "FAIL: logout broken",
		},
		{
			name: "other pass generic", family: roles.FamilyOther,
			agent: "pm", title: "Spec for live mode",
			wantKind: "agent.completed", wantRadioPrefix: "pm delivered:",
			wantReportPrefix: "delivered:", wantSummarySuffix: "delivered",
		},
		{
			name: "other fail generic", family: roles.FamilyOther, isFail: true, reason: "missing approval",
			agent: "shipper", title: "Release v1.21",
			wantKind: "agent.failed", wantRadioPrefix: "shipper delivery failed: missing approval",
			wantReportPrefix: "Release v1.21 delivery failed: missing approval",
			wantSummarySuffix: "delivery failed: missing approval",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tpl := selectTemplate(tt.family, tt.isFail, tt.verdict, tt.reason, tt.title, tt.agent)
			if tpl.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", tpl.Kind, tt.wantKind)
			}
			if !strings.HasPrefix(tpl.RadioDetail, tt.wantRadioPrefix) {
				t.Errorf("RadioDetail = %q, want prefix %q", tpl.RadioDetail, tt.wantRadioPrefix)
			}
			if !strings.HasPrefix(tpl.ReportText, tt.wantReportPrefix) {
				t.Errorf("ReportText = %q, want prefix %q", tpl.ReportText, tt.wantReportPrefix)
			}
			if tpl.SummarySuffix != tt.wantSummarySuffix {
				t.Errorf("SummarySuffix = %q, want %q", tpl.SummarySuffix, tt.wantSummarySuffix)
			}
		})
	}
}

// --- ini-dgt.1: family-aware status transitions + qa_passed/closed no-op guard ---

// runDeliverWithStatus is like runDeliverWith but also lets the caller seed the
// bead's current status — needed for outer-guard tests (qa_passed/closed) and
// for asserting which status value gets written by the family branch. Returns
// the status value written by bdUpdateStatusFn (empty string if never called),
// the IPC requests captured during the run, the stderr buffer, and any error.
func runDeliverWithStatus(t *testing.T, agent, beadTitle, beadStatus string, args ...string) (writtenStatus string, requests []tui.IPCRequest, stderr string, err error) {
	t.Helper()
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return beadTitle, agent, beadStatus, nil
	}
	bdUpdateStatusFn = func(id, status string) error {
		writtenStatus = status
		return nil
	}

	sockPath, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", agent)

	var stderrBuf bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderrBuf)
	rootCmd.SetArgs(append([]string{"deliver", "ini-test"}, args...))
	defer rootCmd.SetArgs(nil)

	err = rootCmd.Execute()
	return writtenStatus, *received, stderrBuf.String(), err
}

// hasIPCSend reports whether any of the captured IPC requests was a "send"
// action (i.e. a report to another agent). Used by no-op guard tests to assert
// that nothing left the box.
func hasIPCSend(reqs []tui.IPCRequest) bool {
	for _, r := range reqs {
		if r.Action == "send" {
			return true
		}
	}
	return false
}

func TestRunDeliver_QA_PassVerdict_WritesQaPassed(t *testing.T) {
	skipWindows(t)
	written, _, _, err := runDeliverWithStatus(t, "qa1", "Login flow", "in_qa", "--verdict", "PASS")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if written != "qa_passed" {
		t.Errorf("expected status write to qa_passed, got %q", written)
	}
}

func TestRunDeliver_Eng_PassWritesReadyForQa(t *testing.T) {
	// Mirror of TestRunDeliver_PassSuccess with explicit current-status seed,
	// so the byte-for-byte eng regression contract is enforced even after the
	// status field flows through bdShowBeadFn.
	skipWindows(t)
	written, _, _, err := runDeliverWithStatus(t, "eng1", "Auth bug", "in_progress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if written != "ready_for_qa" {
		t.Errorf("Eng pass must write ready_for_qa, got %q", written)
	}
}

// TestRunDeliver_QaPassed_FullNoOp is the named regression for the headline
// ini-dgt.1 bug: a deliver against an already-qa_passed bead must NOT write
// status, must NOT send a report, and must exit 0 with a warning. Carries the
// bug's name in the suite forever — if this fails, the regression is back.
func TestRunDeliver_Closed_FullNoOp(t *testing.T) {
	skipWindows(t)
	written, reqs, stderr, err := runDeliverWithStatus(t, "eng1", "Done long ago", "closed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if written != "" {
		t.Errorf("closed must no-op status write, got %q", written)
	}
	if hasIPCSend(reqs) {
		t.Error("closed no-op must not send a report")
	}
	if !strings.Contains(stderr, "no-op") || !strings.Contains(stderr, "closed") {
		t.Errorf("expected no-op warning mentioning closed, got stderr=%q", stderr)
	}
}

// TestRunDeliver_QaPassed_FailComment_AuditTrail: --fail on qa_passed is the
// one carve-out from the no-op guard. QA needs a way to record post-pass
// regressions on the bead even though the status is terminal.
// TestRunDeliver_Closed_FailFullySkipped: --fail on closed is fully a no-op
// (no comment, no status, no report). Commenting on a closed bead is noise.
// TestRunDeliver_NormalStatusesProceed asserts the gate is narrow: every
// non-terminal status that isn't in_qa routes through the family branch for
// Eng. in_qa is excluded from this list because Eng-on-in_qa is its own
// no-op (covered separately); it would otherwise yank the bead away from
// the QA reviewer mid-review. The only universal no-op statuses are
// qa_passed and closed.
// TestRunDeliver_Eng_InQa_NoOp covers the Eng-on-in_qa carve-out (qa1's A3
// regression on the first round of ini-dgt.1). An engineer running deliver
// while QA is mid-review must warn and skip the status reset, because
// writing ready_for_qa here yanks the bead out from under the reviewer.
// This is family-conditional (NOT a universal outer guard) so the QA-on-
// in_qa flow (PASS -> qa_passed, FAIL -> stays) is preserved.
// lint:test-name-allow no-op-contract  // ini-ybe.1: contract IS the no-op; body has 3 assertions verifying it
// TestRunDeliver_QA_InQa_PreservedByCarveOut is the contract test that the
// Eng-on-in_qa carve-out did NOT also block the QA flow. If this fails,
// someone widened the carve-out into a universal guard (the regression
// shape qa1's literal one-line sketch would have introduced).
// TestRunDeliver_Eng_InQa_FailUnchanged: --fail on Eng+in_qa is intentionally
// NOT covered by the carve-out. An engineer recording a regression mid-QA
// is useful audit data and doesn't reset status anyway.
// --- ini-lwd: success path writes -m body as a bd comment ---

// runDeliverCapturingComments wires the same fake IPC + bd stubs as
// runDeliverWith, but also captures every (author, body) pair passed to
// bdCommentAddFn. Returns the list of comments written (in order), the
// captured IPC requests, the stderr buffer, and any rootCmd error. Useful
// for asserting that the success-path comment-add fired exactly once with
// the expected body, and that the failure-path didn't double-up.
func runDeliverCapturingComments(t *testing.T, agent, beadTitle string, args ...string) (comments []deliverComment, requests []tui.IPCRequest, stderr string, err error) {
	t.Helper()
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return beadTitle, agent, "in_progress", nil
	}
	bdCommentAddFn = func(id, author, body string) error {
		comments = append(comments, deliverComment{id: id, author: author, body: body})
		return nil
	}

	sockPath, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", agent)

	var stderrBuf bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderrBuf)
	rootCmd.SetArgs(append([]string{"deliver", "ini-test"}, args...))
	defer rootCmd.SetArgs(nil)

	err = rootCmd.Execute()
	return comments, *received, stderrBuf.String(), err
}

type deliverComment struct {
	id, author, body string
}

func TestRunDeliver_SuccessWithMessage_WritesComment(t *testing.T) {
	skipWindows(t)
	comments, _, _, err := runDeliverCapturingComments(t, "eng1", "Auth bug",
		"-m", "DONE: refactored auth, added 12 tests")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("want exactly 1 comment, got %d: %+v", len(comments), comments)
	}
	if comments[0].body != "DONE: refactored auth, added 12 tests" {
		t.Errorf("comment body = %q, want %q", comments[0].body, "DONE: refactored auth, added 12 tests")
	}
	if comments[0].author != "eng1" {
		t.Errorf("comment author = %q, want %q", comments[0].author, "eng1")
	}
	if comments[0].id != "ini-test" {
		t.Errorf("comment bead id = %q, want %q", comments[0].id, "ini-test")
	}
}

func TestRunDeliver_SuccessWithoutMessage_NoComment(t *testing.T) {
	skipWindows(t)
	comments, _, _, err := runDeliverCapturingComments(t, "eng1", "Auth bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("expected no comment when -m is absent, got %d: %+v", len(comments), comments)
	}
}

func TestRunDeliver_SuccessWithEmptyMessage_NoComment(t *testing.T) {
	skipWindows(t)
	comments, _, _, err := runDeliverCapturingComments(t, "eng1", "Auth bug", "-m", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("expected no comment for empty -m, got %d: %+v", len(comments), comments)
	}
}

// TestRunDeliver_FailWithMessage_OnlyFailedComment: -m on --fail stays
// chat-only. The bead's audit trail comment is "FAILED: <reason>" from
// --reason, NOT a duplicate from -m. Verifies the asymmetry documented
// in the deliverCmd Long doc-comment.
func TestRunDeliver_FailWithMessage_OnlyFailedComment(t *testing.T) {
	skipWindows(t)
	comments, reqs, _, err := runDeliverCapturingComments(t, "eng1", "Auth bug",
		"--fail", "--reason", "tests broken", "-m", "chat-only note")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("want exactly 1 comment on --fail, got %d: %+v", len(comments), comments)
	}
	if !strings.Contains(comments[0].body, "FAILED: tests broken") {
		t.Errorf("comment body = %q, want it to contain 'FAILED: tests broken'", comments[0].body)
	}
	if strings.Contains(comments[0].body, "chat-only note") {
		t.Errorf("--fail comment body must not duplicate the -m body, got %q", comments[0].body)
	}
	// -m body still appears in the chat report.
	var report string
	for _, r := range reqs {
		if r.Action == "send" {
			report = r.Text
			break
		}
	}
	if !strings.Contains(report, "chat-only note") {
		t.Errorf("chat report must include the -m body, got %q", report)
	}
}

// TestRunDeliver_QA_PassWithMessage_WritesComment: contract test that the
// new -m -> bd comment path is family-agnostic (QA family also gets the
// comment landed). User-controlled body — no automatic verdict prefix.
func TestRunDeliver_QA_PassWithMessage_WritesComment(t *testing.T) {
	skipWindows(t)
	comments, _, _, err := runDeliverCapturingComments(t, "qa1", "Login flow",
		"--verdict", "PASS", "-m", "PASS: all AC met")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("want exactly 1 comment, got %d: %+v", len(comments), comments)
	}
	if comments[0].body != "PASS: all AC met" {
		t.Errorf("QA comment body = %q, want it to land verbatim (no auto-prefix)", comments[0].body)
	}
}

// --- ini-98n: roster-aware role classification ---
//
// RoleFamilyOf shipped in ini-dgt.2 as a pure prefix+catalog classifier and
// rejected any custom role not in the built-in list. initech.yaml is the
// canonical roster for any project, so deliver now consults it as a third
// tier (after prefix and catalog) — custom roles like "practitioner" get the
// generic FamilyOther template instead of being rejected. Typo protection
// survives: names not in any tier still error.

// isolateFromProjectWithRoster is isolateFromProject with a caller-supplied
// roles list. Used by ini-98n tests that need a specific custom roster.
func isolateFromProjectWithRoster(t *testing.T, rosterRoles []string) {
	t.Helper()
	dir := t.TempDir()
	var rolesYAML strings.Builder
	for _, r := range rosterRoles {
		rolesYAML.WriteString("  - ")
		rolesYAML.WriteString(r)
		rolesYAML.WriteString("\n")
	}
	cfg := fmt.Sprintf("project: test\nroot: %s\nroles:\n%s", dir, rolesYAML.String())
	os.WriteFile(dir+"/initech.yaml", []byte(cfg), 0644)
	orig, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(orig) })
}

// stubBdFnsWithRoster is stubBdFns variant that takes an explicit roster.
// Also stubs lifecycle.ConfigGetFn so deliver's bd-lifecycle reader (ini-6e54)
// has a valid chain without shelling out.
func stubBdFnsWithRoster(t *testing.T, rosterRoles []string) {
	t.Helper()
	isolateFromProjectWithRoster(t, rosterRoles)
	origShow := bdShowBeadFn
	origUpdate := bdUpdateStatusFn
	origComment := bdCommentAddFn
	origTitle := bdShowTitleFn
	origClaim := bdUpdateClaimFn
	origLifecycle := lifecycle.ConfigGetFn
	t.Cleanup(func() {
		bdShowBeadFn = origShow
		bdUpdateStatusFn = origUpdate
		bdCommentAddFn = origComment
		bdShowTitleFn = origTitle
		bdUpdateClaimFn = origClaim
		lifecycle.ConfigGetFn = origLifecycle
	})
	bdShowBeadFn = func(id string) (string, string, string, error) { return id, "", "in_progress", nil }
	bdUpdateStatusFn = func(id, status string) error { return nil }
	bdCommentAddFn = func(id, author, comment string) error { return nil }
	bdShowTitleFn = func(id string) (string, error) { return id, nil }
	bdUpdateClaimFn = func(id, agent string) error { return nil }
	lifecycle.ConfigGetFn = func(key string) (string, error) {
		return "ready_for_qa,in_qa,qa_passed,ready_to_ship", nil
	}
}

// TestRunDeliver_CustomRoleFromRoster_Accepted: a non-prefix non-catalog
// role defined in initech.yaml is now accepted by deliver and gets the
// FamilyOther generic announce template. This is the core ini-98n fix.
func TestRunDeliver_CustomRoleFromRoster_Accepted(t *testing.T) {
	skipWindows(t)
	stubBdFnsWithRoster(t, []string{"super", "practitioner", "analyst"})
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Fix the bug", "practitioner", "in_progress", nil
	}

	sockPath, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "practitioner")

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"deliver", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("custom roster role must be accepted, got error: %v", err)
	}

	var report string
	for _, req := range *received {
		if req.Action == "send" {
			report = req.Text
			break
		}
	}
	// FamilyOther generic template: "<role> delivered: <title>".
	if !strings.Contains(report, "delivered:") {
		t.Errorf("custom-role report must use generic 'delivered:' template, got %q", report)
	}
	if strings.Contains(report, "ready for QA") {
		t.Errorf("custom role must NOT use eng 'ready for QA' template, got %q", report)
	}
	if strings.Contains(report, "PASS") || strings.Contains(report, "FAIL") {
		t.Errorf("custom role must NOT use QA verdict template, got %q", report)
	}
}

// TestRunDeliver_NotInRoster_Rejected: typo protection survives. A name not
// in any tier (prefix, catalog, roster) errors with the new message naming
// the actual roster so the user knows what to fix.
func TestRunDeliver_NotInRoster_Rejected(t *testing.T) {
	skipWindows(t)
	stubBdFnsWithRoster(t, []string{"super", "practitioner", "analyst"})
	resetDeliverFlags(t)

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "wronk")

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"deliver", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("name not in roster must error")
	}
	if !strings.Contains(err.Error(), "not in initech.yaml roster") {
		t.Errorf("error should name the policy, got %q", err)
	}
	// Error message should include the actual roster so the user can fix it.
	if !strings.Contains(err.Error(), "practitioner") || !strings.Contains(err.Error(), "analyst") {
		t.Errorf("error should list the known roster (practitioner, analyst), got %q", err)
	}
}

// TestRunDeliver_AsOverride_CustomRole: --as <custom-role> respects roster
// just like INITECH_AGENT does. Validates the override path against ini-98n.
func TestRunDeliver_AsOverride_CustomRole(t *testing.T) {
	skipWindows(t)
	stubBdFnsWithRoster(t, []string{"super", "practitioner", "analyst"})
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Some bead", "analyst", "in_progress", nil
	}

	sockPath, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "practitioner") // first roster entry

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	// --as analyst: also in roster, should be respected.
	rootCmd.SetArgs([]string{"deliver", "ini-abc", "--as", "analyst"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("--as custom roster role must be accepted, got error: %v", err)
	}

	var report string
	for _, req := range *received {
		if req.Action == "send" {
			report = req.Text
			break
		}
	}
	// Report should attribute to analyst (the --as value), not practitioner.
	if !strings.Contains(report, "[from analyst]") {
		t.Errorf("--as override should attribute to analyst, got %q", report)
	}
}

// TestRunDeliver_PrefixWinsOverRoster: a name like "engineer" has the eng
// prefix and must classify as FamilyEng even if it appears in the roster.
// Pinning this prevents a future refactor from accidentally reordering tiers
// and silently flipping engineer-template tests to other-template.
func TestRunDeliver_PrefixWinsOverRoster(t *testing.T) {
	skipWindows(t)
	// Roster includes "engineer" — but prefix should still win.
	stubBdFnsWithRoster(t, []string{"super", "engineer"})
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Title", "engineer", "in_progress", nil
	}

	sockPath, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "engineer")

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"deliver", "ini-abc"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("engineer (eng prefix) must be accepted, got error: %v", err)
	}

	var report string
	for _, req := range *received {
		if req.Action == "send" {
			report = req.Text
			break
		}
	}
	// Eng family uses "ready for QA" — NOT the generic "delivered:" Other template.
	if !strings.Contains(report, "ready for QA") {
		t.Errorf("engineer should use Eng family 'ready for QA' template, got %q", report)
	}
	if strings.Contains(report, "delivered:") {
		t.Errorf("engineer must NOT fall through to Other-family template even when in roster, got %q", report)
	}
}

// --- ini-6e54: lifecycle-walker tests ---
//
// deliver advances beads through bd's configured lifecycle chain
// (open → in_progress → ...custom states... → closed) one step per call.
// Role/family no longer gates status writes; anyone delivering on a bead
// moves it. Failure walks back one step AND records a FAILED audit comment.

// runDeliverFromStatus is a focused helper that drives deliver with a
// specific starting bead status and captures (statusWrite, comments, err)
// for the lifecycle assertions below. Keeps the test bodies short so the
// load-bearing full-walk test stays scannable.
func runDeliverFromStatus(t *testing.T, agent, startStatus string, args ...string) (statusWritten string, comments []deliverComment, err error) {
	t.Helper()
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "bead-title", agent, startStatus, nil
	}
	bdUpdateStatusFn = func(id, status string) error {
		statusWritten = status
		return nil
	}
	bdCommentAddFn = func(id, author, body string) error {
		comments = append(comments, deliverComment{id: id, author: author, body: body})
		return nil
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", agent)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(append([]string{"deliver", "ini-test"}, args...))
	defer rootCmd.SetArgs(nil)

	err = rootCmd.Execute()
	return statusWritten, comments, err
}

// TestDeliver_FullLifecycleWalk_Forward is the load-bearing regression
// super specified: exercise the user's full real lifecycle end-to-end
// (open → in_progress → ready_for_qa → in_qa → qa_passed → ready_to_ship
// → closed) via successive deliver calls. The previous test discipline
// (assert a single pair) missed the cross-pair chain construction bugs.
// This walks every pair in one test so any chain-shape regression fails
// here, not in production.
func TestDeliver_FullLifecycleWalk_Forward(t *testing.T) {
	skipWindows(t)
	// Default initech chain comes from stubBdFns:
	//   open → in_progress → ready_for_qa → in_qa → qa_passed → ready_to_ship → closed
	tests := []struct {
		from, to string
	}{
		{"open", "in_progress"},
		{"in_progress", "ready_for_qa"},
		{"ready_for_qa", "in_qa"},
		{"in_qa", "qa_passed"},
		{"qa_passed", "ready_to_ship"},
		{"ready_to_ship", "closed"},
	}
	for _, tt := range tests {
		t.Run(tt.from+"_to_"+tt.to, func(t *testing.T) {
			// QA-family agent uses --verdict so validateDeliverFlags accepts
			// the call regardless of starting state. The status write is
			// purely lifecycle-driven now (role doesn't gate it).
			got, _, err := runDeliverFromStatus(t, "qa1", tt.from, "--verdict", "PASS")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.to {
				t.Errorf("from %s: deliver wrote status=%q, want %q", tt.from, got, tt.to)
			}
		})
	}
}

// TestDeliver_FailWalksBack: --fail walks the bead back one step in the
// chain AND records a FAILED audit comment (ini-6e54 Q2).
func TestDeliver_FailWalksBack(t *testing.T) {
	skipWindows(t)
	tests := []struct {
		from, to string
	}{
		{"in_progress", "open"},
		{"ready_for_qa", "in_progress"},
		{"in_qa", "ready_for_qa"},
		{"qa_passed", "in_qa"},
		{"ready_to_ship", "qa_passed"},
		{"closed", "ready_to_ship"},
	}
	for _, tt := range tests {
		t.Run(tt.from+"_back_to_"+tt.to, func(t *testing.T) {
			got, comments, err := runDeliverFromStatus(t, "qa1", tt.from, "--verdict", "FAIL", "--reason", "regression found")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.to {
				t.Errorf("--fail from %s: deliver wrote status=%q, want %q", tt.from, got, tt.to)
			}
			// FAILED audit comment must accompany the walk-back.
			foundFailed := false
			for _, c := range comments {
				if strings.Contains(c.body, "FAILED: regression found") {
					foundFailed = true
					break
				}
			}
			if !foundFailed {
				t.Errorf("--fail from %s: expected FAILED audit comment, got %+v", tt.from, comments)
			}
		})
	}
}

// TestDeliver_AtTerminal_SuccessNoOps: at the chain's terminal (closed),
// success no-ops with a warning and returns without writing status.
func TestDeliver_AtTerminal_SuccessNoOps(t *testing.T) {
	skipWindows(t)
	got, _, err := runDeliverFromStatus(t, "eng1", "closed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("deliver at closed must not write status, got %q", got)
	}
}

// TestDeliver_AtInitial_FailNoOps: at the chain's initial (open), --fail
// no-ops because there's no previous state to walk back to.
func TestDeliver_AtInitial_FailNoOps(t *testing.T) {
	skipWindows(t)
	got, comments, err := runDeliverFromStatus(t, "eng1", "open", "--fail", "--reason", "blocked")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("deliver --fail at open must not write status, got %q", got)
	}
	// Per ini-6e54 Q2 + bead "failure no-ops with a warning": no audit
	// comment is added on the initial-state no-op path. The walk-back is
	// the action; if we can't walk back, we skip the action+reason both.
	if len(comments) != 0 {
		t.Errorf("deliver --fail at open must not write comments, got %+v", comments)
	}
}

// TestDeliver_CustomLifecycle: a project with a custom status.custom
// (different from initech's default) drives a different chain, and
// deliver walks the new chain correctly. This proves the bead-AC
// requirement that "Custom states declared in bd ... participate
// naturally — no initech code change required to support a new state."
func TestDeliver_CustomLifecycle(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	// Simulate a code-review-heavy project.
	lifecycle.ConfigGetFn = func(key string) (string, error) {
		return "design_review,code_review,ready_to_ship", nil
	}

	// Bead is at design_review (custom state #1).
	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Some change", "eng1", "design_review", nil
	}
	var statusWritten string
	bdUpdateStatusFn = func(id, status string) error {
		statusWritten = status
		return nil
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"deliver", "ini-test"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// design_review → code_review (next in the custom chain).
	if statusWritten != "code_review" {
		t.Errorf("custom chain: design_review should advance to code_review, got %q", statusWritten)
	}
}

// TestDeliver_AnyRole_AdvancesStatus pins the Q3 contract: role/family does
// not gate the status write. Eng, QA, Other, and a custom-roster role all
// advance the bead one step. Announce templates still differ by role (ini-
// dgt.2) but that's tested separately.
func TestDeliver_AnyRole_AdvancesStatus(t *testing.T) {
	skipWindows(t)
	tests := []struct {
		role string
		args []string
	}{
		{"eng1", nil},
		{"qa1", []string{"--verdict", "PASS"}},
		{"shipper", nil},
		{"pm", nil},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			// Bead is at qa_passed; all roles should advance it to ready_to_ship.
			got, _, err := runDeliverFromStatus(t, tt.role, "qa_passed", tt.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != "ready_to_ship" {
				t.Errorf("role %s at qa_passed: deliver wrote %q, want ready_to_ship (any role can advance per Q3)",
					tt.role, got)
			}
		})
	}
}

// TestDeliver_BdUnavailable_HardFails (ini-6e54 Q4): when bd cannot be
// reached, deliver refuses to write status with a clear error message
// pointing the operator at the fix. No silent fallback to a hardcoded
// chain.
func TestDeliver_BdUnavailable_HardFails(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetDeliverFlags(t)

	// Force the lifecycle reader to error.
	lifecycle.ConfigGetFn = func(key string) (string, error) {
		return "", fmt.Errorf("bd: command not found")
	}

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng1")

	rootCmd.SetOut(&bytes.Buffer{})
	var stderr bytes.Buffer
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"deliver", "ini-test"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected hard error when bd is unavailable, got nil")
	}
	if !strings.Contains(err.Error(), "lifecycle") {
		t.Errorf("error should mention 'lifecycle' for operator clarity, got %q", err.Error())
	}
}
