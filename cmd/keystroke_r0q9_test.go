package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-r0q.9: initech keystroke [host:]<agent> <key>.

// r0q9TUI is a fake TUI that answers every request with resp and records it.
type r0q9TUI struct {
	resp tui.IPCResponse
	mu   sync.Mutex
	reqs []tui.IPCRequest
}

func (f *r0q9TUI) got() []tui.IPCRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tui.IPCRequest(nil), f.reqs...)
}

func startR0q9TUI(t *testing.T, f *r0q9TUI) {
	t.Helper()
	skipWindows(t)
	sock := filepath.Join("/tmp", fmt.Sprintf("initech-r0q9-%d-%d.sock", os.Getpid(), fakeIPCCounter.Add(1)))
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sock) })
	t.Setenv("INITECH_SOCKET", sock)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			var req tui.IPCRequest
			_ = json.NewDecoder(conn).Decode(&req)
			f.mu.Lock()
			f.reqs = append(f.reqs, req)
			f.mu.Unlock()
			data, _ := json.Marshal(f.resp)
			conn.Write(append(data, '\n'))
			conn.Close()
		}
	}()
}

var r0q9Delivered = tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered}

// AC1: the key goes on the wire as the keystroke action, named by the sender
// as a send is; delivered prints its line, exit 0.
func TestKeystroke_DeliveredPrintsItsLine(t *testing.T) {
	for _, key := range []string{"enter", "esc", "ctrl-c", "y", "n"} {
		f := &r0q9TUI{resp: r0q9Delivered}
		startR0q9TUI(t, f)
		t.Setenv("INITECH_AGENT", "")
		var out bytes.Buffer
		if err := keystrokeAgent(&out, "a", "", key, false); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if out.String() != "key "+key+" delivered to a\n" {
			t.Errorf("%s: printed %q", key, out.String())
		}
		r := f.got()
		if len(r) != 1 || r[0].Action != "keystroke" || r[0].Target != "a" || r[0].Text != key || r[0].From != tui.OperatorSender || r[0].Urgent {
			t.Errorf("%s: requests %+v", key, r)
		}
	}
	// From inside a pane the sender is that agent; --urgent rides along.
	f := &r0q9TUI{resp: r0q9Delivered}
	startR0q9TUI(t, f)
	t.Setenv("INITECH_AGENT", "super")
	if err := keystrokeAgent(&bytes.Buffer{}, "a", "peer", "esc", true); err != nil {
		t.Fatal(err)
	}
	if r := f.got(); len(r) != 1 || r[0].From != "super" || !r[0].Urgent || r[0].Host != "peer" {
		t.Errorf("requests %+v", r)
	}
}

// AC4, AC5: an unknown or empty key is refused before anything reaches the
// TUI, exit 1.
func TestKeystroke_AnUnknownKeyNeverLeavesTheCLI(t *testing.T) {
	for _, key := range []string{"", "Enter", "tab", "ctrl-d"} {
		f := &r0q9TUI{resp: r0q9Delivered}
		startR0q9TUI(t, f)
		err := keystrokeAgent(&bytes.Buffer{}, "a", "", key, false)
		if v69Exit(err) != 1 || err == nil || err.Error() != "not delivered to a: "+tui.KeystrokeUnknownReason(key) {
			t.Errorf("key %q: exit %d, %v", key, v69Exit(err), err)
		}
		if r := f.got(); len(r) != 0 {
			t.Errorf("key %q reached the TUI: %+v", key, r)
		}
	}
}

// AC4: the TUI's refusal is the not-delivered line, exit 1; an older TUI
// says so.
func TestKeystroke_NotDeliveredIsExitOne(t *testing.T) {
	for reason, want := range map[string]string{
		`pane "zz" not found`:                    `not delivered to a: pane "zz" not found`,
		"a is suspended; a key does not wake it": "not delivered to a: a is suspended; a key does not wake it",
		`unknown action "keystroke"`:             "not delivered to a: older initech running; restart it for keystroke",
	} {
		f := &r0q9TUI{resp: tui.IPCResponse{Error: reason, Delivery: tui.DeliveryNotDelivered}}
		startR0q9TUI(t, f)
		var out bytes.Buffer
		err := keystrokeAgent(&out, "a", "", "y", false)
		if v69Exit(err) != 1 || err == nil || err.Error() != want || out.Len() != 0 {
			t.Errorf("%q: exit %d, %v, printed %q", reason, v69Exit(err), err, out.String())
		}
	}
	// An OK with no delivered verdict is not called delivered.
	f := &r0q9TUI{resp: tui.IPCResponse{OK: true}}
	startR0q9TUI(t, f)
	if err := keystrokeAgent(&bytes.Buffer{}, "a", "", "y", false); v69Exit(err) != 1 {
		t.Errorf("an OK with no verdict: exit %d, %v", v69Exit(err), err)
	}
}

// --urgent rides from the flag to the wire.
func TestKeystroke_TheUrgentFlagReachesTheWire(t *testing.T) {
	f := &r0q9TUI{resp: r0q9Delivered}
	startR0q9TUI(t, f)
	t.Setenv("INITECH_AGENT", "super")
	if err := keystrokeCmd.Flags().Set("urgent", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { keystrokeCmd.Flags().Set("urgent", "false") })
	if err := runKeystroke(keystrokeCmd, []string{"a", "esc"}); err != nil {
		t.Fatal(err)
	}
	if r := f.got(); len(r) != 1 || !r[0].Urgent || r[0].From != "super" {
		t.Errorf("requests %+v, want urgent from super", r)
	}
}

// The command takes exactly the agent and the key.
func TestKeystroke_TakesTheAgentAndOneKey(t *testing.T) {
	if keystrokeCmd.Args(keystrokeCmd, []string{"a"}) == nil || keystrokeCmd.Args(keystrokeCmd, []string{"a", "y", "n"}) == nil {
		t.Error("keystroke accepts other than two arguments")
	}
	if keystrokeCmd.Args(keystrokeCmd, []string{"a", "y"}) != nil {
		t.Error("keystroke refuses <agent> <key>")
	}
}
