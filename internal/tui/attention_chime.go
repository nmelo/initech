package tui

// attention_chime.go rings the operator when an agent starts waiting on them,
// and then gets out of the way (ini-2x8.3).
//
// Policy, all operator-chosen: one chime on the transition INTO waiting, one
// reminder at two minutes if still waiting, then silence while the row persists.
// Chime-grade tiers only. Once per session across windows.
//
// THE TESTING TRAP THIS FILE IS SHAPED AROUND: tcell's tScreen.Beep() writes
// byte 7 through tcell's own writer, which is the correct write path -- but
// SimulationScreen.Beep() is `return nil`, a literal no-op, and every TUI test
// here runs on a SimulationScreen. So a test that calls Beep() and asserts no
// error passes while the operator hears nothing. A swallowed BEL is a SILENT
// DETECTION, the exact failure this whole feature exists to end. Hence the
// Chimer seam: tests assert on counted calls through an interface we own, never
// on tcell returning nil.

import (
	"time"

	"github.com/gdamore/tcell/v2"
)

// WaitingTier is how much a detection is trusted.
type WaitingTier int

const (
	// WaitingTierListOnly appears in the list and NEVER makes a sound. The zero
	// value, so silence is what a detector gets by default and noise is what it
	// has to ask for.
	WaitingTierListOnly WaitingTier = iota

	// WaitingTierChime is a detection trusted enough to interrupt the operator.
	// A tier earns this by measured reliability, never by assumption.
	WaitingTierChime
)

// chimeReminderDelay is how long a chime-grade wait must persist before the one
// and only reminder. Operator-chosen; there is no second reminder.
const chimeReminderDelay = 2 * time.Minute

// Chimer makes the sound. An interface so the chime can be counted in tests --
// see the file comment for why asserting on tcell directly proves nothing.
type Chimer interface {
	Chime()
}

// screenChimer rings through tcell, which owns the terminal.
//
// tcell.Screen.Beep() writes the BEL byte through tcell's OWN buffered writer
// (tscreen.go), so it interleaves correctly with the frames tcell is drawing.
// Writing byte 7 to os.Stdout directly is the swallowed-BEL failure: tcell owns
// that fd and the stray byte lands wherever the current frame happens to be.
type screenChimer struct{ screen tcell.Screen }

func (c screenChimer) Chime() {
	if c.screen == nil {
		return
	}
	_ = c.screen.Beep()
}

// chimeState tracks what has already been announced for one waiting agent.
type chimeState struct {
	since    time.Time // The wait this state belongs to. Identifies the episode.
	reminded bool      // The single 2-minute reminder has been spent.
}

// attentionChimes decides what to ring for the current set of waiting agents and
// rings it. Called once per render tick.
//
// Returns the number of chimes emitted so callers and tests can reason about it
// without reaching into the Chimer.
func (t *TUI) attentionChimes(now time.Time) int {
	// Only window 1 makes noise. Every window RENDERS the list -- the operator
	// asked for it everywhere -- but a session with four windows attached must
	// ring once, not four times, so the sound is pinned to one window while the
	// sight is not.
	if t.windowID != "" && t.windowID != WindowOne {
		return 0
	}
	if t.chime == nil || t.attentionSound == "none" {
		// Config off: no sound, and the list is untouched. Deliberately still
		// walks the bookkeeping below so turning sound back on mid-session does
		// not replay every wait already in progress.
		t.reconcileChimeState(now)
		return 0
	}

	fired := 0
	live := make(map[string]bool)
	for _, p := range t.panes {
		wp, ok := p.(waitingPane)
		if !ok {
			continue
		}
		waiting, since, _ := wp.WaitingInput()
		if !waiting {
			continue
		}
		key := paneKey(p)
		live[key] = true

		// Tier gate. A list-only detection is visible and silent; it never
		// becomes audible by waiting longer, because time does not make a
		// guess more likely to be right.
		tp, ok := p.(tieredWaitingPane)
		if !ok || tp.WaitingTierOf() != WaitingTierChime {
			// Still record the episode, so a detection that is UPGRADED to
			// chime-grade mid-wait does not then fire a rising-edge chime for a
			// wait the operator has already been looking at.
			if st, seen := t.chimeSeen[key]; !seen || !st.since.Equal(since) {
				t.chimeSeen[key] = chimeState{since: since}
			}
			continue
		}

		st, seen := t.chimeSeen[key]
		switch {
		case !seen || !st.since.Equal(since):
			// Rising edge: either a brand-new wait, or a different wait for the
			// same agent. Comparing the START TIME rather than a boolean is what
			// makes answer-then-immediately-ask-again ring twice, correctly,
			// instead of being swallowed as "already chimed for this pane".
			t.chimeSeen[key] = chimeState{since: since}
			t.chime.Chime()
			fired++
		case !st.reminded && now.Sub(since) >= chimeReminderDelay:
			st.reminded = true
			t.chimeSeen[key] = st
			t.chime.Chime()
			fired++
		}
	}

	t.pruneChimeState(live)
	return fired
}

// tieredWaitingPane is the tier half of the waiting contract, kept separate so a
// pane kind can surface in the list without claiming a tier it cannot justify.
type tieredWaitingPane interface {
	WaitingTierOf() WaitingTier
}

// reconcileChimeState keeps the bookkeeping current without ringing. Used when
// sound is off, so that switching it on mid-session announces only NEW waits
// rather than replaying every wait already on screen.
func (t *TUI) reconcileChimeState(now time.Time) {
	live := make(map[string]bool)
	for _, p := range t.panes {
		wp, ok := p.(waitingPane)
		if !ok {
			continue
		}
		waiting, since, _ := wp.WaitingInput()
		if !waiting {
			continue
		}
		key := paneKey(p)
		live[key] = true
		if st, seen := t.chimeSeen[key]; !seen || !st.since.Equal(since) {
			// Mark the reminder spent: a wait that began while sound was off has
			// already gone unannounced, and firing its 2-minute reminder the
			// moment sound comes back would be a chime for an old event.
			t.chimeSeen[key] = chimeState{since: since, reminded: now.Sub(since) >= chimeReminderDelay}
		}
	}
	t.pruneChimeState(live)
}

// pruneChimeState drops bookkeeping for agents no longer waiting, so an answered
// agent that asks again later gets a fresh rising edge, and the map cannot grow
// without bound across a long session.
func (t *TUI) pruneChimeState(live map[string]bool) {
	for key := range t.chimeSeen {
		if !live[key] {
			delete(t.chimeSeen, key)
		}
	}
}
