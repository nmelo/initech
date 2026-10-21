//go:build !windows

package tui

// ini-813u: a pane window 1 replaces, adds or removes reaches the window
// server in the same step. Before the fix only the wake path handed the
// successor over: after a restart or start the server kept serving the
// closed object (its PTY nil), so a second window's keys and sends were
// dropped while the reply said OK, and its copy of the agent froze.

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/hashicorp/yamux"
)

// handoffCatPane is a live /bin/cat pane named eng1.
func handoffCatPane(t *testing.T) *Pane {
	t.Helper()
	p, err := NewPane(PaneConfig{Name: "eng1", Command: []string{"/bin/cat"}, NoBracketedPaste: true}, 24, 80)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	p.Start()
	t.Cleanup(p.Close)
	return p
}

// handoffTUI is a TUI serving its panes on a window server.
func handoffTUI(t *testing.T, panes ...*Pane) (*TUI, *windowServer) {
	t.Helper()
	tui := f6kgTUI(toPaneViews(panes)...)
	ws, cleanup, err := startWindowServer(testWindowProject("127.0.0.1:0"), "test", panes, func(f func()) { go f() }, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("startWindowServer: %v", err)
	}
	t.Cleanup(cleanup)
	tui.windowSrv = ws
	return tui, ws
}

func emuText(p *Pane) string {
	e := p.Emulator()
	var b strings.Builder
	for y := 0; y < e.Height(); y++ {
		for x := 0; x < e.Width(); x++ {
			if c := e.CellAt(x, y); c != nil {
				b.WriteString(c.Content)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func sinkOf(p *Pane) bool {
	p.sinkMu.Lock()
	defer p.sinkMu.Unlock()
	return p.networkSink != nil
}

// viewerSend sends text to agent over a second window's control stream and
// returns the reply's error ("" for OK).
func viewerSend(t *testing.T, addr, agent, text string) string {
	t.Helper()
	// Its own peer name: the daemon keys windows by name, so reusing an
	// attached window's name would replace that window's control stream.
	session, ctrl, _ := dialWindow(t, addr, "viewer-send")
	defer session.Close()
	defer ctrl.Close()
	if err := writeJSON(ctrl, ControlCmd{ID: "1", Action: "send", Target: agent, Text: text, Enter: true}); err != nil {
		t.Fatalf("send: %v", err)
	}
	ctrl.SetReadDeadline(time.Now().Add(10 * time.Second))
	sc := NewIPCScanner(ctrl)
	for sc.Scan() {
		var resp ControlResp
		if json.Unmarshal(sc.Bytes(), &resp) == nil && resp.ID == "1" {
			if !resp.OK && resp.Error == "" {
				return "not ok"
			}
			return resp.Error
		}
	}
	return "no reply"
}

func handoffWaitFor(cond func() bool, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

// AC 1 and 2: after each of the four ways window 1 replaces a pane, the
// server serves the new one, it streams, and a second window's send reaches
// the new process.
func TestWindowHandoff_EveryReplacementReachesTheServer(t *testing.T) {
	// A just-started pane takes sends only after it settles (ini-i35w); the
	// gate's clock is shortened, as its own comment allows for tests, so the
	// send below does not wait out the 15 s production cap.
	origStable, origCap := startupSettleStable, startupSettleCap
	startupSettleStable, startupSettleCap = 200*time.Millisecond, 3*time.Second
	t.Cleanup(func() { startupSettleStable, startupSettleCap = origStable, origCap })
	entries := map[string]func(t *testing.T, tui *TUI, old *Pane){
		"ipc restart": func(t *testing.T, tui *TUI, old *Pane) {
			if resp := i35wIPC(t, func(c net.Conn) { tui.handleIPCRestart(c, IPCRequest{Target: "eng1"}) }); !resp.OK {
				t.Fatalf("restart: %+v", resp)
			}
		},
		"ipc start": func(t *testing.T, tui *TUI, old *Pane) {
			old.Close() // a stopped agent
			if !handoffWaitFor(func() bool { return !old.IsAlive() }, 5*time.Second) {
				t.Fatal("setup: the pane did not stop")
			}
			if resp := i35wIPC(t, func(c net.Conn) { tui.handleIPCStart(c, IPCRequest{Target: "eng1"}) }); !resp.OK {
				t.Fatalf("start: %+v", resp)
			}
		},
		"command-bar restart": func(t *testing.T, tui *TUI, old *Pane) {
			if err := tui.restartPane(old); err != nil {
				t.Fatalf("restartPane: %v", err)
			}
		},
		"top-overlay restart": func(t *testing.T, tui *TUI, old *Pane) {
			tui.top = topModal{active: true, data: []topEntry{{Key: "eng1"}}}
			tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone))
		},
	}
	for name, act := range entries {
		t.Run(name, func(t *testing.T) {
			old := handoffCatPane(t)
			tui, ws := handoffTUI(t, old)
			act(t, tui, old)
			np, ok := tui.panes[0].(*Pane)
			if !ok || np == old {
				t.Fatal("the entry point did not put a new pane in the slot")
			}
			t.Cleanup(np.Close)

			if got := ws.daemon.currentPane("eng1"); got != np {
				t.Fatal("the window server still serves the old eng1; a second window's keys go to a closed PTY")
			}
			if !sinkOf(np) {
				t.Fatal("the new eng1 has no network sink; a second window's copy freezes at its last frame")
			}
			if errText := viewerSend(t, ws.Addr(), "eng1", "MARK813"); errText != "" {
				t.Fatalf("a send from the second window failed: %s", errText)
			}
			if !handoffWaitFor(func() bool { return strings.Contains(emuText(np), "MARK813") }, 5*time.Second) {
				t.Fatal("a send from the second window never reached the new process")
			}
		})
	}
}

// readControl reads a second window's control stream until a message
// matches want, or five seconds pass, and returns that message's stream id.
func readControl(t *testing.T, ctrl net.Conn, sc *bufio.Scanner, want func(action, name string) bool) (uint32, bool) {
	t.Helper()
	ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	for sc.Scan() {
		var m struct {
			Action   string `json:"action"`
			Name     string `json:"name"`
			StreamID uint32 `json:"stream_id"`
		}
		if json.Unmarshal(sc.Bytes(), &m) == nil && want(m.Action, m.Name) {
			return m.StreamID, true
		}
	}
	return 0, false
}

// acceptStreams accepts every stream window 1 opens on a second window's
// session, as the window does, so a test can type into one by its id.
func acceptStreams(t *testing.T, session *yamux.Session) func(id uint32) net.Conn {
	t.Helper()
	var mu sync.Mutex
	streams := map[uint32]net.Conn{}
	go func() {
		for {
			s, err := session.AcceptStream()
			if err != nil {
				return
			}
			mu.Lock()
			streams[s.StreamID()] = s
			mu.Unlock()
		}
	}()
	return func(id uint32) net.Conn {
		var c net.Conn
		handoffWaitFor(func() bool {
			mu.Lock()
			defer mu.Unlock()
			c = streams[id]
			return c != nil
		}, 5*time.Second)
		return c
	}
}

func handoffCatPaneNamed(t *testing.T, name string) *Pane {
	t.Helper()
	p, err := NewPane(PaneConfig{Name: name, Command: []string{"/bin/cat"}, NoBracketedPaste: true}, 24, 80)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	p.Start()
	t.Cleanup(p.Close)
	return p
}

// AC 3: a hot-added agent reaches an attached second window, and a removed
// one leaves it; a send to the removed agent from the second window errors.
func TestWindowHandoff_AddedAndRemovedAgentsReachSecondWindows(t *testing.T) {
	origStable, origCap := startupSettleStable, startupSettleCap
	startupSettleStable, startupSettleCap = 200*time.Millisecond, 3*time.Second
	t.Cleanup(func() { startupSettleStable, startupSettleCap = origStable, origCap })

	tui, ws := handoffTUI(t, handoffCatPane(t))
	session, ctrl, _ := dialWindow(t, ws.Addr(), "window2")
	defer session.Close()
	defer ctrl.Close()
	sc := NewIPCScanner(ctrl)
	streamByID := acceptStreams(t, session)

	added := handoffCatPaneNamed(t, "eng2")
	tui.insertPane(added)
	if ws.daemon.currentPane("eng2") != added || !sinkOf(added) {
		t.Fatal("a hot-added agent is not served to second windows")
	}
	id, ok := readControl(t, ctrl, sc, func(a, n string) bool { return a == "stream_added" && n == "eng2" })
	if !ok {
		t.Fatal("the attached second window was never told about the added agent")
	}
	// Keys typed in the second window travel on the agent's stream, not the
	// control stream a send uses: both must reach the added agent.
	stream := streamByID(id)
	if stream == nil {
		t.Fatal("the stream announced for the added agent never arrived")
	}
	if _, err := stream.Write([]byte("KEY813\n")); err != nil {
		t.Fatalf("type into the added agent's stream: %v", err)
	}
	if !handoffWaitFor(func() bool { return strings.Contains(emuText(added), "KEY813") }, 5*time.Second) {
		t.Fatal("keys typed in the second window never reached the added agent")
	}
	if errText := viewerSend(t, ws.Addr(), "eng2", "ADD813"); errText != "" {
		t.Fatalf("a send to the added agent from a second window failed: %s", errText)
	}
	if !handoffWaitFor(func() bool { return strings.Contains(emuText(added), "ADD813") }, 5*time.Second) {
		t.Fatal("the send never reached the added agent")
	}

	if err := tui.removePane("eng2"); err != nil {
		t.Fatalf("removePane: %v", err)
	}
	if ws.daemon.currentPane("eng2") != nil {
		t.Fatal("a removed agent is still served")
	}
	if _, ok := readControl(t, ctrl, sc, func(a, n string) bool { return a == "agent_removed" && n == "eng2" }); !ok {
		t.Fatal("the attached second window was never told the agent was removed")
	}
	if errText := viewerSend(t, ws.Addr(), "eng2", "x"); !strings.Contains(errText, "not found") {
		t.Fatalf("a send to the removed agent from a second window: %q, want not found", errText)
	}
}

// Feed it nothing: with no window server the list still updates, and a
// restart whose spawn fails leaves the server on the old pane.
func TestWindowHandoff_NoServerAndAFailedSpawn(t *testing.T) {
	old := handoffCatPane(t)
	tui := f6kgTUI(old)
	if err := tui.restartPane(old); err != nil {
		t.Fatalf("restart with no window server: %v", err)
	}
	if np, ok := tui.panes[0].(*Pane); !ok || np == old {
		t.Fatal("with no window server the list was not updated")
	} else {
		t.Cleanup(np.Close)
	}

	keep := handoffCatPane(t)
	tui2, ws := handoffTUI(t, keep)
	// A missing directory fails the spawn itself; a missing binary would not,
	// since the command runs under the login shell (see the f6kg test).
	keep.cfg.Dir = "/nonexistent/initech-813u-no-such-dir"
	if err := tui2.restartPane(keep); err == nil {
		t.Fatal("setup: a restart with a missing command succeeded")
	}
	if ws.daemon.currentPane("eng1") != keep || tui2.panes[0] != PaneView(keep) {
		t.Fatal("a failed spawn changed what is listed or served")
	}
}
