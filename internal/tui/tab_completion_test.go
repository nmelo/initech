package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// ── longestCommonPrefix ───────────────────────────────────────────────

func TestLongestCommonPrefix(t *testing.T) {
	tests := []struct {
		input []string
		want  string
	}{
		{[]string{"eng1", "eng2"}, "eng"},
		{[]string{"eng1"}, "eng1"},
		{[]string{"qa1", "qa2"}, "qa"},
		{[]string{"super", "eng1"}, ""},
		{[]string{}, ""},
		{[]string{"abc", "abc"}, "abc"},
	}
	for _, tt := range tests {
		if got := longestCommonPrefix(tt.input); got != tt.want {
			t.Errorf("longestCommonPrefix(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── completionCandidates ──────────────────────────────────────────────

func TestCompletionCandidatesAll(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
		newTestPane("eng2", true),
	)
	for _, cmd := range []string{"focus", "remove", "rm", "restart", "r"} {
		got := tui.completionCandidates(cmd)
		if len(got) != 3 {
			t.Errorf("completionCandidates(%q) = %v, want 3 names", cmd, got)
		}
	}
}

func TestCompletionCandidatesHide(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
		newTestPane("eng2", false), // hidden
	)
	got := tui.completionCandidates("hide")
	if len(got) != 2 {
		t.Errorf("completionCandidates(hide) = %v, want 2 visible panes", got)
	}
	for _, name := range got {
		if name == "eng2" {
			t.Error("completionCandidates(hide) should not include hidden pane eng2")
		}
	}
}

func TestCompletionCandidatesShow(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", false), // hidden
		newTestPane("eng2", false), // hidden
	)
	got := tui.completionCandidates("show")
	// Should include hidden panes + "all".
	if len(got) != 3 {
		t.Errorf("completionCandidates(show) = %v, want [eng1 eng2 all]", got)
	}
	hasAll := false
	for _, name := range got {
		if name == "all" {
			hasAll = true
		}
	}
	if !hasAll {
		t.Error("completionCandidates(show) should include 'all'")
	}
}

// ── tabComplete ───────────────────────────────────────────────────────

func TestTabCompleteSingleMatch(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
		newTestPane("qa1", true),
	)
	tui.cmd.buf = []rune("focus s")
	tui.tabComplete()
	got := string(tui.cmd.buf)
	if got != "focus super " {
		t.Errorf("single match: buf = %q, want %q", got, "focus super ")
	}
	if tui.cmd.tabHint != "" {
		t.Errorf("single match: tabHint should be empty, got %q", tui.cmd.tabHint)
	}
}

func TestTabCompleteMultipleMatchesLCP(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
		newTestPane("eng2", true),
		newTestPane("qa1", true),
	)
	// "e" matches eng1 and eng2; LCP is "eng"
	tui.cmd.buf = []rune("focus e")
	tui.tabComplete()
	got := string(tui.cmd.buf)
	if got != "focus eng" {
		t.Errorf("LCP complete: buf = %q, want %q", got, "focus eng")
	}
	if tui.cmd.tabHint != "" {
		t.Error("LCP complete: tabHint should be empty before second Tab")
	}
}

func TestTabCompleteDoubleTapShowsHint(t *testing.T) {
	tui := newTestTUI(
		newTestPane("eng1", true),
		newTestPane("eng2", true),
	)
	// After LCP completes to "focus eng", a second Tab shows the hint.
	tui.cmd.buf = []rune("focus eng")
	tui.cmd.tabBuf = "focus eng" // Simulate same state as after first Tab.
	tui.tabComplete()
	if tui.cmd.tabHint == "" {
		t.Error("double-Tab: tabHint should show all matches")
	}
	if tui.cmd.tabHint != "eng1  eng2" {
		t.Errorf("double-Tab: tabHint = %q, want %q", tui.cmd.tabHint, "eng1  eng2")
	}
}

func TestTabCompleteEmptyArgShowsHint(t *testing.T) {
	tui := newTestTUI(
		newTestPane("eng1", true),
		newTestPane("eng2", true),
		newTestPane("qa1", true),
	)
	// Trailing space means empty arg slot; show all candidates.
	tui.cmd.buf = []rune("focus ")
	tui.tabComplete()
	if tui.cmd.tabHint == "" {
		t.Error("empty arg slot: tabHint should show all candidates")
	}
}

func TestTabCompleteNoMatchNoop(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true))
	tui.cmd.buf = []rune("focus zzz")
	tui.tabComplete()
	if string(tui.cmd.buf) != "focus zzz" {
		t.Error("no match: buf should not change")
	}
	if tui.cmd.tabHint != "" {
		t.Error("no match: tabHint should be empty")
	}
}

func TestTabCompleteNonAgentCommandNoop(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true))
	tui.cmd.buf = []rune("grid e")
	tui.tabComplete()
	if string(tui.cmd.buf) != "grid e" {
		t.Error("non-agent command: buf should not change")
	}
}

func TestTabCompleteOnlyCommandNoSpaceNoop(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true))
	// "focus" typed but no space; nothing to complete yet.
	tui.cmd.buf = []rune("focus")
	tui.tabComplete()
	if string(tui.cmd.buf) != "focus" {
		t.Error("command only (no space): buf should not change")
	}
}

func TestTabCompleteViewLastArg(t *testing.T) {
	tui := newTestTUI(
		newTestPane("qa1", true),
		newTestPane("qa2", true),
		newTestPane("eng1", true),
	)
	// "view eng1 q" — complete the last argument.
	tui.cmd.buf = []rune("view eng1 q")
	tui.tabComplete()
	got := string(tui.cmd.buf)
	if got != "view eng1 qa" {
		t.Errorf("view multi-arg: buf = %q, want %q", got, "view eng1 qa")
	}
}

func TestTabCompleteHideOnlyVisible(t *testing.T) {
	tui := newTestTUI(
		newTestPane("eng1", true),
		newTestPane("eng2", false), // hidden
	)
	// "hide e" should only complete to visible eng1.
	tui.cmd.buf = []rune("hide e")
	tui.tabComplete()
	got := string(tui.cmd.buf)
	if got != "hide eng1 " {
		t.Errorf("hide visible only: buf = %q, want %q", got, "hide eng1 ")
	}
}

func TestTabCompleteShowOnlyHidden(t *testing.T) {
	tui := newTestTUI(
		newTestPane("eng1", true),
		newTestPane("eng2", false), // hidden
	)
	// "show e" should only complete to hidden eng2.
	tui.cmd.buf = []rune("show e")
	tui.tabComplete()
	got := string(tui.cmd.buf)
	if got != "show eng2 " {
		t.Errorf("show hidden only: buf = %q, want %q", got, "show eng2 ")
	}
}

// ── handleCmdKey Tab reset ────────────────────────────────────────────

func TestTabHintClearedOnRune(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true), newTestPane("eng2", true))
	tui.cmd.active = true
	tui.cmd.buf = []rune("focus eng")
	tui.cmd.tabHint = "eng1  eng2"
	tui.cmd.tabBuf = "focus eng"

	// Typing any rune should clear the hint.
	tui.handleCmdKey(tcell.NewEventKey(tcell.KeyRune, '1', 0))
	if tui.cmd.tabHint != "" {
		t.Error("tabHint should be cleared after rune keypress")
	}
}

func TestTabHintClearedOnBackspace(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true))
	tui.cmd.active = true
	tui.cmd.buf = []rune("focus eng")
	tui.cmd.tabHint = "eng1"

	tui.handleCmdKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0))
	if tui.cmd.tabHint != "" {
		t.Error("tabHint should be cleared after backspace")
	}
}
