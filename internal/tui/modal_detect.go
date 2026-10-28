package tui

// modal_detect.go — detection of a target pane's blocking Claude Code modal
// (AskUserQuestion, permission prompt, etc.) from its emulator output.
//
// The send/inject path (sendPaneTextLocked) must never paste a message body or
// fire a submit key into one of these modals: the body is swallowed by the
// option picker and the submit auto-selects the highlighted option — including
// destructive defaults the operator never saw (ini-2jpo). When paneHasModal is
// true the send is deferred and re-delivered once the modal closes.

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

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
	// The Write/Edit permission prompt's second option (ini-zrdt), measured
	// on Claude Code 2.1.299 and 2.1.306: "Yes, and switch to accept edits
	// (auto-approve file edits ...)". Sufficient on its own; the prompt's
	// question ("Do you want to create w.txt?") is wording no phrase above
	// knows, which permissionPromptShape covers.
	"yes, and switch to accept edits",
}

// isModalPrompt reports whether text (a slice of the pane's recent emulator
// output) carries a blocking-modal signature: one of the phrases above, or
// the permission prompt's shape.
func isModalPrompt(text string) bool {
	compacted := compactPromptText(strings.ToLower(strings.ReplaceAll(text, "’", "'")))
	for _, pattern := range modalPromptPatterns {
		if strings.Contains(compacted, compactPromptText(pattern)) {
			return true
		}
	}
	return permissionPromptShape(text)
}

// optionOneRow is a picker's first option, with or without the cursor on it.
var optionOneRow = regexp.MustCompile(`^\s*❯?\s*1[.)]\s+\S`)

// cursorOptionRow is the picker's selected option: the ❯ cursor on a
// numbered row. A composer's ❯ prompt is never followed by a number and a
// separator.
var cursorOptionRow = regexp.MustCompile(`^\s*❯\s*\d+[.)]\s+\S`)

// permissionPromptMaxRows bounds how far below the first option the footer
// may sit: the option list, its wrapped lines, and a blank row.
const permissionPromptMaxRows = 30

// permissionPromptShape reports Claude Code's permission prompt by its shape
// rather than its words (ini-zrdt), measured from real Write and Edit prompts
// (2.1.299, 2.1.306): a question ending in "?" on the line directly above a
// numbered option list, one option carrying the ❯ cursor, and a footer below
// offering "Esc to cancel". Every part is required. A numbered list in an
// answer has no cursor on an option and no "Esc to cancel"; the resting
// composer's ❯ is not on a numbered row; a full composer rule ends the search.
func permissionPromptShape(text string) bool {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !optionOneRow.MatchString(line) {
			continue
		}
		question := ""
		for k := i - 1; k >= 0; k-- {
			if t := strings.TrimSpace(lines[k]); t != "" {
				question = t
				break
			}
		}
		if !strings.HasSuffix(question, "?") {
			continue
		}
		cursor := false
		for j := i; j < len(lines) && j < i+permissionPromptMaxRows; j++ {
			if isComposerRule(lines[j]) {
				break
			}
			if cursorOptionRow.MatchString(lines[j]) {
				cursor = true
			}
			if strings.Contains(strings.ToLower(lines[j]), "esc to cancel") {
				if cursor {
					return true
				}
				break
			}
		}
	}
	return false
}

// paneHasModal reports whether the pane is currently showing a blocking Claude
// Code modal. It reads the emulator's bottom rows via emulatorBottomTextBlocking,
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
	return screenShowsModal(emuRowsBlocking(p.emu))
}

// screenShowsModal is paneShowsModalOnScreen over an already-read screen. It
// reads only the live dialog region (liveDialogRows), never the transcript.
func screenShowsModal(rows []string) bool {
	return screenShowsLiveDialog(bottomTextFromRows(liveDialogRows(rows), modalScanWholePane))
}

// composerFrameMaxRows bounds how tall a composer (prompt plus wrapped lines)
// may be when looking for its frame.
const composerFrameMaxRows = 20

