package cmd

import (
	"bytes"
	"os"
	"testing"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/nmelo/initech/internal/tui"
)

// ini-r0q.14: the TUI's refusal at the folder trust prompt reaches the
// command and POST /v1/keys word for word; neither grows a check of its own.
func TestKeystroke_TheTrustRefusalReachesTheCommandAndTheService(t *testing.T) {
	const reason = "the folder trust prompt is up and Enter would choose No, exit; open the pane to answer it"
	const line = "not delivered to a: " + reason
	f := &r0q9TUI{resp: tui.IPCResponse{Error: reason, Delivery: tui.DeliveryNotDelivered}}
	startR0q9TUI(t, f)
	t.Setenv("INITECH_AGENT", "")
	var out bytes.Buffer
	if err := keystrokeAgent(&out, "a", "", "enter", false); err == nil || err.Error() != line {
		t.Errorf("the command: %v, want %q", err, line)
	}
	if out.Len() != 0 {
		t.Errorf("the command printed %q as delivered", out.String())
	}
	got := (&appBackend{}).Key(appserve.Rig{Name: "r", Socket: os.Getenv("INITECH_SOCKET")}, "a", "enter")
	if got.Verdict != tui.DeliveryNotDelivered || got.Words != line {
		t.Errorf("the service: %+v, want not_delivered with %q", got, line)
	}
}
