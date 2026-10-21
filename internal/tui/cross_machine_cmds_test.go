package tui

// ini-58m8: host:agent requests act on the named remote agent and return what
// that machine answered. Before the fix: interrupt host:agent was forwarded as
// a send of the text "hard" (or an empty paste) and reported "interrupted";
// suspend and resume ignored the host and acted on the LOCAL agent of the
// same name; and a forwarded send answered OK before the peer replied.

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// crossRig is a TUI with a local eng1 and a remote workbench:eng1 served by
// a fake peer whose control replies the test scripts.
type crossRig struct {
	tui   *TUI
	local *Pane
	mu    sync.Mutex
	got   []ControlCmd
	reply func(ControlCmd) *ControlResp
}

func (r *crossRig) received() []ControlCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ControlCmd(nil), r.got...)
}

func newCrossRig(t *testing.T) *crossRig {
	t.Helper()
	r := &crossRig{reply: func(ControlCmd) *ControlResp { return &ControlResp{OK: true} }}
	srv := startRemotePeerServer(t, remotePeerServerConfig{
		firstResponse: HelloOKMsg{Action: "hello_ok", Version: ProtocolVersion, PeerName: "workbench"},
		agentNames:    []string{"eng1"},
		onControl: func(c ControlCmd) *ControlResp {
			r.mu.Lock()
			r.got = append(r.got, c)
			fn := r.reply
			r.mu.Unlock()
			return fn(c)
		},
	})
	pc, err := connectPeer("workbench", config.Remote{Addr: srv.addr, Token: "t"}, &config.Project{PeerName: "local-peer"})
	if err != nil {
		t.Fatalf("connectPeer: %v", err)
	}
	t.Cleanup(func() {
		for _, p := range pc.panes {
			if rp, ok := p.(*RemotePane); ok {
				rp.Close()
			}
		}
		pc.Close()
	})
	r.local = testPane("eng1")
	r.local.activity = StateIdle
	r.tui = newTestTUI(r.local)
	r.tui.project = &config.Project{PeerName: "local-peer"}
	r.tui.panes = append(r.tui.panes, pc.panes...)
	return r
}

func (r *crossRig) call(t *testing.T, handle func(*TUI, *fakeConn, IPCRequest), req IPCRequest) IPCResponse {
	t.Helper()
	conn := &fakeConn{}
	handle(r.tui, conn, req)
	if len(conn.written) == 0 {
		t.Fatalf("%s %s:%s wrote no reply", req.Action, req.Host, req.Target)
	}
	var resp IPCResponse
	if err := json.Unmarshal(conn.written[:conn.findNewline()], &resp); err != nil {
		t.Fatalf("reply %q: %v", conn.written, err)
	}
	return resp
}

func interruptH(t *TUI, c *fakeConn, r IPCRequest) { t.handleIPCInterrupt(c, r) }
func suspendH(t *TUI, c *fakeConn, r IPCRequest)   { t.handleIPCSuspend(c, r) }
func resumeH(t *TUI, c *fakeConn, r IPCRequest)    { t.handleIPCResume(c, r) }
func sendH(t *TUI, c *fakeConn, r IPCRequest)      { t.forwardSendToRemote(c, r) }

// AC 1: interrupt host:agent reaches the peer as an interrupt, never a send.
func TestCrossMachine_InterruptIsForwardedAsAnInterruptNotASend(t *testing.T) {
	for _, hard := range []bool{false, true} {
		r := newCrossRig(t)
		text := ""
		if hard {
			text = "hard"
		}
		resp := r.call(t, interruptH, IPCRequest{Action: "interrupt", Host: "workbench", Target: "eng1", Text: text})
		if !resp.OK {
			t.Fatalf("hard=%v: interrupt reply %+v", hard, resp)
		}
		got := r.received()
		if len(got) != 1 || got[0].Action != "interrupt" || got[0].Target != "eng1" || got[0].Text != text {
			t.Fatalf("hard=%v: the peer received %+v, want one interrupt for eng1 with text %q and no send", hard, got, text)
		}
	}
}

