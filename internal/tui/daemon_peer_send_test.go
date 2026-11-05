package tui

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// ini-18pz: initech send from an agent the serve runs reaches the Mac.

// macSide is the Mac's window 1 as the serve sees it: a client on the control
// stream that answers forward_send through the real answerForwardSend.
type macSide struct {
	mu      sync.Mutex
	got     []ControlResp // What the Mac was asked to deliver.
	verdict SendVerdict
	err     error
}

func (m *macSide) deliver(target, text string, enter bool, o sendOrigin) (SendVerdict, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.got = append(m.got, ControlResp{Target: target, Text: text, Enter: enter, From: o.From})
	return m.verdict, m.err
}

func (m *macSide) asked() []ControlResp {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ControlResp(nil), m.got...)
}

// peerServe is a serve on the workbench with one pushed agent, eng2, owned by
// the Mac "laptop", connected over a real stream to mac.
func peerServe(t *testing.T, mac *macSide) *Daemon {
	t.Helper()
	d := newTestDaemon(t)
	d.serve = true
	d.project.PeerName = "workbench"
	d.panes = []*Pane{newEmuPane("eng2", 80, 24)}
	d.clients = map[string]net.Conn{}
	d.clientCtrlMu = map[string]*sync.Mutex{}
	d.ownership.claim("eng2", "laptop", PaneConfig{Name: "eng2"})
	connectMac(t, d, "laptop", mac)
	return d
}

