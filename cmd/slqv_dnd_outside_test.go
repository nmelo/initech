package cmd

import (
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-slqv: outside a pane, a send, a send --after and a withdrawal are
// stamped "outside a pane", so do-not-disturb holds them; a keystroke keeps
// the operator's stamp (not a send, unchanged), and so does the companion,
// which stamps the operator itself. Assign and deliver reports go through
// the same stamp, in the one place every call goes (ipcCallSocket).
func TestDNDOutside_TheStampOutsideAPane(t *testing.T) {
	t.Setenv("INITECH_AGENT", "")
	for _, action := range []string{"send", "send_after", "withdraw"} {
		if got := stampSender(tui.IPCRequest{Action: action, Target: "super", Text: "x"}); got.From != tui.OutsideSender {
			t.Errorf("%s stamped %q, want %q", action, got.From, tui.OutsideSender)
		}
	}
	if got := stampSender(tui.IPCRequest{Action: "keystroke", Target: "super", Text: "y"}); got.From != tui.OperatorSender {
		t.Errorf("keystroke stamped %q, want the operator", got.From)
	}
	if got := stampSender(tui.IPCRequest{Action: "send", From: tui.OperatorSender}); got.From != tui.OperatorSender {
		t.Errorf("the companion's own stamp was overwritten: %q", got.From)
	}
	t.Setenv("INITECH_AGENT", "eng1")
	if got := stampSender(tui.IPCRequest{Action: "send"}); got.From != "eng1" {
		t.Errorf("inside a pane stamped %q", got.From)
	}
}