// AC 5b: a peer that does not know the interrupt action answers an error,
// and it is never degraded to a send.
func TestCrossMachine_AnOlderPeerRejectsInterruptAndNothingIsTyped(t *testing.T) {
	r := newCrossRig(t)
	r.reply = func(c ControlCmd) *ControlResp {
		if c.Action == "interrupt" {
			return &ControlResp{Error: "unknown action: interrupt"}
		}
		return &ControlResp{OK: true}
	}
	resp := r.call(t, interruptH, IPCRequest{Action: "interrupt", Host: "workbench", Target: "eng1", Text: "hard"})
	if resp.OK || !strings.Contains(resp.Error, "not interrupted") || !strings.Contains(resp.Error, "unknown action") {
		t.Fatalf("reply %+v, want not interrupted with the peer's reason", resp)
	}
	for _, c := range r.received() {
		if c.Action == "send" {
			t.Fatal("an interrupt the peer did not know was retried as a send")
		}
	}
}

// AC 2: suspend and resume of a remote agent are refused, nothing is
// forwarded, and the local namesake is not touched (asserted by effect).
func TestCrossMachine_SuspendOfARemoteAgentIsRefusedAndLeavesTheLocalOne(t *testing.T) {
	r := newCrossRig(t)
	resp := r.call(t, suspendH, IPCRequest{Action: "suspend", Host: "workbench", Target: "eng1"})
	if r.local.IsSuspended() {
		t.Fatal("suspend workbench:eng1 parked the LOCAL eng1")
	}
	want := "suspend is not supported on a remote agent; run initech suspend eng1 on workbench"
	if resp.OK || resp.Error != want {
		t.Fatalf("reply %+v, want error %q", resp, want)
	}
	if len(r.received()) != 0 {
		t.Fatal("a refused suspend was forwarded")
	}
}

func TestCrossMachine_ResumeOfARemoteAgentIsRefusedAndLeavesTheLocalOne(t *testing.T) {
	r := newCrossRig(t)
	r.local.mu.Lock()
	r.local.suspended = true
	r.local.mu.Unlock()
	resp := r.call(t, resumeH, IPCRequest{Action: "resume", Host: "workbench", Target: "eng1"})
	if !r.local.IsSuspended() {
		t.Fatal("resume workbench:eng1 woke the LOCAL eng1")
	}
	want := "resume is not supported on a remote agent; run initech resume eng1 on workbench"
	if resp.OK || resp.Error != want {
		t.Fatalf("reply %+v, want error %q", resp, want)
	}
	if len(r.received()) != 0 {
		t.Fatal("a refused resume was forwarded")
	}
}

// AC 3: a forwarded send returns what the peer answered: its rejection, or
// an error when the connection drops; OK only once the peer accepted.
func TestCrossMachine_AForwardedSendReturnsThePeersAnswer(t *testing.T) {
	cases := []struct {
		name   string
		reply  *ControlResp
		wantOK bool
		want   string
	}{
		{"accepted", &ControlResp{OK: true}, true, ""},
		{"agent not found", &ControlResp{Error: `agent "eng1" not found`}, false, `agent "eng1" not found`},
		{"oversized", &ControlResp{Error: "text too large"}, false, "text too large"},
		{"connection closed", &ControlResp{Action: "__close__"}, false, "not delivered"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newCrossRig(t)
			r.reply = func(ControlCmd) *ControlResp { return c.reply }
			resp := r.call(t, sendH, IPCRequest{Action: "send", Host: "workbench", Target: "eng1", Text: "hello", Enter: true})
			if resp.OK != c.wantOK || !strings.Contains(resp.Error, c.want) {
				t.Fatalf("reply %+v, want ok=%v with %q", resp, c.wantOK, c.want)
			}
			if !c.wantOK && !strings.Contains(resp.Error, "not delivered to workbench:eng1") {
				t.Errorf("a rejected send's reply %q does not say it was not delivered, and to whom", resp.Error)
			}
		})
	}
}

