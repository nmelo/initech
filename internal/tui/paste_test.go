package tui

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// ── SendPaste (low-level marker writes, still used by non-buffered paths) ──

// paneWithPipe returns a minimal Pane whose ptmx is the write end of an
// os.Pipe(). The caller receives the read end for inspecting writes.
func paneWithPipe(t *testing.T) (*Pane, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	p := &Pane{ptmx: w}
	return p, r
}

func TestSendPasteStart(t *testing.T) {
	p, r := paneWithPipe(t)
	p.SendPaste(true)
	p.ptmx.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "\x1b[200~" {
		t.Errorf("SendPaste(true) wrote %q, want %q", got, "\x1b[200~")
	}
}

func TestSendPasteEnd(t *testing.T) {
	p, r := paneWithPipe(t)
	p.SendPaste(false)
	p.ptmx.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "\x1b[201~" {
		t.Errorf("SendPaste(false) wrote %q, want %q", got, "\x1b[201~")
	}
}

func TestSendPasteNilPTY(t *testing.T) {
	// SendPaste on a pane with no PTY must not panic.
	p := &Pane{}
	p.SendPaste(true)
	p.SendPaste(false)
}

// ── Buffered paste (handlePaste + handleEvent integration) ─────────────

// newTUIWithPipePane builds a minimal TUI containing one pane whose ptmx is
// a pipe, and returns the read end of that pipe for inspection.
func newTUIWithPipePane(t *testing.T) (*TUI, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })

	p := &Pane{name: "eng1", ptmx: w, alive: true, visible: true}
	tui := &TUI{
		layoutState: LayoutState{Focused: "eng1"},
		panes:       toPaneViews([]*Pane{p}),
		agentEvents: make(chan AgentEvent, 8),
		quitCh:      make(chan struct{}),
	}
	return tui, r
}

