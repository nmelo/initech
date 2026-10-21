package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-2s7q: a stopped or exited agent gets nothing claimed and the sender is
// told why, with the command that brings the agent back.

const s7qStoppedReply = ": agent eng1 is stopped; start it with initech start eng1"

// Criterion 2: assign to a stopped agent writes nothing to bd and prints the
// reason.
func TestRunAssign_AStoppedAgentGetsNothingClaimed(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)
	assignTitles(func(id string) (string, error) { return "Fix the bug", nil })
	dispatched := 0
	bdDispatchFn = func(id, agent, status string, recordImplementer bool) error {
		dispatched++
		return nil
	}
	reply := tui.ErrPaneNotRunning.Error() + s7qStoppedReply
	sock := i35wActionIPC(t, map[string]tui.IPCResponse{"send_ready": {Error: reply}, "send": {Error: reply}})
	t.Setenv("INITECH_SOCKET", sock)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"assign", "eng1", "ini-2s7q"})
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing assigned") || !strings.Contains(err.Error(), "initech start eng1") {
		t.Fatalf("err = %v, want a nothing-assigned refusal with the start command", err)
	}
	if dispatched != 0 {
		t.Fatalf("bd was written %d time(s) for a stopped agent", dispatched)
	}
}

// Criterion 2: send to a stopped agent exits non-zero with the reason.
func TestRunSend_AStoppedAgentIsAnErrorWithTheReason(t *testing.T) {
	skipWindows(t)
	reply := tui.ErrPaneNotRunning.Error() + s7qStoppedReply
	sock := i35wActionIPC(t, map[string]tui.IPCResponse{"send": {Error: reply}})
	t.Setenv("INITECH_SOCKET", sock)
	out := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"send", "eng1", "hello"})
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "initech start eng1") {
		t.Fatalf("err = %v, want the stopped refusal", err)
	}
	if strings.Contains(out.String(), "delivered") {
		t.Fatalf("send printed %q for a stopped agent", out.String())
	}
}