// connectMac attaches mac to d as the client named name.
func connectMac(t *testing.T, d *Daemon, name string, mac *macSide) {
	t.Helper()
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
	d.clients[name] = accept
	d.clientCtrlMu[name] = &sync.Mutex{}
	d.sessionsMu.Unlock()
	pm := &peerManager{onForwardSend: mac.deliver}
	go func() {
		sc := bufio.NewScanner(dial)
		for sc.Scan() {
			var cmd ControlCmd
			json.Unmarshal(sc.Bytes(), &cmd)
			r := pm.answerForwardSend("workbench", ControlResp{Action: cmd.Action, Target: cmd.Target, Text: cmd.Text,
				Enter: cmd.Enter, From: cmd.From, Urgent: cmd.Urgent})
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
}

// sendVia asks the serve's IPC to send req, as initech send in a pane would.
func sendVia(t *testing.T, d *Daemon, req IPCRequest) IPCResponse {
	t.Helper()
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
	data, _ := json.Marshal(req)
	client.Write(append(data, '\n'))
	select {
	case r := <-respCh:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("no answer from the serve within 5s")
		return IPCResponse{}
	}
}

func fromPane(name string) func(net.Conn) (string, error) {
	return func(net.Conn) (string, error) { return name, nil }
}

// TestPeerSend_ABareNameFromAServedPaneReachesTheMacWithItsVerdict is AC1 at
// the serve: red at base, which answers "agent super not found".
func TestPeerSend_ABareNameFromAServedPaneReachesTheMacWithItsVerdict(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verdict SendVerdict
		check   func(IPCResponse) bool
	}{
		{"delivered", SendVerdict{Outcome: DeliveryDelivered},
			func(r IPCResponse) bool { return r.OK && r.Delivery == DeliveryDelivered }},
		{"deferred", SendVerdict{Outcome: DeliveryDeferred},
			func(r IPCResponse) bool {
				return r.OK && r.Delivery == DeliveryDeferred && r.Data == "deferred to super — modal open"
			}},
		{"not delivered", SendVerdict{Outcome: DeliveryNotDelivered, Reason: "super is suspended"},
			func(r IPCResponse) bool {
				return !r.OK && r.Delivery == DeliveryNotDelivered && r.Error == "not delivered to super: super is suspended"
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mac := &macSide{verdict: tc.verdict}
			d := peerServe(t, mac)
			d.identifySender = fromPane("eng2")
			resp := sendVia(t, d, IPCRequest{Action: "send", Target: "super", Text: "[from eng2] probe", Enter: true, From: "someone-else"})
			if !tc.check(resp) {
				t.Fatalf("the sender heard %+v", resp)
			}
			got := mac.asked()
			if len(got) != 1 || got[0].Target != "super" || got[0].Text != "[from eng2] probe" || !got[0].Enter {
				t.Fatalf("the Mac was asked %+v", got)
			}
			if got[0].From != "eng2" {
				t.Fatalf("the Mac was told the send is from %q; it is from the pane, eng2", got[0].From)
			}
		})
	}
}

// TestPeerSend_OnlyAPaneTheServeStartedMaySendThrough is AC2's refusal.
func TestPeerSend_OnlyAPaneTheServeStartedMaySendThrough(t *testing.T) {
	mac := &macSide{verdict: SendVerdict{Outcome: DeliveryDelivered}}
	d := peerServe(t, mac)
	d.identifySender = func(net.Conn) (string, error) { return "", errors.New("not inside one of its panes") }
	resp := sendVia(t, d, IPCRequest{Action: "send", Target: "super", Text: "hi", Enter: true, From: "eng2"})
	if resp.OK || !strings.Contains(resp.Error, "only an agent this serve started can send to another machine through it") {
		t.Fatalf("a process outside the serve's panes got %+v", resp)
	}
	if n := len(mac.asked()); n != 0 {
		t.Fatalf("it was forwarded %d times", n)
	}
}

// TestPeerSend_TheMacUnreachableIsSaidAndNothingIsQueued is a feed-it-nothing
// case: the owning machine is not connected.
func TestPeerSend_TheMacUnreachableIsSaidAndNothingIsQueued(t *testing.T) {
	mac := &macSide{verdict: SendVerdict{Outcome: DeliveryDelivered}}
	d := peerServe(t, mac)
	d.identifySender = fromPane("eng2")
	d.sessionsMu.Lock()
	delete(d.clients, "laptop")
	d.clients["other-mac"] = nil // Someone else is connected; not eng2's owner.
	d.sessionsMu.Unlock()
	resp := sendVia(t, d, IPCRequest{Action: "send", Target: "super", Text: "hi", Enter: true})
	if resp.OK || resp.Delivery != DeliveryNotDelivered || resp.Error != "not delivered to super: laptop is not connected to this serve" {
		t.Fatalf("with the Mac away the sender heard %+v", resp)
	}
	if n := len(mac.asked()); n != 0 {
		t.Fatalf("it was forwarded %d times", n)
	}
	// An agent with no owner, and nothing connected.
	d.ownership.release("eng2")
	d.sessionsMu.Lock()
	d.clients = map[string]net.Conn{}
	d.sessionsMu.Unlock()
	if resp := sendVia(t, d, IPCRequest{Action: "send", Target: "super", Text: "hi"}); resp.Error != "not delivered to super: no machine is connected to this serve" {
		t.Fatalf("with nothing connected the sender heard %+v", resp)
	}
}

// TestPeerSend_AnUnknownAgentGetsTheMacsWords is the other feed-it-nothing
// case: the Mac has no such agent and says so in its own words.
func TestPeerSend_AnUnknownAgentGetsTheMacsWords(t *testing.T) {
	mac := &macSide{err: fmt.Errorf("agent %q not found", "ghost")}
	d := peerServe(t, mac)
	d.identifySender = fromPane("eng2")
	resp := sendVia(t, d, IPCRequest{Action: "send", Target: "ghost", Text: "hi", Enter: true})
	if resp.OK || resp.Error != `agent "ghost" not found` {
		t.Fatalf("the sender heard %+v", resp)
	}
}

// TestPeerSend_AStartTimeAgentGoesToTheOnlyMachineAndSeveralAreNamed: an
// agent in the serve's own config has no pushing owner.
func TestPeerSend_AStartTimeAgentGoesToTheOnlyMachineAndSeveralAreNamed(t *testing.T) {
	mac := &macSide{verdict: SendVerdict{Outcome: DeliveryDelivered}}
	d := peerServe(t, mac)
	d.ownership.release("eng2")
	d.identifySender = fromPane("eng2")
	if resp := sendVia(t, d, IPCRequest{Action: "send", Target: "super", Text: "hi", Enter: true}); !resp.OK {
		t.Fatalf("one machine connected: %+v", resp)
	}
	connectMac(t, d, "desk", &macSide{})
	resp := sendVia(t, d, IPCRequest{Action: "send", Target: "super", Text: "hi", Enter: true})
	if resp.OK || !strings.Contains(resp.Error, "2 machines are connected (desk, laptop): name one") {
		t.Fatalf("two machines connected: %+v", resp)
	}
}

// TestPeerSend_APushedPaneCarriesTheServesSocketAndItsOwnName is AC2's
// discovery, by effect: the agent's process sees the serve's socket and its
// own name, never what the push said.
func TestPeerSend_APushedPaneCarriesTheServesSocketAndItsOwnName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the probe is a POSIX shell")
	}
	d := newTestDaemon(t)
	d.sockPath = "/tmp/serve-18pz.sock"
	out := filepath.Join(t.TempDir(), "env")
	cmd := ConfigureAgentCmd{
		Action:  "configure_agent",
		Name:    "eng2",
		Command: []string{"/bin/sh", "-c", `printf '%s|%s|%s' "$INITECH_SOCKET" "$INITECH_AGENT" "$BEADS_ACTOR" > ` + out + `; sleep 30`},
		Dir:     filepath.Join(d.project.Root, "eng2"),
		Env:     []string{"INITECH_SOCKET=/Users/mac/rig/.initech/initech.sock", "INITECH_AGENT=super", "KEEP=1"},
	}
	line, _ := json.Marshal(cmd)
	if resp := d.handleConfigureAgent(line, "laptop"); !resp.OK {
		t.Fatalf("configure: %+v", resp)
	}
	defer d.removePane("eng2")
	var got []byte
	waitFor(t, 5*time.Second, func() bool { got, _ = os.ReadFile(out); return len(got) > 0 }, "the agent's env")
	if string(got) != "/tmp/serve-18pz.sock|eng2|eng2" {
		t.Fatalf("the pushed agent sees %q", got)
	}
	cfg, _ := d.ownership.config("eng2")
	keep := false
	for _, kv := range cfg.Env {
		keep = keep || kv == "KEEP=1"
	}
	if !keep {
		t.Fatalf("the push's other variables were dropped: %v", cfg.Env)
	}
}

