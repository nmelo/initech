package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/tui"
)

// ini-sf5.1: initech compact <agent> [what to keep] sends /compact through
// the send path and reports the context figure dropping.

// sf5TUI is a fake TUI: list answers from figure(call), send answers with
// sendResp and records what was typed.
type sf5TUI struct {
	ln        net.Listener
	figure    func(call int) *int
	agentType string
	host      string // the listed pane's host, for a peer's agent
	listed    bool   // the agent is in the list
	sendResp  tui.IPCResponse
	lists     atomic.Int32
	mu        sync.Mutex
	sent      []string
}

func sf5Pct(n int) *int { return &n }

func startSf5TUI(t *testing.T, f *sf5TUI) {
	t.Helper()
	skipWindows(t)
	sock := filepath.Join("/tmp", fmt.Sprintf("initech-sf5-%d-%d.sock", os.Getpid(), fakeIPCCounter.Add(1)))
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
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
			var resp tui.IPCResponse
			switch req.Action {
			case "list":
				n := int(f.lists.Add(1))
				var panes []map[string]any
				if f.listed {
					pane := map[string]any{"name": "a", "activity": "idle", "alive": true, "visible": true}
					if f.agentType != "" {
						pane["agent_type"] = f.agentType
					}
					if f.host != "" {
						pane["host"] = f.host
					}
					if f.figure != nil {
						if pct := f.figure(n); pct != nil {
							pane["context_percent"] = *pct
						}
					}
					panes = append(panes, pane)
				}
				data, _ := json.Marshal(panes)
				resp = tui.IPCResponse{OK: true, Data: string(data)}
			case "send":
				f.mu.Lock()
				f.sent = append(f.sent, req.Text)
				f.mu.Unlock()
				resp = f.sendResp
			}
			data, _ := json.Marshal(resp)
			conn.Write(append(data, '\n'))
			conn.Close()
		}
	}()
}

func (f *sf5TUI) typed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.sent...)
}

func sf5Window(t *testing.T, window, poll time.Duration) {
	t.Helper()
	ow, op := compactWatchWindow, compactPoll
	compactWatchWindow, compactPoll = window, poll
	t.Cleanup(func() { compactWatchWindow, compactPoll = ow, op })
}

var sf5Delivered = tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered}

func sf5Run(target, text, self string) (string, error) {
	var out bytes.Buffer
	err := compactAgent(&out, target, "", text, self)
	return out.String(), err
}

// AC1: delivered, then the figure drops: the drop is reported, exit 0, and
// /compact with the keep text verbatim is what was typed.
func TestCompact_ReportsTheDrop(t *testing.T) {
	sf5Window(t, 2*time.Second, 10*time.Millisecond)
	f := &sf5TUI{listed: true, agentType: "claude-code", sendResp: sf5Delivered,
		figure: func(n int) *int {
			if n < 4 {
				return sf5Pct(62)
			}
			return sf5Pct(9)
		}}
	startSf5TUI(t, f)
	out, err := sf5Run("a", "/compact keep the bead id", "")
	if err != nil {
		t.Fatalf("err %v (out %q)", err, out)
	}
	if !strings.Contains(out, "delivered to a\n") || !strings.HasSuffix(out, "compacted: 62% -> 9%\n") {
		t.Errorf("printed %q", out)
	}
	if got := f.typed(); len(got) != 1 || got[0] != "/compact keep the bead id" {
		t.Errorf("typed %q", got)
	}
}

// The command line: the words after the agent are passed after "/compact ",
// joined with single spaces; none gives /compact alone (AC5).
func TestCompact_TheTextTyped(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"keep text":    {[]string{"a", "keep", "the bead id"}, "/compact keep the bead id"},
		"verbatim":     {[]string{"a", "ini-sf5.1:", "\"quotes\" & $dollars"}, "/compact ini-sf5.1: \"quotes\" & $dollars"},
		"no keep text": {[]string{"a"}, "/compact"},
	} {
		t.Run(name, func(t *testing.T) {
			sf5Window(t, 50*time.Millisecond, 10*time.Millisecond)
			f := &sf5TUI{listed: true, agentType: "claude-code", sendResp: sf5Delivered, figure: func(int) *int { return sf5Pct(40) }}
			startSf5TUI(t, f)
			t.Setenv("INITECH_AGENT", "")
			cmd := compactCmd
			cmd.SetErr(&bytes.Buffer{})
			_ = runCompact(cmd, c.args)
			if got := f.typed(); len(got) != 1 || got[0] != c.want {
				t.Errorf("typed %q, want %q", got, c.want)
			}
		})
	}
}

