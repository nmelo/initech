package tui

// attention_detect.go is tier-1 detection: it decides WHEN an agent is blocked
// on the operator (ini-2x8.2).
//
// Nelson's decision, 2026-08-12: OSC 777 is tier-1. Claude Code writes
//
//	ESC ] 777 ; notify ; Claude Code ; Claude needs your permission BEL
//
// into the PTY the moment a blocking dialog opens -- questions and permission
// prompts alike. The application declaring its own state beats every inference
// tier the spec originally proposed: no consent, no agent-side writes, no
// polling, and it arrives on the stream the emulator already reads.
//
// THE JOURNAL IS NOT A DETECTION TIER. Measured on three real dialogs held open
// 85s, 75s and 75s: the pending tool_use never reached the JSONL while the
// dialog was open, because the assistant entry is not flushed until the turn
// unblocks. It lands only after the operator answers, when detection is
// worthless. It survives as post-answer enrichment and nothing else -- do not
// describe it as detection anywhere.
//
// RAISING AND CLEARING ARE ASYMMETRIC ON PURPOSE:
//
//   - RAISE from OSC 777 only. It is precise, so it can be trusted with the
//     chime, and a false chime is a defect by the operator's own rule.
//   - CLEAR from the screen -- because measurement says there is nothing else.
//     A full open/answer cycle emits OSC 777 exactly once, at open. Nothing is
//     emitted when the dialog closes, so a clear signal has to be observed
//     rather than received.
//
// And the screen has to EARN the right to clear: it may only retire a row after
// it has first SEEN that row's dialog. Otherwise a dialog whose wording our
// patterns do not match would be raised by OSC 777 and cleared by the very next
// screen poll -- the row would flicker and vanish, which is the original bug
// wearing a new hat.

import (
	"bytes"
	"regexp"
	"strings"
	"sync"

	"github.com/nmelo/initech/internal/config"
)

// oscNotify is the OSC command number Claude Code uses for desktop
// notifications. Measured, not guessed: see the signature capture on ini-2x8.
const oscNotify = 777

// oscNotifyPrefix is the payload prefix that marks a notification. The full
// measured payload is "notify;Claude Code;Claude needs your permission".
const oscNotifyPrefix = "notify;"

// oscNotifyCmdPrefix is the command number as it appears at the head of the
// payload x/vt hands to the handler.
const oscNotifyCmdPrefix = "777;"

// attentionSignal is the mailbox the OSC handler writes into.
//
// It carries its OWN mutex and nothing else touches it, which is the point: the
// handler runs inside Emulator.Write, under the emulator's write lock and the
// pane's renderMu. Taking a leaf-level lock there is safe; reaching for the
// pane's main mutex from that position would invite a lock-order inversion with
// every other path that goes pane-lock-then-render-lock. So the handler records
// and returns, and the render tick does the work.
type attentionSignal struct {
	mu      sync.Mutex
	pending bool   // A notification arrived and has not been applied yet.
	message string // The human-readable tail of the payload, as measured.
}

// noteNotify records a notification. Must stay allocation-light and lock-light:
// it runs on the PTY read path for every OSC the emulator sees.
func (a *attentionSignal) noteNotify(message string) {
	a.mu.Lock()
	a.pending = true
	a.message = message
	a.mu.Unlock()
}

// takeNotify consumes a pending notification, reporting whether there was one.
func (a *attentionSignal) takeNotify() (bool, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.pending {
		return false, ""
	}
	a.pending = false
	msg := a.message
	a.message = ""
	return true, msg
}

// registerAttentionOSC wires the OSC 777 handler onto a pane's emulator.
//
// MUST be called before the pane starts reading, because registering mutates
// the emulator's handler map while Write reads it -- doing this on a live pane
// is a data race, not merely a timing question.
func registerAttentionOSC(p *Pane) {
	if p == nil || p.emu == nil || p.attn == nil {
		return
	}
	attn := p.attn
	p.emu.RegisterOscHandler(oscNotify, func(data []byte) bool {
		// x/vt hands the handler the WHOLE OSC payload INCLUDING the command
		// number -- "777;notify;Claude Code;Claude needs your permission" -- as
		// its own OSC 0 title handler shows by reading parts[1] rather than
		// parts[0]. Stripping the "777;" here is not cosmetic: matching against
		// the un-stripped form silently never fires, which is a detector that
		// looks correct and is permanently deaf.
		payload := bytes.TrimPrefix(data, []byte(oscNotifyCmdPrefix))
		if !bytes.HasPrefix(payload, []byte(oscNotifyPrefix)) {
			return false // Not a notification; let any other handler see it.
		}
		msg := notifyMessage(string(payload))
		if !raisesAttention(msg) {
			// Consumed (it IS our sequence) but deliberately not raised.
			return true
		}
		attn.noteNotify(msg)
		return true
	})
}

