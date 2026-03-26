package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// TestPasteDialogThreshold verifies the threshold constant matches the value
// chosen during ini-4j5 triage (50 runes — Claude Code's paste detection fires
// for any message whose characters arrive faster than typing speed, which is
// always the case for injectText).
func TestPasteDialogThreshold(t *testing.T) {
	if pasteDialogThreshold != 50 {
		t.Errorf("pasteDialogThreshold = %d, want 50 (ini-4j5)", pasteDialogThreshold)
	}
}

// TestInjectText_LongMessageAllCharsDelivered verifies that a message at the
// paste-dialog threshold delivers every character to the emulator without
// dropping any (ini-4j5 regression guard).
func TestInjectText_LongMessageAllCharsDelivered(t *testing.T) {
	msg := strings.Repeat("x", pasteDialogThreshold) // exactly at threshold
	if utf8.RuneCountInString(msg) < pasteDialogThreshold {
		t.Fatal("test message too short — adjust construction")
	}

	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{name: "eng1", emu: emu, alive: true}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	readDone := make(chan []byte, 1)
	go func() {
		var got []byte
		buf := make([]byte, 256)
		for len(got) < len(msg) {
			n, err := emu.Read(buf)
			got = append(got, buf[:n]...)
			if err != nil {
				break
			}
		}
		readDone <- got
	}()

	// enter=false: skip Enter and retry loop so the test doesn't sleep.
	tui.injectText(p, msg, false)

	var got []byte
	select {
	case got = <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for long message chars")
	}

	if len(got) < len(msg) {
		t.Errorf("got %d bytes, want %d — some chars dropped", len(got), len(msg))
	}
}

// TestInjectText_DeadPaneShortCircuits verifies that injectText with enter=true
// exits the retry loop early when the pane is dead, for both the short-message
// path (3 retries at 100ms) and the long-message path (8 retries at 150ms).
// Neither path should block for the full retry budget when alive=false.
func TestInjectText_DeadPaneShortCircuits(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  string
	}{
		{"short", "hi"},
		{"long", strings.Repeat("a", pasteDialogThreshold)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewSafeEmulator(80, 24)
			// Drain emulator output so responseLoop-like goroutines don't block.
			go func() {
				buf := make([]byte, 256)
				for {
					if _, err := emu.Read(buf); err != nil {
						return
					}
				}
			}()

			p := &Pane{name: "eng1", emu: emu, alive: false} // dead from the start
			tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

			done := make(chan struct{})
			go func() {
				defer close(done)
				tui.injectText(p, tc.msg, true)
			}()

			// Both paths must complete well within 2s (max would be 150+8*150=1350ms).
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Errorf("injectText(%s, enter=true) did not return after pane died", tc.name)
			}
		})
	}
}
