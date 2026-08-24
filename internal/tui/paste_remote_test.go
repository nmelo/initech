package tui

// Pasting into a viewer-window pane (ini-a9d8).
//
// handlePaste type-asserted the focused pane to *Pane and returned when that
// failed, with the comment "Remote panes don't support paste flush (no local
// ptmx)". Every pane in a viewer window is a *RemotePane, so the whole
// buffered paste was discarded: no delivery, no error, no log line. Typing
// worked, because handleKey goes through SendKey and forwards over IPC -- so
// the operator saw a window where keys work and paste does nothing.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// pasteRecorder is a PaneView that records what it was asked to flush.
//
// The interface is EMBEDDED rather than implemented: every method this test
// does not set is nil and panics if called, so the stand-in can never do
// something the real type cannot. An over-capable fake certifies capabilities
// nobody has (ini-9isx).
type pasteRecorder struct {
	PaneView
	name string
	host string
	got  []byte
	n    int
}

func (r *pasteRecorder) Name() string { return r.name }
func (r *pasteRecorder) Host() string { return r.host }
func (r *pasteRecorder) FlushPaste(content []byte) {
	r.got = append([]byte(nil), content...)
	r.n++
}

func pasteTUI(t *testing.T, pv PaneView) *TUI {
	t.Helper()
	tui := &TUI{panes: []PaneView{pv}, quitCh: make(chan struct{})}
	tui.layoutState.Focused = agentKey(pv)
	return tui
}

// TestPaste_ReachesAViewerWindowPane is the bug. It fails on the type
// assertion: a pane that is not a *Pane received nothing at all.
func TestPaste_ReachesAViewerWindowPane(t *testing.T) {
	rec := &pasteRecorder{name: "eng1", host: "macbook"} // non-empty host => viewer pane
	tui := pasteTUI(t, rec)

	body := "first line\nsecond line\nthird line"
	tui.handlePaste(true)
	tui.pasteBuf = append(tui.pasteBuf, body...)
	tui.handlePaste(false)

	if rec.n == 0 {
		t.Fatal("the paste never reached the pane.\n\n" +
			"Every pane in a viewer window is a *RemotePane, so the whole buffer is " +
			"discarded with no delivery, no error and no log line -- the operator sees " +
			"a window where typing works and paste silently does nothing.")
	}
	if string(rec.got) != body {
		t.Errorf("flushed %q, want %q", rec.got, body)
	}
}

// TestPaste_DeliversTheBufferExactly guards the bytes, not just the call: a
// paste that arrives mangled is a different bug wearing this one's fix.
func TestPaste_DeliversTheBufferExactly(t *testing.T) {
	rec := &pasteRecorder{name: "eng1", host: "macbook"}
	tui := pasteTUI(t, rec)

	body := "tabs\tand \"quotes\" and unicode ✅ and a trailing newline\n"
	tui.handlePaste(true)
	tui.pasteBuf = append(tui.pasteBuf, body...)
	tui.handlePaste(false)

	if string(rec.got) != body {
		t.Errorf("flushed %q, want %q", rec.got, body)
	}
}

// TestPaste_LocalPaneStillFlushesThroughTheSameCallSite is the control: the
// contract change must not move local paste onto a different path.
func TestPaste_LocalPaneStillFlushesThroughTheSameCallSite(t *testing.T) {
	rec := &pasteRecorder{name: "eng1"} // no host => local
	tui := pasteTUI(t, rec)

	tui.handlePaste(true)
	tui.pasteBuf = append(tui.pasteBuf, "local body"...)
	tui.handlePaste(false)

	if rec.n != 1 || string(rec.got) != "local body" {
		t.Errorf("local paste flushed %d time(s) with %q; want 1 and %q",
			rec.n, rec.got, "local body")
	}
}

// TestPaste_ModalDropIsUnchanged pins the deliberate drop above this code so
// the contract change does not quietly widen it.
func TestPaste_ModalDropIsUnchanged(t *testing.T) {
	rec := &pasteRecorder{name: "eng1", host: "macbook"}
	tui := pasteTUI(t, rec)
	tui.help.active = true

	tui.handlePaste(true)
	tui.pasteBuf = append(tui.pasteBuf, "into a modal"...)
	tui.handlePaste(false)

	if rec.n != 0 {
		t.Errorf("paste reached the pane with a TUI modal open (%q); modals expect "+
			"typed input and that drop is deliberate", rec.got)
	}
}

// --- the wire, and the two behaviours the AC said to verify rather than assume ---

// pasteMuxServer stands in for the daemon: it reads one control command and
// answers with respErr (empty means OK), handing the command back to the test.
func pasteMuxServer(t *testing.T, respErr string) (*ControlMux, <-chan ControlCmd) {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close(); client.Close() })
	got := make(chan ControlCmd, 1)

	go func() {
		scanner := bufio.NewScanner(server)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for scanner.Scan() {
			var cmd ControlCmd
			if json.Unmarshal(scanner.Bytes(), &cmd) != nil || cmd.ID == "" {
				continue
			}
			got <- cmd
			resp, _ := json.Marshal(ControlResp{ID: cmd.ID, OK: respErr == "", Error: respErr})
			server.Write(resp)
			server.Write([]byte("\n"))
			return
		}
	}()
	return NewControlMux(client), got
}

