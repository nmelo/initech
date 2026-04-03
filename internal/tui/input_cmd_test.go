package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func cmdTUI(buf string) *TUI {
	t := &TUI{}
	t.cmd.active = true
	t.cmd.buf = []rune(buf)
	t.cmd.cursor = len(t.cmd.buf)
	return t
}

func cmdBuf(t *TUI) string  { return string(t.cmd.buf) }
func cmdCur(t *TUI) int     { return t.cmd.cursor }

func key(k tcell.Key) *tcell.EventKey {
	return tcell.NewEventKey(k, 0, 0)
}

func rune_(r rune) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, r, 0)
}

// --- Movement ---

func TestCmdKey_CtrlA_MovesToStart(t *testing.T) {
	tui := cmdTUI("hello")
	tui.handleCmdKey(key(tcell.KeyCtrlA))
	if cmdCur(tui) != 0 {
		t.Errorf("cursor = %d, want 0", cmdCur(tui))
	}
}

func TestCmdKey_Home_MovesToStart(t *testing.T) {
	tui := cmdTUI("hello")
	tui.handleCmdKey(key(tcell.KeyHome))
	if cmdCur(tui) != 0 {
		t.Errorf("cursor = %d, want 0", cmdCur(tui))
	}
}

func TestCmdKey_CtrlE_MovesToEnd(t *testing.T) {
	tui := cmdTUI("hello")
	tui.cmd.cursor = 0
	tui.handleCmdKey(key(tcell.KeyCtrlE))
	if cmdCur(tui) != 5 {
		t.Errorf("cursor = %d, want 5", cmdCur(tui))
	}
}

func TestCmdKey_End_MovesToEnd(t *testing.T) {
	tui := cmdTUI("hello")
	tui.cmd.cursor = 2
	tui.handleCmdKey(key(tcell.KeyEnd))
	if cmdCur(tui) != 5 {
		t.Errorf("cursor = %d, want 5", cmdCur(tui))
	}
}

func TestCmdKey_CtrlB_MovesBack(t *testing.T) {
	tui := cmdTUI("abc")
	tui.handleCmdKey(key(tcell.KeyCtrlB))
	if cmdCur(tui) != 2 {
		t.Errorf("cursor = %d, want 2", cmdCur(tui))
	}
}

func TestCmdKey_Left_MovesBack(t *testing.T) {
	tui := cmdTUI("abc")
	tui.handleCmdKey(key(tcell.KeyLeft))
	if cmdCur(tui) != 2 {
		t.Errorf("cursor = %d, want 2", cmdCur(tui))
	}
}

func TestCmdKey_CtrlB_ClampsAtZero(t *testing.T) {
	tui := cmdTUI("abc")
	tui.cmd.cursor = 0
	tui.handleCmdKey(key(tcell.KeyCtrlB))
	if cmdCur(tui) != 0 {
		t.Errorf("cursor = %d, want 0", cmdCur(tui))
	}
}

func TestCmdKey_CtrlF_MovesForward(t *testing.T) {
	tui := cmdTUI("abc")
	tui.cmd.cursor = 1
	tui.handleCmdKey(key(tcell.KeyCtrlF))
	if cmdCur(tui) != 2 {
		t.Errorf("cursor = %d, want 2", cmdCur(tui))
	}
}

func TestCmdKey_Right_MovesForward(t *testing.T) {
	tui := cmdTUI("abc")
	tui.cmd.cursor = 0
	tui.handleCmdKey(key(tcell.KeyRight))
	if cmdCur(tui) != 1 {
		t.Errorf("cursor = %d, want 1", cmdCur(tui))
	}
}

func TestCmdKey_CtrlF_ClampsAtEnd(t *testing.T) {
	tui := cmdTUI("abc")
	tui.handleCmdKey(key(tcell.KeyCtrlF))
	if cmdCur(tui) != 3 {
		t.Errorf("cursor = %d, want 3", cmdCur(tui))
	}
}

// --- Cursor-relative insertion ---

func TestCmdKey_InsertAtCursor(t *testing.T) {
	tui := cmdTUI("hllo")
	tui.cmd.cursor = 1
	tui.handleCmdKey(rune_('e'))
	if cmdBuf(tui) != "hello" {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "hello")
	}
	if cmdCur(tui) != 2 {
		t.Errorf("cursor = %d, want 2", cmdCur(tui))
	}
}

func TestCmdKey_InsertAtStart(t *testing.T) {
	tui := cmdTUI("ello")
	tui.cmd.cursor = 0
	tui.handleCmdKey(rune_('h'))
	if cmdBuf(tui) != "hello" {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "hello")
	}
	if cmdCur(tui) != 1 {
		t.Errorf("cursor = %d, want 1", cmdCur(tui))
	}
}

func TestCmdKey_InsertAtEnd(t *testing.T) {
	tui := cmdTUI("hell")
	tui.handleCmdKey(rune_('o'))
	if cmdBuf(tui) != "hello" {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "hello")
	}
	if cmdCur(tui) != 5 {
		t.Errorf("cursor = %d, want 5", cmdCur(tui))
	}
}