// liveDialogRows returns the rows where an OPEN dialog can be drawn: those
// below the lowest composer frame, or every row when there is no frame.
//
// MEASURED (Claude Code 2.1.299, a real permission prompt): an open dialog
// REPLACES the composer -- the bottom of the screen is the dialog, with no
// composer below it. At rest the bottom is the composer frame: a ─ rule, the
// ❯ prompt row (and its wrapped lines), a ─ rule. So dialog words ABOVE a
// composer frame are transcript: an agent quoting a dialog, printing a test
// fixture, reporting on one. Reading them as a dialog held mail at a bare
// prompt for minutes (the 9gvn class, from a new source).
//
// THE ASYMMETRY IS KEPT BY THE FALLBACK. No composer frame on screen -- a
// real open dialog, the startup trust prompt, a codex pane, a screen we do not
// recognise -- means every row is read, exactly as before. The narrowing
// applies only when the screen positively shows Claude at its prompt.
//
// The LOWEST frame, scanning up from the bottom: the live composer is the one
// nearest the bottom; a frame-shaped block higher up is history.
//
// A DIALOG ENDING RIGHT ABOVE THE FRAME STILL COUNTS. Every dialog measured on
// 2.1.299 replaces the composer, in default and in bypass-with-ask mode alike;
// but an older layout (ini-t6n's composed run) put the input box under an open
// permission prompt. So when the last non-blank row above the frame offers an
// answer (an option row, "Esc to cancel", "Enter to confirm"), the whole pane
// is read as before. Transcript ends with Claude's own lines ("✻ … done"),
// not with a dialog's answer row, so quoted dialogs above it are still skipped.
func liveDialogRows(rows []string) []string {
	top, bottom, ok := lowestComposerFrame(rows)
	if !ok || answerRowJustAbove(rows, top) {
		return rows
	}
	return rows[bottom+1:]
}

// lowestComposerFrame finds the composer frame nearest the bottom of a
// screen, the pane's width read from the screen itself: the widest row (a
// live row is padded to the pane's width; in a capture the full-width rule
// is the widest). ok is false when there is none.
func lowestComposerFrame(rows []string) (top, bottom int, ok bool) {
	width := 0
	for _, r := range rows {
		width = max(width, utf8.RuneCountInString(r))
	}
	return composerFrame(rows, width)
}

// composerFrame finds the composer frame nearest the bottom: a ─ rule at
// top, the ❯ prompt row under it, a ─ rule at bottom, at most
// composerFrameMaxRows apart. A rule spans at least composerRuleMinRunes or
// the whole pane when it is narrower (ini-gav: a 32-column pane never showed
// a 40-rune rule, so its input box was never found and a dialog latch on it
// never cleared). The top rule may end in a label, a named session's
// (ini-2tec); the bottom rule never does. The footer parser and the dialog
// guard share this one definition of the input box.
func composerFrame(rows []string, width int) (top, bottom int, ok bool) {
	minRunes := min(composerRuleMinRunes, width)
	isRule := func(row string) bool {
		t := strings.TrimSpace(row)
		n := utf8.RuneCountInString(t)
		return n > 0 && n >= minRunes && strings.Trim(t, "\u2500") == ""
	}
	isTopRule := func(row string) bool {
		if isRule(row) {
			return true
		}
		t := strings.TrimSpace(row)
		m := labelledRule.FindStringSubmatch(t)
		return m != nil && utf8.RuneCountInString(t) >= minRunes && utf8.RuneCountInString(m[1]) >= utf8.RuneCountInString(t)/2
	}
	for bottom := len(rows) - 1; bottom >= 2; bottom-- {
		if !isRule(rows[bottom]) {
			continue
		}
		top := bottom - 1
		for top > 0 && bottom-top <= composerFrameMaxRows && !isTopRule(rows[top]) {
			top--
		}
		if top <= 0 || top == bottom-1 || !isTopRule(rows[top]) || !isComposerPromptRow(rows[top+1]) {
			continue
		}
		return top, bottom, true
	}
	return 0, 0, false
}