func TestBufferedPaste_BasicFlush(t *testing.T) {
	tui, r := newTUIWithPipePane(t)

	// Simulate: paste start, 5 chars, paste end.
	tui.handleEvent(tcell.NewEventPaste(true))
	for _, ch := range "hello" {
		tui.handleEvent(tcell.NewEventKey(tcell.KeyRune, ch, 0))
	}
	tui.handleEvent(tcell.NewEventPaste(false))

	tui.panes[0].(*Pane).ptmx.Close()
	got, _ := io.ReadAll(r)

	want := "\x1b[200~hello\x1b[201~"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBufferedPaste_NoBracketedPaste(t *testing.T) {
	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()

	p := &Pane{name: "eng1", ptmx: w, alive: true, visible: true, noBracketedPaste: true}
	tui := &TUI{
		layoutState: LayoutState{Focused: "eng1"},
		panes:       toPaneViews([]*Pane{p}),
		agentEvents: make(chan AgentEvent, 8),
		quitCh:      make(chan struct{}),
	}

	tui.handleEvent(tcell.NewEventPaste(true))
	for _, ch := range "world" {
		tui.handleEvent(tcell.NewEventKey(tcell.KeyRune, ch, 0))
	}
	tui.handleEvent(tcell.NewEventPaste(false))

	p.ptmx.Close()
	got, _ := io.ReadAll(r)

	// No bracketed paste markers for NoBracketedPaste panes.
	if string(got) != "world" {
		t.Errorf("got %q, want %q", got, "world")
	}
}

func TestBufferedPaste_EmptyPaste(t *testing.T) {
	tui, r := newTUIWithPipePane(t)

	// Empty paste: start immediately followed by end.
	tui.handleEvent(tcell.NewEventPaste(true))
	tui.handleEvent(tcell.NewEventPaste(false))

	tui.panes[0].(*Pane).ptmx.Close()
	got, _ := io.ReadAll(r)

	if len(got) != 0 {
		t.Errorf("empty paste should write nothing, got %q", got)
	}
}

func TestBufferedPaste_SpecialChars(t *testing.T) {
	tui, r := newTUIWithPipePane(t)

	tui.handleEvent(tcell.NewEventPaste(true))
	// Rune characters.
	tui.handleEvent(tcell.NewEventKey(tcell.KeyRune, 'A', 0))
	// Enter -> \r in paste.
	tui.handleEvent(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	// Tab.
	tui.handleEvent(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	// Escape.
	tui.handleEvent(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	// Backspace.
	tui.handleEvent(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0))
	tui.handleEvent(tcell.NewEventPaste(false))

	tui.panes[0].(*Pane).ptmx.Close()
	got, _ := io.ReadAll(r)

	want := "\x1b[200~A\r\t\x1b\x7f\x1b[201~"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBufferedPaste_MultiLineLF(t *testing.T) {
	tui, r := newTUIWithPipePane(t)

	tui.handleEvent(tcell.NewEventPaste(true))
	tui.handleEvent(tcell.NewEventKey(tcell.KeyRune, 'a', 0))
	// LF (0x0A) arrives as KeyCtrlJ from macOS clipboard.
	tui.handleEvent(tcell.NewEventKey(tcell.KeyCtrlJ, 0, 0))
	tui.handleEvent(tcell.NewEventKey(tcell.KeyRune, 'b', 0))
	tui.handleEvent(tcell.NewEventKey(tcell.KeyCtrlJ, 0, 0))
	tui.handleEvent(tcell.NewEventKey(tcell.KeyRune, 'c', 0))
	tui.handleEvent(tcell.NewEventPaste(false))

	tui.panes[0].(*Pane).ptmx.Close()
	got, _ := io.ReadAll(r)

	want := "\x1b[200~a\nb\nc\x1b[201~"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBufferedPaste_ModalDropsPaste(t *testing.T) {
	tui, r := newTUIWithPipePane(t)

	// Open the command modal.
	tui.cmd.active = true

	tui.handleEvent(tcell.NewEventPaste(true))
	for _, ch := range "dropped" {
		tui.handleEvent(tcell.NewEventKey(tcell.KeyRune, ch, 0))
	}
	tui.handleEvent(tcell.NewEventPaste(false))

	tui.panes[0].(*Pane).ptmx.Close()
	got, _ := io.ReadAll(r)

	if len(got) != 0 {
		t.Errorf("paste during modal should be dropped, got %q", got)
	}
}

func TestBufferedPaste_NoFocusedPane(t *testing.T) {
	tui := &TUI{
		layoutState: LayoutState{Focused: "nobody"},
		panes:       toPaneViews([]*Pane{}),
		agentEvents: make(chan AgentEvent, 8),
		quitCh:      make(chan struct{}),
	}

	// Should not panic.
	tui.handleEvent(tcell.NewEventPaste(true))
	for _, ch := range "test" {
		tui.handleEvent(tcell.NewEventKey(tcell.KeyRune, ch, 0))
	}
	tui.handleEvent(tcell.NewEventPaste(false))
}

func TestBufferedPaste_PastingSkipsRender(t *testing.T) {
	tui, _ := newTUIWithPipePane(t)

	// After paste start, pasting should be true.
	tui.handleEvent(tcell.NewEventPaste(true))
	if !tui.pasting {
		t.Error("pasting should be true after paste start")
	}

	// Send some characters.
	tui.handleEvent(tcell.NewEventKey(tcell.KeyRune, 'x', 0))
	if !tui.pasting {
		t.Error("pasting should still be true during char accumulation")
	}

	// After paste end, pasting should be false.
	tui.handleEvent(tcell.NewEventPaste(false))
	if tui.pasting {
		t.Error("pasting should be false after paste end")
	}
}

func TestBufferedPaste_DoesNotQuit(t *testing.T) {
	tui, _ := newTUIWithPipePane(t)
	if quit := tui.handleEvent(tcell.NewEventPaste(true)); quit {
		t.Error("paste start returned quit")
	}
	if quit := tui.handleEvent(tcell.NewEventKey(tcell.KeyRune, 'a', 0)); quit {
		t.Error("buffered key returned quit")
	}
	if quit := tui.handleEvent(tcell.NewEventPaste(false)); quit {
		t.Error("paste end returned quit")
	}
}

func TestBufferedPaste_UnicodeContent(t *testing.T) {
	tui, r := newTUIWithPipePane(t)

	tui.handleEvent(tcell.NewEventPaste(true))
	for _, ch := range "cafe\u0301" { // "café" with combining accent
		tui.handleEvent(tcell.NewEventKey(tcell.KeyRune, ch, 0))
	}
	tui.handleEvent(tcell.NewEventPaste(false))

	tui.panes[0].(*Pane).ptmx.Close()
	got, _ := io.ReadAll(r)

	want := "\x1b[200~cafe\u0301\x1b[201~"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBufferedPaste_LargePaste(t *testing.T) {
	tui, r := newTUIWithPipePane(t)

	// Drain the pipe concurrently. os.Pipe has a finite kernel buffer
	// (~64KB on macOS), so large writes block without a concurrent reader.
	doneCh := make(chan []byte, 1)
	go func() {
		got, _ := io.ReadAll(r)
		doneCh <- got
	}()

	// Simulate a 100KB paste.
	text := strings.Repeat("A", 100*1024)
	tui.handleEvent(tcell.NewEventPaste(true))
	for _, ch := range text {
		tui.handleEvent(tcell.NewEventKey(tcell.KeyRune, ch, 0))
	}
	tui.handleEvent(tcell.NewEventPaste(false))

	tui.panes[0].(*Pane).ptmx.Close()
	got := <-doneCh

	// Verify markers and content.
	if !strings.HasPrefix(string(got), "\x1b[200~") {
		t.Error("missing paste start marker")
	}
	if !strings.HasSuffix(string(got), "\x1b[201~") {
		t.Error("missing paste end marker")
	}
	// Content between markers should be exactly the text.
	inner := string(got[6 : len(got)-6])
	if inner != text {
		t.Errorf("content length: got %d, want %d", len(inner), len(text))
	}
}

// ── FlushPaste unit tests ──────────────────────────────────────────────

func TestFlushPaste_NilPTY(t *testing.T) {
	p := &Pane{}
	// Should not panic.
	p.FlushPaste([]byte("test"))
}

func TestFlushPaste_EmptyContent(t *testing.T) {
	p, r := paneWithPipe(t)
	p.FlushPaste(nil)
	p.FlushPaste([]byte{})
	p.ptmx.Close()
	got, _ := io.ReadAll(r)
	if len(got) != 0 {
		t.Errorf("empty FlushPaste should write nothing, got %q", got)
	}
}

func TestFlushPaste_WithBrackets(t *testing.T) {
	p, r := paneWithPipe(t)
	p.FlushPaste([]byte("data"))
	p.ptmx.Close()
	got, _ := io.ReadAll(r)
	want := "\x1b[200~data\x1b[201~"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFlushPaste_WithoutBrackets(t *testing.T) {
	p, r := paneWithPipe(t)
	p.noBracketedPaste = true
	p.FlushPaste([]byte("data"))
	p.ptmx.Close()
	got, _ := io.ReadAll(r)
	if string(got) != "data" {
		t.Errorf("got %q, want %q", got, "data")
	}
}

// ── modalActive ────────────────────────────────────────────────────────

func TestModalActive_AllModals(t *testing.T) {
	tui := &TUI{}
	if tui.modalActive() {
		t.Error("no modals active, but modalActive returned true")
	}

	tests := []struct {
		name string
		set  func(*TUI)
	}{
		{"welcome", func(t *TUI) { t.welcome.active = true }},
		{"help", func(t *TUI) { t.help.active = true }},
		{"eventLog", func(t *TUI) { t.eventLogM.active = true }},
		{"top", func(t *TUI) { t.top.active = true }},
		{"mcp", func(t *TUI) { t.mcpM.active = true }},
		{"web", func(t *TUI) { t.webM.active = true }},
		{"agents", func(t *TUI) { t.agents.active = true }},
		{"cmd", func(t *TUI) { t.cmd.active = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tui := &TUI{}
			tt.set(tui)
			if !tui.modalActive() {
				t.Errorf("modalActive should be true with %s active", tt.name)
			}
		})
	}
}

// ── appendRune ─────────────────────────────────────────────────────────

func TestAppendRune_ASCII(t *testing.T) {
	got := appendRune(nil, 'a')
	if string(got) != "a" {
		t.Errorf("got %q, want %q", got, "a")
	}
}

func TestAppendRune_MultiByte(t *testing.T) {
	got := appendRune(nil, '\u00E9') // é
	if string(got) != "\u00E9" {
		t.Errorf("got %q, want %q", got, "\u00E9")
	}
}

func TestAppendRune_Emoji(t *testing.T) {
	got := appendRune(nil, '\U0001F600') // 😀
	if string(got) != "\U0001F600" {
		t.Errorf("got %q, want %q", got, "\U0001F600")
	}
}