// TestPaste_RemotePaneSendsTheBodyUnsubmitted pins the wire contract: the exact
// bytes, and Enter FALSE. Enter true here would submit the operator's paste for
// them -- and would also arm ini-vpwg's belt, which this path must stay clear
// of by construction.
func TestPaste_RemotePaneSendsTheBodyUnsubmitted(t *testing.T) {
	mux, got := pasteMuxServer(t, "")
	rp := &RemotePane{name: "eng1", host: "macbook", mux: mux}

	body := "line one\nline two"
	rp.FlushPaste([]byte(body))

	select {
	case cmd := <-got:
		if cmd.Text != body {
			t.Errorf("wire carried %q, want %q", cmd.Text, body)
		}
		if cmd.Enter {
			t.Error("Enter is TRUE on a paste: the operator's text would be submitted for " +
				"them, and it would arm the never-submit belt on a path that must never " +
				"reach it")
		}
		if cmd.Action != "send" || cmd.Target != "eng1" {
			t.Errorf("addressed %q/%q, want send/eng1", cmd.Action, cmd.Target)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing reached the wire")
	}
}

// TestPaste_AnEmptyBufferIsNotSentAtAll keeps the absence assertion above
// honest: the instrument sees a command when one is sent, so "no command"
// means something.
func TestPaste_AnEmptyBufferIsNotSentAtAll(t *testing.T) {
	mux, got := pasteMuxServer(t, "")
	rp := &RemotePane{name: "eng1", host: "macbook", mux: mux}

	rp.FlushPaste(nil)
	select {
	case cmd := <-got:
		t.Fatalf("an empty paste was sent anyway: %+v", cmd)
	case <-time.After(300 * time.Millisecond):
	}

	// POSITIVE CONTROL.
	rp.FlushPaste([]byte("real body"))
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("instrument check failed: a real paste produced no command either, so " +
			"the absence above measured nothing")
	}
}

// TestPaste_ARejectedPasteIsNotSilent is the AC's last bullet. The daemon
// refuses an unknown target or a body over maxSendTextLen (64KB) with an error
// in the RESPONSE and a nil transport error -- so discarding the response, as
// SendText does, loses a large paste exactly as silently as the bug this fixes.
func TestPaste_ARejectedPasteIsNotSilent(t *testing.T) {
	mux, got := pasteMuxServer(t, "text too large (99999 bytes, max 65536)")
	rp := &RemotePane{name: "eng1", host: "macbook", mux: mux}

	logs := captureLogs(t)
	rp.FlushPaste([]byte("a body the daemon will refuse"))
	<-got
	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(logs.String(), "PASTE NOT DELIVERED")
	}, "a paste the daemon REFUSED left no record at default log level; a swallowed "+
		"paste is the same invisibility class as the bug being fixed")
}

// waitFor polls until cond holds, because FlushPaste is fire-and-forget: the
// log lands on a goroutine and a bare read races it.
func waitFor(t *testing.T, limit time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

// --- the stash, which is the divergence this fix had to make on purpose ---

// drainKeys collects everything the emulator emits for d.
//
// It consumes proofPane's existing drain channel rather than reading emu
// directly: two readers on one emulator would race for the same bytes, and the
// cell would flake on whichever won.
func drainKeys(ch <-chan []byte, d time.Duration) []byte {
	var out []byte
	deadline := time.After(d)
	for {
		select {
		case b := <-ch:
			out = append(out, b...)
		case <-deadline:
			return out
		}
	}
}

// TestPaste_NoStashWhenNothingWillBeSubmitted is the divergence, and the
// reason for it.
//
// sendPaneTextLocked stashes partially typed input (ctrl+S, ini-gd0) so an
// arriving message does not corrupt what the operator was composing, and
// Claude restores it AFTER A SUBMIT. A paste sends with enter=false and never
// submits, so an unconditional stash would take the operator's typed prefix
// away and never give it back: today they lose the paste, and inheriting the
// stash would have made this fix lose what they TYPED instead. Strictly worse
// for that operator, so the stash is conditional on enter.
func TestPaste_NoStashWhenNothingWillBeSubmitted(t *testing.T) {
	p, keys, _ := proofPane(t)

	p.sendMu.Lock()
	sendPaneTextLocked(p, "pasted body", false)
	p.sendMu.Unlock()

	if bytes.ContainsRune(drainKeys(keys, 400*time.Millisecond), 0x13) {
		t.Error("ctrl+S (stash) was sent on a paste that will never be submitted; the " +
			"operator's typed prefix is stashed with no submit to restore it, so they " +
			"lose what they typed instead of what they pasted")
	}
}

// TestPaste_AnActualMessageStillStashes is the positive control WITHOUT which
// the cell above proves nothing -- an instrument that never sees a stash
// cannot report its absence.
func TestPaste_AnActualMessageStillStashes(t *testing.T) {
	p, keys, _ := proofPane(t)

	p.sendMu.Lock()
	sendPaneTextLocked(p, "a real message", true)
	p.sendMu.Unlock()

	if !bytes.ContainsRune(drainKeys(keys, 600*time.Millisecond), 0x13) {
		t.Fatal("a submitting send did NOT stash: the ini-gd0 protection for text the " +
			"operator was composing is gone, and the absence assertion in the paste " +
			"cell above is measuring nothing")
	}
}
