// foldback.go implements ini-9ka.7: when a secondary window disconnects --
// cleanly or by crashing -- its agents render in window 1 instead, and
// reattaching hands them back exactly.
//
// THE WHOLE MECHANISM IS A RENDER-TIME PREDICATE, NOT A DISCONNECT HANDLER.
// Fold-back mutates rendering only; the assignment store (ini-9ka.4) is never
// written during it. Three properties fall out of that choice rather than
// having to be separately maintained:
//
//   - "Assignment read back unmodified, not re-derived": nothing writes, so
//     there is nothing to re-derive. Reattach needs no restore code at all --
//     the window reappears in the connected set and the predicate flips back.
//   - "Within one render cycle": the render path evaluates this fresh each
//     frame, so the first frame after a disconnect already folds back. There
//     is no queued action that could lag.
//   - "Never hidden, never stopped, including between detection and
//     re-render": there is no mutation sequence to catch a pane mid-way
//     through. Any frame sees a window as connected or not, and in both cases
//     every agent satisfies the predicate for exactly one window.
//
// Detection is transport-level and needs no code here: a window's process
// dying (cleanly or via kill -9) closes its TCP connection, the control-stream
// read errors, and the client leaves the connected set. Both of the AC's cases
// converge on that one signal, which is why they share one consequence.
package tui

import "sort"

// rendersInWindow reports whether windowID should render the given agent,
// given the current assignment and the set of currently-attached secondary
// windows. This is the single source of truth for "which window shows this
// agent", read by the render path and by fold-back alike.
//
// connected holds the identities of attached SECONDARY windows. Window 1 is
// never in it: window 1 is the session itself, so if it is gone there is
// nothing left to render into.
//
// The two branches are deliberately asymmetric, because the windows are:
//
//   - A secondary window renders exactly what is assigned to it, and only
//     while it is itself attached. The liveness check is what stops a
//     disconnected window from still "claiming" its agents and leaving them
//     rendered by nobody.
//   - Window 1 renders what is assigned to it, PLUS anything whose assigned
//     window is not attached. That is fold-back, and it is stated as a
//     property of window 1 rather than an event so it cannot be missed.
//
// Together those make "exactly one window renders each agent" true in every
// liveness state, which is the invariant AC 5 turns on.
func rendersInWindow(agentKey, windowID string, a *WindowAssignment, groupOf map[string]string, connected map[string]bool) bool {
	if a == nil {
		// No assignment store: single-window behavior, window 1 shows all.
		return windowID == WindowOne
	}
	assigned := a.WindowOfAgent(agentKey, groupOf)

	if windowID != WindowOne {
		return assigned == windowID && connected[windowID]
	}
	// Window 1: its own agents, plus orphans from any window that is gone.
	return assigned == WindowOne || !connected[assigned]
}

// foldedBackAgents returns the agents currently folded back into window 1 --
// those assigned to a secondary window that is not attached. Used to raise the
// session-level notice, and to answer "what is window 1 covering for right
// now" without recomputing the predicate at the call site.
//
// Returns them in the caller's agent order so the notice is stable frame to
// frame rather than reordering with map iteration.
func foldedBackAgents(agentKeys []string, a *WindowAssignment, groupOf map[string]string, connected map[string]bool) []string {
	if a == nil {
		return nil
	}
	var out []string
	for _, key := range agentKeys {
		assigned := a.WindowOfAgent(key, groupOf)
		if assigned != WindowOne && !connected[assigned] {
			out = append(out, key)
		}
	}
	return out
}

// windowLivenessTracker turns the connected-window snapshot into transitions,
// so the session-level notice fires once when a window goes away or comes
// back rather than every frame it stays gone.
//
// It is deliberately separate from the rendering predicate. The predicate is
// stateless and must stay that way -- it answers "where does this agent render
// right now" from current inputs only. Notices, by contrast, are inherently
// about change, so the small amount of remembered state lives here where it
// cannot affect what gets rendered.
type windowLivenessTracker struct {
	seen   map[string]bool // windows connected as of the last observation
	primed bool            // false until the first observation establishes a baseline
}

func newWindowLivenessTracker() *windowLivenessTracker {
	return &windowLivenessTracker{seen: make(map[string]bool)}
}

// observe records the current connected set and reports which windows left and
// which returned since the previous call. Both slices are sorted so notices
// are deterministic rather than ordered by map iteration.
//
// The first observation reports no transitions: windows already attached when
// tracking starts have not "arrived", and treating them as arrivals would fire
// a restore notice for every window at startup.
func (t *windowLivenessTracker) observe(connected map[string]bool) (gone, returned []string) {
	if t.seen == nil {
		t.seen = make(map[string]bool)
	}
	// The first observation only establishes the baseline. Windows already
	// attached at that moment have not just arrived, and announcing them
	// would fire a spurious restore notice per window whenever tracking
	// starts against a live session.
	if !t.primed {
		t.primed = true
		next := make(map[string]bool, len(connected))
		for w := range connected {
			next[w] = true
		}
		t.seen = next
		return nil, nil
	}
	for w := range t.seen {
		if !connected[w] {
			gone = append(gone, w)
		}
	}
	for w := range connected {
		if !t.seen[w] {
			returned = append(returned, w)
		}
	}
	next := make(map[string]bool, len(connected))
	for w := range connected {
		next[w] = true
	}
	t.seen = next
	sort.Strings(gone)
	sort.Strings(returned)
	return gone, returned
}
