package cmd

import (
	"bytes"
	"os"
	"testing"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/nmelo/initech/internal/tui"
)

// ini-r0q.11: the app service presses a key through initech keystroke's own
// code, against the rig's socket, as the operator; its verdict and words are
// the command's.

// The service's answer is word for word what initech keystroke prints for
// the same call, delivered or not, and the key goes as the operator even
// when app-serve runs inside a pane.
func TestAppKeys_TheWordsAreTheCommands(t *testing.T) {
	for _, c := range []struct {
		resp    tui.IPCResponse
		verdict string
	}{
		{r0q9Delivered, tui.DeliveryDelivered},
		{tui.IPCResponse{Error: "a is in do-not-disturb (2m); a key is never queued", Delivery: tui.DeliveryNotDelivered}, tui.DeliveryNotDelivered},
		{tui.IPCResponse{Error: `pane "a" not found`, Delivery: tui.DeliveryNotDelivered}, tui.DeliveryNotDelivered},
		{tui.IPCResponse{Error: `unknown action "keystroke"`}, tui.DeliveryNotDelivered},
	} {
		// The CLI's own output for the call, run as the operator.
		f := &r0q9TUI{resp: c.resp}
		startR0q9TUI(t, f)
		t.Setenv("INITECH_AGENT", "")
		var cli bytes.Buffer
		cliWords := ""
		if err := keystrokeAgent(&cli, "a", "", "y", false); err != nil {
			cliWords = err.Error()
		} else {
			cliWords = string(bytes.TrimRight(cli.Bytes(), "\n"))
		}
		// The service, from inside a pane, against the same TUI.
		t.Setenv("INITECH_AGENT", "eng1")
		b := &appBackend{}
		got := b.Key(appserve.Rig{Name: "r", Socket: os.Getenv("INITECH_SOCKET")}, "a", "y")
		if got.Verdict != c.verdict || got.Words != cliWords {
			t.Errorf("%+v: service %+v, want verdict %q and the CLI's words %q", c.resp, got, c.verdict, cliWords)
		}
		reqs := f.got()
		if len(reqs) != 2 || reqs[1].Action != "keystroke" || reqs[1].Target != "a" || reqs[1].Text != "y" || reqs[1].From != tui.OperatorSender || reqs[1].Urgent {
			t.Errorf("%+v: the service's request %+v, want one keystroke y for a from the operator, not urgent", c.resp, reqs)
		}
	}
}

// A key outside the five: the command's own refusal line, and nothing
// reaches the TUI.
func TestAppKeys_ARefusedKeyIsTheCommandsLineAndNoCall(t *testing.T) {
	f := &r0q9TUI{resp: r0q9Delivered}
	startR0q9TUI(t, f)
	b := &appBackend{}
	for _, key := range []string{"tab", "", "Enter"} {
		cliErr := keystrokeAgent(&bytes.Buffer{}, "a", "", key, false)
		words, ok := b.KeyRefusal("a", key)
		if ok || cliErr == nil || words != cliErr.Error() {
			t.Errorf("key %q: refusal %q (ok %v), want the CLI's %v", key, words, ok, cliErr)
		}
	}
	for _, key := range []string{"enter", "esc", "ctrl-c", "y", "n"} {
		if words, ok := b.KeyRefusal("a", key); !ok || words != "" {
			t.Errorf("key %q refused: %q", key, words)
		}
	}
	if r := f.got(); len(r) != 0 {
		t.Errorf("the checks reached the TUI: %+v", r)
	}
}

// The backend the service runs with can press keys.
func TestAppKeys_TheServiceBackendCanType(t *testing.T) {
	var b appserve.Backend = &appBackend{}
	if _, ok := b.(appserve.KeyTypist); !ok {
		t.Error("appBackend does not implement appserve.KeyTypist")
	}
}
