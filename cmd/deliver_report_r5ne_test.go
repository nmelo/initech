package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// r5neDeliver runs one `initech deliver` from the given bead state and
// returns every report it sent (IPC "send" requests).
func r5neDeliver(t *testing.T, agent, state string, args ...string) []tui.IPCRequest {
	t.Helper()
	stubBdFns(t)
	resetDeliverFlags(t)
	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Release notes v2.19.1", agent, state, nil
	}
	sock, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sock)
	t.Setenv("INITECH_AGENT", agent)
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(append([]string{"deliver", "ini-o09i"}, args...))
	defer rootCmd.SetArgs(nil)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("deliver from %s: %v", state, err)
	}
	var sends []tui.IPCRequest
	for _, r := range *received {
		if r.Action == "send" {
			sends = append(sends, r)
		}
	}
	return sends
}

// The observed case (ini-o09i), under the chain that ends at qa_passed
// (ini-q2k): one agent walks a skip-QA bead from ready_for_qa to qa_passed and
// no step reports (the same agent continues); a deliver at qa_passed writes
// nothing; ready_to_ship is a side state and is refused. Super closes the
// bead with bd close, so there is no "closed:" report any more.
func TestRunDeliver_ASkipQAWalkStopsAtQAPassedWithNoReport(t *testing.T) {
	for _, state := range []string{"ready_for_qa", "in_qa", "qa_passed"} {
		if sends := r5neDeliver(t, "pm", state); len(sends) != 0 {
			t.Fatalf("step from %s by the same agent sent %d report(s): %q", state, len(sends), sends[0].Text)
		}
	}
	stubBdFns(t)
	resetDeliverFlags(t)
	bdShowBeadFn = func(id string) (string, string, string, error) { return "Release notes v2.19.1", "pm", "ready_to_ship", nil }
	sock, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sock)
	t.Setenv("INITECH_AGENT", "pm")
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"deliver", "ini-o09i"})
	defer rootCmd.SetArgs(nil)
	if err := rootCmd.Execute(); err == nil || !strings.Contains(err.Error(), "is ready_to_ship") {
		t.Fatalf("deliver from ready_to_ship: err %v, want the side-state refusal", err)
	}
	if len(*received) != 0 {
		t.Fatalf("the refusal sent %d IPC requests", len(*received))
	}
}

// A hand-off still reports, to the named QA: someone else acts next.
func TestRunDeliver_AnImplementerHandoffStillReportsToTheNamedQA(t *testing.T) {
	sends := r5neDeliver(t, "eng2", "in_progress", "--to", "qa2")
	if len(sends) != 1 || sends[0].Target != "qa2" {
		t.Fatalf("handoff sent %+v, want one report to qa2", sends)
	}
}

// A QA verdict always reports, whatever the assignee does.
func TestRunDeliver_AQAVerdictStillReports(t *testing.T) {
	if sends := r5neDeliver(t, "qa2", "in_qa", "--verdict", "PASS"); len(sends) != 1 {
		t.Fatalf("QA PASS sent %d reports, want 1", len(sends))
	}
	if sends := r5neDeliver(t, "qa2", "in_qa", "--verdict", "FAIL", "--reason", "broken"); len(sends) != 1 {
		t.Fatalf("QA FAIL sent %d reports, want 1", len(sends))
	}
}

// --report forces a report on a step that would otherwise be silent.
func TestRunDeliver_ReportFlagForcesAReportOnASameAgentStep(t *testing.T) {
	if sends := r5neDeliver(t, "pm", "in_qa", "--report"); len(sends) != 1 {
		t.Fatalf("--report on a walk step sent %d reports, want 1", len(sends))
	}
}
