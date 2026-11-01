package tui

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// ini-r0q.9: initech keystroke writes exactly one key into a pane: enter,
// esc, ctrl-c, y or n. These tests read the bytes that reach the pane's PTY.

var r0q9Bytes = map[string]string{"enter": "\r", "esc": "\x1b", "ctrl-c": "\x03", "y": "y", "n": "n"}

// r0q9Typed waits briefly for the reader to catch up and returns what the
// pane's PTY received.
func r0q9Typed(typed func() string, want string) string {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(typed()) < len(want) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond) // anything after the key would land now
	return typed()
}

// AC1: each key writes its own bytes and nothing else: no paste brackets, no
// stash, no Enter after it.
func TestKeystroke_EachKeyWritesItsBytesAndNothingElse(t *testing.T) {
	for key, want := range r0q9Bytes {
		t.Run(key, func(t *testing.T) {
			p, _, typed := dndPane(t, "a")
			v := keystrokePane(p, key, sendOrigin{From: operatorSender})
			if v.Outcome != DeliveryDelivered {
				t.Fatalf("verdict %q (%s), want delivered", v.Outcome, v.Reason)
			}
			if got := r0q9Typed(typed, want); got != want {
				t.Errorf("the PTY got %q, want exactly %q", got, want)
			}
		})
	}
}

// The table is exactly the five keys, no more.
func TestKeystroke_TheKeysAreExactlyTheFive(t *testing.T) {
	if len(keystrokeKeys) != len(r0q9Bytes) || len(KeystrokeKeyNames) != len(r0q9Bytes) {
		t.Fatalf("table %v names %v, want exactly %v", keystrokeKeys, KeystrokeKeyNames, r0q9Bytes)
	}
	for _, k := range KeystrokeKeyNames {
		if keystrokeKeys[k] != r0q9Bytes[k] {
			t.Errorf("%s is %q, want %q", k, keystrokeKeys[k], r0q9Bytes[k])
		}
	}
}

// AC4, AC5: any other key name, and an empty one, is refused with nothing
// typed.
func TestKeystroke_AnyOtherKeyIsRefused(t *testing.T) {
	for _, key := range []string{"", "Enter", "ENTER", "space", "ctrl-d", "yes", "y\r", "\r", "escape", "ctrl+c"} {
		p, _, typed := dndPane(t, "a")
		v := keystrokePane(p, key, sendOrigin{From: operatorSender})
		if v.Outcome != DeliveryNotDelivered || v.Reason != KeystrokeUnknownReason(key) {
			t.Errorf("key %q: %q (%s), want not delivered with the unknown-key line", key, v.Outcome, v.Reason)
		}
		if got := r0q9Typed(typed, ""); got != "" {
			t.Errorf("key %q typed %q", key, got)
		}
	}
}

// AC5: a suspended pane is not woken by a key: not delivered, nothing typed,
// nothing queued, and it stays parked.
func TestKeystroke_ASuspendedPaneStaysParked(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	p.SetSuspended(true)
	v := keystrokePane(p, "enter", sendOrigin{From: operatorSender})
	if v.Outcome != DeliveryNotDelivered || !strings.Contains(v.Reason, "suspended") {
		t.Errorf("verdict %q (%s), want not delivered: suspended", v.Outcome, v.Reason)
	}
	if got := r0q9Typed(typed, ""); got != "" {
		t.Errorf("typed %q into a suspended pane", got)
	}
	if !p.IsSuspended() || len(dndQueue(p)) != 0 {
		t.Errorf("suspended %v, queue %v: the key woke it or was queued", p.IsSuspended(), dndQueue(p))
	}
}

// A pane whose process is not running: not delivered.
func TestKeystroke_APaneNotRunningIsNotDelivered(t *testing.T) {
	p := testPane("a")
	p.ptmx = nil
	if v := keystrokePane(p, "esc", sendOrigin{From: operatorSender}); v.Outcome != DeliveryNotDelivered || v.Reason != "a is not running" {
		t.Errorf("verdict %q (%s)", v.Outcome, v.Reason)
	}
}

// AC3 (the safe side): an agent's key to a pane in do-not-disturb is not
// delivered, nothing typed and nothing queued.
func TestKeystroke_DoNotDisturbNeverQueuesAKey(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	p.startDND()
	v := keystrokePane(p, "y", sendOrigin{From: "eng1"})
	if v.Outcome != DeliveryNotDelivered || !strings.HasPrefix(v.Reason, "a is in do-not-disturb (") || !strings.HasSuffix(v.Reason, "); a key is never queued") {
		t.Errorf("verdict %q (%s)", v.Outcome, v.Reason)
	}
	if got := r0q9Typed(typed, ""); got != "" {
		t.Errorf("typed %q into a do-not-disturb pane", got)
	}
	if q := dndQueue(p); len(q) != 0 {
		t.Errorf("the key was queued: %v", q)
	}
}