// AC2: from the agent's own pane it returns as soon as the send is
// delivered, with the queued line, exit 0, and never polls for a drop.
func TestCompact_FromItsOwnPaneReturnsAtOnce(t *testing.T) {
	sf5Window(t, 5*time.Second, 10*time.Millisecond)
	f := &sf5TUI{listed: true, agentType: "claude-code", sendResp: sf5Delivered, figure: func(int) *int { return sf5Pct(70) }}
	startSf5TUI(t, f)
	start := time.Now()
	out, err := sf5Run("a", "/compact", "a")
	if err != nil || !strings.HasSuffix(out, compactQueuedLine+"\n") {
		t.Fatalf("err %v, printed %q", err, out)
	}
	if time.Since(start) > time.Second || f.lists.Load() != 1 {
		t.Errorf("took %v with %d list reads: it waited for a drop", time.Since(start), f.lists.Load())
	}
	if len(f.typed()) != 1 {
		t.Errorf("typed %q", f.typed())
	}
	// The twin: another agent's pane watches.
	f2 := &sf5TUI{listed: true, agentType: "claude-code", sendResp: sf5Delivered, figure: func(int) *int { return sf5Pct(70) }}
	startSf5TUI(t, f2)
	sf5Window(t, 100*time.Millisecond, 10*time.Millisecond)
	if _, err := sf5Run("a", "/compact", "b"); v69Exit(err) != 2 {
		t.Errorf("from another pane, exit %d (%v), want the watch's 2", v69Exit(err), err)
	}
	// A peer's agent with the caller's own name is not the caller's pane.
	f3 := &sf5TUI{listed: true, agentType: "claude-code", sendResp: sf5Delivered}
	startSf5TUI(t, f3)
	var out3 bytes.Buffer
	if err := compactAgent(&out3, "a", "peer", "/compact", "a"); v69Exit(err) != 2 || strings.Contains(out3.String(), compactQueuedLine) {
		t.Errorf("peer:a from a's own pane: exit %d (%v), printed %q; want no queued line", v69Exit(err), err, out3.String())
	}
}

// AC3: no drop in the window: the sent line, exit 2. The figure going up or
// staying put is not a drop.
func TestCompact_NoDropInTheWindow(t *testing.T) {
	for name, fig := range map[string]func(int) *int{
		"flat":           func(int) *int { return sf5Pct(55) },
		"rising":         func(n int) *int { return sf5Pct(55 + n) },
		"unread mid-way": func(n int) *int { return map[bool]*int{true: sf5Pct(55), false: nil}[n == 1] },
	} {
		t.Run(name, func(t *testing.T) {
			sf5Window(t, 150*time.Millisecond, 10*time.Millisecond)
			startSf5TUI(t, &sf5TUI{listed: true, agentType: "claude-code", sendResp: sf5Delivered, figure: fig})
			_, err := sf5Run("a", "/compact", "")
			if v69Exit(err) != 2 || err.Error() != compactNoDropLine {
				t.Errorf("exit %d, %v; want 2 and %q", v69Exit(err), err, compactNoDropLine)
			}
		})
	}
}

// AC3: the figure unread before the send (a narrow pane): sent, the unknown
// line, exit 2, no watch.
func TestCompact_UnknownFigure(t *testing.T) {
	sf5Window(t, 5*time.Second, 10*time.Millisecond)
	f := &sf5TUI{listed: true, agentType: "claude-code", sendResp: sf5Delivered}
	startSf5TUI(t, f)
	start := time.Now()
	_, err := sf5Run("a", "/compact", "")
	if v69Exit(err) != 2 || err.Error() != compactUnknownLine {
		t.Fatalf("exit %d, %v; want 2 and %q", v69Exit(err), err, compactUnknownLine)
	}
	if len(f.typed()) != 1 || time.Since(start) > time.Second {
		t.Errorf("typed %q, took %v", f.typed(), time.Since(start))
	}
}

