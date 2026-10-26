package tui

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// ini-69fy: initech send's reply waits for the submit verdict and carries
// it. Delivered means our text was seen in the box and the box cleared;
// anything less comes back with its reason. These cells drive the real
// handler (handleIPCSend) against the pkin fake Claude on the far side of
// the PTY.

func v69Send(t *testing.T, tui *TUI, target, text string, enter bool) IPCResponse {
	t.Helper()
	return i35wIPC(t, func(c net.Conn) {
		tui.handleIPCSend(c, IPCRequest{Target: target, Text: text, Enter: enter})
	})
}

func v69Rig(t *testing.T, f *pkinFake) (*TUI, *Pane) {
	t.Helper()
	p, _ := pkinSendRig(t, f)
	return &TUI{panes: toPaneViews([]*Pane{p})}, p
}

// Criterion 1: a box that clears after the Enter answers delivered.
func TestSendVerdict_ABoxThatClearsAnswersDelivered(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	tui, _ := v69Rig(t, &pkinFake{body: nma5Body, after: "❯ ", takes: func(int) bool { return true }})
	resp := v69Send(t, tui, "eng2", nma5Body, true)
	if !resp.OK || resp.Delivery != DeliveryDelivered {
		t.Fatalf("reply %+v, want OK and delivered", resp)
	}
}

// Criterion 1: our text still in the box after the one resend answers not
// delivered, as an error, naming the agent and the toast's reason.
func TestSendVerdict_TextKeptThroughTheRetryAnswersNotDelivered(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	f := &pkinFake{body: nma5Body, after: "❯ ", takes: func(int) bool { return false }}
	tui, _ := v69Rig(t, f)
	resp := v69Send(t, tui, "eng2", nma5Body, true)
	want := "not delivered to eng2: Enter not taken twice; the text is still in the composer"
	if resp.OK || resp.Error != want || resp.Delivery != DeliveryNotDelivered {
		t.Fatalf("reply %+v, want error %q", resp, want)
	}
	if n := f.enters.Load(); n != 2 {
		t.Fatalf("sent %d Enters, want 2: the retry is unchanged", n)
	}
}

// Criterion 1 and the feed-it-nothing case: a pane that never shows a
// composer gives the watcher nothing to see. The reply is could not confirm,
// with the watcher's reason, inside the watcher's own deadline: never a hang.
func TestSendVerdict_NoComposerAnswersCouldNotConfirmWithinTheDeadline(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	f := &pkinFake{body: nma5Body, shows: "Loading conversation...", after: "Loading conversation...", takes: func(int) bool { return true }}
	tui, p := v69Rig(t, f)
	f.paint("Loading conversation...")
	_ = p
	start := time.Now()
	resp := v69Send(t, tui, "eng2", nma5Body, true)
	if !resp.OK || resp.Delivery != DeliveryUnconfirmed || resp.Data != "no composer visible" {
		t.Fatalf("reply %+v, want OK, unconfirmed, reason \"no composer visible\"", resp)
	}
	if el := time.Since(start); el > bracketedPasteSubmitDelay+2*submitConfirmWindow+time.Second {
		t.Fatalf("answered after %s, past the watcher's deadline", el)
	}
}

// G2: the belt withholds the submit (the box never changed when our paste
// ended). The submit is armed for later, so the reply is could not confirm
// with the ruling's reason, not delivered and not a failure.
func TestSendVerdict_AWithheldSubmitAnswersCouldNotConfirm(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	f := &pkinFake{body: nma5Body, shows: "❯ ", after: "❯ ", takes: func(int) bool { return true }}
	tui, p := v69Rig(t, f)
	resp := v69Send(t, tui, "eng2", nma5Body, true)
	if !resp.OK || resp.Delivery != DeliveryUnconfirmed || resp.Data != withheldVerdictReason {
		t.Fatalf("reply %+v, want OK, unconfirmed, the withheld reason", resp)
	}
	if n := f.enters.Load(); n != 0 {
		t.Fatalf("sent %d Enters, want none: the belt withholds", n)
	}
	p.clearPendingSubmit()
}

// G1: --no-enter submits nothing, so it has nothing to confirm: delivered,
// as before.
func TestSendVerdict_NoEnterAnswersDelivered(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	f := &pkinFake{body: nma5Body, after: "❯ ", takes: func(int) bool { return true }}
	tui, _ := v69Rig(t, f)
	resp := v69Send(t, tui, "eng2", nma5Body, false)
	if !resp.OK || resp.Delivery != DeliveryDelivered {
		t.Fatalf("reply %+v, want OK and delivered", resp)
	}
	if n := f.enters.Load(); n != 0 {
		t.Fatalf("sent %d Enters for --no-enter", n)
	}
}