// The IPC action, from the CLI through the shared dispatch: the TUI's pane
// takes the key; an unknown agent and an unknown key are not delivered.
func TestKeystroke_TheIPCAction(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	call := func(req IPCRequest) IPCResponse {
		return dndCall(t, func(c net.Conn) { dispatchIPC(tu, c, req, nil) })
	}
	if r := call(IPCRequest{Action: "keystroke", Target: "a", Text: "ctrl-c", From: operatorSender}); !r.OK || r.Delivery != DeliveryDelivered {
		t.Fatalf("reply %+v, want delivered", r)
	}
	if got := r0q9Typed(typed, "\x03"); got != "\x03" {
		t.Errorf("the PTY got %q, want ctrl-c alone", got)
	}
	for _, c := range []struct {
		req  IPCRequest
		want string
	}{
		{IPCRequest{Action: "keystroke", Target: "zz", Text: "y"}, `pane "zz" not found`},
		{IPCRequest{Action: "keystroke", Target: "a", Text: "tab"}, KeystrokeUnknownReason("tab")},
		{IPCRequest{Action: "keystroke", Text: "y"}, "target is required"},
	} {
		if r := call(c.req); r.OK || r.Error != c.want || r.Delivery != DeliveryNotDelivered {
			t.Errorf("%+v: reply %+v, want not delivered %q", c.req, r, c.want)
		}
	}
	if got := typed(); got != "\x03" {
		t.Errorf("the refusals typed something: the PTY has %q", got)
	}
}

// The daemon takes a key from another machine with the same verdicts.
func TestKeystroke_TheDaemonTakesAKeyFromAPeer(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	d := newTestDaemon(t)
	d.panes = []*Pane{p}
	if r := d.keystrokeControl(ControlCmd{Action: "keystroke", Target: "a", Text: "n", From: operatorSender}); !r.OK || r.Delivery != DeliveryDelivered {
		t.Fatalf("reply %+v", r)
	}
	if got := r0q9Typed(typed, "n"); got != "n" {
		t.Errorf("the PTY got %q, want n", got)
	}
	if r := d.keystrokeControl(ControlCmd{Action: "keystroke", Target: "zz", Text: "n"}); r.OK || r.Error != `pane "zz" not found` {
		t.Errorf("unknown agent: %+v", r)
	}
	p.SetSuspended(true)
	if r := d.keystrokeControl(ControlCmd{Action: "keystroke", Target: "a", Text: "n"}); r.OK || !strings.Contains(r.Error, "suspended") {
		t.Errorf("suspended: %+v", r)
	}
}

// A key from another machine arrives on the daemon's control stream as the
// keystroke action and is answered with its verdict.
func TestKeystroke_TheDaemonControlStreamTakesTheAction(t *testing.T) {
	p, _, typed := dndPane(t, "eng1")
	d := &Daemon{
		serve:        true,
		reportQ:      make(chan AgentEvent, reportQueueDepth),
		panes:        []*Pane{p},
		clients:      map[string]net.Conn{},
		clientCtrlMu: map[string]*sync.Mutex{},
	}
	client, server := ei8lConnect(t, d, "hub")
	go d.handleControlStream(server, bufio.NewScanner(server), "hub")
	lines := ei8lRead(client)
	ask := func(id, key string) ControlResp {
		if err := writeJSON(client, ControlCmd{ID: id, Action: "keystroke", Target: "eng1", Text: key, From: operatorSender}); err != nil {
			t.Fatal(err)
		}
		deadline := time.After(5 * time.Second)
		for {
			select {
			case r := <-lines:
				if r.ID == id {
					return r
				}
			case <-deadline:
				t.Fatalf("no answer to %s", id)
			}
		}
	}
	if r := ask("k1", "esc"); !r.OK || r.Delivery != DeliveryDelivered {
		t.Fatalf("answer %+v", r)
	}
	if got := r0q9Typed(typed, "\x1b"); got != "\x1b" {
		t.Errorf("the PTY got %q, want esc alone", got)
	}
	if r := ask("k2", "f1"); r.OK || r.Error != KeystrokeUnknownReason("f1") {
		t.Errorf("unknown key answer %+v", r)
	}
}

func keystrokeH(t *TUI, c *fakeConn, r IPCRequest) { handleIPCKeystroke(t, c, r) }

