package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// y8vRun runs one initech deliver from the given bead state and counts the bd
// reads and writes it made.
func y8vRun(t *testing.T, sock, agent, assignee, state string, args ...string) (updates, shows int, stderr string, err error) {
	t.Helper()
	stubBdFns(t)
	resetDeliverFlags(t)
	bdShowBeadFn = func(id string) (string, string, string, error) {
		shows++
		return "Hand it off", assignee, state, nil
	}
	bdUpdateBeadFn = func(id string, w beadWrite) error {
		updates++
		return nil
	}
	t.Setenv("INITECH_SOCKET", sock)
	t.Setenv("INITECH_AGENT", agent)
	errBuf := &bytes.Buffer{}
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs(append([]string{"deliver", "ini-y8v"}, args...))
	defer rootCmd.SetArgs(nil)
	err = rootCmd.Execute()
	return updates, shows, errBuf.String(), err
}

func y8vSends(received *[]tui.IPCRequest) []tui.IPCRequest {
	var sends []tui.IPCRequest
	for _, r := range *received {
		if r.Action == "send" {
			sends = append(sends, r)
		}
	}
	return sends
}

func y8vSummary(t *testing.T, stderr string) string {
	t.Helper()
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, "delivered ini-y8v:") {
			return l
		}
	}
	t.Fatalf("no summary line in:\n%s", stderr)
	return ""
}

// AC1: a step the table treats as the same agent continuing, with --to: the
// report reaches that agent and the summary names it. The handoff clears the
// assignee, so at ready_for_qa an empty assignee passes the caller check; a
// caller holding the bead takes the same path.
func TestDeliver_ToReportsOnAStepTheTableKeepsSilent(t *testing.T) {
	skipWindows(t)
	for _, assignee := range []string{"", "pm"} {
		t.Run("assignee "+assignee, func(t *testing.T) {
			sock, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
			updates, _, stderr, err := y8vRun(t, sock, "pm", assignee, "ready_for_qa", "--to", "qa1")
			if err != nil {
				t.Fatalf("deliver: %v", err)
			}
			if updates != 1 {
				t.Errorf("bd writes = %d, want the one move", updates)
			}
			sends := y8vSends(received)
			if len(sends) != 1 {
				t.Fatalf("qa1 received %d reports, want 1; summary: %s", len(sends), y8vSummary(t, stderr))
			}
			if sends[0].Target != "qa1" || !sends[0].Enter || !strings.HasPrefix(sends[0].Text, "[from pm] ini-y8v: ") {
				t.Errorf("report = %+v, want the standard text to qa1 with Enter", sends[0])
			}
			sum := y8vSummary(t, stderr)
			if !strings.Contains(sum, "-> in_qa") || !strings.Contains(sum, "-> qa1;") || strings.Contains(sum, "no report") {
				t.Errorf("summary %q, want the move and qa1 named, no no-report text", sum)
			}
		})
	}
}

// A hand-off that already reports still sends one report, not two.
func TestDeliver_ToOnAHandoffReportsOnce(t *testing.T) {
	skipWindows(t)
	sock, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	if _, _, _, err := y8vRun(t, sock, "eng7", "eng7", "in_progress", "--to", "qa1"); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if sends := y8vSends(received); len(sends) != 1 || sends[0].Target != "qa1" {
		t.Errorf("reports = %+v, want one to qa1", sends)
	}
}

// AC2: --to with a recipient the TUI refuses, or a transport error: the bead
// has moved, the exit is an error, and the caller gets the recipient and the
// manual send line.
func TestDeliver_ToAFailedReportIsLoudOnAStepTheTableKeepsSilent(t *testing.T) {
	skipWindows(t)
	check := func(t *testing.T, sock string) {
		t.Helper()
		updates, _, stderr, err := y8vRun(t, sock, "pm", "", "ready_for_qa", "--to", "qa1")
		if updates != 1 {
			t.Errorf("bd writes = %d, want the bead moved", updates)
		}
		if err == nil {
			t.Fatal("exit 0 for a hand-off nobody heard")
		}
		for _, want := range []string{"hand-off to qa1 was not delivered", "initech send qa1 "} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q, want %q", err, want)
			}
		}
		if sum := y8vSummary(t, stderr); !strings.Contains(sum, "report NOT sent to qa1") {
			t.Errorf("summary %q, want the recipient named", sum)
		}
	}
	for _, refusal := range []string{"no such pane: qa1", "agent qa1 is stopped", "agent qa1 is still starting"} {
		t.Run(refusal, func(t *testing.T) {
			sock, _ := startCapturingFakeIPC(t, tui.IPCResponse{Error: refusal})
			check(t, sock)
		})
	}
	t.Run("transport error", func(t *testing.T) {
		check(t, filepath.Join(t.TempDir(), "no-tui.sock"))
	})
}

// AC3: without --to the same step stays silent and says how to report; the
// positive twin of AC1. --report alone still forces one, to the default.
func TestDeliver_WithoutToTheSameStepStaysSilent(t *testing.T) {
	skipWindows(t)
	sock, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	_, _, stderr, err := y8vRun(t, sock, "pm", "", "ready_for_qa")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if sends := y8vSends(received); len(sends) != 0 {
		t.Errorf("reports = %+v, want none", sends)
	}
	if sum := y8vSummary(t, stderr); !strings.Contains(sum, "(no report: same agent continues; --to or --report sends one)") {
		t.Errorf("summary %q, want the no-report text naming --to and --report", sum)
	}
}

func TestDeliver_ReportAloneStillForcesOneToTheDefault(t *testing.T) {
	skipWindows(t)
	sock, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	if _, _, _, err := y8vRun(t, sock, "pm", "", "ready_for_qa", "--report"); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if sends := y8vSends(received); len(sends) != 1 || sends[0].Target != "super" {
		t.Errorf("reports = %+v, want one to super", sends)
	}
}

// AC4: a --to that names nobody is refused before bd is touched.
func TestDeliver_ABlankToIsRefusedBeforeAnyBd(t *testing.T) {
	skipWindows(t)
	for _, to := range []string{"", "   ", "workbench:", "workbench:  "} {
		t.Run("to "+to, func(t *testing.T) {
			sock, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
			updates, shows, _, err := y8vRun(t, sock, "pm", "", "ready_for_qa", "--to", to)
			if err == nil || !strings.Contains(err.Error(), "--to") {
				t.Errorf("err = %v, want a refusal naming --to", err)
			}
			if updates != 0 || shows != 0 {
				t.Errorf("bd reads %d, writes %d, want none", shows, updates)
			}
			if sends := y8vSends(received); len(sends) != 0 {
				t.Errorf("reports = %+v, want none", sends)
			}
		})
	}
}

// AC4: --to yourself is a message to yourself, not an error.
func TestDeliver_ToSelfSendsToSelf(t *testing.T) {
	skipWindows(t)
	sock, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	if _, _, _, err := y8vRun(t, sock, "pm", "pm", "ready_for_qa", "--to", "pm"); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if sends := y8vSends(received); len(sends) != 1 || sends[0].Target != "pm" {
		t.Errorf("reports = %+v, want one to pm", sends)
	}
}

// A host:agent --to reports too, to that host.
func TestDeliver_ToAHostAgentReportsToThatHost(t *testing.T) {
	skipWindows(t)
	sock, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	if _, _, _, err := y8vRun(t, sock, "pm", "", "ready_for_qa", "--to", "workbench:qa1"); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if sends := y8vSends(received); len(sends) != 1 || sends[0].Target != "qa1" || sends[0].Host != "workbench" {
		t.Errorf("reports = %+v, want one to qa1 on workbench", sends)
	}
}