// answerRowJustAbove reports whether the last non-blank row above row index
// frameTop offers an answer: the shape of a dialog drawn above the input box.
func answerRowJustAbove(rows []string, frameTop int) bool {
	for i := frameTop - 1; i >= 0; i-- {
		if strings.TrimSpace(rows[i]) == "" {
			continue
		}
		return screenOffersAnswer(rows[i])
	}
	return false
}

// isComposerRule reports a full-width horizontal rule: a row of '─' only,
// at least composerRuleMinRunes long. Dashed separators (╌) inside a dialog
// do not count.
func isComposerRule(row string) bool {
	t := strings.TrimSpace(row)
	if len([]rune(t)) < composerRuleMinRunes {
		return false
	}
	return strings.Trim(t, "\u2500") == ""
}

// composerRuleMinRunes keeps a short rule inside rendered output (a table, an
// insight box) from passing for the composer's full-width frame.
const composerRuleMinRunes = 40

// isComposerPromptRow reports the composer's first row: the ❯ prompt glyph at
// the start of the row. A dialog's selection cursor ("❯ 1. Yes") never sits
// directly under a full-width rule, which is what the frame check adds.
func isComposerPromptRow(row string) bool {
	return strings.HasPrefix(strings.TrimLeft(row, " "), "\u276f")
}

// screenShowsDialogText is the BROAD screen face: are a dialog's words
// rendered, whether or not anything is currently there to answer.
//
// THE THIRD CONSUMER OF THIS QUESTION, and it needs the opposite answer from
// the send guard (ini-ynrp). The attention row's clear is EARNED: a row only
// retires once the screen has PROVED it can see the dialog (markModalSeen),
// because "no modal visible" otherwise means "our patterns do not match this
// dialog" rather than "the operator answered". Feed that rule a narrow
// predicate and a dialog it cannot recognise never earns its clear, so the row
// is permanent -- which is precisely what ini-9gvn's affordance requirement did
// composed: an announced dialog with no options rendered, was never sighted,
// and its row outlived the answer in every window.
//
// WHY BROAD IS SAFE HERE, though it is not safe for the send guard: a row only
// exists because an OSC 777 DECLARED a dialog, so the words being on screen
// while a row is up means the dialog is there. Prose alone cannot raise a row.
// The send guard has no such precondition -- it must answer for any pane at any
// time -- which is why it keeps the affordance requirement and this does not.
//
// Same doctrine as zjhg's paneHasModal/paneShowsModalOnScreen split (opposite
// RISK asymmetries) and l5sy's scoped/fleet members split (opposite SCOPES):
// when consumers want different answers in the edge case, they get their own
// names, never a union.
func screenShowsDialogText(rows []string) bool {
	return isModalPrompt(bottomTextFromRows(rows, modalScanWholePane))
}

// screenShowsLiveDialog distinguishes a DIALOG from a QUOTATION of one
// (ini-9gvn): the prompt's words must be there AND something must be there to
// answer.
//
// MEASURED FROM THE LIVE SPECIMEN. An agent's pane containing prose ABOUT a
// dialog held the whole fleet's mail for ~30 minutes: every send to it
// deferred with "target has a modal open" while nothing was open. The pane
// read "eDoyouwanttoproceed?." -- a report about compactPromptText quoting its
// own compacted output -- and since isModalPrompt compacts whitespace out of
// BOTH sides, that prose contains "doyouwanttoproceed" exactly. The matcher
// was tripped by a bug report about the matcher.
//
// The discriminator is the ANSWER AFFORDANCE, because that is what a dialog
// has and a description of one does not: a real blocking prompt always offers
// something to choose. Verified against both real artifacts -- the captured
// specimen has no option row and no option vocabulary anywhere on screen,
// while a live 2.1.233 permission dialog renders "Do you want to proceed?"
// directly above "❯ 1. Yes".
//
// THE SCAN REGION IS DELIBERATELY UNCHANGED. Narrowing to the bottom rows
// would also have fixed the specimen, and it is the one change this file
// already documents as harmful: a real dialog gets pushed up by footer and
// padding, and a screen face that cannot see it answers a false NO -- which
// forges an operator response. The asymmetry rule (ini-2jpo) permits removing
// false positives only in ways that cannot add false negatives, so the fix
// adds a REQUIREMENT about content rather than a limit on where we look.
//
// KNOWN RESIDUAL, stated rather than papered over: prose that quotes a dialog
// VERBATIM, options and all, still defers. That is the asymmetry working as
// designed -- uncertain defers -- and it is why ini-9gvn also gives the queue
// a re-check and the latch a self-heal, so an over-cautious defer costs a
// short delay instead of a stalled fleet.
func screenShowsLiveDialog(text string) bool {
	if !isModalPrompt(text) {
		return false
	}
	return screenOffersAnswer(text)
}

