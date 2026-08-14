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
	//
	// "enter to confirm", not "press enter to confirm" (ini-zjhg). MEASURED on
	// Claude Code 2.1.232: the fresh-workspace trust prompt -- a real blocking
	// option picker, the FIRST thing an agent sees in a new workspace -- footers
	// as "Enter to confirm · Esc to cancel", with no "press", no "to navigate"
	// and no "do you want to proceed". paneHasModal answered FALSE with it
	// plainly on screen, so a queued message would have pasted a body and fired
	// Enter into the highlighted option. The longer pattern was a guess that
	// matched no dialog this fleet actually renders.
	"enter to confirm",
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
// It is the UNION of two independent sources, and the union is the fix for
// ini-zjhg. The screen scan alone is a predicate about what is RENDERED, not
// about what is OPEN: a dialog that is open and waiting but has scrolled out of
// the pane's rows answers false, the drain fires, and the queued message's
// submit key answers the operator's question for them (ini-543b captured the
// bytes). The latch below answers from the application's own declaration
// instead, so scrolling cannot blind it.
//
// UNION, NEVER REPLACEMENT. The latch cannot see every dialog -- a permission
// prompt carrying custom message text emits no allowlisted OSC 777 (see
// dialogNotifyTexts), and codex-family agents emit none at all -- so the screen
// term stays as the one that catches those. Adding a term can only ever ADD
// deferrals, and that is the safe direction by the rule this guard was born
// under (ini-2jpo): a false "modal present" delays a message, a false "no
// modal" forges an operator answer.
func paneHasModal(p *Pane) bool {
	if p.dialogLatched() {
		return true
	}
	return paneShowsModalOnScreen(p)
}

// paneShowsModalOnScreen is the SCREEN term alone: is a modal RENDERED right
// now. It is not the send guard and must never be used as one.
//
// The two predicates are separate because their consumers want opposite things
// from a dialog that has scrolled out of view. The send guard must say "still
// open" (a wrong answer forges an operator response). The attention row must be
// free to say "gone" (its clear rule is deliberately screen-based and earned:
// OSC 777 emits nothing when a dialog closes, so a row can only ever be retired
// by observation -- see refreshWaitingState).
//
// Collapsing them cost a test run to discover, which is the cheapest possible
// price for the lesson: feeding the union into refreshWaitingState made
// onScreen permanently true, so no attention row could ever be retired and the
// ini-zfm phantom-row bug came back wearing the fix's clothes.
func paneShowsModalOnScreen(p *Pane) bool {
	if p == nil || p.emu == nil {
		return false
	}
	return isModalPrompt(emulatorBottomText(p.emu, modalScanWholePane))
}

// ── The dialog latch (ini-zjhg) ─────────────────────────────────────
//
// WHY THIS IS NOT p.WaitingInput(). The attention row looks like the state to
// read -- it is raised by the same OSC 777 -- but its CLEAR rule is the screen
// scan (refreshWaitingState retires the row once paneHasModal goes false and
// the dialog has been seen). Reading the row would therefore inherit the exact
// blindness this fixes, verbatim, and every test would still pass. The latch is
// separate because the two consumers have OPPOSITE risk asymmetries: a stale
// attention row is a visible nuisance the operator can ignore, while a stale
// "no dialog" is a forged answer. So the row keeps its eager, measured clear and
// the guard gets a conservative one.
//
// CLEARED BY OPERATOR INPUT, which is positive evidence rather than absence of
// evidence. A dialog can only be answered through this pane's PTY, so an answer
// implies a forwarded keystroke or click (SendKey / ForwardMouse). Our own
// injected bytes deliberately do not count -- counting them would let the first
// drain clear the very latch that is gating it. Death is handled separately, in
// refreshWaitingState: a pane whose process is gone has nobody waiting on it.

// latchDialogOpen records that the application declared a blocking dialog.
func (p *Pane) latchDialogOpen() {
	p.mu.Lock()
	p.dialogOpen = true
	p.mu.Unlock()
}

// noteOperatorInput clears the dialog latch: the operator has acted on this
// pane, which is the only way a dialog gets answered. Called from the operator
// input paths only.
func (p *Pane) noteOperatorInput() {
	p.mu.Lock()
	p.dialogOpen = false
	p.mu.Unlock()
}

// dialogLatched reports whether a declared dialog is still believed open.
func (p *Pane) dialogLatched() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dialogOpen
}
