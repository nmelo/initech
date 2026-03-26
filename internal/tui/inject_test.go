package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/vt"
)

// TestInjectText_CtrlS_AlwaysSent verifies that injectText sends Ctrl+S (0x13)
// before the text payload regardless of pane activity state (ini-gd0).
// Ctrl+S in Claude Code stashes the current input line and restores it after
// the next response, protecting any text the user was composing. This applies
// to both idle and running panes.
func TestInjectText_CtrlS_AlwaysSent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		activity ActivityState
	}{
		{"idle", StateIdle},
		{"running", StateRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewSafeEmulator(80, 24)
			p := &Pane{
				name:     "eng1",
				emu:      emu,
				alive:    true,
				activity: tc.activity,
			}
			tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

			// Collect enough bytes to see Ctrl+S (0x13) followed by the text.
			// "hi" = 2 bytes; Ctrl+S + "hi" = 3 bytes minimum.
			readDone := make(chan []byte, 1)
			go func() {
				var got []byte
				buf := make([]byte, 16)
				for len(got) < 3 {
					n, err := emu.Read(buf)
					got = append(got, buf[:n]...)
					if err != nil {
						break
					}
				}
				readDone <- got
			}()

			// enter=false avoids the 50ms sleep and stuck-input retry loop.
			tui.injectText(p, "hi", false)

			var got []byte
			select {
			case got = <-readDone:
			case <-time.After(500 * time.Millisecond):
				t.Fatal("timed out waiting for emu response bytes")
			}

			if len(got) == 0 || got[0] != 0x13 {
				t.Errorf("injectText (%s pane) did not send Ctrl+S (0x13) first — stash missing (ini-gd0); got %v", tc.name, got)
			}
		})
	}
}

// TestInjectText_TextDelivered verifies that the injected characters are
// forwarded through the emulator. injectText now sends Ctrl+S (1 byte) before
// the text payload; the goroutine drains all bytes so the pipe doesn't block.
func TestInjectText_TextDelivered(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{
		name:     "eng1",
		emu:      emu,
		alive:    true,
		activity: StateIdle, // idle: Ctrl+S stash will be sent before the text
	}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	want := "ok"
	// Ctrl+S (1 byte) + "ok" (2 bytes) = 3 bytes total. Read all of them so
	// the pipe doesn't fill up and block injectText's SendKey calls.
	const totalBytes = 3
	readDone := make(chan []byte, 1)
	go func() {
		var got []byte
		buf := make([]byte, 16)
		for len(got) < totalBytes {
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

	// First byte is Ctrl+S; text follows.
	if len(got) < totalBytes || string(got[1:1+len(want)]) != want {
		t.Errorf("injected text: got %q, want Ctrl+S prefix then %q", got, want)
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
// dropping any (ini-4j5 regression guard). injectText now sends Ctrl+S first,
// so the goroutine drains 1 + len(msg) bytes to prevent blocking.
func TestInjectText_LongMessageAllCharsDelivered(t *testing.T) {
	msg := strings.Repeat("x", pasteDialogThreshold) // exactly at threshold
	if utf8.RuneCountInString(msg) < pasteDialogThreshold {
		t.Fatal("test message too short — adjust construction")
	}

	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{
		name:     "eng1",
		emu:      emu,
		alive:    true,
		activity: StateIdle, // idle: Ctrl+S prefix byte is sent before the text
	}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	// Ctrl+S (1 byte) + len(msg) text bytes. Must drain all to avoid blocking.
	totalBytes := 1 + len(msg)
	readDone := make(chan []byte, 1)
	go func() {
		var got []byte
		buf := make([]byte, 256)
		for len(got) < totalBytes {
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

	// First byte is Ctrl+S; remaining bytes are the message.
	if len(got) < totalBytes {
		t.Errorf("got %d bytes, want %d (Ctrl+S + message) — some chars dropped", len(got), totalBytes)
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
