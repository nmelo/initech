package tui

// modal_detect.go — detection of a target pane's blocking Claude Code modal
// (AskUserQuestion, permission prompt, etc.) from its emulator output.
//
// The send/inject path (sendPaneTextLocked) must never paste a message body or
// fire a submit key into one of these modals: the body is swallowed by the
// option picker and the submit auto-selects the highlighted option — including
// destructive defaults the operator never saw (ini-2jpo). When paneHasModal is
// true the send is deferred and re-delivered once the modal closes.

import "strings"

// modalScanWholePane makes the modal scan cover EVERY rendered row of the pane
// rather than a fixed count of bottom rows.
//
// It was a fixed 14 rows until ini-t6n, and that was a fixed-window fence in a
// variable-window world. Measured in a composed 50-row TUI: a permission
// prompt's "Do you want to proceed?" anchor sat ~16 rows above the pane bottom
// -- options, the esc/tab/ctrl+e hints, a spacer, the input box, the bypass
// footer and padding all push it up -- so the scan never saw it. paneHasModal
// stayed false with the dialog plainly on screen, markModalSeen never set, and
// the attention row could never be retired. The signature captures that chose
// 14 were real but taken in a raw 40-row PTY, where the same dialog sits much
// lower; the composed pane's own chrome is what moved it.
//
// AskUserQuestion anchored ~12 rows up and worked, which brackets the boundary
// AT THAT PANE SIZE ONLY. Any other number would just relocate the cliff.
//
// The scan reads already-rendered rows, so covering the whole pane is cheap.
// The tradeoff is honest: a wider window can match text that is merely VISIBLE
// rather than part of a live dialog. That direction is the safe one for the
// send-deferral consumer -- a false "modal present" defers a send, while a
// false "no modal" auto-confirms a destructive default (ini-2jpo, a P1) -- and
// for the attention consumer it trades a measured, reproducible stale row for a
// speculative one.
const modalScanWholePane = 0

// modalPromptPatterns are substrings (lowercased, whitespace-compacted) that
// identify a blocking Claude Code modal. They are deliberately conservative:
// each must appear in a real modal but NOT in a normal "❯ " input prompt, a
// running "esc to interrupt" spinner, or a codex "›" prompt — otherwise routine
// sends would be wrongly deferred.
var modalPromptPatterns = []string{
	// AskUserQuestion / selection-modal footer ("↑/↓ to navigate",
	// "up/down to navigate"). The running spinner says "esc to interrupt",
	// never "to navigate", so this does not false-match a busy agent.
	"to navigate",
	// Claude permission prompts.
	"do you want to proceed",
	"yes, and don't ask again",
	"yes, and dont ask again",
	"no, and tell claude",
	// Generic confirm dialogs (incl. codex-native).
	"press enter to confirm",
}

// isModalPrompt reports whether text (a slice of the pane's recent emulator
// output) carries a blocking-modal signature.
func isModalPrompt(text string) bool {
	compacted := compactPromptText(strings.ToLower(strings.ReplaceAll(text, "’", "'")))
	for _, pattern := range modalPromptPatterns {
		if strings.Contains(compacted, compactPromptText(pattern)) {
			return true
		}
	}
	return false
}

// paneHasModal reports whether the pane is currently showing a blocking Claude
// Code modal. It reads the emulator's bottom rows via emulatorBottomText,
// which uses SafeEmulator.RowText — NOT the raw pointer-returning CellAt.
// SafeEmulator.CellAt is NOT safe for concurrent reads: it releases its lock
// before returning a pointer into the live buffer, so a caller dereferencing
// the pointee afterward races a concurrent Write (ini-wizq). RowText copies
// under the lock instead, which is what makes this call safe.
func paneHasModal(p *Pane) bool {
	if p == nil || p.emu == nil {
		return false
	}
	return isModalPrompt(emulatorBottomText(p.emu, modalScanWholePane))
}
