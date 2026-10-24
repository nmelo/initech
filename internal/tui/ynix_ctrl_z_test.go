package tui

import (
	"net"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-ynix: Ctrl+Z aimed at a pane is swallowed, so a slip while reaching for
// zoom (Alt+z) can never suspend or disturb the agent. Nothing is forwarded,
// written or woken. Every other key, Ctrl+C above all, passes unchanged.

// The shapes Ctrl+Z arrives in. tcell normalizes a kitty-style rune z with
// Ctrl to KeyCtrlZ (measured, tcell v2.13), so every Ctrl+Z is KeyCtrlZ, with
// or without the modifier bit. Ctrl+Shift+Z and Ctrl+Alt+z stay runes and do
// not encode to 0x1A; they are not swallowed (see the unchanged-keys test).
func ynixCtrlZ() map[string]*tcell.EventKey {
	return map[string]*tcell.EventKey{
		"KeyCtrlZ":          tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModCtrl),
		"KeyCtrlZ, no mods": tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone),
		"rune z with Ctrl":  tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModCtrl),
	}
}

// ynixLocal is a TUI whose one focused pane is a local pane; the channel
// carries the bytes the emulator encodes for keys, which go to the PTY.
func ynixLocal(t *testing.T) (*TUI, <-chan []byte) {
	t.Helper()
	p, keys, _ := proofPane(t)
	p.visible = true
	tui := newTestTUI(p)
	tui.layoutState.Focused = p.name
	tui.applyLayout()
	if tui.inputPane() == nil {
		t.Fatal("precondition: the pane is the input pane")
	}
	return tui, keys
}

// ynixRemote is a TUI whose one focused pane is a RemotePane; the channel
// carries what reaches its stream.
func ynixRemote(t *testing.T) (*TUI, <-chan []byte) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	got := make(chan []byte, 16)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := server.Read(buf)
			if n > 0 {
				got <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	rp := NewRemotePane("eng1", "workbench", client, nil, 80, 24)
	tui := &TUI{panes: []PaneView{rp}, quitCh: make(chan struct{})}
	tui.layoutState = DefaultLayoutState([]string{rp.Name()})
	tui.layoutState.Focused = rp.Name()
	tui.applyLayout()
	if tui.inputPane() == nil {
		t.Fatal("precondition: the remote pane is the input pane")
	}
	return tui, got
}

// ynixBytes collects what arrives within d.
func ynixBytes(ch <-chan []byte, d time.Duration) []byte {
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

// Criterion 1, a local pane: Ctrl+Z writes nothing to the PTY.
func TestCtrlZ_NothingReachesALocalPane(t *testing.T) {
	for shape, ev := range ynixCtrlZ() {
		tui, keys := ynixLocal(t)
		tui.handleKey(ev)
		if got := ynixBytes(keys, 200*time.Millisecond); len(got) != 0 {
			t.Errorf("%s: %q reached the local pane, want nothing", shape, got)
		}
	}
}

// Criterion 1, a pane on another machine or in a viewer window: nothing on
// its stream.
func TestCtrlZ_NothingReachesARemotePane(t *testing.T) {
	for shape, ev := range ynixCtrlZ() {
		tui, got := ynixRemote(t)
		tui.handleKey(ev)
		if b := ynixBytes(got, 200*time.Millisecond); len(b) != 0 {
			t.Errorf("%s: %q reached the remote stream, want nothing", shape, b)
		}
	}
}

// Criterion 3, the positive twin through the same instruments: Ctrl+C and
// Ctrl+Y still reach both kinds of pane as 0x03 and 0x19.
func TestCtrlZ_OtherControlKeysStillReachThePane(t *testing.T) {
	for _, k := range []struct {
		key  tcell.Key
		want byte
	}{{tcell.KeyCtrlC, 0x03}, {tcell.KeyCtrlY, 0x19}} {
		ev := tcell.NewEventKey(k.key, 0, tcell.ModCtrl)
		tui, keys := ynixLocal(t)
		tui.handleKey(ev)
		if got := ynixBytes(keys, 200*time.Millisecond); len(got) != 1 || got[0] != k.want {
			t.Errorf("local: key %v sent %q, want %q", k.key, got, []byte{k.want})
		}
		rtui, rgot := ynixRemote(t)
		rtui.handleKey(ev)
		if got := ynixBytes(rgot, 200*time.Millisecond); len(got) != 1 || got[0] != k.want {
			t.Errorf("remote: key %v sent %q, want %q", k.key, got, []byte{k.want})
		}
	}
}

// Criterion 1: a parked pane does not wake on Ctrl+Z; a letter still wakes
// it, as before. The wake is read SYNCHRONOUSLY: wakeSuspendedPaneFromKeystroke
// sets p.waking before it returns, so a check right after handleKey sees a
// wake that started whatever the machine's load. The completion signal is
// held back 400 ms, longer than the wait below, so the timing check alone can
// never be what catches a wake (qa1, ini-ynix: under load a real wake outlasted
// a 300 ms wait and the swallow-deleted mutant survived once).
func TestCtrlZ_AParkedPaneStaysParked(t *testing.T) {
	p := testPane("eng1")
	tui := newTestTUI(p)
	tui.layoutState.Focused = "eng1"
	tui.applyLayout()
	setSuspendedForTest(tui, "eng1", true)
	woke := make(chan struct{}, 4)
	tui.onWakeComplete = func() {
		time.Sleep(400 * time.Millisecond)
		woke <- struct{}{}
	}
	waking := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.waking
	}
	for shape, ev := range ynixCtrlZ() {
		tui.handleKey(ev)
		if waking() {
			t.Fatalf("%s started a wake of a parked pane", shape)
		}
	}
	select {
	case <-woke:
		t.Fatal("Ctrl+Z woke a parked pane")
	case <-time.After(300 * time.Millisecond):
	}
	if !p.IsSuspended() {
		t.Fatal("the pane is no longer parked after Ctrl+Z")
	}
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if !waking() {
		t.Error("a letter at the parked pane did not start a wake")
	}
	select {
	case <-woke:
	case <-time.After(5 * time.Second):
		t.Error("the wake a letter started never completed")
	}
}