// answerOptionRow matches a rendered choice line: an optional selection marker,
// a number, a separator, then text. Line-anchored, because a choice is a ROW in
// a dialog while a quotation wraps it into a paragraph.
var answerOptionRow = regexp.MustCompile(`(?m)^\s*[❯>»]?\s*\d+[.)]\s+\S`)

// answerVocabulary are the answer affordances a dialog renders that are not
// numbered rows. Compacted before comparison, like every other matcher here.
var answerVocabulary = []string{
	"yes, and",     // "Yes, and don't ask again"
	"no, and tell", // "No, and tell Claude what to do differently"
	"enter to confirm",
	"esc to cancel",
}

// screenOffersAnswer reports whether the screen shows something to answer WITH.
func screenOffersAnswer(text string) bool {
	if answerOptionRow.MatchString(text) {
		return true
	}
	compacted := compactPromptText(strings.ToLower(text))
	for _, v := range answerVocabulary {
		if strings.Contains(compacted, compactPromptText(v)) {
			return true
		}
	}
	return false
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
func (p *Pane) latchDialogOpen(notice string) {
	p.mu.Lock()
	raised := !p.dialogOpen
	if raised {
		p.dialogOpen = true
		p.dialogOpenAt = time.Now()
		p.dialogCorroborated = false
	}
	queued := len(p.messageQueue)
	p.mu.Unlock()
	if raised {
		// The notice text only, and only an allowlisted one: never anything a
		// tool or a message could put on the line.
		if !raisesAttention(notice) {
			notice = "(unlisted notice)"
		}
		LogInfo("modal", "dialog latch raised", "pane", p.name, "source", "OSC 777", "notice", notice, "queued", queued)
	}
}

// latchCorroborationWindow is how long a declared dialog has to be SEEN before
// the latch is treated as a false raise (ini-9gvn). Generous on purpose: a real
// dialog renders within a frame or two of its announcement, so anything this
// old that sight has never confirmed is not a dialog anyone can answer.
const latchCorroborationWindow = 90 * time.Second

// noteDialogSighting records that the screen has confirmed the latched dialog.
// This is what makes the latch trustworthy: a raise corroborated by sight keeps
// the earned-clear rule and is never downgraded on a timer.
func (p *Pane) noteDialogSighting() {
	p.mu.Lock()
	first := p.dialogOpen && !p.dialogCorroborated
	if p.dialogOpen {
		p.dialogCorroborated = true
	}
	p.mu.Unlock()
	if first {
		LogInfo("modal", "dialog latch corroborated", "pane", p.name, "source", "screen")
	}
}

// auditDialogLatch downgrades a latch that sight has never corroborated,
// reporting whether it did (ini-9gvn AC4).
//
// A FALSE RAISE MUST BE SELF-HEALING, NOT OPERATOR-HEALING. noteOperatorInput
// is the only other clear, so before this the sole cure for a phantom was the
// operator typing into the pane -- which an unattended fleet cannot do, and
// which is why a restart did not fix the live specimen.
//
// It never touches a CORROBORATED latch. That distinction is the whole safety
// argument: this heals raises that were never real, and leaves real dialogs to
// the earned clear. Downgrading on age alone would forge an operator answer
// into a dialog that is genuinely open, which is the failure this guard exists
// to prevent.
func (p *Pane) auditDialogLatch(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.dialogOpen || p.dialogCorroborated {
		return false
	}
	if p.dialogOpenAt.IsZero() || now.Sub(p.dialogOpenAt) < latchCorroborationWindow {
		return false
	}
	p.dialogOpen = false
	p.dialogOpenAt = time.Time{}
	return true
}

// idlePromptStableWindow is how long a pane must render its normal idle
// composer before a CORROBORATED latch is cleared on that evidence.
//
// Five ticks of modalMaintenance. Long enough that a transient repaint between
// two dialogs is not read as a return to rest; short enough that mail held
// behind a dialog that closed itself moves in seconds rather than hours (the
// live specimen deferred for HOURS).
const idlePromptStableWindow = 5 * time.Second

// paneShowsIdleComposer reports POSITIVE evidence that the pane is at rest:
// its prompt is on screen AND no dialog is rendered.
//
// THE ASYMMETRY OF ini-2jpo IS PRESERVED BY REQUIRING THE PROMPT, and that is
// the whole safety argument. A dialog that merely SCROLLED OUT OF VIEW shows
// neither a dialog nor an idle prompt, so this returns false and the latch
// stands -- absence of a dialog is still never treated as proof it closed.
// What clears the latch is not "I cannot see a dialog" but "I can see the
// application redrawn at its resting state", which a blocking modal does not
// permit. Different KIND of evidence, not a weaker threshold on the same kind.
func paneShowsIdleComposer(p *Pane) bool {
	if p == nil || p.emu == nil {
		return false
	}
	return screenShowsIdleComposer(emuRowsBlocking(p.emu))
}

// screenShowsIdleComposer is paneShowsIdleComposer over an already-read screen.
//
// THE PROMPT MUST BE THE COMPOSER FRAME, not any ❯ on screen (ini-0g5s).
// A dialog's own selection cursor ("❯ 1. Yes") is a ❯ too, and a dialog whose
// wording the matchers do not know (a Write-file prompt: "Do you want to
// create w.txt?") reads as no dialog, so "no dialog + a ❯" called an open
// dialog idle and the latch cleared under it. Idle now needs Claude's measured
// resting shape: the lowest composer frame, nothing below it offering an
// answer (a fake frame inside a dialog's preview has the options under it),
// and no answer row right above it (the older layout with the input box under
// a live dialog).
func screenShowsIdleComposer(rows []string) bool {
	if screenShowsModal(rows) {
		return false
	}
	top, bottom, ok := lowestComposerFrame(rows)
	if !ok || answerRowJustAbove(rows, top) {
		return false
	}
	return !screenOffersAnswer(strings.Join(rows[bottom+1:], "\n"))
}

// observeIdlePrompt records whether the pane is at rest right now and returns
// how long it has been continuously so. Zero when it is not.
func (p *Pane) observeIdlePrompt(now time.Time, idle bool) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !idle {
		p.idlePromptSince = time.Time{}
		return 0
	}
	if p.idlePromptSince.IsZero() {
		p.idlePromptSince = now
	}
	return now.Sub(p.idlePromptSince)
}

