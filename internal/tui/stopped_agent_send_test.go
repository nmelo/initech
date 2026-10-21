package tui

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ini-2s7q: a send to an agent whose process is stopped or has exited is
// refused with the command that brings it back, and nothing is typed. Before
// this, the handler answered OK and logged "Message sent" for a pane with no
// process at all: after `initech stop`, the text went nowhere and the sender
// was told it was delivered.

// s7qSettledPane is a pane whose process started long enough ago that the
// startup gate is open, with its PTY replaced by a pipe so the test reads
// exactly what reached the child.
func s7qSettledPane(t *testing.T, name string) (*Pane, *os.File) {
	t.Helper()
	p, r := i35wFreshPane(t, name)
	p.mu.Lock()
	p.startedAt = time.Now().Add(-2 * startupGateWindow)
	p.mu.Unlock()
	return p, r
}

// s7qStopped is a pane after `initech stop`: the stop handler calls Close.
func s7qStopped(t *testing.T, name string) (*Pane, *os.File) {
	t.Helper()
	p, r := s7qSettledPane(t, name)
	p.Close()
	return p, r
}

// s7qExited is a pane whose process ended on its own: readLoop saw EOF and
// set alive=false; nothing closed the PTY.
func s7qExited(t *testing.T, name string) (*Pane, *os.File) {
	t.Helper()
	p, r := s7qSettledPane(t, name)
	p.mu.Lock()
	p.alive = false
	p.mu.Unlock()
	return p, r
}

