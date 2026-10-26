package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-69fy: initech send prints the TUI's verdict, each line exactly, with
// its exit status: delivered 0, not delivered 1, could not confirm 2, an
// agent type with no delivery check 0, deferred 0.

func v69RunSend(t *testing.T, resp tui.IPCResponse, target string) (stderr string, err error) {
	t.Helper()
	skipWindows(t)
	t.Setenv("INITECH_SOCKET", startFakeIPC(t, resp))
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetArgs([]string{"send", target, "hello"})
	defer rootCmd.SetArgs(nil)
	err = rootCmd.Execute()
	return errOut.String(), err
}

// v69Exit is the status Execute would exit with for err.
func v69Exit(err error) int {
	if err == nil {
		return 0
	}
	var ec exitCoder
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return 1
}

func TestSendVerdict_EachVerdictPrintsItsLineAndExitStatus(t *testing.T) {
	cases := []struct {
		name   string
		target string
		resp   tui.IPCResponse
		line   string // the one line printed, by deliverText or by Execute
		exit   int
	}{
		{"delivered", "eng1", tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered},
			"delivered to eng1", 0},
		{"not delivered", "eng1", tui.IPCResponse{Error: "not delivered to eng1: Enter not taken twice; the text is still in the composer", Delivery: tui.DeliveryNotDelivered, Data: "Enter not taken twice; the text is still in the composer"},
			"not delivered to eng1: Enter not taken twice; the text is still in the composer", 1},
		{"could not confirm", "eng1", tui.IPCResponse{OK: true, Delivery: tui.DeliveryUnconfirmed, Data: "no composer visible"},
			"could not confirm delivery to eng1: no composer visible; peek the pane", 2},
		{"no delivery check", "eng3", tui.IPCResponse{OK: true, Delivery: tui.DeliveryUnchecked, Data: "no delivery check for this agent type"},
			"sent to eng3, unconfirmed: no delivery check for this agent type", 0},
		{"deferred", "eng1", tui.IPCResponse{OK: true, Delivery: tui.DeliveryDeferred, Data: "deferred to eng1 — modal open, 1 queued"},
			"deferred to eng1 — target has a modal open; will deliver when it closes", 0},
		{"queued and resumed", "eng1", tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered, Data: `"resumed and delivered"`},
			"delivered to eng1", 0},
		{"remote could not confirm", "workbench:eng1", tui.IPCResponse{OK: true, Delivery: tui.DeliveryUnconfirmed, Data: "older initech on workbench"},
			"could not confirm delivery to workbench:eng1: older initech on workbench; peek the pane", 2},
		// G5: a TUI older than the CLI answers OK with no verdict.
		{"older TUI", "eng1", tui.IPCResponse{OK: true},
			"could not confirm delivery to eng1: older initech running; restart it for delivery verdicts; peek the pane", 2},
		// An older TUI's deferral is still a deferral.
		{"older TUI deferred", "eng1", tui.IPCResponse{OK: true, Data: "deferred to eng1 — modal open, 1 queued"},
			"deferred to eng1 — target has a modal open; will deliver when it closes", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stderr, err := v69RunSend(t, c.resp, c.target)
			if got := v69Exit(err); got != c.exit {
				t.Errorf("exit %d (err %v), want %d", got, err, c.exit)
			}
			var printed string
			if err != nil {
				// Execute prints the error once; cobra adds nothing.
				if stderr != "" {
					t.Errorf("cobra printed %q beside the error: want the line alone", stderr)
				}
				printed = err.Error()
			} else {
				printed = strings.TrimSuffix(stderr, "\n")
			}
			if printed != c.line {
				t.Errorf("printed %q, want %q", printed, c.line)
			}
		})
	}
}

// assign's no-bead dispatch prints the send line and exits as send does;
// a could-not-confirm still marks the ribbon, since the message may well
// have arrived.
func TestSendVerdict_AssignNoBeadCouldNotConfirmStillMarksAndExitsTwo(t *testing.T) {
	skipWindows(t)
	sock, actions := downFake(t, map[string]tui.IPCResponse{
		"send":    {OK: true, Delivery: tui.DeliveryUnconfirmed, Data: "no composer visible"},
		"no_bead": {OK: true},
	})
	t.Setenv("INITECH_SOCKET", sock)
	var w bytes.Buffer
	err := assignNoBead(&w, "eng1", "", "look at the flaky test")
	if v69Exit(err) != 2 || err.Error() != "could not confirm delivery to eng1: no composer visible; peek the pane" {
		t.Fatalf("err %v (exit %d), want the could-not-confirm line and exit 2", err, v69Exit(err))
	}
	if got := strings.Join(actions(), ","); !strings.HasPrefix(got, "send,no_bead") {
		t.Fatalf("actions %s, want send then the no_bead marker", got)
	}
}

// assign's no-bead dispatch on not delivered sets no marker: nothing landed.
func TestSendVerdict_AssignNoBeadNotDeliveredSetsNoMarker(t *testing.T) {
	skipWindows(t)
	sock, actions := downFake(t, map[string]tui.IPCResponse{
		"send":    {Error: "not delivered to eng1: r", Delivery: tui.DeliveryNotDelivered, Data: "r"},
		"no_bead": {OK: true},
	})
	t.Setenv("INITECH_SOCKET", sock)
	err := assignNoBead(&bytes.Buffer{}, "eng1", "", "look at the flaky test")
	if err == nil || err.Error() != "not delivered to eng1: r" || v69Exit(err) != 1 {
		t.Fatalf("err %v, want not delivered, exit 1", err)
	}
	if got := strings.Join(actions(), ","); got != "send" {
		t.Fatalf("actions %s, want the send alone", got)
	}
}