// auditIdleLatch clears the latch on positive evidence that the dialog is
// gone: the pane at its idle prompt, continuously, for idlePromptStableWindow
// (ini-gbqc).
//
// auditDialogLatch deliberately never touches a corroborated latch, and that
// reasoning holds for AGE: downgrading because time passed would forge an
// operator answer into a dialog that is genuinely open. It does not cover
// POSITIVE EVIDENCE. Before this, a corroborated latch had exactly one clear --
// an operator keystroke into that pane -- so a dialog closed any other way
// (the harness resolving its own AskUserQuestion, a permission prompt
// auto-resolving under bypass, a timeout, a /compact redraw) left the pane
// deferring every message forever. Measured on the live fleet: mail held for
// HOURS behind a flag whose dialog was long gone, worked around by bypassing
// the agent entirely.
//
// IT CLEARS AN UNCORROBORATED LATCH TOO, counting only idle time AFTER the
// raise. A dialog declared over OSC 777 paints within a frame or two, so a
// pane that has then sat at its idle prompt, with nothing in the live dialog
// region, for idlePromptStableWindow is not showing a dialog: the declaration
// came from text echoed into the pane, not from an open dialog. Idle time
// from BEFORE the raise never counts -- the screen a dialog has not yet
// painted over is exactly that -- so the window starts at the raise.
func (p *Pane) auditIdleLatch(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.dialogOpen || p.idlePromptSince.IsZero() {
		return false
	}
	since := p.idlePromptSince
	if since.Before(p.dialogOpenAt) {
		since = p.dialogOpenAt
	}
	if now.Sub(since) < idlePromptStableWindow {
		return false
	}
	p.dialogOpen = false
	p.dialogCorroborated = false
	p.dialogOpenAt = time.Time{}
	p.idlePromptSince = time.Time{}
	return true
}