// ── the real thing: a client process inside a pane the serve started ──

// peerSendHelperEnv names the file the helper child writes its answer to;
// set, the test binary runs as that child (see TestMain).
const peerSendHelperEnv = "INITECH_18PZ_HELPER_OUT"

// runPeerSendHelper is a child process, not a test: TestMain runs it when
// TestPeerSend_AClientInsideAServedPaneIsIdentifiedByItsProcess starts the
// test binary inside a pane. It sends one request over the socket the serve
// set in the pane and writes the answer to the file it was given.
func runPeerSendHelper(out string) {
	conn, err := net.Dial("unix", os.Getenv("INITECH_SOCKET"))
	if err != nil {
		os.WriteFile(out, []byte("dial: "+err.Error()), 0o600)
		return
	}
	defer conn.Close()
	req, _ := json.Marshal(IPCRequest{Action: "send", Target: "super", Text: "[from eng2] probe", Enter: true, From: os.Getenv("INITECH_AGENT")})
	conn.Write(append(req, '\n'))
	sc := bufio.NewScanner(conn)
	sc.Scan()
	os.WriteFile(out, sc.Bytes(), 0o600)
}

// TestPeerSend_AClientInsideAServedPaneIsIdentifiedByItsProcess is AC1 and
// AC2 with no stand-in for identity: a process the serve's pane runs dials
// the serve's socket and is forwarded; the test process itself, outside any
// pane, is refused.
func TestPeerSend_AClientInsideAServedPaneIsIdentifiedByItsProcess(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("connection identity is darwin and linux only")
	}
	if testing.Short() {
		t.Skip("starts a real process in a pane")
	}
	mac := &macSide{verdict: SendVerdict{Outcome: DeliveryDelivered}}
	d := newTestDaemon(t)
	d.serve = true
	d.project.PeerName = "workbench"
	d.clients = map[string]net.Conn{}
	d.clientCtrlMu = map[string]*sync.Mutex{}
	connectMac(t, d, "laptop", mac)
	sock := fmt.Sprintf("/tmp/ini18pz-%d.sock", os.Getpid())
	os.Remove(sock)
	stop, err := d.startDaemonIPC(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	d.sockPath = sock
	out := filepath.Join(t.TempDir(), "answer")
	cmd := ConfigureAgentCmd{
		Action:  "configure_agent",
		Name:    "eng2",
		Command: []string{os.Args[0]},
		Dir:     filepath.Join(d.project.Root, "eng2"),
		Env:     []string{peerSendHelperEnv + "=" + out},
	}
	line, _ := json.Marshal(cmd)
	if resp := d.handleConfigureAgent(line, "laptop"); !resp.OK {
		t.Fatalf("configure: %+v", resp)
	}
	defer d.removePane("eng2")
	var got []byte
	waitFor(t, 10*time.Second, func() bool { got, _ = os.ReadFile(out); return len(got) > 0 }, "the pane's send")
	var resp IPCResponse
	if err := json.Unmarshal(got, &resp); err != nil || !resp.OK || resp.Delivery != DeliveryDelivered {
		t.Fatalf("the agent heard %s", got)
	}
	if a := mac.asked(); len(a) != 1 || a[0].From != "eng2" || a[0].Text != "[from eng2] probe" {
		t.Fatalf("the Mac was asked %+v", a)
	}
	// This test process is outside every pane: refused.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req, _ := json.Marshal(IPCRequest{Action: "send", Target: "super", Text: "hi", Enter: true, From: "eng2"})
	conn.Write(append(req, '\n'))
	sc := bufio.NewScanner(conn)
	sc.Scan()
	var refused IPCResponse
	json.Unmarshal(sc.Bytes(), &refused)
	if refused.OK || !strings.Contains(refused.Error, "only an agent this serve started") {
		t.Fatalf("a process outside the panes got %+v", refused)
	}
	if n := len(mac.asked()); n != 1 {
		t.Fatalf("the Mac was asked %d times, want only the pane's", n)
	}
}

// TestPeerSend_AHostAgentSendIsUnchanged: host:agent still goes to that host.
func TestPeerSend_AHostAgentSendIsUnchanged(t *testing.T) {
	mac := &macSide{verdict: SendVerdict{Outcome: DeliveryDelivered}}
	d := peerServe(t, mac)
	d.identifySender = func(net.Conn) (string, error) { t.Error("host:agent asked who sends"); return "", nil }
	if resp := sendVia(t, d, IPCRequest{Action: "send", Target: "super", Host: "laptop", Text: "hi", Enter: true, From: "x"}); !resp.OK {
		t.Fatalf("host:agent: %+v", resp)
	}
	if a := mac.asked(); len(a) != 1 || a[0].From != "x" {
		t.Fatalf("the Mac was asked %+v", a)
	}
}