// G3: an agent type with no composer check (a raw pane) answers unchecked:
// OK, with the reason, and never could not confirm.
func TestSendVerdict_ARawPaneAnswersUnchecked(t *testing.T) {
	f := &pkinFake{body: nma5Body, after: "❯ ", takes: func(int) bool { return true }}
	tui, p := v69Rig(t, f)
	p.noBracketedPaste = true
	resp := v69Send(t, tui, "eng2", nma5Body, true)
	if !resp.OK || resp.Delivery != DeliveryUnchecked || resp.Data != "no delivery check for this agent type" {
		t.Fatalf("reply %+v, want OK, unchecked, \"no delivery check for this agent type\"", resp)
	}
}

// Positive twin, deferred: a send held behind an open dialog keeps its
// words and its OK, now with the deferred outcome beside them.
func TestSendVerdict_ADialogStillAnswersDeferred(t *testing.T) {
	f := &pkinFake{body: nma5Body, after: "❯ ", takes: func(int) bool { return true }}
	tui, p := v69Rig(t, f)
	p.latchDialogOpen("")
	resp := v69Send(t, tui, "eng2", nma5Body, true)
	if !resp.OK || resp.Delivery != DeliveryDeferred || !strings.HasPrefix(resp.Data, "deferred to eng2 — modal open") {
		t.Fatalf("reply %+v, want OK, deferred, the deferral words", resp)
	}
	if q := p.QueuedMessageCount(); q != 1 {
		t.Fatalf("%d held, want the message held", q)
	}
}

// Positive twin, queued: a suspended agent's "resumed and delivered" keeps
// its words, now with the delivered outcome.
func TestSendVerdict_AResumedSendStillAnswersResumedAndDelivered(t *testing.T) {
	resp := resumedSendReply()
	if !resp.OK || resp.Data != `"resumed and delivered"` || resp.Delivery != DeliveryDelivered {
		t.Fatalf("reply %+v, want OK, \"resumed and delivered\", delivered", resp)
	}
}

// The mapping, cell by cell: each outcome's reply.
func TestSendVerdict_EachOutcomeMapsToItsReply(t *testing.T) {
	cases := []struct {
		v    SendVerdict
		want IPCResponse
	}{
		{SendVerdict{DeliveryDelivered, ""}, IPCResponse{OK: true, Delivery: DeliveryDelivered}},
		{SendVerdict{DeliveryNotDelivered, "r"}, IPCResponse{Error: "not delivered to eng2: r", Delivery: DeliveryNotDelivered, Data: "r"}},
		{SendVerdict{DeliveryUnconfirmed, "r"}, IPCResponse{OK: true, Delivery: DeliveryUnconfirmed, Data: "r"}},
		{SendVerdict{DeliveryUnchecked, "r"}, IPCResponse{OK: true, Delivery: DeliveryUnchecked, Data: "r"}},
		{SendVerdict{DeliveryDeferred, ""}, IPCResponse{OK: true, Delivery: DeliveryDeferred, Data: "deferred to eng2 — modal open, 1 queued"}},
	}
	for _, c := range cases {
		got := verdictIPCResponse("eng2", c.v, "deferred to eng2 — modal open, 1 queued")
		if got.OK != c.want.OK || got.Error != c.want.Error || got.Delivery != c.want.Delivery || got.Data != c.want.Data {
			t.Errorf("%+v: reply %+v, want %+v", c.v, got, c.want)
		}
	}
}

// v69PipePTY gives a test pane a PTY whose writes are drained and dropped,
// for tests about something other than what reaches the child. Before
// ini-69fy a pane with no PTY answered OK while its text went nowhere; now
// it answers not delivered, so a test that needs OK needs a PTY.
func v69PipePTY(t *testing.T, p *Pane) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	p.ptmx = &filePty{w}
	go func() { _, _ = io.Copy(io.Discard, r) }()
	t.Cleanup(func() { w.Close(); r.Close() })
}

// A pane with no PTY that reaches the send path is not delivered to, and
// says so: before, the reply was OK and the text went nowhere.
func TestSendVerdict_APaneWithNoPTYAnswersNotDelivered(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	tui := &TUI{panes: toPaneViews([]*Pane{p})}
	resp := v69Send(t, tui, "eng1", "hi", true)
	if resp.OK || resp.Error != "not delivered to eng1: pane has no PTY" {
		t.Fatalf("reply %+v, want not delivered, pane has no PTY", resp)
	}
}

// Item 4: a peer that answers with its verdict has it passed through as is.
func TestSendVerdict_APeersVerdictIsPassedThrough(t *testing.T) {
	r := newCrossRig(t)
	for _, peer := range []ControlResp{
		{OK: true, Delivery: DeliveryDelivered},
		{OK: true, Delivery: DeliveryUnconfirmed, Data: "no composer visible"},
		{OK: true, Delivery: DeliveryUnchecked, Data: "no delivery check for this agent type"},
	} {
		peer := peer
		r.reply = func(ControlCmd) *ControlResp { return &peer }
		resp := r.call(t, sendH, IPCRequest{Action: "send", Host: "workbench", Target: "eng1", Text: "hi", Enter: true})
		if !resp.OK || resp.Delivery != peer.Delivery || resp.Data != peer.Data {
			t.Errorf("peer %+v: IPC answer %+v, want its verdict passed through", peer, resp)
		}
	}
}