// AC3: a send that is not delivered is the send's own answer, words and
// exit code, and nothing is watched.
func TestCompact_ARefusedSendIsTheSendsAnswer(t *testing.T) {
	for _, c := range []struct {
		name string
		resp tui.IPCResponse
		line string
		exit int
	}{
		{"held for a dialog", tui.IPCResponse{OK: true, Delivery: tui.DeliveryDeferred}, "deferred to a — target has a modal open; will deliver when it closes\n", 0},
		{"do-not-disturb", tui.IPCResponse{OK: true, Delivery: tui.DeliveryHeld, Data: "held: a is in do-not-disturb (1m, 0 messages ahead)"}, "held: a is in do-not-disturb (1m, 0 messages ahead)\n", 0},
		{"queue full", tui.IPCResponse{Error: "not delivered to a: do-not-disturb queue is full", Delivery: tui.DeliveryNotDelivered}, "not delivered to a: do-not-disturb queue is full", 1},
		{"could not confirm", tui.IPCResponse{OK: true, Delivery: tui.DeliveryUnconfirmed, Data: "no composer visible"}, "could not confirm delivery to a: no composer visible; peek the pane", 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			sf5Window(t, 5*time.Second, 10*time.Millisecond)
			f := &sf5TUI{listed: true, agentType: "claude-code", sendResp: c.resp, figure: func(int) *int { return sf5Pct(60) }}
			startSf5TUI(t, f)
			out, err := sf5Run("a", "/compact", "")
			if v69Exit(err) != c.exit {
				t.Errorf("exit %d (%v), want %d", v69Exit(err), err, c.exit)
			}
			got := out
			if err != nil {
				got = err.Error()
			}
			if got != c.line {
				t.Errorf("printed %q, want %q", got, c.line)
			}
			if f.lists.Load() != 1 {
				t.Errorf("%d list reads: it watched after a send that was not delivered", f.lists.Load())
			}
		})
	}
}

// AC4: Codex, OpenCode and generic agents are refused, exit 1, nothing typed.
func TestCompact_NonClaudeAgentsAreRefused(t *testing.T) {
	for _, typ := range []string{"codex", "opencode", "generic"} {
		t.Run(typ, func(t *testing.T) {
			// A short window and no initech.yaml above: a refusal that fails
			// fails here, on the assertion, not on the suite's timeout.
			t.Chdir(t.TempDir())
			sf5Window(t, 50*time.Millisecond, 10*time.Millisecond)
			f := &sf5TUI{listed: true, agentType: typ, sendResp: sf5Delivered, figure: func(int) *int { return sf5Pct(60) }}
			startSf5TUI(t, f)
			_, err := sf5Run("a", "/compact", "")
			if v69Exit(err) != 1 || err == nil || !strings.Contains(err.Error(), "not compacted: a is a "+typ+" agent") {
				t.Errorf("exit %d, %v", v69Exit(err), err)
			}
			if len(f.typed()) != 0 {
				t.Errorf("typed %q into a %s agent", f.typed(), typ)
			}
		})
	}
	// A peer's agent: the list does not carry its type, so it is refused
	// rather than guessed; its twin, a peer's agent the list does not have,
	// is the send's own answer.
	t.Run("a peer's agent of unknown type", func(t *testing.T) {
		t.Chdir(t.TempDir())
		sf5Window(t, 50*time.Millisecond, 10*time.Millisecond)
		f := &sf5TUI{listed: true, host: "peer", sendResp: sf5Delivered, figure: func(int) *int { return sf5Pct(60) }}
		startSf5TUI(t, f)
		var out bytes.Buffer
		err := compactAgent(&out, "a", "peer", "/compact", "")
		if v69Exit(err) != 1 || err == nil || err.Error() != "not compacted: peer:a runs on another machine and its agent type is not known here" || len(f.typed()) != 0 {
			t.Errorf("exit %d, %v, typed %q", v69Exit(err), err, f.typed())
		}
		g := &sf5TUI{listed: true, host: "other", sendResp: tui.IPCResponse{Error: "unknown agent \"peer:a\""}}
		startSf5TUI(t, g)
		if err := compactAgent(&out, "a", "peer", "/compact", ""); err == nil || err.Error() != "unknown agent \"peer:a\"" {
			t.Errorf("a peer agent the list lacks: %v, want the send's error", err)
		}
	})
}

