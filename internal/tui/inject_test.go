package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// TestInjectText_NoCtrlS verifies that injectText does not send Ctrl+S (0x13)
// to the pane's emulator. Ctrl+S is Claude Code's "cancel current operation"
// keybinding; sending it unconditionally before every injection was cancelling
// super's active work (ini-a1e.20).
//
// The stuck-input retry loop in injectText already handles the paste dialog
// without needing Ctrl+S, so removing it is a clean fix.
func TestInjectText_NoCtrlS(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{
		name:  "eng1",
		emu:   emu,
		alive: true,
	}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	// Collect the bytes that would be forwarded to the PTY via responseLoop.
	// Two printable characters "hi" encode to exactly 2 bytes (0x68, 0x69).
	readDone := make(chan []byte, 1)
	go func() {
		var got []byte
		buf := make([]byte, 16)
		for len(got) < 2 {
			n, err := emu.Read(buf)
			got = append(got, buf[:n]...)
			if err != nil {
				break
			}
		}
		readDone <- got
	}()

	// enter=false avoids the 50ms sleep and the stuck-input retry loop.
	tui.injectText(p, "hi", false)

	var got []byte
	select {
	case got = <-readDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for emu response bytes")
	}

	for _, b := range got {
		if b == 0x13 {
			t.Errorf("injectText sent Ctrl+S (0x13) to pane — should have been removed (ini-a1e.20)")
		}
	}
	// Sanity check: at least one printable byte was delivered.
	if len(got) == 0 {
		t.Error("injectText sent no bytes — text injection is broken")
	}
}

// TestInjectText_TextDelivered verifies that the injected characters are
// forwarded through the emulator (no regression from removing Ctrl+S).
func TestInjectText_TextDelivered(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{
		name:  "eng1",
		emu:   emu,
		alive: true,
	}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	want := "ok"
	readDone := make(chan []byte, 1)
	go func() {
		var got []byte
		buf := make([]byte, 16)
		for len(got) < len(want) {
			n, err := emu.Read(buf)
			got = append(got, buf[:n]...)
			if err != nil {
				break
			}
		}
		readDone <- got
	}()

	tui.injectText(p, want, false)

	var got []byte
	select {
	case got = <-readDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for injected text")
	}

	if string(got[:len(want)]) != want {
		t.Errorf("injected text: got %q, want %q", got, want)
	}
}
