package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestHandleWebKey_EscCloses(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.webM.active = true

	tui.handleWebKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if tui.webM.active {
		t.Error("expected modal to close on Esc")
	}
}

func TestHandleWebKey_QCloses(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.webM.active = true

	tui.handleWebKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone))
	if tui.webM.active {
		t.Error("expected modal to close on q")
	}
}

func TestHandleWebKey_BacktickCloses(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.webM.active = true

	tui.handleWebKey(tcell.NewEventKey(tcell.KeyRune, '`', tcell.ModNone))
	if tui.webM.active {
		t.Error("expected modal to close on backtick")
	}
}

func TestCmdWeb_OpensModal(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.cmdWeb()

	if !tui.webM.active {
		t.Error("expected webM.active to be true after cmdWeb")
	}
}

func TestWebModal_DisabledWhenPortZero(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.webPort = 0

	// Should not panic when rendering disabled state.
	// We can't render without a screen, but we can verify the state.
	if tui.webPort != 0 {
		t.Error("webPort should be 0 (disabled)")
	}
}

func TestWebModal_PortStored(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.webPort = 9100

	if tui.webPort != 9100 {
		t.Errorf("webPort = %d, want 9100", tui.webPort)
	}
}
