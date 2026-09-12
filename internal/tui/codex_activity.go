// codex_activity.go derives a Codex pane's running/idle state from its
// RENDERED screen instead of from PTY byte recency (ini-lnnk).
//
// WHY BYTES CANNOT WORK FOR CODEX. Codex CLI 0.154.0 draws an ambient
// animation at its idle prompt -- braille dots (U+2800–U+28FF) drifting across
// the composer every frame -- so an idle Codex pane emits bytes continuously
// and never accumulates the 15s of silence the byte rule needed. Measured on
// the live eng3 pane 2026-09-12: two peeks 1.2s apart, six rows changed, the
// agent at rest the whole time. initech status said running, super hired a
// second engineer for a P1 eng3 could have taken, the idle-with-bead net could
// never fire for it, and every send to it took the queued-submit path.
//
// WHAT DISCRIMINATES. The animation runs while Codex is WORKING too (the same
// capture shows "• Working (2m 40s • esc to interrupt)" above an animated
// prompt), so the animation is noise on both frames. The status line is the
// signal: Working on screen = running; the composer at rest with no Working
// line = idle; neither = hold the last state. And the dots land INSIDE the
// prompt text ("›⠁Ask Codex to do anything", captured verbatim), so every
// match strips U+2800–U+28FF first -- the tier-2 discipline (rendered rows,
// lowercase, whitespace-compact) plus that one strip.
//
// Fixtures: internal/tui/testdata/codex/0.154.0/ -- captured from the real
// eng3 pane through the TUI's own emulator, never hand-drawn. A new Codex
// version that changes the prompt or the status line changes this file only
// after a new capture.
package tui

import (
	"strings"

	"github.com/nmelo/initech/internal/config"
)

// Which rule decided a pane's activity state; logged once per transition.
const (
	activityByByteRecency  = "byte-recency"
	activityByCodexScreen  = "codex-screen"
	activityByWaitingInput = "waiting-input"
	activityByLiveness     = "liveness"
)

// codexWorkingPrefix is the normalized start of Codex's status line while it
// works: "• Working (2m 40s • esc to interrupt)". The "esc to interrupt" tail
// is truncated at pane width in the capture, so only the head is relied on.
const codexWorkingPrefix = "•working("

// codexComposerPrefix is Codex's composer chevron.
const codexComposerPrefix = "›"

// codexIdlePlaceholder is the normalized composer-at-rest row: Codex 0.154.0
// shows this placeholder only while the composer is EMPTY, which is what
// "at rest" means. Prefix, not equality: the animation parks dots after the
// text too ("› Ask Codex to do anything         ⠈"), and those are harmless
// even unstripped -- it is the dot INSIDE the text ("›⠁Ask Codex…") that
// only the strip can rescue, and that one row is what the strip is for.
const codexIdlePlaceholder = codexComposerPrefix + "askcodextodoanything"

// stripBraille removes the ambient animation's glyphs (U+2800–U+28FF).
func stripBraille(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0x2800 && r <= 0x28FF {
			return -1
		}
		return r
	}, s)
}

// normalizeCodexRow is the one normalization every Codex signature match goes
// through: strip the animation, lowercase, fold the curly apostrophe, compact
// whitespace.
func normalizeCodexRow(row string) string {
	return compactPromptText(strings.ToLower(strings.ReplaceAll(stripBraille(row), "’", "'")))
}

// codexScreenSignature reads a Codex pane's rendered rows and reports
// (StateRunning, true) when the Working status line is on screen,
// (StateIdle, true) when the composer is at rest with no Working line and no
// startup (trust / MCP boot) screen, and ok=false when neither signature is
// present -- the caller holds its last state then.
//
// The composer counts as at rest only when it shows the placeholder or is
// bare; see codexIdlePlaceholder.
func codexScreenSignature(rows []string) (ActivityState, bool) {
	composer := false
	var all strings.Builder
	for _, row := range rows {
		n := normalizeCodexRow(row)
		if n == "" {
			continue
		}
		if strings.HasPrefix(n, codexWorkingPrefix) {
			return StateRunning, true
		}
		all.WriteString(n)
		all.WriteByte('\n')
		// The composer at rest: the placeholder, or a bare chevron (the
		// composer-only frames older fixtures carry). A composer holding text
		// -- typed input, or a dialog's "› 1. Yes, continue" -- is not at
		// rest and yields no signature, so the caller holds its state.
		if n == codexComposerPrefix || strings.HasPrefix(n, codexIdlePlaceholder) {
			composer = true
		}
	}
	if !composer {
		return StateRunning, false
	}
	screen := all.String()
	for _, pattern := range codexNotReadyPromptPatterns {
		if strings.Contains(screen, compactPromptText(pattern)) {
			return StateRunning, false
		}
	}
	return StateIdle, true
}

// paneUsesCodexScreenActivity reports whether this pane's activity comes from
// the screen signature: Codex only. opencode keeps byte recency until a real
// opencode pane is captured (none exists in this fleet, ini-lnnk AC8) -- a
// predicate measured on one program must not be assumed for another.
func paneUsesCodexScreenActivity(agentType string) bool {
	return config.NormalizeAgentType(agentType) == config.AgentTypeCodex
}

// codexShouldQueueSubmit is THE decision both send sites make: a Codex pane
// that is running gets its submit queued rather than injected mid-task. It
// now follows the screen-derived state, so an idle Codex takes the direct
// path (ini-lnnk AC5) instead of being permanently "busy".
func codexShouldQueueSubmit(pane *Pane) bool {
	return pane.AgentType() == config.AgentTypeCodex && pane.Activity() == StateRunning
}