// An older TUI sends no agent type: the role's type in initech.yaml stands
// in, so a Codex role is still refused; with no config, the send goes ahead.
func TestCompact_AnOlderTUIFallsBackToTheConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := "project: p\nroot: " + dir + "\nroles: [a]\nrole_overrides:\n  a:\n    agent_type: codex\n"
	if err := os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	f := &sf5TUI{listed: true, sendResp: sf5Delivered, figure: func(int) *int { return sf5Pct(60) }}
	startSf5TUI(t, f)
	if _, err := sf5Run("a", "/compact", ""); v69Exit(err) != 1 || len(f.typed()) != 0 {
		t.Errorf("a codex role behind an older TUI: exit %d (%v), typed %q", v69Exit(err), err, f.typed())
	}
	t.Chdir(t.TempDir())
	sf5Window(t, 50*time.Millisecond, 10*time.Millisecond)
	g := &sf5TUI{listed: true, sendResp: sf5Delivered, figure: func(int) *int { return sf5Pct(60) }}
	startSf5TUI(t, g)
	_, _ = sf5Run("a", "/compact", "")
	if len(g.typed()) != 1 {
		t.Errorf("no config and an older TUI: typed %q, want the send", g.typed())
	} // A role the config gives no agent type is Claude Code: sent.
	dir2 := t.TempDir()
	cfg2 := "project: p\nroot: " + dir2 + "\nroles: [a, b]\nrole_overrides:\n  b:\n    agent_type: codex\n"
	if err := os.WriteFile(filepath.Join(dir2, "initech.yaml"), []byte(cfg2), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir2)
	h := &sf5TUI{listed: true, sendResp: sf5Delivered, figure: func(int) *int { return sf5Pct(60) }}
	startSf5TUI(t, h)
	_, _ = sf5Run("a", "/compact", "")
	if len(h.typed()) != 1 {
		t.Errorf("a role with no override behind an older TUI: typed %q, want the send", h.typed())
	}
}

// AC5: an agent the TUI does not have is the send's own error; the TUI going
// away mid-watch ends the window at once with the sent line, exit 2.
func TestCompact_NothingToWorkWith(t *testing.T) {
	t.Run("unknown agent", func(t *testing.T) {
		t.Chdir(t.TempDir())
		f := &sf5TUI{listed: false, sendResp: tui.IPCResponse{Error: "unknown agent \"zz\""}}
		startSf5TUI(t, f)
		_, err := sf5Run("zz", "/compact", "")
		if err == nil || err.Error() != "unknown agent \"zz\"" || v69Exit(err) != 1 {
			t.Errorf("err %v exit %d, want the send's error", err, v69Exit(err))
		}
	})
	t.Run("TUI gone mid-watch", func(t *testing.T) {
		sf5Window(t, 10*time.Second, 10*time.Millisecond)
		f := &sf5TUI{listed: true, agentType: "claude-code", sendResp: sf5Delivered}
		f.figure = func(n int) *int {
			if n >= 2 {
				f.ln.Close()
			}
			return sf5Pct(60)
		}
		startSf5TUI(t, f)
		start := time.Now()
		_, err := sf5Run("a", "/compact", "")
		if v69Exit(err) != 2 || err.Error() != compactNoDropLine || time.Since(start) > 2*time.Second {
			t.Errorf("exit %d, %v, after %v; want 2 and %q at once", v69Exit(err), err, time.Since(start), compactNoDropLine)
		}
	})
}

// The window is two minutes, as the bead rules.
func TestCompact_TheWindowIsTwoMinutes(t *testing.T) {
	if compactWatchWindow != 2*time.Minute {
		t.Errorf("window %v, want 2m", compactWatchWindow)
	}
}