// dialogNotifyTexts is the ALLOWLIST of OSC 777 notify messages that mean a
// human must act (ini-zfm).
//
// The bug this exists to prevent: OSC 777 is not the dialog signal it was taken
// for. It is Claude's general notification channel, and MEASURED on 2026-08-13,
// Claude Code 2.1.229, a turn that ended with NO dialog open emitted
//
//	ESC]777;notify;Claude Code;Claude is waiting for your input BEL
//
// at t+64s -- the idle notification, on a 60s default threshold
// (messageIdleNotifThresholdMs). Raising on ANY notify therefore gave every
// agent a needs-input row at turn-end, with an empty preview because there was
// no dialog to read, and no way to clear it because the earned-clear gate
// correctly refuses to retire a row whose dialog was never seen on screen. That
// is the operator's 4m39s phantom row.
//
// ALLOWLIST, NOT BLOCKLIST, and the difference is the whole design. Excluding
// the one known idle text would fix today's report and re-break the day Claude
// adds another notification type -- and it has NINE more already:
// agent_completed, agent_needs_input, auth_success ("Claude Code login
// successful"), computer_use_enter/exit ("Claude is done using your computer"),
// elicitation_complete/response, push_notification, worker_permission_prompt.
// Every one of those rides the same OSC door. Unknown text is EXCLUDED, so a
// new notification type can only ever cost a missed raise, never a false one.
// This mirrors the hook tier, which allowlists permission_prompt (ini-2x8.4);
// the two tiers now agree instead of disagreeing by accident.
//
// WHAT A MISS COSTS, so the tradeoff is not hidden: the permission dialog's text
// is the dialog's own message prop, not a fixed constant, so a permission dialog
// carrying custom text would not match here and would not raise at TIER 1. It
// would still be caught by tier-2 screen detection and by the hook tier -- this
// is the tier the layering exists to make fallible safely. A false chime has no
// such backstop, which is why the asymmetry runs this way.
var dialogNotifyTexts = map[string]bool{
	// MEASURED, repeatedly, on 2.1.229: the permission dialog's default message.
	"Claude needs your permission": true,
	// DECOMPILED, not measured: the elicitation dialogs dispatch this text
	// (notificationType elicitation_dialog / elicitation_url_dialog). Included
	// because its call site IS a blocking dialog, so it cannot reproduce the
	// idle-style false row; labelled so nobody mistakes it for a capture.
	"Claude Code needs your input": true,
}

// raisesAttention reports whether a notify message is one of the measured
// dialog texts. Everything else -- including the empty message a malformed
// payload yields -- is excluded by design.
func raisesAttention(message string) bool {
	return dialogNotifyTexts[message]
}

// notifyMessage pulls the human-readable tail out of a notify payload:
// "notify;Claude Code;Claude needs your permission" -> the last field.
// Returns "" when the payload has no message field.
func notifyMessage(payload string) string {
	parts := strings.Split(payload, ";")
	if len(parts) < 3 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

// refreshWaitingState applies any pending notification and decides whether an
// existing wait should be retired. Called once per pane per render tick, from
// the main goroutine.
func (p *Pane) refreshWaitingState() {
	if p.attn == nil {
		return
	}

	if notified, _ := p.attn.takeNotify(); notified {
		// Latch the send guard's dialog state from the same declaration
		// (ini-zjhg). This is the only RAISE point: the application saying so
		// is the precise signal, which is why the chime is allowed to trust it.
		// The latch and the row diverge from here on -- the row clears from the
		// screen, the guard's latch clears only on operator input.
		p.latchDialogOpen()
		// Raise. Preview starts empty: OSC 777's message is the same generic
		// "Claude needs your permission" for a question and for an approval, so
		// it says nothing worth showing. Real text comes from the screen below,
		// usually within the same tick.
		//
		// Chime-grade because the application said so itself. This is the one
		// place in the system entitled to make noise.
		p.SetWaitingInputTier(p.waitingPreviewText(), WaitingTierChime)
	}

	waiting, _, _ := p.WaitingInput()
	if !waiting {
		return
	}

	if !p.alivePane() {
		// The process is gone; nobody is waiting on an answer any more. Without
		// this the row would outlive the agent with no way to retire it.
		//
		// The dialog latch goes with it (ini-zjhg). Death is the one close this
		// pane will never see operator input for, so it is also the one way an
		// input-cleared latch could otherwise pin the queue open forever.
		p.noteOperatorInput()
		p.ClearWaitingInput()
		return
	}

	// The SCREEN term only, never the union (ini-zjhg). This consumer's whole
	// clear rule is "the screen has earned the right to retire this row"; asking
	// the union would ask a question that includes the latch, and the latch is
	// designed never to answer no on its own.
	onScreen := paneShowsModalOnScreen(p)
	if onScreen {
		p.markModalSeen()
		// Upgrade the row's text now that the dialog is actually rendered. Costs
		// nothing when the text has not changed and does not disturb the clock.
		if preview := p.waitingPreviewText(); preview != "" {
			p.SetWaitingInputTier(preview, WaitingTierChime)
		}
		return
	}

	// Not on screen. Only retire the row if the screen has PROVED it can see
	// this dialog; otherwise "no modal visible" means "our patterns do not match
	// this dialog", not "the operator answered".
	if p.modalSeen() {
		p.ClearWaitingInput()
	}
}

// markModalSeen records that the screen has confirmed this wait's dialog.
func (p *Pane) markModalSeen() {
	p.mu.Lock()
	p.waitingModalSeen = true
	p.mu.Unlock()
}

// modalSeen reports whether the screen has confirmed the current wait's dialog.
func (p *Pane) modalSeen() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitingModalSeen
}

