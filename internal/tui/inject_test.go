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

	// enter=false: skip Enter so the test doesn't need to drain extra bytes.
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

// --- Tests for getPromptContent (ini-f0d) ---

// TestGetPromptContent_WithPrompt verifies getPromptContent extracts text after ❯.
func TestGetPromptContent_WithPrompt(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	// Write a prompt row with ❯ followed by text.
	emu.Write([]byte("\033[10;1H\u276f some user input"))
	got := getPromptContent(&Pane{emu: emu})
	if got != "some user input" {
		t.Errorf("getPromptContent = %q, want %q", got, "some user input")
	}
}

// TestGetPromptContent_EmptyPrompt verifies getPromptContent returns empty for an empty prompt.
func TestGetPromptContent_EmptyPrompt(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	emu.Write([]byte("\033[10;1H\u276f "))
	got := getPromptContent(&Pane{emu: emu})
	if got != "" {
		t.Errorf("getPromptContent = %q, want empty string", got)
	}
}

// TestGetPromptContent_NoPrompt verifies getPromptContent returns empty when no ❯ is visible.
func TestGetPromptContent_NoPrompt(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	emu.Write([]byte("no prompt here"))
	got := getPromptContent(&Pane{emu: emu})
	if got != "" {
		t.Errorf("getPromptContent = %q, want empty string", got)
	}
}

// TestGetPromptContent_PasteReference verifies getPromptContent returns the paste reference.
func TestGetPromptContent_PasteReference(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	emu.Write([]byte("\033[10;1H\u276f [Pasted text #5 +1 lines]"))
	got := getPromptContent(&Pane{emu: emu})
	if got != "[Pasted text #5 +1 lines]" {
		t.Errorf("getPromptContent = %q, want paste reference", got)
	}
}

// --- Tests for looksLikeInjectedText (ini-f0d) ---

func TestLooksLikeInjectedText_PasteReference(t *testing.T) {
	if !looksLikeInjectedText("[Pasted text #5 +1 lines]", "long message") {
		t.Error("should match paste reference")
	}
}

func TestLooksLikeInjectedText_ExactMatch(t *testing.T) {
	if !looksLikeInjectedText("hello world", "hello world") {
		t.Error("should match exact injected text")
	}
}

func TestLooksLikeInjectedText_PrefixMatch(t *testing.T) {
	long := strings.Repeat("a", 200)
	prefix := long[:80] // terminal-width truncation
	if !looksLikeInjectedText(prefix, long) {
		t.Error("should match prefix of long injected text")
	}
}

func TestLooksLikeInjectedText_UserInput_NoMatch(t *testing.T) {
	if looksLikeInjectedText("user was typing this", "totally different injected message") {
		t.Error("should NOT match unrelated user input")
	}
}

func TestLooksLikeInjectedText_Empty_NoMatch(t *testing.T) {
	if looksLikeInjectedText("", "some text") {
		t.Error("empty prompt content should not match")
	}
}

// --- Test for smart retry path (ini-f0d) ---

// TestInjectText_DeadPaneSkipsRetry verifies that injectText with enter=true
// does not hang on the 200ms retry check when the pane is dead.
func TestInjectText_DeadPaneSkipsRetry(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()

	p := &Pane{name: "eng1", emu: emu, alive: false}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		tui.injectText(p, "hi", true)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("injectText(dead pane, enter=true) did not return promptly")
	}
}