// s7qWritten closes the pipe's write end (if Close has not) and returns what
// reached the child.
func s7qWritten(t *testing.T, p *Pane, r *os.File) string {
	t.Helper()
	if p.ptmx != nil {
		p.ptmx.Close()
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func s7qLog(t *testing.T) func() string {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(InitLogger(root, slog.LevelInfo))
	return func() string {
		b, _ := os.ReadFile(filepath.Join(root, ".initech", logFileName))
		return string(b)
	}
}

func s7qSend(t *testing.T, tui *TUI, target, text string) IPCResponse {
	t.Helper()
	return i35wIPC(t, func(c net.Conn) {
		tui.handleIPCSend(c, IPCRequest{Target: target, Text: text, Enter: true})
	})
}

func s7qSendReady(t *testing.T, tui *TUI, target string) IPCResponse {
	t.Helper()
	return i35wIPC(t, func(c net.Conn) { tui.handleIPCSendReady(c, IPCRequest{Target: target}) })
}

// Criterion 1: stopped. Refused naming the agent and the start command, the
// refusal logged, nothing typed, and no "Message sent".
func TestStoppedAgent_ASendIsRefusedWithTheStartCommand(t *testing.T) {
	log := s7qLog(t)
	p, r := s7qStopped(t, "eng1")
	tui := &TUI{panes: toPaneViews([]*Pane{p})}
	resp := s7qSend(t, tui, "eng1", "MARKER_2S7Q_STOPPED")
	if resp.OK || !strings.Contains(resp.Error, "eng1 is stopped") || !strings.Contains(resp.Error, "initech start eng1") {
		t.Fatalf("send to a stopped agent: %+v, want a refusal naming eng1 and `initech start eng1`", resp)
	}
	if got := s7qWritten(t, p, r); got != "" {
		t.Fatalf("a refused send still wrote %q to the child", got)
	}
	l := log()
	if !strings.Contains(l, "SEND NOT DELIVERED") || !strings.Contains(l, "eng1 is stopped") {
		t.Fatalf("the refusal is not in the log:\n%s", l)
	}
	if strings.Contains(l, "Message sent") {
		t.Fatalf("a refused send was logged as sent:\n%s", l)
	}
}

// Criterion 1: exited on its own. Refused with the restart command.
func TestStoppedAgent_ASendToAnExitedAgentIsRefusedWithTheRestartCommand(t *testing.T) {
	p, r := s7qExited(t, "eng1")
	tui := &TUI{panes: toPaneViews([]*Pane{p})}
	resp := s7qSend(t, tui, "eng1", "MARKER_2S7Q_EXITED")
	if resp.OK || !strings.Contains(resp.Error, "eng1 has exited") || !strings.Contains(resp.Error, "initech restart eng1") {
		t.Fatalf("send to an exited agent: %+v, want a refusal naming eng1 and `initech restart eng1`", resp)
	}
	if got := s7qWritten(t, p, r); got != "" {
		t.Fatalf("a refused send still wrote %q to the child", got)
	}
}

// The twin: a live agent, the same send, is delivered.
func TestStoppedAgent_ALiveAgentStillGetsTheMessage(t *testing.T) {
	p, r := s7qSettledPane(t, "eng1")
	tui := &TUI{panes: toPaneViews([]*Pane{p})}
	if resp := s7qSend(t, tui, "eng1", "MARKER_2S7Q_LIVE"); !resp.OK {
		t.Fatalf("send to a live agent: %+v, want OK", resp)
	}
	if got := s7qWritten(t, p, r); !strings.Contains(got, "MARKER_2S7Q_LIVE") {
		t.Fatalf("a live agent got %q", got)
	}
}

// Criterion 5: stopped, then started again (start replaces the pane): the
// next send is delivered as normal.
func TestStoppedAgent_StartedAgainIsDeliveredAsNormal(t *testing.T) {
	old, _ := s7qStopped(t, "eng1")
	tui := &TUI{panes: toPaneViews([]*Pane{old})}
	if resp := s7qSend(t, tui, "eng1", "BEFORE_START"); resp.OK {
		t.Fatalf("stopped: %+v, want refused", resp)
	}
	fresh, r := s7qSettledPane(t, "eng1")
	tui.panes = toPaneViews([]*Pane{fresh})
	if resp := s7qSend(t, tui, "eng1", "MARKER_2S7Q_STARTED"); !resp.OK {
		t.Fatalf("started again: %+v, want OK", resp)
	}
	if got := s7qWritten(t, fresh, r); !strings.Contains(got, "MARKER_2S7Q_STARTED") {
		t.Fatalf("the started agent got %q", got)
	}
}

// Feed it nothing: an unknown agent, empty text to a stopped agent, and a
// pane mid-restart (the old one closed, the new one not yet in the list) each
// end in an error, never OK.
func TestStoppedAgent_NothingToDeliverToIsNeverOK(t *testing.T) {
	p, _ := s7qStopped(t, "eng1")
	tui := &TUI{panes: toPaneViews([]*Pane{p})}
	for name, c := range map[string]struct{ target, text string }{
		"unknown agent":           {"nobody", "hello"},
		"empty text, stopped":     {"eng1", ""},
		"mid-restart, old closed": {"eng1", "hello"},
	} {
		if resp := s7qSend(t, tui, c.target, c.text); resp.OK || resp.Error == "" {
			t.Errorf("%s: %+v, want an error reply", name, resp)
		}
	}
}

// Criterion 3: send_ready answers not ready, with the reason, for a stopped
// or exited agent; a live one is unchanged.
func TestStoppedAgent_SendReadySaysNotReady(t *testing.T) {
	stopped, _ := s7qStopped(t, "eng1")
	exited, _ := s7qExited(t, "eng2")
	live, _ := s7qSettledPane(t, "eng3")
	tui := &TUI{panes: toPaneViews([]*Pane{stopped, exited, live})}
	if resp := s7qSendReady(t, tui, "eng1"); resp.OK || !strings.Contains(resp.Error, "initech start eng1") {
		t.Errorf("send_ready, stopped: %+v, want not ready with the start command", resp)
	}
	if resp := s7qSendReady(t, tui, "eng2"); resp.OK || !strings.Contains(resp.Error, "initech restart eng2") {
		t.Errorf("send_ready, exited: %+v, want not ready with the restart command", resp)
	}
	if resp := s7qSendReady(t, tui, "eng3"); !resp.OK {
		t.Errorf("send_ready, live: %+v, want OK", resp)
	}
}

// The nil-PTY return in sendPaneTextLocked is never silent again.
func TestStoppedAgent_ASendWithNoPTYLogsAWarning(t *testing.T) {
	log := s7qLog(t)
	p := newEmuPane("eng1", 80, 24)
	p.sendMu.Lock()
	sendPaneTextLocked(p, "MARKER_2S7Q_NOPTY", true)
	p.sendMu.Unlock()
	l := log()
	if !strings.Contains(l, "level=WARN") || !strings.Contains(l, "no PTY") || !strings.Contains(l, "eng1") {
		t.Fatalf("a send dropped for want of a PTY left no warning:\n%s", l)
	}
}

// Criterion 4, and the operator's rule: a suspended agent is woken, never
// refused as stopped, even though suspension closes its process. Both shapes:
// suspended by the real primitive, and parked at launch by the spawn stagger.
// The respawn is refused by the test's spawner, so no process starts; that
// the spawner was asked at all proves the send took the queue-and-wake path.
func TestStoppedAgent_ASuspendedAgentIsWokenNotRefused(t *testing.T) {
	suspended, _ := s7qSettledPane(t, "eng1")
	parkPaneSuspended(suspended)
	parked := NewParkedPane(PaneConfig{Name: "eng2"}, 24, 80)
	for name, p := range map[string]*Pane{"suspended": suspended, "parked by the stagger": parked} {
		spawns := 0
		tui := &TUI{panes: toPaneViews([]*Pane{p})}
		tui.paneSpawner = func(PaneConfig, int, int) (*Pane, error) {
			spawns++
			return nil, errors.New("spawn refused by the test")
		}
		if resp := s7qSendReady(t, tui, p.name); !resp.OK {
			t.Errorf("%s: send_ready %+v, want ready (a send wakes it)", name, resp)
		}
		resp := s7qSend(t, tui, p.name, "MARKER_2S7Q_WAKE")
		if strings.HasPrefix(resp.Error, ErrPaneNotRunning.Error()) {
			t.Errorf("%s: refused as not running: %q", name, resp.Error)
		}
		if spawns != 1 {
			t.Errorf("%s: the wake asked for %d spawn(s), want 1", name, spawns)
		}
	}
}