func (p *Pane) alivePane() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.alive
}

// ── Preview text ────────────────────────────────────────────────────

// questionLineRe matches the AskUserQuestion dialog's question line: the box
// puts a "☐ <header>" line above the question and numbered options below it.
var questionLineRe = regexp.MustCompile(`(?m)^\s*☐\s*(.+?)\s*$`)

// bashCommandRe matches the permission prompt's command echo.
var bashCommandRe = regexp.MustCompile(`(?m)^\s*(Bash command|Bash)\s*$`)

// waitingPreviewText reads the open dialog off the screen and returns what the
// needs-input row should say, or "" when it cannot tell.
//
// Best-effort BY DESIGN, and note what is and is not staked on it: the CHIME is
// staked on OSC 777, which is exact. Only the row's text comes from here, so a
// miss costs a less informative row, never a false chime and never a missed one.
func (p *Pane) waitingPreviewText() string {
	if p == nil || p.emu == nil {
		return ""
	}
	text := emulatorBottomText(p.emu, modalScanWholePane)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")

	// AskUserQuestion: the question sits on the line after the "☐ header" line.
	for i, l := range lines {
		if questionLineRe.MatchString(l) {
			for _, cand := range lines[i+1:] {
				cand = strings.TrimSpace(cand)
				if cand == "" || strings.HasPrefix(cand, "❯") || numberedOption(cand) {
					continue
				}
				return cand
			}
		}
	}

	// Permission prompt: the tool and command are echoed above the
	// "Do you want to proceed?" line. Report them as "Bash: <command>", which is
	// the form the approved frame uses.
	for i, l := range lines {
		if bashCommandRe.MatchString(l) {
			for _, cand := range lines[i+1:] {
				cand = strings.TrimSpace(cand)
				if cand == "" {
					continue
				}
				return "Bash: " + cand
			}
		}
	}

	return ""
}

// numberedOption reports whether a line is one of a dialog's numbered choices
// ("1. Yes", "2. No"), which are never the question itself.
func numberedOption(s string) bool {
	s = strings.TrimPrefix(strings.TrimSpace(s), "❯")
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return false
	}
	if s[0] < '0' || s[0] > '9' {
		return false
	}
	return s[1] == '.' || (len(s) > 2 && s[1] >= '0' && s[1] <= '9' && s[2] == '.')
}

// hasTier1Signal reports whether this pane's agent declares its own waiting
// state, which is what makes tier-1 detection possible for it.
//
// Decided by agent type because that is where the measurement landed: Claude
// Code 2.1.229 emits OSC 777 on every blocking dialog (captured, and pinned by
// the live emitter probe), while codex 0.145.0 emits nothing at all -- zero
// OSC 777 and zero OSC 9 across a 13987-byte capture holding two open dialogs.
//
// Deliberately NOT "has this pane emitted an OSC 777 yet": a Claude pane has not
// emitted one before its first dialog, so that test would leave it tier-2
// eligible at startup and let the screen heuristic drive a pane that has an
// authoritative signal available.
func (p *Pane) hasTier1Signal() bool {
	if p == nil {
		return false
	}
	return !config.IsCodexLikeAgentType(p.agentType) && p.agentType != config.AgentTypeGeneric
}
