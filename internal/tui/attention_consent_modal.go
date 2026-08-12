// attention_consent_modal.go renders the one-time attention-hooks consent
// question for EXISTING projects (ini-2x8.6) -- the upgrade case, where the
// fleet is already running and `initech init`'s stdin flow never fires again.
//
// New projects are asked by init instead; both surfaces record the same
// attention.hooks tri-state, so whichever asks first is the only one that ever
// asks.
package tui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/hooks"
)

// attentionConsentModal is the first-run consent question's state.
//
// It has no expiry, unlike welcomeOverlay: a hint may time out, a consent
// question may not. Timing one out would record nothing while consuming the
// operator's attention, or worse, invite a default -- so it stays until
// answered or explicitly deferred.
type attentionConsentModal struct {
	active bool
}

// AttentionConsentAnswer is the outcome of the modal.
type AttentionConsentAnswer int

const (
	// AttentionConsentDeferred means the operator dismissed without answering.
	// The tri-state stays UNSET and the question returns next startup.
	//
	// Deliberate (ini-2x8.6): a dismissal is not an answer. Recording Esc as No
	// would permanently decline something the operator may never have read,
	// via a key that means "not now" everywhere else in this TUI -- silently
	// burning the single ask a consent question gets. Being asked again is
	// visible and self-correcting; a recorded non-answer is neither.
	AttentionConsentDeferred AttentionConsentAnswer = iota
	AttentionConsentGranted
	AttentionConsentDeclined
)

// shouldAskAttentionConsent reports whether this window should raise the
// question at startup.
//
// WINDOW 1 ONLY (pm's rule): attention.hooks is session-level consent to write
// files inside the operator's agents, and a viewer window is a view of someone
// else's session -- it has no standing to grant that, and prompting in every
// window would ask the same question N times. This is also what makes "never
// blocks a headless invocation" structural for this surface: serve, send, peek
// and viewer windows have no modal loop at all.
func (t *TUI) shouldAskAttentionConsent() bool {
	if t.project == nil || t.windowID != WindowOne {
		return false
	}
	return !t.project.Attention.HooksAnswered()
}

// maybeStartAttentionConsent raises the modal if this window should ask.
func (t *TUI) maybeStartAttentionConsent() {
	if t.shouldAskAttentionConsent() {
		t.attentionConsent.active = true
	}
}

// handleAttentionConsentKey processes a key while the consent modal is open and
// reports whether it consumed the event.
//
// Only y/n answer. Every other key defers, rather than a subset of keys
// deferring and the rest falling through to the fleet beneath -- a stray
// keystroke must not reach the panes while a modal owns the screen, and it must
// not be read as consent either.
func (t *TUI) handleAttentionConsentKey(ev *tcell.EventKey) bool {
	if !t.attentionConsent.active {
		return false
	}
	answer := AttentionConsentDeferred
	if ev.Key() == tcell.KeyRune {
		switch ev.Rune() {
		case 'y', 'Y':
			answer = AttentionConsentGranted
		case 'n', 'N':
			answer = AttentionConsentDeclined
		}
	}
	t.applyAttentionConsent(answer)
	return true
}

// applyAttentionConsent closes the modal and records the answer. A deferral
// records NOTHING, which is what keeps the tri-state at unset.
func (t *TUI) applyAttentionConsent(answer AttentionConsentAnswer) {
	t.attentionConsent.active = false
	if answer == AttentionConsentDeferred || t.project == nil {
		return
	}
	granted := answer == AttentionConsentGranted
	t.project.Attention.Hooks = &granted

	if t.onAttentionConsent != nil {
		t.onAttentionConsent(granted)
	}
}

// renderAttentionConsent draws the consent question.
//
// The text comes from hooks.ConsentPromptLines() -- the SAME constant the init
// stdin flow prints -- rather than a copy phrased for the TUI. Two surfaces
// asking one question must not be able to describe it differently, and a
// forked string is how one keeps a claim the other has already corrected.
func (t *TUI) renderAttentionConsent() {
	s := t.screen
	sw, sh := s.Size()

	lines := append([]string{}, hooks.ConsentPromptLines()...)
	lines = append(lines, "", "y = yes    n = no    any other key = ask me next time")

	boxW := 0
	for _, l := range lines {
		if n := len([]rune(l)) + 4; n > boxW {
			boxW = n
		}
	}
	if boxW > sw {
		boxW = sw
	}
	boxH := len(lines) + 2
	startX, startY := (sw-boxW)/2, (sh-boxH)/2
	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}

	bgStyle := tcell.StyleDefault.Background(tcell.NewRGBColor(20, 20, 20)).Foreground(tcell.ColorSilver)
	titleStyle := bgStyle.Foreground(tcell.ColorDodgerBlue).Bold(true)
	dimStyle := bgStyle.Foreground(tcell.ColorGray)

	for y := startY; y < startY+boxH && y < sh; y++ {
		for x := startX; x < startX+boxW && x < sw; x++ {
			s.SetContent(x, y, ' ', nil, bgStyle)
		}
	}
	for i, line := range lines {
		y := startY + 1 + i
		if y >= sh {
			break
		}
		style := bgStyle
		switch {
		case i == 0:
			style = titleStyle
		case i == len(lines)-1:
			style = dimStyle
		}
		for j, ch := range line {
			x := startX + 2 + j
			if x < startX+boxW-1 && x < sw {
				s.SetContent(x, y, ch, nil, style)
			}
		}
	}
}