// noteOperatorInput clears the dialog latch: the operator has acted on this
// pane, which is the only way a dialog gets answered. Called from the operator
// input paths only.
func (p *Pane) noteOperatorInput() {
	p.clearDialogLatch("operator input")
}

// clearDialogLatch drops the latch and, if it was raised, says why once.
func (p *Pane) clearDialogLatch(by string) {
	p.mu.Lock()
	was := p.dialogOpen
	p.dialogOpen = false
	p.mu.Unlock()
	if was {
		LogInfo("modal", "dialog latch cleared", "pane", p.name, "by", by)
	}
}

// latchAge reports how long the dialog latch has been raised, and whether it
// is raised at all. For telling a sender "just dialoged" from "stuck".
func (p *Pane) latchAge(now time.Time) (time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.dialogOpen || p.dialogOpenAt.IsZero() {
		return 0, p.dialogOpen
	}
	return now.Sub(p.dialogOpenAt), true
}

// screenSkipLogEvery rate-limits the "emulator lock held" record. Once per
// tick would be a log line per second per wedged pane; never would be the
// invisibility class this fleet keeps paying for (ini-9gvn).
const screenSkipLogEvery = 30 * time.Second

// noteScreenSkipped records that the main loop could not read this pane's
// screen because its emulator lock was held, and says so at INFO, rate-limited
// across all readers (render, attention, modal, resize).
func (p *Pane) noteScreenSkipped(now time.Time, reader string) {
	p.mu.Lock()
	if p.screenSkipSince.IsZero() {
		p.screenSkipSince = now
	}
	since := p.screenSkipSince
	log := now.Sub(p.screenSkipLogged) >= screenSkipLogEvery
	if log {
		p.screenSkipLogged = now
	}
	p.mu.Unlock()
	if log {
		LogInfo("screen", "screen read SKIPPED: emulator lock held; pane keeps its last state",
			"pane", p.name, "reader", reader, "held_for", now.Sub(since).Round(time.Second))
	}
}

// noteScreenRead clears the skip record once a read succeeds.
func (p *Pane) noteScreenRead() {
	p.mu.Lock()
	p.screenSkipSince = time.Time{}
	p.mu.Unlock()
}

// dialogLatched reports whether a declared dialog is still believed open.
func (p *Pane) dialogLatched() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dialogOpen
}

// modalMaintenance is the once-a-second housekeeping the modal guard needs to
// be self-correcting rather than operator-correcting (ini-9gvn).
//
// Three jobs, all of which the guard previously left undone:
//   - SIGHT corroborates a latch, so a real dialog keeps its earned clear.
//   - An uncorroborated latch is downgraded, loudly, so a false raise heals
//     itself instead of holding the fleet's mail until someone types.
//   - A deferred queue is retried WITHOUT the recipient producing output.
//     Deferral used to drain only from readLoop, so an idle pane held mail
//     indefinitely -- the fleet looked stalled and nothing was wrong with it.
//
// Rate-limited rather than per-frame: this walks panes and reads screens, and
// there is no reason to do that at render cadence.
func (t *TUI) modalMaintenance(now time.Time) {
	maintainModal(t.panes, now, &t.lastModalMaint)
}
