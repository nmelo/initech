package cmd

import (
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-x432.4: every send the CLI makes says who it is from (the agent named
// by INITECH_AGENT, or the operator outside a pane), send --urgent says so,
// and the TUI's refusal of a non-super --urgent prints as not delivered,
// exit 1.

func x432dSend(t *testing.T, agent string, resp tui.IPCResponse, args ...string) (tui.IPCRequest, error) {
	t.Helper()
	sendUrgent, sendNoEnter = false, false
	t.Cleanup(func() { sendUrgent = false })
	_, _, err, reqs := x432cRunAs(t, agent, resp, args...)
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	return reqs[0], err
}

func TestDNDBreak_EverySendSaysWhoItIsFrom(t *testing.T) {
	req, _ := x432dSend(t, "eng2", tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered}, "send", "qa1", "hi")
	if req.From != "eng2" || req.Urgent {
		t.Errorf("inside a pane: from %q urgent %v, want eng2, not urgent", req.From, req.Urgent)
	}
	req, _ = x432dSend(t, "", tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered}, "send", "qa1", "hi")
	if req.From != "@operator" {
		t.Errorf("outside a pane: from %q, want @operator", req.From)
	}
	req, _ = x432dSend(t, "super", tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered}, "send", "--urgent", "eng2", "P0")
	if req.From != "super" || !req.Urgent {
		t.Errorf("--urgent: from %q urgent %v", req.From, req.Urgent)
	}
}

// Sends made by other commands carry the sender too: the stamp is in the one
// place every IPC call goes through.
func TestDNDBreak_TheStampIsWhereEveryCallGoes(t *testing.T) {
	t.Setenv("INITECH_AGENT", "super")
	req := tui.IPCRequest{Action: "send", Target: "eng1", Text: "x"}
	if got := stampSender(req); got.From != "super" {
		t.Errorf("stamped %q, want super", got.From)
	}
	if got := stampSender(tui.IPCRequest{Action: "send", From: "qa1"}); got.From != "qa1" {
		t.Errorf("an explicit sender was overwritten: %q", got.From)
	}
	if got := stampSender(tui.IPCRequest{Action: "peek"}); got.From != "" {
		t.Errorf("a peek was stamped: %q", got.From)
	}
}

// AC3 at the sender: the refusal prints as the TUI words it, exit 1.
func TestDNDBreak_TheRefusalIsExitOne(t *testing.T) {
	_, err := x432dSend(t, "eng1", tui.IPCResponse{Error: "not delivered to a: only super may break do-not-disturb", Delivery: tui.DeliveryNotDelivered, Data: "only super may break do-not-disturb"}, "send", "--urgent", "a", "let me in")
	if v69Exit(err) != 1 || err == nil || err.Error() != "not delivered to a: only super may break do-not-disturb" {
		t.Errorf("err %v exit %d", err, v69Exit(err))
	}
}
