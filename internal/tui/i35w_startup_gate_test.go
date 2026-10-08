package tui

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// ini-i35w: a send to an agent that has not settled since its process started
// waits for it, by the ini-hbj4 quiescence rule. A sender-facing entry point
// that is still waiting at the cap refuses and writes nothing; every other
// caller delivers anyway, because it may already have popped the message.

// i35wGateClock shortens the gate for a test and restores it.
func i35wGateClock(t *testing.T, stable, cap time.Duration) {
	t.Helper()
	oldStable, oldCap := startupSettleStable, startupSettleCap
	startupSettleStable, startupSettleCap = stable, cap
	t.Cleanup(func() { startupSettleStable, startupSettleCap = oldStable, oldCap })
}

// i35wFreshPane is a pane whose process "just started", with its PTY replaced
// by a pipe so the test reads exactly what reached the child.
func i35wFreshPane(t *testing.T, name string) (*Pane, *os.File) {
	t.Helper()
	p := newEmuPane(name, 80, 24)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	p.ptmx = &filePty{w}
	p.cfg.NoBracketedPaste = true
	p.mu.Lock()
	p.alive = true
	p.startedAt = time.Now()
	p.lastOutputTime = time.Now()
	p.mu.Unlock()
	return p, r
}

// i35wKeepTalking keeps the child "producing output" until the test ends, the
// way a Claude rendering its --continue transcript does.
func i35wKeepTalking(t *testing.T, p *Pane) {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				p.mu.Lock()
				p.lastOutputTime = time.Now()
				p.mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() { close(stop); <-done })
}

func i35wDrain(t *testing.T, p *Pane, r *os.File) string {
	t.Helper()
	p.ptmx.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func i35wIPC(t *testing.T, handle func(net.Conn)) IPCResponse {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() { defer close(done); handle(server) }()
	scanner := bufio.NewScanner(client)
	scanner.Scan()
	var resp IPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("response %q: %v", scanner.Text(), err)
	}
	<-done
	return resp
}

func TestStartupGate_WaitsForAFreshAgentToGoQuietThenOpens(t *testing.T) {
	i35wGateClock(t, 100*time.Millisecond, 3*time.Second)
	p, _ := i35wFreshPane(t, "eng4")
	stop := make(chan struct{})
	go func() {
		deadline := time.After(300 * time.Millisecond)
		for {
			select {
			case <-deadline:
				close(stop)
				return
			case <-time.After(20 * time.Millisecond):
				p.mu.Lock()
				p.lastOutputTime = time.Now()
				p.mu.Unlock()
			}
		}
	}()
	start := time.Now()
	if err := p.awaitStartupSettled(); err != nil {
		t.Fatalf("a child that went quiet was refused: %v", err)
	}
	<-stop
	if waited := time.Since(start); waited < 300*time.Millisecond {
		t.Fatalf("returned after %s, before the child stopped talking", waited)
	}
}

func TestStartupGate_OpensOnceSoABusyAgentIsNeverDelayed(t *testing.T) {
	i35wGateClock(t, 50*time.Millisecond, 3*time.Second)
	p, _ := i35wFreshPane(t, "eng4")
	if err := p.awaitStartupSettled(); err != nil {
		t.Fatalf("settle: %v", err)
	}
	i35wKeepTalking(t, p) // now mid-turn: output never stops
	start := time.Now()
	if err := p.awaitStartupSettled(); err != nil {
		t.Fatalf("a settled agent mid-turn was refused: %v", err)
	}
	if waited := time.Since(start); waited > 40*time.Millisecond {
		t.Fatalf("a settled agent mid-turn waited %s; the gate must open once, for good", waited)
	}
}

func TestStartupGate_AnAgentPastItsStartupWindowIsNotWaitedFor(t *testing.T) {
	i35wGateClock(t, 50*time.Millisecond, 3*time.Second)
	p, _ := i35wFreshPane(t, "eng4")
	p.mu.Lock()
	p.startedAt = time.Now().Add(-2 * startupGateWindow)
	p.mu.Unlock()
	i35wKeepTalking(t, p)
	start := time.Now()
	if err := p.awaitStartupSettled(); err != nil {
		t.Fatalf("a long-running busy agent was refused: %v", err)
	}
	if waited := time.Since(start); waited > 40*time.Millisecond {
		t.Fatalf("a long-running busy agent waited %s", waited)
	}
}

