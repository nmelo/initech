package tui

import (
	"testing"

	"github.com/charmbracelet/x/vt"
)

// TestIsModalPrompt_DetectsClaudeModals verifies the modal signature matches the
// blocking Claude Code modals a send must NOT be injected into, and does NOT
// match normal prompts / running spinners (which would wrongly defer sends).
func TestIsModalPrompt_DetectsClaudeModals(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "AskUserQuestion footer",
			text: "│ ❯ 1. Yes, delete it\n│   2. Cancel\n│ Enter to select · up/down to navigate · Esc to cancel",
			want: true,
		},
		{
			name: "permission proceed prompt",
			text: "Do you want to proceed?\n❯ 1. Yes\n  2. Yes, and don't ask again\n  3. No, and tell Claude what to do differently",
			want: true,
		},
		{
			name: "permission tell-claude option",
			text: "  3. No, and tell Claude what to do differently (esc)",
			want: true,
		},
		{
			name: "press enter to confirm",
			text: "Allow this command?\nPress Enter to confirm or Esc to cancel",
			want: true,
		},
		{
			name: "normal claude input prompt",
			text: "❯ ",
			want: false,
		},
		{
			name: "claude prompt with typed text",
			text: "❯ some half-typed message",
			want: false,
		},
		{
			name: "running spinner esc to interrupt",
			text: "✻ Working… (12s · esc to interrupt)",
			want: false,
		},
		{
			name: "codex prompt glyph",
			text: "› ",
			want: false,
		},
		{
			name: "plain output",
			text: "Done. All tests pass.",
			want: false,
		},
		{
			name: "empty",
			text: "",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isModalPrompt(tc.text); got != tc.want {
				t.Errorf("isModalPrompt(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestPaneHasModal_ReadsEmulator verifies paneHasModal reads the pane's emulator
// bottom rows and matches a rendered modal but not a rendered normal prompt.
func TestPaneHasModal_ReadsEmulator(t *testing.T) {
	modalEmu := vt.NewSafeEmulator(80, 24)
	renderAskUserQuestionModal(modalEmu)
	if !paneHasModal(&Pane{emu: modalEmu}) {
		t.Error("paneHasModal = false for a rendered AskUserQuestion modal, want true")
	}

	normalEmu := vt.NewSafeEmulator(80, 24)
	_, _ = normalEmu.Write([]byte("\x1b[24;1H❯ "))
	if paneHasModal(&Pane{emu: normalEmu}) {
		t.Error("paneHasModal = true for a normal prompt, want false")
	}
}
