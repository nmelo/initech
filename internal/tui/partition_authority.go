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
	return snapshotOwnershipInputs(panes, a, groupOf).ownershipFor(connected)
}

// ownerOfAgent answers, for ONE agent, which window renders it.
//
// It carries the fold-back rule the old per-window predicate carried: an agent
// whose window is absent is covered by window 1. The rule did not change; what
// changed is that only the authority evaluates it, once, and serves the
// result. It is deliberately unexported and called from exactly one place in
// production -- a second caller would be a second computer.
// ownershipInputs is an IMMUTABLE copy of everything the partition decision
// needs. The authority publishes one on its own loop; any goroutine may read
// it.
//
// It exists because the handshake must answer correctly. Window 1 used to
// hand an attaching window whatever map it had last computed -- necessarily
// computed while that window was still absent, so it said "window 1 owns
// everything", and the correct answer arrived later in a broadcast. That is a
// RACE, and it lost often enough to fail this bead's own rig on one run and
// pass on the next with identical code. A viewer must be told what it owns in
// the same breath it is admitted.
//
// Recomputing inside the handshake was the obvious alternative and is worse:
// it runs on the daemon's connection goroutine, so it would have to marshal
// onto the main loop, and an attach that waits on the render loop can wedge.
type ownershipInputs struct {
	agents      []string
	groupOf     map[string]string
	groupWindow map[string]string
	hasStore    bool
}

// snapshotOwnershipInputs freezes the decision's inputs.
func snapshotOwnershipInputs(panes []PaneView, a *WindowAssignment, groupOf map[string]string) *ownershipInputs {
	in := &ownershipInputs{
		groupOf:  make(map[string]string, len(groupOf)),
		hasStore: a != nil,
	}
	for _, p := range panes {
		in.agents = append(in.agents, agentKey(p))
	}
	for k, v := range groupOf {
		in.groupOf[k] = v
	}
	if a != nil {
		in.groupWindow = a.GroupWindows()
	}
	return in
}

// ownerIn resolves ONE agent. Every path -- the render plan, the broadcast,
// the handshake -- lands here, because the whole point of this file is that
// the answer has one computer, and two functions that agree today are two
// functions that can disagree tomorrow.
func (in *ownershipInputs) ownerIn(key string, connected map[string]bool) string {
	if in == nil || !in.hasStore {
		// No assignment store: single-window behaviour, window 1 shows all.
		return WindowOne
	}
	group, ok := in.groupOf[key]
	if !ok {
		return WindowOne
	}
	assigned, ok := in.groupWindow[group]
	if !ok {
		return WindowOne
	}
	if assigned != WindowOne && !connected[assigned] {
		// Orphan: its window is gone, so window 1 covers for it.
		return WindowOne
	}
	return assigned
}

// ownershipFor resolves every agent in the snapshot.
func (in *ownershipInputs) ownershipFor(connected map[string]bool) map[string]string {
	owner := make(map[string]string, len(in.agents))
	for _, key := range in.agents {
		owner[key] = in.ownerIn(key, connected)
	}
	return owner
}

// ownerOfAgent resolves one agent from live state. Thin wrapper over the
// snapshot path so there is exactly one resolution.
func ownerOfAgent(key string, a *WindowAssignment, groupOf map[string]string, connected map[string]bool) string {
	return snapshotOwnershipInputs(nil, a, groupOf).ownerIn(key, connected)
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
	// SEED GROUPS BEFORE DECIDING. GroupOf is seeded lazily by ensureGroups,
	// which runs off grid layout and navigation -- i.e. only once something
	// touches the agents modal. That was survivable while every window derived
	// its own view, because the window that CARED had usually opened the grid.
	// It is not survivable once one process decides for everyone: window 1
	// would compute the partition from an empty map, WindowOfAgent would find
	// no group for any agent and hand every one of them back to window 1, and
	// a viewer would be served nothing while the assignment plainly said
	// otherwise.
	//
	// Measured, not reasoned: with eng assigned to window-2 and window-2
	// attached, window 1 logged groupOf="" and owner=all-five. The order that
	// worked only worked because moving a group goes through the modal, which
	// seeds groups on the way past.
	//
	// persist=false: seeding is a derivation, not an operator decision, and
	// this runs on every publish.
	t.ensureGroups(false)

	in := snapshotOwnershipInputs(t.panes, t.assignment, t.layoutState.GroupOf)
	t.ownershipMu.Lock()
	t.ownershipSnap = in
	t.ownershipMu.Unlock()

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

// currentPaneOwnership answers the hello handshake for the window that is
// attaching RIGHT NOW, resolved from the published snapshot rather than from
// the last map window 1 happened to compute.
//
// The peer is passed in and unioned into the connected set deliberately. It is
// already registered in the daemon's client table by this point, but relying
// on that ordering would make a correct answer depend on two files agreeing
// about registration order forever. Naming the newcomer here makes the
// handshake's answer true by construction: whoever is being admitted is
// counted as present while their own partition is computed.
func (t *TUI) currentPaneOwnership(peer string) map[string]string {
	t.ownershipMu.Lock()
	in := t.ownershipSnap
	t.ownershipMu.Unlock()
	if in == nil {
		return nil
	}
	connected := t.connectedWindowSet()
	if peer != "" {
		connected[peer] = true
	}
	return in.ownershipFor(connected)
}
