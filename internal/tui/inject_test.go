package tui

import (
	"strings"
	"testing"
	"time"

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

			// enter=false avoids sending Enter.
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
// forwarded through the emulator. injectText sends Ctrl+S (1 byte) before
// the text payload; the goroutine drains all bytes so the pipe doesn't block.
func TestInjectText_TextDelivered(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{
		name:     "eng1",
		emu:      emu,
		alive:    true,
		activity: StateIdle,
	}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	want := "ok"
	// Ctrl+S (1 byte) + "ok" (2 bytes) = 3 bytes total.
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

// TestInjectText_LongMessageAllCharsDelivered verifies that a long message
// delivers every character to the emulator without dropping any. injectText
// sends Ctrl+S first, so the goroutine drains 1 + len(msg) bytes total.
func TestInjectText_LongMessageAllCharsDelivered(t *testing.T) {
	const msgLen = 50
	msg := strings.Repeat("x", msgLen)

	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{
		name:     "eng1",
		emu:      emu,
		alive:    true,
		activity: StateIdle,
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

	// enter=false: skip Enter so the test doesn't need to drain an extra byte.
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