// --- Deletion ---

func TestCmdKey_Backspace_DeletesLeftOfCursor(t *testing.T) {
	tui := cmdTUI("hello")
	tui.cmd.cursor = 3 // between 'l' and 'l'
	tui.handleCmdKey(key(tcell.KeyBackspace2))
	if cmdBuf(tui) != "helo" {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "helo")
	}
	if cmdCur(tui) != 2 {
		t.Errorf("cursor = %d, want 2", cmdCur(tui))
	}
}

func TestCmdKey_Backspace_AtStartNoOp(t *testing.T) {
	tui := cmdTUI("hello")
	tui.cmd.cursor = 0
	tui.handleCmdKey(key(tcell.KeyBackspace2))
	if cmdBuf(tui) != "hello" {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "hello")
	}
}

func TestCmdKey_CtrlD_DeletesAtCursor(t *testing.T) {
	tui := cmdTUI("hello")
	tui.cmd.cursor = 1
	tui.handleCmdKey(key(tcell.KeyCtrlD))
	if cmdBuf(tui) != "hllo" {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "hllo")
	}
	if cmdCur(tui) != 1 {
		t.Errorf("cursor = %d, want 1", cmdCur(tui))
	}
}

func TestCmdKey_Delete_DeletesAtCursor(t *testing.T) {
	tui := cmdTUI("hello")
	tui.cmd.cursor = 4
	tui.handleCmdKey(key(tcell.KeyDelete))
	if cmdBuf(tui) != "hell" {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "hell")
	}
}

func TestCmdKey_CtrlD_AtEndNoOp(t *testing.T) {
	tui := cmdTUI("hello")
	tui.handleCmdKey(key(tcell.KeyCtrlD))
	if cmdBuf(tui) != "hello" {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "hello")
	}
}

func TestCmdKey_CtrlW_DeletesWord(t *testing.T) {
	tui := cmdTUI("focus eng1")
	tui.handleCmdKey(key(tcell.KeyCtrlW))
	if cmdBuf(tui) != "focus " {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "focus ")
	}
	if cmdCur(tui) != 6 {
		t.Errorf("cursor = %d, want 6", cmdCur(tui))
	}
}

func TestCmdKey_CtrlW_DeletesWordMidLine(t *testing.T) {
	tui := cmdTUI("focus eng1 extra")
	tui.cmd.cursor = 10 // after "eng1"
	tui.handleCmdKey(key(tcell.KeyCtrlW))
	if cmdBuf(tui) != "focus  extra" {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "focus  extra")
	}
	if cmdCur(tui) != 6 {
		t.Errorf("cursor = %d, want 6", cmdCur(tui))
	}
}

func TestCmdKey_CtrlW_AtStartNoOp(t *testing.T) {
	tui := cmdTUI("hello")
	tui.cmd.cursor = 0
	tui.handleCmdKey(key(tcell.KeyCtrlW))
	if cmdBuf(tui) != "hello" {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "hello")
	}
}

func TestCmdKey_CtrlU_DeletesToStart(t *testing.T) {
	tui := cmdTUI("focus eng1")
	tui.cmd.cursor = 6
	tui.handleCmdKey(key(tcell.KeyCtrlU))
	if cmdBuf(tui) != "eng1" {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "eng1")
	}
	if cmdCur(tui) != 0 {
		t.Errorf("cursor = %d, want 0", cmdCur(tui))
	}
}

func TestCmdKey_CtrlK_DeletesToEnd(t *testing.T) {
	tui := cmdTUI("focus eng1")
	tui.cmd.cursor = 5
	tui.handleCmdKey(key(tcell.KeyCtrlK))
	if cmdBuf(tui) != "focus" {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "focus")
	}
	if cmdCur(tui) != 5 {
		t.Errorf("cursor = %d, want 5", cmdCur(tui))
	}
}

// --- Existing behavior preserved ---

func TestCmdKey_EscClosesAndResetsCursor(t *testing.T) {
	tui := cmdTUI("partial")
	tui.handleCmdKey(key(tcell.KeyEscape))
	if tui.cmd.active {
		t.Error("cmd bar should be closed")
	}
	if len(tui.cmd.buf) != 0 {
		t.Error("buf should be empty")
	}
	if cmdCur(tui) != 0 {
		t.Errorf("cursor = %d, want 0", cmdCur(tui))
	}
}

func TestCmdKey_BacktickEmptyCloses(t *testing.T) {
	tui := cmdTUI("")
	tui.handleCmdKey(rune_('`'))
	if tui.cmd.active {
		t.Error("cmd bar should close on backtick with empty buf")
	}
}

func TestCmdKey_BacktickNonEmptyInserts(t *testing.T) {
	tui := cmdTUI("a")
	tui.handleCmdKey(rune_('`'))
	if cmdBuf(tui) != "a`" {
		t.Errorf("buf = %q, want %q", cmdBuf(tui), "a`")
	}
}
