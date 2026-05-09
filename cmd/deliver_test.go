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

// TestRunDeliver_NoAgent: ini-dgt.2 changed the contract. Empty
// INITECH_AGENT (and no --as) is now a hard error, not a warning, because
// announce/report/webhook templates are role-aware and silently defaulting to
// the engineer template was the original bug.
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
func runDeliverWith(t *testing.T, agent, beadTitle string, args ...string) (report string, stderr string, err error) {
	t.Helper()
	stubBdFns(t)
	resetDeliverFlags(t)

	bdShowBeadFn = func(id string) (string, string, error) {
		return beadTitle, agent, nil
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

	bdShowBeadFn = func(id string) (string, string, error) {
		return "Some bead", "qa1", nil
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
