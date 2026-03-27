// Coverage tests Tier 3: formatDuration, completionCandidates, executeConfirmed,
// renderCmdLine, handleIPCBead.
package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// ── formatDuration ──────────────────────────────────────────────────

func TestFormatDuration_Minutes(t *testing.T) {
	if got := formatDuration(5); got != "5m" {
		t.Errorf("formatDuration(5) = %q, want '5m'", got)
	}
	if got := formatDuration(59); got != "59m" {
		t.Errorf("formatDuration(59) = %q, want '59m'", got)
	}
}

func TestFormatDuration_ExactHours(t *testing.T) {
	if got := formatDuration(60); got != "1h" {
		t.Errorf("formatDuration(60) = %q, want '1h'", got)
	}
	if got := formatDuration(120); got != "2h" {
		t.Errorf("formatDuration(120) = %q, want '2h'", got)
	}
}

func TestFormatDuration_HoursAndMinutes(t *testing.T) {
	if got := formatDuration(90); got != "1h30m" {
		t.Errorf("formatDuration(90) = %q, want '1h30m'", got)
	}
	if got := formatDuration(150); got != "2h30m" {
		t.Errorf("formatDuration(150) = %q, want '2h30m'", got)
	}
}

// ── completionCandidates ────────────────────────────────────────────

func testTUIWithPanes(names ...string) *TUI {
	panes := make([]*Pane, len(names))
	for i, n := range names {
		emu := vt.NewSafeEmulator(40, 10)
		go func() {
			buf := make([]byte, 256)
			for {
				if _, err := emu.Read(buf); err != nil {
					return
				}
			}
		}()
		panes[i] = &Pane{name: n, emu: emu, alive: true, visible: true}
	}
	ls := DefaultLayoutState(nil)
	ls.Hidden = make(map[string]bool)
	ls.Pinned = make(map[string]bool)
	return &TUI{panes: panes, layoutState: ls}
}

func TestCompletionCandidates_Default(t *testing.T) {
	tui := testTUIWithPanes("eng1", "eng2", "super")
	got := tui.completionCandidates("focus")
	if len(got) != 3 {
		t.Errorf("focus candidates = %v, want 3 names", got)
	}
}

func TestCompletionCandidates_Show(t *testing.T) {
	tui := testTUIWithPanes("eng1", "eng2", "super")
	tui.layoutState.Hidden["eng2"] = true
	got := tui.completionCandidates("show")
	// Should include hidden panes + "all".
	if len(got) != 2 {
		t.Errorf("show candidates = %v, want [eng2, all]", got)
	}
	found := false
	for _, c := range got {
		if c == "eng2" {
			found = true
		}
	}
	if !found {
		t.Errorf("show candidates should include hidden 'eng2': %v", got)
	}
}

func TestCompletionCandidates_Hide(t *testing.T) {
	tui := testTUIWithPanes("eng1", "eng2", "super")
	tui.layoutState.Hidden["eng2"] = true
	got := tui.completionCandidates("hide")
	// Should include visible panes only (eng1, super).
	if len(got) != 2 {
		t.Errorf("hide candidates = %v, want 2 visible names", got)
	}
	for _, c := range got {
		if c == "eng2" {
			t.Error("hide candidates should NOT include hidden eng2")
		}
	}
}

func TestCompletionCandidates_Pin(t *testing.T) {
	tui := testTUIWithPanes("eng1", "eng2")
	tui.layoutState.Pinned["eng1"] = true
	got := tui.completionCandidates("pin")
	// Should only include unpinned panes (eng2).
	if len(got) != 1 || got[0] != "eng2" {
		t.Errorf("pin candidates = %v, want [eng2]", got)
	}
}

func TestCompletionCandidates_Unpin(t *testing.T) {
	tui := testTUIWithPanes("eng1", "eng2")
	tui.layoutState.Pinned["eng1"] = true
	got := tui.completionCandidates("unpin")
	if len(got) != 1 || got[0] != "eng1" {
		t.Errorf("unpin candidates = %v, want [eng1]", got)
	}
}

