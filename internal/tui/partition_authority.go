package tui

// partition_authority.go makes window 1 the SINGLE COMPUTER of pane ownership
// (ini-x5ob, operator decision 2026-08-14).
//
// THE INVARIANT THIS PURCHASES: pane ownership has one computer. Exactly one
// process decides which window renders which agent, and every other window
// renders the answer it is served rather than deriving its own.
//
// WHY, from the evidence rather than from taste. rendersInWindow used to be
// evaluated independently in every process, over three inputs each process
// held its own copy of: the assignment, the group map, and the connected set.
// Exclusivity was therefore not a property of the predicate -- it held only
// while the copies agreed, and one of them differed BY CONSTRUCTION
// (connectedWindowSet returns the real attached set on window 1 and a
// hardcoded {self:true} on a viewer). Disagreement was the normal case, and it
// failed in both directions:
//
//   - MODE 1, DOUBLE (eng2's ini-9isx rig): both windows render an agent. The
//     viewer matches its own assignment and believes itself connected; window
//     1 still believes that window is gone and covers for it via the orphan
//     clause.
//   - MODE 2, HOLE (the operator's live incident): neither window renders it.
//     Window 1 has the fresh assignment and correctly sees the viewer
//     attached, so it stops; the viewer's assignment copy is stale, so it
//     never starts.
//
// Serving the answer removes the disagreement class rather than shortening it:
// there is no second copy to be stale, because the viewer no longer consults
// one for ownership. That is the whole difference between this and the
// convergence palliative that was declined.
//
// SCOPE: ownership only. A viewer still renders its own panes from its own
// streams and still owns its own arrangement; this is not serve-the-view,
// which remains deferred with the residual watch open.

import "sort"

// paneOwnershipAction is the unsolicited control event carrying window 1's
// ownership decision. It rides ControlResp.Owner, keyed by CANONICAL agent
// identity (ini-yc03) so the map means the same thing in every process that
// receives it -- a window-prefixed key would be a second identity family on
// the wire and could not be looked up by the receiver.
const paneOwnershipAction = "pane_ownership"

// computePaneOwnership is THE ownership computation. Only window 1 runs it.
//
// It answers, for every agent in the fleet, which window renders it -- folding
// the agents of an absent window back into window 1, exactly as the old
// per-window predicate did. The difference is not the rule; it is that the
// rule now runs in ONE place over ONE set of inputs, so its answers cannot
// disagree with themselves.
func computePaneOwnership(panes []PaneView, a *WindowAssignment, groupOf map[string]string, connected map[string]bool) map[string]string {
	owner := make(map[string]string, len(panes))
	for _, p := range panes {
		key := agentKey(p)
		owner[key] = ownerOfAgent(key, a, groupOf, connected)
	}
	return owner
}

// ownerOfAgent answers, for ONE agent, which window renders it.
//
// It carries the fold-back rule the old per-window predicate carried: an agent
// whose window is absent is covered by window 1. The rule did not change; what
// changed is that only the authority evaluates it, once, and serves the
// result. It is deliberately unexported and called from exactly one place in
// production -- a second caller would be a second computer.
func ownerOfAgent(key string, a *WindowAssignment, groupOf map[string]string, connected map[string]bool) string {
	if a == nil {
		// No assignment store: single-window behaviour, window 1 shows all.
		return WindowOne
	}
	assigned := a.WindowOfAgent(key, groupOf)
	if assigned != WindowOne && !connected[assigned] {
		// Orphan: its window is gone, so window 1 covers for it.
		return WindowOne
	}
	return assigned
}

// ownershipEqual reports whether two ownership maps agree, so window 1 can
// broadcast on CHANGE rather than every frame.
func ownershipEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// ownershipKeysFor returns the agents a window owns, sorted, for logging and
// for tests that must compare SETS rather than counts (this bug's own trap:
// the pre- and post-move memberships were both four panes).
func ownershipKeysFor(owner map[string]string, windowID string) []string {
	var out []string
	for k, w := range owner {
		if w == windowID {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// publishPaneOwnership recomputes ownership on window 1 and serves it to every
// attached window when it has changed.
//
// Window 1 applies the result to itself directly rather than waiting for its
// own broadcast: it is the authority, and a round trip through its own server
// would make the authority the slowest window to learn its own decision.
func (t *TUI) publishPaneOwnership() {
	if !t.isFleetAuthority() {
		return
	}
	connected := t.connectedWindowSet()
	owner := computePaneOwnership(t.panes, t.assignment, t.layoutState.GroupOf, connected)

	// Re-serve when the ATTACHED SET changes even if the map did not, because
	// a window that just attached has never been served one. Without this a
	// viewer owning nothing at attach is served nothing, renders nothing (it
	// derives no fallback, by design), and stays blank until some unrelated
	// change happens to move the map -- a hole introduced by the fix itself.
	sameWindows := ownershipEqual(boolsAsStrings(connected), boolsAsStrings(t.lastServedTo))
	if ownershipEqual(owner, t.paneOwnership) && sameWindows {
		return
	}
	t.lastServedTo = connected
	t.paneOwnership = owner
	LogInfo("ownership", "recomputed",
		"window1", joinKeys(ownershipKeysFor(owner, WindowOne)))
	t.windowSrv.broadcastPaneOwnership(owner)
}

// applyServedPaneOwnership records the ownership a secondary window was served.
// It is the ONLY way a viewer learns which panes it renders.
func (t *TUI) applyServedPaneOwnership(owner map[string]string) {
	if t.isFleetAuthority() {
		// Window 1 computes its own; accepting a served map here would create
		// the second copy this design exists to remove.
		return
	}
	t.paneOwnership = owner
	t.ownershipServed = true
	LogInfo("ownership", "served",
		"window", t.windowID, "mine", joinKeys(ownershipKeysFor(owner, t.windowID)))
	t.recalcGrid(false)
	t.applyLayout()
}

// joinKeys renders a key list for logging. The ownership lines record SETS,
// like plan_set, because counts cannot distinguish stale membership from
// correct membership -- this bug's own measurement trap, where both the pre-
// and post-move memberships were four panes.
func joinKeys(keys []string) string {
	if len(keys) == 0 {
		return "(none)"
	}
	out := keys[0]
	for _, k := range keys[1:] {
		out += "," + k
	}
	return out
}

// boolsAsStrings adapts a connected-window set to ownershipEqual's shape, so
// "did the attached set change" reuses one comparison rather than growing a
// second almost-identical one.
func boolsAsStrings(m map[string]bool) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if v {
			out[k] = "1"
		}
	}
	return out
}

// currentPaneOwnership returns window 1's ownership map for the hello
// handshake. Read-only: it never recomputes, so an attaching window sees
// exactly what the attached ones were last served rather than a fresher answer
// nobody else has.
func (t *TUI) currentPaneOwnership() map[string]string {
	out := make(map[string]string, len(t.paneOwnership))
	for k, v := range t.paneOwnership {
		out[k] = v
	}
	return out
}