func TestStartupGate_AnAgentThatNeverSettlesIsReportedStillStarting(t *testing.T) {
	i35wGateClock(t, 100*time.Millisecond, 400*time.Millisecond)
	p, _ := i35wFreshPane(t, "eng4")
	i35wKeepTalking(t, p)
	err := p.awaitStartupSettled()
	if !errors.Is(err, ErrPaneStillStarting) {
		t.Fatalf("err = %v, want ErrPaneStillStarting", err)
	}
	if !strings.Contains(err.Error(), "eng4") {
		t.Fatalf("refusal does not name the agent: %v", err)
	}
}

// The incident, end to end at the IPC: a send to a booting agent is refused
// and NOTHING reaches the child, instead of "delivered" over a PTY that eats it.
func TestHandleIPCSend_RefusesAnAgentStillStartingAndWritesNothing(t *testing.T) {
	i35wGateClock(t, 100*time.Millisecond, 400*time.Millisecond)
	p, r := i35wFreshPane(t, "eng4")
	i35wKeepTalking(t, p)
	tui := &TUI{panes: toPaneViews([]*Pane{p})}
	resp := i35wIPC(t, func(c net.Conn) {
		tui.handleIPCSend(c, IPCRequest{Target: "eng4", Text: "MARKER_I35W", Enter: true})
	})
	if resp.OK || !strings.HasPrefix(resp.Error, ErrPaneStillStarting.Error()) {
		t.Fatalf("send to a booting agent: %+v, want a still-starting refusal", resp)
	}
	if got := i35wDrain(t, p, r); got != "" {
		t.Fatalf("a refused send still wrote %q to the child", got)
	}
}

// The hbj4 contract: a caller that may already have popped the message
// (a queue drain) is never made to drop it. At the cap it is delivered.
func TestStartupGate_SendTextDeliversAnywayWhenTheCapExpires(t *testing.T) {
	i35wGateClock(t, 100*time.Millisecond, 300*time.Millisecond)
	p, r := i35wFreshPane(t, "eng4")
	i35wKeepTalking(t, p)
	p.SendText("MARKER_I35W_DRAIN", false)
	if got := i35wDrain(t, p, r); !strings.Contains(got, "MARKER_I35W_DRAIN") {
		t.Fatalf("SendText dropped the text at the cap; wrote %q", got)
	}
}

func TestHandleIPCSendReady_AnswersStillStartingThenReady(t *testing.T) {
	i35wGateClock(t, 100*time.Millisecond, 300*time.Millisecond)
	p, _ := i35wFreshPane(t, "eng4")
	i35wKeepTalking(t, p)
	tui := &TUI{panes: toPaneViews([]*Pane{p})}
	ready := func() IPCResponse {
		return i35wIPC(t, func(c net.Conn) { tui.handleIPCSendReady(c, IPCRequest{Action: "send_ready", Target: "eng4"}) })
	}
	if resp := ready(); resp.OK || !strings.HasPrefix(resp.Error, ErrPaneStillStarting.Error()) {
		t.Fatalf("booting agent: %+v, want a still-starting answer", resp)
	}
	p.mu.Lock()
	p.startupSettled = true
	p.mu.Unlock()
	if resp := ready(); !resp.OK {
		t.Fatalf("settled agent: %+v, want OK", resp)
	}
}

func TestHandleIPCStart_RefusesASuspendedAgentAndSpawnsNothing(t *testing.T) {
	p := testPane("eng4")
	p.mu.Lock()
	p.suspended = true
	p.alive = false
	p.mu.Unlock()
	tui := &TUI{panes: toPaneViews([]*Pane{p})}
	resp := i35wIPC(t, func(c net.Conn) { tui.handleIPCStart(c, IPCRequest{Action: "start", Target: "eng4"}) })
	if resp.OK || resp.Error != "eng4 is suspended — use initech resume eng4" {
		t.Fatalf("start on a suspended agent: %+v", resp)
	}
	if tui.panes[0] != PaneView(p) {
		t.Fatal("start replaced a suspended agent's pane; its queue and beads went with it")
	}
}

func TestHandleIPCStart_ARunningAgentIsStillAlreadyRunning(t *testing.T) {
	p := testPane("eng4") // alive, not suspended
	tui := &TUI{panes: toPaneViews([]*Pane{p})}
	resp := i35wIPC(t, func(c net.Conn) { tui.handleIPCStart(c, IPCRequest{Action: "start", Target: "eng4"}) })
	if !resp.OK || resp.Data != "already running" {
		t.Fatalf("start on a running agent: %+v", resp)
	}
}
