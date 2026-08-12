package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/hooks"
)

// Tests for ini-2x8.6's in-TUI consent surface.

func consentTUI(t *testing.T, windowID string, hooksAnswer *bool) (*TUI, *bool) {
	t.Helper()
	tui, _ := newTestTUIWithScreen("eng1", "qa1")
	tui.windowID = windowID
	tui.project = &config.Project{Name: "test", Roles: []string{"eng1"}}
	tui.project.Attention.Hooks = hooksAnswer

	var recorded *bool
	tui.onAttentionConsent = func(granted bool) {
		g := granted
		recorded = &g
	}
	return tui, recorded
}

// ── when the question is asked ───────────────────────────────────────

func TestConsentModal_AsksWhenUnsetOnWindowOne(t *testing.T) {
	tui, _ := consentTUI(t, WindowOne, nil)
	tui.maybeStartAttentionConsent()
	if !tui.attentionConsent.active {
		t.Error("window 1 with an unanswered config should raise the consent question")
	}
}

// TestConsentModal_NeverAsksOnSecondaryWindow is pm's window-1-only rule, and
// it is what makes "never blocks a headless invocation" structural for this
// surface: a viewer window has no standing to grant session-level consent, and
// prompting per window would ask one question N times.
func TestConsentModal_NeverAsksOnSecondaryWindow(t *testing.T) {
	tui, _ := consentTUI(t, "window-2", nil)
	tui.maybeStartAttentionConsent()
	if tui.attentionConsent.active {
		t.Error("a secondary window must never prompt for session-level consent")
	}
}

// TestConsentModal_NeverAsksOnceAnswered covers both recorded states. This is
// the exactly-once property, and the reason the config field is a *bool: with
// a plain bool a recorded No is indistinguishable from unset.
func TestConsentModal_NeverAsksOnceAnswered(t *testing.T) {
	for _, answered := range []bool{true, false} {
		a := answered
		tui, _ := consentTUI(t, WindowOne, &a)
		tui.maybeStartAttentionConsent()
		if tui.attentionConsent.active {
			t.Errorf("hooks=%v: re-asked an already-answered question", answered)
		}
	}
}

func TestConsentModal_NoProjectDoesNotAsk(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1")
	tui.windowID = WindowOne
	tui.maybeStartAttentionConsent()
	if tui.attentionConsent.active {
		t.Error("a TUI with no project config should not prompt")
	}
}

// ── answering ────────────────────────────────────────────────────────

func TestConsentModal_YesRecordsGranted(t *testing.T) {
	tui, _ := consentTUI(t, WindowOne, nil)
	var got *bool
	tui.onAttentionConsent = func(granted bool) { g := granted; got = &g }
	tui.maybeStartAttentionConsent()

	if !tui.handleAttentionConsentKey(tcell.NewEventKey(tcell.KeyRune, 'y', 0)) {
		t.Fatal("the modal should consume the keypress")
	}
	if tui.attentionConsent.active {
		t.Error("answering should close the modal")
	}
	if tui.project.Attention.Hooks == nil || !*tui.project.Attention.Hooks {
		t.Error("y did not record granted")
	}
	if got == nil || !*got {
		t.Error("the persist/install callback was not invoked with granted=true")
	}
}

func TestConsentModal_NoRecordsDeclined(t *testing.T) {
	tui, _ := consentTUI(t, WindowOne, nil)
	var got *bool
	tui.onAttentionConsent = func(granted bool) { g := granted; got = &g }
	tui.maybeStartAttentionConsent()
	tui.handleAttentionConsentKey(tcell.NewEventKey(tcell.KeyRune, 'n', 0))

	if tui.project.Attention.Hooks == nil || *tui.project.Attention.Hooks {
		t.Error("n did not record declined")
	}
	if got == nil || *got {
		t.Error("the callback was not invoked with granted=false")
	}
	// Declining is durable: the question does not come back.
	tui.maybeStartAttentionConsent()
	if tui.attentionConsent.active {
		t.Error("a recorded No was re-asked")
	}
}