// ── executeConfirmed ────────────────────────────────────────────────

func TestExecuteConfirmed_Empty(t *testing.T) {
	tui := testTUIWithPanes("eng1")
	tui.cmd.pendingConfirm = ""
	tui.cmd.active = true
	quit := tui.executeConfirmed()
	if quit {
		t.Error("empty pendingConfirm should not quit")
	}
	if tui.cmd.active {
		t.Error("cmd.active should be cleared")
	}
}

func TestExecuteConfirmed_Quit(t *testing.T) {
	tui := testTUIWithPanes("eng1")
	tui.cmd.pendingConfirm = "quit"
	quit := tui.executeConfirmed()
	if !quit {
		t.Error("quit should return true")
	}
}

func TestExecuteConfirmed_RemoveInvalid(t *testing.T) {
	tui := testTUIWithPanes("eng1", "eng2")
	tui.cmd.pendingConfirm = "remove nonexistent"
	tui.executeConfirmed()
	if tui.cmd.error == "" {
		t.Error("removing nonexistent pane should set cmd.error")
	}
}

// ── renderCmdLine ───────────────────────────────────────────────────

func TestRenderCmdLine_PromptAndInput(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{screen: s, cmd: cmdModal{active: true, buf: []rune("grid 3x3")}}
	tui.renderCmdLine()

	_, sh := s.Size()
	y := sh - 1
	// First char should be '>'.
	c, _, _, _ := s.GetContent(0, y)
	if c != '>' {
		t.Errorf("prompt char = %q, want '>'", c)
	}
	// Input text should start at x=2.
	var buf strings.Builder
	for x := 2; x < 12; x++ {
		c, _, _, _ := s.GetContent(x, y)
		buf.WriteRune(c)
	}
	if !strings.HasPrefix(buf.String(), "grid 3x3") {
		t.Errorf("input = %q, want starts with 'grid 3x3'", buf.String())
	}
}

func TestRenderCmdLine_ConfirmPrompt(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{screen: s, cmd: cmdModal{
		active:         true,
		pendingConfirm: "quit",
		confirmMsg:     "Are you sure? [y/N]",
		confirmExpiry:  time.Now().Add(5 * time.Second),
	}}
	tui.renderCmdLine()

	_, sh := s.Size()
	var buf strings.Builder
	for x := 0; x < 60; x++ {
		c, _, _, _ := s.GetContent(x, sh-1)
		buf.WriteRune(c)
	}
	row := buf.String()
	if !strings.Contains(row, "Are you sure") {
		t.Errorf("confirm bar = %q, want contains 'Are you sure'", row)
	}
}

func TestRenderCmdLine_HintText(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{screen: s, cmd: cmdModal{active: true}}
	tui.renderCmdLine()

	_, sh := s.Size()
	var buf strings.Builder
	for x := 40; x < 80; x++ {
		c, _, _, _ := s.GetContent(x, sh-1)
		buf.WriteRune(c)
	}
	if !strings.Contains(buf.String(), "?:help") {
		t.Errorf("hint text = %q, want contains '?:help'", buf.String())
	}
}

func TestRenderCmdLine_TabHint(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{screen: s, cmd: cmdModal{
		active:  true,
		tabHint: "eng1  eng2  eng3",
	}}
	tui.renderCmdLine()

	_, sh := s.Size()
	// Tab hint is drawn one row above the input (sh-2).
	var buf strings.Builder
	for x := 0; x < 40; x++ {
		c, _, _, _ := s.GetContent(x, sh-2)
		buf.WriteRune(c)
	}
	if !strings.Contains(buf.String(), "eng1  eng2  eng3") {
		t.Errorf("tab hint = %q, want contains 'eng1  eng2  eng3'", buf.String())
	}
}