// Item 4: the peer's not delivered is an error naming host:agent once, with
// the peer's reason.
func TestSendVerdict_APeersNotDeliveredNamesHostAndAgent(t *testing.T) {
	r := newCrossRig(t)
	r.reply = func(ControlCmd) *ControlResp {
		return &ControlResp{Error: "Enter not taken twice; the text is still in the composer", Delivery: DeliveryNotDelivered}
	}
	resp := r.call(t, sendH, IPCRequest{Action: "send", Host: "workbench", Target: "eng1", Text: "hi", Enter: true})
	want := "not delivered to workbench:eng1: Enter not taken twice; the text is still in the composer"
	if resp.OK || resp.Error != want {
		t.Fatalf("IPC answer %+v, want error %q", resp, want)
	}
}

// G7: a bare name that resolves to another machine's agent takes the
// host:agent route and answers with that peer's verdict. Before, RemotePane
// SendText answered nothing and the handler said OK at once.
func TestSendVerdict_ABareNameOnARemotePaneGetsThePeersVerdict(t *testing.T) {
	r := newCrossRig(t)
	r.tui.panes = r.tui.panes[1:] // only the peer's eng1 is left
	r.reply = func(ControlCmd) *ControlResp {
		return &ControlResp{OK: true, Delivery: DeliveryUnconfirmed, Data: "no composer visible"}
	}
	resp := r.call(t, func(tu *TUI, c *fakeConn, req IPCRequest) { tu.handleIPCSend(c, req) },
		IPCRequest{Action: "send", Target: "eng1", Text: "hi", Enter: true})
	if !resp.OK || resp.Delivery != DeliveryUnconfirmed || resp.Data != "no composer visible" {
		t.Fatalf("IPC answer %+v, want the peer's could not confirm", resp)
	}
}

// v69DaemonForward sends host:agent through a daemon's IPC to its connected
// client "macbook", which answers the forward_send with reply, and returns
// the daemon's IPC answer.
func v69DaemonForward(t *testing.T, reply ControlResp) IPCResponse {
	t.Helper()
	d := &Daemon{
		project: &config.Project{Name: "test", Root: t.TempDir(), PeerName: "workbench", Roles: []string{"eng1"}},
		panes:   []*Pane{newEmuPane("eng1", 80, 24)},
		clients: make(map[string]net.Conn),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	dial, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dial.Close() })
	accept, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { accept.Close() })
	d.sessionsMu.Lock()
	d.clients["macbook"] = accept
	d.clientCtrlMu = map[string]*sync.Mutex{"macbook": {}}
	d.sessionsMu.Unlock()
	go func() {
		sc := bufio.NewScanner(dial)
		if sc.Scan() {
			var cmd ControlCmd
			json.Unmarshal(sc.Bytes(), &cmd)
			r := reply
			r.ID = cmd.ID
			writeJSON(dial, r)
		}
	}()
	go func() {
		sc := bufio.NewScanner(accept)
		for sc.Scan() {
			d.deliverForwardResp(sc.Bytes())
		}
	}()
	server, client := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go func() { d.handleDaemonIPCConn(server); server.Close() }()
	respCh := make(chan IPCResponse, 1)
	go func() {
		sc := bufio.NewScanner(client)
		sc.Scan()
		var r IPCResponse
		json.Unmarshal(sc.Bytes(), &r)
		respCh <- r
	}()
	data, _ := json.Marshal(IPCRequest{Action: "send", Target: "super", Host: "macbook", Text: "hi", Enter: true})
	client.Write(append(data, '\n'))
	select {
	case r := <-respCh:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("no daemon answer within 5s")
		return IPCResponse{}
	}
}

// Item 4, the other direction: a CLI on the daemon's machine sending to an
// agent in the connected initech hears that initech's verdict; an older one
// that answers OK with none reads could not confirm, never delivered.
func TestSendVerdict_AForwardedSendCarriesTheClientsVerdict(t *testing.T) {
	cases := []struct {
		name  string
		reply ControlResp
		check func(IPCResponse) bool
	}{
		{"delivered", ControlResp{OK: true, Delivery: DeliveryDelivered},
			func(r IPCResponse) bool { return r.OK && r.Delivery == DeliveryDelivered }},
		{"not delivered", ControlResp{Error: "r", Delivery: DeliveryNotDelivered},
			func(r IPCResponse) bool { return !r.OK && r.Error == "not delivered to macbook:super: r" }},
		{"older client", ControlResp{OK: true},
			func(r IPCResponse) bool {
				return r.OK && r.Delivery == DeliveryUnconfirmed && r.Data == "older initech on macbook"
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if resp := v69DaemonForward(t, c.reply); !c.check(resp) {
				t.Errorf("client %+v: daemon answer %+v", c.reply, resp)
			}
		})
	}
}
