package tui

import (
	"io"
	"os"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// ── SendPaste ─────────────────────────────────────────────────────────

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

// ── handleEvent / *tcell.EventPaste ──────────────────────────────────

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
		panes:       []*Pane{p},
		agentEvents: make(chan AgentEvent, 8),
		quitCh:      make(chan struct{}),
	}
	return tui, r
}

func TestHandleEventPasteStartForwardedToPTY(t *testing.T) {
	tui, r := newTUIWithPipePane(t)

	tui.handleEvent(tcell.NewEventPaste(true))
	tui.panes[0].ptmx.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "\x1b[200~" {
		t.Errorf("paste start: PTY received %q, want %q", got, "\x1b[200~")
	}
}

func TestHandleEventPasteEndForwardedToPTY(t *testing.T) {
	tui, r := newTUIWithPipePane(t)

	tui.handleEvent(tcell.NewEventPaste(false))
	tui.panes[0].ptmx.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "\x1b[201~" {
		t.Errorf("paste end: PTY received %q, want %q", got, "\x1b[201~")
	}
}

func TestHandleEventPasteNoFocusedPane(t *testing.T) {
	// No pane named "nobody" exists — focusedPane returns nil.
	// handleEvent must not panic.
	tui := &TUI{
		layoutState: LayoutState{Focused: "nobody"},
		panes:       []*Pane{},
		agentEvents: make(chan AgentEvent, 8),
		quitCh:      make(chan struct{}),
	}
	tui.handleEvent(tcell.NewEventPaste(true))
	tui.handleEvent(tcell.NewEventPaste(false))
}

func TestHandleEventPasteDoesNotQuit(t *testing.T) {
	tui, _ := newTUIWithPipePane(t)
	quit := tui.handleEvent(tcell.NewEventPaste(true))
	if quit {
		t.Error("handleEvent(EventPaste) returned true (quit signal), want false")
	}
}