// AC 4: a host equal to the local peer name resolves locally.
func TestCrossMachine_TheLocalPeerNameResolvesLocally(t *testing.T) {
	r := newCrossRig(t)
	resp := r.call(t, suspendH, IPCRequest{Action: "suspend", Host: "local-peer", Target: "eng1"})
	if strings.Contains(resp.Error, "not supported on a remote") {
		t.Fatalf("a host naming this machine was treated as remote: %+v", resp)
	}
	if !r.local.IsSuspended() {
		t.Fatalf("suspend local-peer:eng1 did not reach the local eng1: %+v", resp)
	}
	if len(r.received()) != 0 {
		t.Fatal("a local request was forwarded")
	}
}

// AC 5 and feed-it-nothing: an unknown host or agent is an error naming
// both, and nothing reaches the peer.
func TestCrossMachine_UnknownHostOrAgentIsAnErrorAndNothingIsSent(t *testing.T) {
	r := newCrossRig(t)
	for _, req := range []IPCRequest{
		{Action: "interrupt", Host: "nohost", Target: "eng1"},
		{Action: "interrupt", Host: "workbench", Target: "ghost"},
		{Action: "send", Host: "nohost", Target: "eng1", Text: "x"},
		{Action: "send", Host: "workbench", Target: "ghost", Text: "x"},
	} {
		h := interruptH
		if req.Action == "send" {
			h = sendH
		}
		resp := r.call(t, h, req)
		if resp.OK || !strings.Contains(resp.Error, req.Host) || !strings.Contains(resp.Error, req.Target) {
			t.Errorf("%s %s:%s: reply %+v, want an error naming both", req.Action, req.Host, req.Target, resp)
		}
	}
	if got := r.received(); len(got) != 0 {
		t.Fatalf("requests for an unknown host or agent reached the peer: %+v", got)
	}
}

// Feed it nothing: a peer that never answers an interrupt times out as an
// error, never OK. Slow by nature (the 10 s request wait), so it runs in the
// full suite.
func TestCrossMachine_APeerThatNeverAnswersIsAnErrorNeverOK(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the 10 s control request timeout; runs in the full suite")
	}
	r := newCrossRig(t)
	r.reply = func(ControlCmd) *ControlResp { return nil }
	start := time.Now()
	resp := r.call(t, interruptH, IPCRequest{Action: "interrupt", Host: "workbench", Target: "eng1"})
	if resp.OK || !strings.Contains(resp.Error, "not interrupted") {
		t.Fatalf("reply after %v: %+v, want not interrupted", time.Since(start), resp)
	}
}

// The daemon's interrupt action writes Escape, or Ctrl+C for "hard", to its
// own agent's PTY, and refuses unknown, suspended and not-running agents.
func TestDaemon_InterruptWritesTheControlByteToItsAgent(t *testing.T) {
	for _, c := range []struct {
		text string
		want byte
	}{{"", 0x1B}, {"hard", 0x03}} {
		p, r := paneWithPipe(t)
		p.name = "eng1"
		d := &Daemon{project: &config.Project{Name: "t"}, panes: []*Pane{p}}
		if resp := d.interruptPane(ControlCmd{Action: "interrupt", Target: "eng1", Text: c.text}); !resp.OK {
			t.Fatalf("text %q: %+v", c.text, resp)
		}
		buf := make([]byte, 4)
		n, err := r.Read(buf)
		if err != nil || n != 1 || buf[0] != c.want {
			t.Fatalf("text %q: PTY got % x (err %v), want %02x", c.text, buf[:n], err, c.want)
		}
	}

	suspended := testPane("eng2")
	suspended.suspended = true
	d := &Daemon{project: &config.Project{Name: "t"}, panes: []*Pane{suspended, testPane("eng3")}}
	for target, want := range map[string]string{"ghost": "not found", "eng2": "suspended", "eng3": "not running"} {
		if resp := d.interruptPane(ControlCmd{Action: "interrupt", Target: target}); resp.OK || !strings.Contains(resp.Error, want) {
			t.Errorf("interrupt %s: %+v, want an error containing %q", target, resp, want)
		}
	}
}
