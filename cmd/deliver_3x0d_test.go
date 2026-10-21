package cmd

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-3x0d: a hand-off report that did not reach the recipient is reported as
// not sent, and deliver exits non-zero with the command to notify by hand.
// The bd write stays: the step happened.

type x0dResult struct {
	err    error
	stderr string
	writes []beadWrite
}

// x0dDeliver runs `initech deliver ini-3x0d --to <to>` as eng2 against the
// socket in INITECH_SOCKET, with bd stubbed, from in_progress.
func x0dDeliver(t *testing.T, sock, to string, extra ...string) x0dResult {
	t.Helper()
	stubBdFns(t)
	resetDeliverFlags(t)
	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Fix the hand-off report", "eng2", "in_progress", nil
	}
	var r x0dResult
	bdUpdateBeadFn = func(id string, w beadWrite) error {
		r.writes = append(r.writes, w)
		return nil
	}
	t.Setenv("INITECH_SOCKET", sock)
	t.Setenv("INITECH_AGENT", "eng2")
	errBuf := &bytes.Buffer{}
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs(append([]string{"deliver", "ini-3x0d", "--to", to}, extra...))
	defer rootCmd.SetArgs(nil)
	r.err = rootCmd.Execute()
	r.stderr = errBuf.String()
	return r
}

func x0dSummary(t *testing.T, stderr string) string {
	t.Helper()
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, "delivered ini-3x0d:") {
			return l
		}
	}
	t.Fatalf("no summary line in:\n%s", stderr)
	return ""
}

// Criterion 1: the TUI refuses the report (pane not found). The bead moves,
// the summary says the report was not sent and why, and deliver fails with
// the manual send line.
func TestDeliver_ARefusedReportIsReportedAsNotSent(t *testing.T) {
	skipWindows(t)
	sock := i35wActionIPC(t, map[string]tui.IPCResponse{"send": {Error: `pane "qa9" not found`}})
	r := x0dDeliver(t, sock, "qa9")
	if len(r.writes) == 0 || r.writes[0].Status != "ready_for_qa" {
		t.Fatalf("bd writes %+v, want the move to ready_for_qa (the step happened)", r.writes)
	}
	sum := x0dSummary(t, r.stderr)
	if !strings.Contains(sum, `-> report NOT sent to qa9: pane "qa9" not found;`) {
		t.Errorf("summary %q, want it to say the report was not sent to qa9 and why", sum)
	}
	if r.err == nil {
		t.Fatalf("deliver exited 0 after the hand-off failed; summary %q", sum)
	}
	msg := r.err.Error()
	for _, want := range []string{"bead moved to ready_for_qa", "hand-off to qa9 was not delivered", `pane "qa9" not found`, "Run: initech send qa9 '[from eng2] ini-3x0d: "} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
}

// Criterion 2: no TUI listening. The same, with the transport error text.
func TestDeliver_AReportWithNoTUIIsReportedAsNotSent(t *testing.T) {
	skipWindows(t)
	sock := filepath.Join(t.TempDir(), "nobody.sock")
	r := x0dDeliver(t, sock, "qa1")
	sum := x0dSummary(t, r.stderr)
	if !strings.Contains(sum, "-> report NOT sent to qa1: ") {
		t.Errorf("summary %q, want report NOT sent to qa1 with the transport error", sum)
	}
	if r.err == nil || !strings.Contains(r.err.Error(), "hand-off to qa1 was not delivered") || !strings.Contains(r.err.Error(), "Run: initech send qa1 ") {
		t.Fatalf("err = %v, want the failed hand-off with the manual send line", r.err)
	}
}

// Criteria 3 and 5: a report that is delivered changes nothing: exit 0, the
// summary names the recipient, the ribbon outcome ends the line.
func TestDeliver_ADeliveredReportIsUnchanged(t *testing.T) {
	skipWindows(t)
	sock := i35wActionIPC(t, nil)
	r := x0dDeliver(t, sock, "qa1")
	if r.err != nil {
		t.Fatalf("deliver with a delivered report: %v", r.err)
	}
	sum := x0dSummary(t, r.stderr)
	if !regexp.MustCompile(`\) -> qa1; [^;]+$`).MatchString(sum) || strings.Contains(sum, "NOT sent") {
		t.Errorf("summary %q, want -> qa1; then the ribbon outcome, as before", sum)
	}
}

