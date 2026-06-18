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

// modalScanRows is how many bottom emulator rows to inspect for a modal
// signature. Generous enough to capture a multi-line AskUserQuestion box and
// its footer.
const modalScanRows = 14

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
// Code modal. It reads the emulator's bottom rows directly (like
// promptHasContent); SafeEmulator is safe for concurrent reads.
func paneHasModal(p *Pane) bool {
	if p == nil || p.emu == nil {
		return false
	}
	return isModalPrompt(emulatorBottomText(p.emu, modalScanRows))
}