// TestConsentModal_EscDefersAndAsksAgain pins the deliberate Esc semantics.
//
// A dismissal is NOT an answer. Recording Esc as No would permanently decline
// something the operator may never have read, via a key that means "not now"
// everywhere else in this TUI -- silently burning the single ask a consent
// question gets. Being asked again is visible and self-correcting; a recorded
// non-answer is neither.
func TestConsentModal_EscDefersAndAsksAgain(t *testing.T) {
	tui, _ := consentTUI(t, WindowOne, nil)
	var called bool
	tui.onAttentionConsent = func(bool) { called = true }
	tui.maybeStartAttentionConsent()

	if !tui.handleAttentionConsentKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0)) {
		t.Fatal("Esc should still be consumed by the modal")
	}
	if tui.attentionConsent.active {
		t.Error("Esc should close the modal")
	}
	if tui.project.Attention.Hooks != nil {
		t.Errorf("Esc recorded an answer (%v); deferral must leave the tri-state UNSET", *tui.project.Attention.Hooks)
	}
	if called {
		t.Error("Esc invoked the persist/install callback; nothing should be written on a deferral")
	}

	// Next startup asks again, and a real answer then still works.
	tui.maybeStartAttentionConsent()
	if !tui.attentionConsent.active {
		t.Fatal("a deferred question must return on the next startup")
	}
	tui.handleAttentionConsentKey(tcell.NewEventKey(tcell.KeyRune, 'y', 0))
	if tui.project.Attention.Hooks == nil || !*tui.project.Attention.Hooks {
		t.Error("answering after a deferral did not record")
	}
}

// TestConsentModal_StrayKeyDefersRatherThanAnswering: any key that is not y/n
// defers. A stray keystroke must never be read as consent, and must never fall
// through to the fleet while a modal owns the screen.
func TestConsentModal_StrayKeyDefersRatherThanAnswering(t *testing.T) {
	for _, r := range []rune{'q', 'x', ' ', '1'} {
		tui, _ := consentTUI(t, WindowOne, nil)
		tui.maybeStartAttentionConsent()
		if !tui.handleAttentionConsentKey(tcell.NewEventKey(tcell.KeyRune, r, 0)) {
			t.Errorf("%q: the modal must consume the key, not leak it to the panes", r)
		}
		if tui.project.Attention.Hooks != nil {
			t.Errorf("%q was recorded as an answer", r)
		}
	}
}

// TestConsentModal_DoesNotConsumeKeysWhenClosed guards the other direction:
// once answered, the modal must not eat the operator's keystrokes.
func TestConsentModal_DoesNotConsumeKeysWhenClosed(t *testing.T) {
	tui, _ := consentTUI(t, WindowOne, nil)
	if tui.handleAttentionConsentKey(tcell.NewEventKey(tcell.KeyRune, 'y', 0)) {
		t.Error("a closed modal consumed a keypress")
	}
}

// ── rendering ────────────────────────────────────────────────────────

// TestConsentModal_RendersTheSharedCopy asserts the modal shows the SAME text
// the init stdin flow prints, rather than a TUI-phrased paraphrase. Two
// surfaces asking one question must not be able to describe it differently --
// a forked string is how one keeps a claim the other has already corrected.
func TestConsentModal_RendersTheSharedCopy(t *testing.T) {
	tui, _ := consentTUI(t, WindowOne, nil)
	tui.maybeStartAttentionConsent()
	tui.renderAttentionConsent()

	out := screenText(t, tui)
	for _, line := range hooks.ConsentPromptLines() {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.Contains(out, trimmed) {
			t.Errorf("modal is missing shared copy line %q\n%s", trimmed, out)
		}
	}
	// And the deferral affordance is stated, since Esc's meaning is a design
	// choice the operator cannot guess.
	if !strings.Contains(out, "ask me next time") {
		t.Error("modal does not tell the operator that any other key defers")
	}
}
