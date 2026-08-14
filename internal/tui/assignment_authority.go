package tui

// assignment_authority.go is the assignment store's write SEAM, modeled on
// fleet_state.go's rather than invented (ini-la97). The three parts are the
// same three, one store over:
//
//  1. one seam every keybinding and IPC path funnels through,
//  2. the authority test at that seam, routing a secondary's mutation to
//     window 1 instead of applying it locally,
//  3. the same authority re-enforced in the write PRIMITIVE (save), so no
//     present-or-future call site can reach the file by skipping the seam.
//
// docs/spec.md's Single-writer invariant is the law: the primary window is the
// sole writer of fleet-global state, a viewer requests mutations through
// window 1, and "a viewer that cannot reach window 1 declines the mutation; it
// never improvises". That last clause is why the unreachable case surfaces a
// refusal to the operator instead of applying the move locally -- applying it
// locally is precisely the two-truths state ini-i7fr traced.

import "fmt"

// GroupWindowCmd is a secondary window's request that window 1 move a group.
// It mirrors FleetStateCmd: the secondary asks, the authority writes.
type GroupWindowCmd struct {
	ID     string `json:"id,omitempty"`
	Action string `json:"action"` // "set_group_window"
	Group  string `json:"group"`  // Band label being moved.
	Window string `json:"window"` // Destination window identity.
}

// moveGroupToWindow is the seam. Every group move -- the `m` keybinding, new
// group creation, pruning a group back to window 1 -- reaches the store
// through here.
func (t *TUI) moveGroupToWindow(group, windowID string) error {
	if !t.isFleetAuthority() {
		if err := t.sendGroupWindowCmd(group, windowID); err != nil {
			t.noticeAssignmentWriteFailed("move group "+group, err)
			return err
		}
		return nil
	}
	writer, ok := t.agentsAssignment().Writer()
	if !ok {
		// The authority could not obtain a writer: the store is a read-only
		// fallback (unparseable file). Surfaced, never silently dropped.
		err := ErrAssignmentReadOnly
		t.noticeAssignmentWriteFailed("move group "+group, err)
		return err
	}
	return writer.MoveGroup(group, windowID)
}

// sendGroupWindowCmd asks window 1 to apply a group move on this secondary's
// behalf, then re-reads so this window's own modal stops showing the state it
// just asked to change -- the same follower refresh sendFleetStateCmd does.
//
// A secondary with no route to window 1 returns an error rather than writing:
// declining is the invariant's required behavior.
func (t *TUI) sendGroupWindowCmd(group, windowID string) error {
	mux := t.windowOneMux()
	if mux == nil {
		return fmt.Errorf("not connected to window 1; the move of group %q was not applied", group)
	}
	resp, err := mux.RequestRaw(GroupWindowCmd{
		Action: "set_group_window",
		Group:  group,
		Window: windowID,
	})
	if err != nil {
		return fmt.Errorf("send group move to window 1: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("window 1 refused the group move: %s", resp.Error)
	}
	t.reloadAssignmentIfFollower()
	return nil
}

// applyGroupWindowCmd is window 1's side: it applies a secondary's routed
// request through the same seam every local keybinding uses, so a routed move
// and a local move are the same write with the same guard.
func (t *TUI) applyGroupWindowCmd(cmd GroupWindowCmd) error {
	if cmd.Group == "" {
		return fmt.Errorf("set_group_window: empty group")
	}
	return t.moveGroupToWindow(cmd.Group, cmd.Window)
}

// ---------- fleet-scoped layout membership (ini-la97, pm ruling 2026-08-14) ----------

// GroupOfCmd is a secondary window's request that window 1 record an agent's
// band membership. Membership is FLEET-SCOPED state that happens to live in
// layout.yaml; the routing boundary is the scope of the state, never the file
// it sits in, so this routes exactly as an assignment move does.
//
// Agent is the CANONICAL identity -- the bare agent name. A viewer's own pane
// keys are window-prefixed presentation identities, and docs/systemdesign.md
// is explicit that prefixed forms must never be persisted; sending the bare
// name lets window 1 apply the change in its own key space without this
// message becoming a second identity family on the wire.
type GroupOfCmd struct {
	ID     string `json:"id,omitempty"`
	Action string `json:"action"` // "set_group_of"
	Agent  string `json:"agent"`  // Bare agent name.
	Label  string `json:"label"`  // Band label.
}

// agentsPersistGrouping is the seam for a band-membership change made in the
// modal. Window 1 persists directly; a viewer routes the fleet-scoped fact and
// keeps only the in-memory display update that its own modal renders from.
//
// A viewer updating its own display is NOT the "improvising" the single-writer
// invariant prohibits -- that clause is about writing state. The viewer's
// GroupOf is in-memory presentation and is never persisted; the durable fact
// is the one window 1 writes on its behalf.
func (t *TUI) agentsPersistGrouping(agentName, label string) {
	if t.isFleetAuthority() {
		t.agentsPersistOrder()
		return
	}
	// Arrangement is viewer-local: recompute and apply it in memory, but do
	// not persist it -- saveLayoutIfConfigured refuses for a viewer anyway,
	// and calling it here would only look like an attempted write.
	order := make([]string, len(t.panes))
	for i, p := range t.panes {
		order[i] = paneKey(p)
	}
	t.layoutState.Order = order
	t.applyLayout()

	if err := t.sendGroupOfCmd(agentName, label); err != nil {
		t.noticeAssignmentWriteFailed("regroup "+agentName, err)
	}
}

// sendGroupOfCmd asks window 1 to record a membership change on this
// secondary's behalf. An unreachable window 1 returns an error rather than
// applying anything durable: the viewer declines, it never improvises.
func (t *TUI) sendGroupOfCmd(agentName, label string) error {
	mux := t.windowOneMux()
	if mux == nil {
		return fmt.Errorf("not connected to window 1; the regroup of %q was not applied fleet-wide", agentName)
	}
	resp, err := mux.RequestRaw(GroupOfCmd{
		Action: "set_group_of",
		Agent:  agentName,
		Label:  label,
	})
	if err != nil {
		return fmt.Errorf("send regroup to window 1: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("window 1 refused the regroup: %s", resp.Error)
	}
	return nil
}

// applyGroupOfCmd is window 1's side: it records the membership change against
// window 1's OWN pane key for that agent, adds the label to the band universe
// if it is new, and persists through the same primitive a local regroup uses.
func (t *TUI) applyGroupOfCmd(cmd GroupOfCmd) error {
	if cmd.Agent == "" || cmd.Label == "" {
		return fmt.Errorf("set_group_of: agent and label are both required")
	}
	if !t.isFleetAuthority() {
		return ErrAssignmentNotAuthority
	}
	var target PaneView
	for _, p := range t.panes {
		if p.Name() == cmd.Agent {
			target = p
			break
		}
	}
	if target == nil {
		return fmt.Errorf("set_group_of: no agent named %q in this fleet", cmd.Agent)
	}
	if t.layoutState.GroupOf == nil {
		t.layoutState.GroupOf = make(map[string]string)
	}
	t.layoutState.GroupOf[paneKey(target)] = cmd.Label
	seen := false
	for _, g := range t.layoutState.Groups {
		if g == cmd.Label {
			seen = true
			break
		}
	}
	if !seen {
		t.layoutState.Groups = append(t.layoutState.Groups, cmd.Label)
	}
	t.saveLayoutIfConfigured()
	return nil
}