// Criterion 2, defence in depth: the encoders produce nothing for Ctrl+Z and
// still map the Ctrl letters around it.
func TestCtrlZ_TheEncodersProduceNothing(t *testing.T) {
	for shape, ev := range ynixCtrlZ() {
		if _, ok := tcellKeyToUV(ev); ok {
			t.Errorf("tcellKeyToUV(%s) produced a key", shape)
		}
	}
	if b := tcellKeyToANSI(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModCtrl)); len(b) != 0 {
		t.Errorf("tcellKeyToANSI(Ctrl+Z) = %q, want nothing", b)
	}
	for k := tcell.KeyCtrlA; k <= tcell.KeyCtrlY; k++ {
		ev := tcell.NewEventKey(k, 0, tcell.ModCtrl)
		if _, ok := tcellKeyToUV(ev); !ok {
			t.Errorf("tcellKeyToUV(%v) produced nothing", k)
		}
		if b := tcellKeyToANSI(ev); len(b) == 0 {
			t.Errorf("tcellKeyToANSI(%v) produced nothing", k)
		}
	}
}

// Feed it nothing: Ctrl+Z with no pane to type into does not panic.
func TestCtrlZ_NoFocusedPane(t *testing.T) {
	tui := newTestTUI()
	tui.applyLayout()
	for _, ev := range ynixCtrlZ() {
		tui.handleKey(ev)
	}
}

// Alt+z still zooms.
func TestCtrlZ_AltZStillZooms(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"))
	tui.layoutState.Focused = "eng1"
	tui.applyLayout()
	before := tui.layoutState.Zoomed
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModAlt))
	if tui.layoutState.Zoomed == before {
		t.Error("Alt+z did not toggle zoom")
	}
}

// Every other key passes as before: Ctrl+Shift+Z still reaches a remote pane
// as it did (the letter), and Ctrl+Alt+z is still the zoom chord.
func TestCtrlZ_NeighbouringChordsAreUnchanged(t *testing.T) {
	rtui, got := ynixRemote(t)
	rtui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'Z', tcell.ModCtrl|tcell.ModShift))
	if b := ynixBytes(got, 200*time.Millisecond); string(b) != "Z" {
		t.Errorf("Ctrl+Shift+Z sent %q to a remote pane, want \"Z\" as before", b)
	}
	tui := newTestTUI(testPane("eng1"), testPane("eng2"))
	tui.layoutState.Focused = "eng1"
	tui.applyLayout()
	before := tui.layoutState.Zoomed
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModCtrl|tcell.ModAlt))
	if tui.layoutState.Zoomed == before {
		t.Error("Ctrl+Alt+z no longer zooms")
	}
}

// The second line, reached directly as a reordered handleKey would reach it:
// both panes' SendKey write nothing for Ctrl+Z, and the local one does not
// count it as operator input (an open dialog's latch stays up).
func TestCtrlZ_TheSendKeysWriteNothing(t *testing.T) {
	ev := tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModCtrl)
	p, keys, _ := proofPane(t)
	p.latchDialogOpen("Claude needs your permission")
	p.SendKey(ev)
	if got := ynixBytes(keys, 200*time.Millisecond); len(got) != 0 {
		t.Errorf("Pane.SendKey(Ctrl+Z) wrote %q", got)
	}
	if !p.dialogLatched() {
		t.Error("Ctrl+Z counted as operator input and retired the dialog latch")
	}
	rtui, got := ynixRemote(t)
	rtui.inputPane().SendKey(ev)
	if b := ynixBytes(got, 200*time.Millisecond); len(b) != 0 {
		t.Errorf("RemotePane.SendKey(Ctrl+Z) wrote %q", b)
	}
}
