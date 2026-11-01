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

// AC3: the service's send answer is word for word what initech send prints
// for the same reply, every verdict of the family, and the text goes on the
// wire verbatim with one Enter, as the operator, even inside a pane.
func TestAppSend_TheWordsAreTheCommands(t *testing.T) {
	for _, c := range []struct {
		resp    tui.IPCResponse
		verdict string
	}{
		{tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered}, tui.DeliveryDelivered},
		{tui.IPCResponse{OK: true, Delivery: tui.DeliveryDeferred}, tui.DeliveryDeferred},
		{tui.IPCResponse{OK: true, Delivery: tui.DeliveryHeld, Data: "held: a is in do-not-disturb (2m, 0 messages ahead)"}, tui.DeliveryHeld},
		{tui.IPCResponse{OK: true, Delivery: tui.DeliveryUnchecked, Data: "codex panes have no composer check"}, tui.DeliveryUnchecked},
		{tui.IPCResponse{OK: true, Delivery: tui.DeliveryUnconfirmed, Data: "the box was never seen"}, tui.DeliveryUnconfirmed},
		{tui.IPCResponse{Error: "not delivered to a: the text is still in the box", Delivery: tui.DeliveryNotDelivered}, tui.DeliveryNotDelivered},
		{tui.IPCResponse{OK: true, Data: "deferred to a — modal open"}, tui.DeliveryDeferred}, // an older TUI's deferral
	} {
		f := &r0q9TUI{resp: c.resp}
		startR0q9TUI(t, f)
		t.Setenv("INITECH_AGENT", "")
		var cli bytes.Buffer
		cliWords := ""
		if _, err := deliverTextVerdict(&cli, "a", "", "hello \"there\"\nline two", true, false); err != nil {
			cliWords = err.Error()
		} else {
			cliWords = string(bytes.TrimRight(cli.Bytes(), "\n"))
		}
		t.Setenv("INITECH_AGENT", "eng1")
		got := (&appBackend{}).Send(appserve.Rig{Name: "r", Socket: os.Getenv("INITECH_SOCKET")}, "a", "hello \"there\"\nline two", true)
		if got.Verdict != c.verdict || got.Words != cliWords {
			t.Errorf("%+v: service %+v, want verdict %q and the CLI's words %q", c.resp, got, c.verdict, cliWords)
		}
		reqs := f.got()
		if len(reqs) != 2 || reqs[1].Action != "send" || reqs[1].Target != "a" || reqs[1].Text != "hello \"there\"\nline two" || !reqs[1].Enter || reqs[1].From != tui.OperatorSender || reqs[1].Urgent {
			t.Errorf("%+v: the service's request %+v", c.resp, reqs)
		}
	}
	// enter false is initech send --no-enter.
	f := &r0q9TUI{resp: tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered}}
	startR0q9TUI(t, f)
	(&appBackend{}).Send(appserve.Rig{Name: "r", Socket: os.Getenv("INITECH_SOCKET")}, "a", "draft", false)
	if r := f.got(); len(r) != 1 || r[0].Enter {
		t.Errorf("enter false: %+v", r)
	}
}

// The service's backend can send.
func TestAppSend_TheServiceBackendCanSend(t *testing.T) {
	var b appserve.Backend = &appBackend{}
	if _, ok := b.(appserve.MessageTypist); !ok {
		t.Error("appBackend does not implement appserve.MessageTypist")
	}
}

// A rig whose TUI does not answer: not_delivered, with the error the
// command prints for the same dead socket.
func TestAppSend_ADeadSocketIsNotDelivered(t *testing.T) {
	dead := t.TempDir() + "/gone.sock"
	t.Setenv("INITECH_SOCKET", dead)
	_, cliErr := deliverTextVerdict(&bytes.Buffer{}, "a", "", "x", true, false)
	got := (&appBackend{}).Send(appserve.Rig{Name: "r", Socket: dead}, "a", "x", true)
	if cliErr == nil || got.Verdict != tui.DeliveryNotDelivered || got.Words != cliErr.Error() {
		t.Errorf("service %+v, want not_delivered with the CLI's %v", got, cliErr)
	}
	k := (&appBackend{}).Key(appserve.Rig{Name: "r", Socket: dead}, "a", "y")
	if k.Verdict != tui.DeliveryNotDelivered || k.Words == "" {
		t.Errorf("key to a dead socket: %+v", k)
	}
}