// host:agent reaches the peer as a keystroke, never a send, with the key and
// the sender; the peer's refusal is the answer; an older peer says so.
func TestKeystroke_AHostAgentKeyIsForwardedAsAKeystroke(t *testing.T) {
	r := newCrossRig(t)
	r.reply = func(ControlCmd) *ControlResp { return &ControlResp{OK: true, Delivery: DeliveryDelivered} }
	resp := r.call(t, keystrokeH, IPCRequest{Action: "keystroke", Host: "workbench", Target: "eng1", Text: "y", From: "super", Urgent: true})
	if !resp.OK || resp.Delivery != DeliveryDelivered {
		t.Fatalf("reply %+v", resp)
	}
	got := r.received()
	if len(got) != 1 || got[0].Action != "keystroke" || got[0].Target != "eng1" || got[0].Text != "y" || got[0].From != "super" || !got[0].Urgent {
		t.Fatalf("the peer received %+v, want one keystroke y for eng1 from super, urgent", got)
	}
	r.reply = func(ControlCmd) *ControlResp {
		return &ControlResp{Error: "eng1 is suspended; a key does not wake it", Delivery: DeliveryNotDelivered}
	}
	if resp := r.call(t, keystrokeH, IPCRequest{Action: "keystroke", Host: "workbench", Target: "eng1", Text: "y"}); resp.OK || resp.Error != "eng1 is suspended; a key does not wake it" {
		t.Errorf("the peer's refusal: %+v", resp)
	}
	r.reply = func(ControlCmd) *ControlResp { return &ControlResp{Error: `unknown action "keystroke"`} }
	if resp := r.call(t, keystrokeH, IPCRequest{Action: "keystroke", Host: "workbench", Target: "eng1", Text: "y"}); resp.OK || resp.Error != "its initech is older; restart it for keystroke" {
		t.Errorf("an older peer: %+v", resp)
	}
	for _, c := range r.received() {
		if c.Action == "send" {
			t.Fatal("a key was forwarded as a send")
		}
	}
	// A key name outside the table never reaches the peer.
	before := len(r.received())
	if resp := r.call(t, keystrokeH, IPCRequest{Action: "keystroke", Host: "workbench", Target: "eng1", Text: "f1"}); resp.OK || resp.Error != KeystrokeUnknownReason("f1") {
		t.Errorf("unknown key to a peer: %+v", resp)
	}
	if n := len(r.received()); n != before {
		t.Errorf("an unknown key was forwarded to the peer")
	}
	// The local peer name is this machine: the local pane takes it.
	if resp := r.call(t, keystrokeH, IPCRequest{Action: "keystroke", Host: "local-peer", Target: "eng1", Text: "y"}); resp.OK || resp.Error != "eng1 is not running" {
		t.Errorf("host = local peer name: %+v, want the local pane's own answer", resp)
	}
}

// AC3: into an open dialog only the operator's key goes; an agent's key,
// super --urgent included, is not delivered, nothing typed, nothing queued.
// Both terms of the send guard count: a dialog the latch declares, and one
// only the screen shows (the rm safety prompt, captured on Haiku).
func TestKeystroke_ADialogTakesOnlyTheOperatorsKey(t *testing.T) {
	open := map[string]func(*Pane){
		"latched": func(p *Pane) { p.latchDialogOpen("Claude needs your permission") },
		"on screen": func(p *Pane) {
			rows := lvxwFixture(t, "haiku312_rm_guard_prompt_w80.txt")
			p.emu.Resize(80, len(rows)+2)
			lvxwPaint(p, rows)
			if !paneShowsModalOnScreen(p) || p.dialogLatched() {
				t.Fatal("precondition: the prompt is on screen and nothing is latched")
			}
		},
	}
	for how, openIt := range open {
		t.Run(how, func(t *testing.T) {
			for _, o := range []sendOrigin{{From: "eng1"}, {From: "super", Urgent: true}, {From: ""}} {
				p, _, typed := dndPane(t, "a")
				openIt(p)
				v := keystrokePane(p, "y", o)
				if v.Outcome != DeliveryNotDelivered || v.Reason != keystrokeDialogReason {
					t.Errorf("%+v: %q (%s), want not delivered: %s", o, v.Outcome, v.Reason, keystrokeDialogReason)
				}
				if got := r0q9Typed(typed, ""); got != "" || len(dndQueue(p)) != 0 {
					t.Errorf("%+v: typed %q, queued %v", o, got, dndQueue(p))
				}
			}
			p, _, typed := dndPane(t, "a")
			openIt(p)
			if v := keystrokePane(p, "y", sendOrigin{From: operatorSender}); v.Outcome != DeliveryDelivered {
				t.Fatalf("the operator's key: %q (%s), want delivered into the dialog", v.Outcome, v.Reason)
			}
			if got := r0q9Typed(typed, "y"); got != "y" {
				t.Errorf("the operator's key: the PTY got %q, want y", got)
			}
		})
	}
	// The twin: no dialog, an agent's key goes in.
	p, _, typed := dndPane(t, "a")
	if v := keystrokePane(p, "esc", sendOrigin{From: "eng1"}); v.Outcome != DeliveryDelivered || r0q9Typed(typed, "\x1b") != "\x1b" {
		t.Errorf("an agent's key with no dialog: %q (%s), typed %q", v.Outcome, v.Reason, typed())
	}
}