// Criterion 5: the ribbon outcome stays last on the line when the report
// failed, too.
func TestDeliver_TheRibbonOutcomeStaysLast(t *testing.T) {
	skipWindows(t)
	sock := i35wActionIPC(t, map[string]tui.IPCResponse{"send": {Error: "agent qa1 is stopped"}})
	sum := x0dSummary(t, x0dDeliver(t, sock, "qa1").stderr)
	if !regexp.MustCompile(`-> report NOT sent to qa1: agent qa1 is stopped; [^;]+$`).MatchString(sum) {
		t.Errorf("summary %q, want the report outcome then the ribbon outcome last", sum)
	}
}

// Criterion 4: a step where the same agent continues sends no report and is
// unchanged: no error, the no-report text on the line. The socket refuses
// every send, so a report attempted here would fail the step.
func TestDeliver_NoReportDueIsUnchanged(t *testing.T) {
	skipWindows(t)
	sock := i35wActionIPC(t, map[string]tui.IPCResponse{"send": {Error: "must not be sent"}})
	stubBdFns(t)
	resetDeliverFlags(t)
	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Walk it", "pm", "ready_for_qa", nil
	}
	t.Setenv("INITECH_SOCKET", sock)
	t.Setenv("INITECH_AGENT", "pm")
	errBuf := &bytes.Buffer{}
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"deliver", "ini-3x0d"})
	defer rootCmd.SetArgs(nil)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("a same-agent step: %v", err)
	}
	if sum := x0dSummary(t, errBuf.String()); !strings.Contains(sum, "(no report: same agent continues") {
		t.Errorf("summary %q, want the no-report text", sum)
	}
}

// The printed manual command is safe to paste: a report with a single quote
// reads back, through sh, as exactly the report text.
func TestDeliver_TheManualSendLineIsShellSafe(t *testing.T) {
	skipWindows(t)
	sock := i35wActionIPC(t, map[string]tui.IPCResponse{"send": {Error: "pane not found"}})
	r := x0dDeliver(t, sock, "qa1", "-m", "it's ready, don't wait")
	if r.err == nil {
		t.Fatal("deliver exited 0 after the hand-off failed; no manual line to check")
	}
	m := regexp.MustCompile(`Run: initech send qa1 (.*) to notify manually`).FindStringSubmatch(r.err.Error())
	if m == nil {
		t.Fatalf("no manual line in %v", r.err)
	}
	out, err := exec.Command("sh", "-c", "printf %s "+m[1]).Output()
	if err != nil {
		t.Fatalf("sh could not parse the manual line's text %s: %v", m[1], err)
	}
	if got := string(out); !strings.HasPrefix(got, "[from eng2] ini-3x0d: ") || !strings.HasSuffix(got, "it's ready, don't wait") {
		t.Errorf("the manual line reads back as %q, want the report with its quotes intact", got)
	}
}

// Feed it nothing: an empty recipient with --report is refused by the TUI,
// so deliver fails; a recipient equal to the deliverer is an ordinary send.
func TestDeliver_EmptyOrSelfRecipient(t *testing.T) {
	skipWindows(t)
	sock := i35wActionIPC(t, map[string]tui.IPCResponse{"send": {Error: "target is required"}})
	if r := x0dDeliver(t, sock, "", "--report"); r.err == nil {
		t.Errorf("--to '' --report: exit 0 for a report nobody received; stderr %q", r.stderr)
	}
	ok := i35wActionIPC(t, nil)
	if r := x0dDeliver(t, ok, "eng2"); r.err != nil {
		t.Errorf("--to eng2 by eng2: %v, want an ordinary delivered report", r.err)
	}
}
