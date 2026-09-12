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

import (
	"errors"
	"fmt"
	"github.com/nmelo/initech/internal/config"
	"net"
	"sort"
	"strings"
	"time"
)

// OWNERSHIP IS NO LONGER DECIDED HERE (ini-x5ob). rendersInWindow used to
// answer "does this window render this agent" independently in every process,
// over three inputs each process kept its own copy of -- which is why the
// partition was not exclusive and failed in both directions (double and hole).
// It is deleted rather than deprecated: while it existed, a future call site
// could reintroduce a second computer of ownership, and the invariant this bug
// purchased is that ownership has exactly ONE. See partition_authority.go.
//
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

// visiblePanesForWindow filters the TUI's panes down to those this window
// should render, consulting the served ownership map with the current
// the live connected-window set (ini-9ka.6 wires what ini-9ka.7 decided).
//
// Returns the pane list unchanged when no assignment store is loaded, which
// is every ordinary single-window session: window 1 renders everything, via
// the same slice it always did. That keeps multi-monitor from being a second
// code path for users who never enabled it.
func (t *TUI) visiblePanesForWindow() []PaneView {
	// A SECONDARY WINDOW RENDERS SERVED OWNERSHIP AND DERIVES NOTHING
	// (ini-x5ob). Its assignment copy and its connected set are no longer
	// ownership inputs: window 1 is the single computer, and consulting local
	// copies here is exactly what made the partition non-exclusive in both
	// directions. A viewer that has not been served yet renders nothing rather
	// than guessing -- guessing is the behaviour that produced the double.
	if !t.isFleetAuthority() {
		if !t.ownershipServed {
			return nil
		}
		out := make([]PaneView, 0, len(t.panes))
		for _, p := range t.panes {
			if t.paneOwnership[agentKey(p)] == t.windowID {
				out = append(out, p)
			}
		}
		return out
	}

	if t.assignment == nil {
		return t.panes
	}
	// Window 1 uses its OWN computation -- it is the authority, and waiting on
	// a round trip through its own server would make it the last window to
	// learn its own decision.
	owner := t.paneOwnership
	if owner == nil {
		owner = computePaneOwnership(t.panes, t.assignment, t.layoutState.GroupOf, t.connectedWindowSet())
	}
	out := make([]PaneView, 0, len(t.panes))
	for _, p := range t.panes {
		if owner[agentKey(p)] == WindowOne {
			out = append(out, p)
		}
	}
	return out
}

// emptyViewerHint is the operator-decided copy for a secondary window that
// owns no groups (ini-9fn, decision 2026-08-13 via pm). PM-OWNED STRING --
// render it verbatim, never paraphrase, and if it must appear anywhere else it
// references THIS constant (the one-copy rule from the consent prompt). The
// alternatives were decided against out loud: bare empty reads as broken
// (the operator lived through a crash loop that looked exactly like it), and
// auto-assignment would mean initech deciding his monitor layout for him.
//
// REWORDED FOR ini-uz42: it used to end "press Alt+a to assign". ini-fn77
// made the agents panel main-window only, so in the one window that shows
// this hint that keypress now produces a main-window-only notice -- the copy
// sent the operator to a dead end it had created. It names the window that
// can act instead.
const emptyViewerHint = "no agents are assigned to this window — assign some from the main window's Agents panel"

// unservedViewerHint is the operator-decided copy for a secondary window that
// has not yet been told what it owns (ini-x5ob; pm ruling 2026-08-14).
// PM-OWNED STRING — render it verbatim and reference THIS constant anywhere it
// must appear, the same one-copy rule emptyViewerHint carries.
//
// WHY AN ANNOUNCED BLANK RATHER THAN A SILENT ONE, which is the whole point of
// the addition: since ownership is served, a viewer with no served answer
// renders nothing rather than guessing — and a silently empty window 2 looks
// EXACTLY like the vanished-pane symptom this bead exists to fix. It would
// also contradict what v2.7.9 shipped: that a viewer which cannot reach
// window 1 tells you. Announcing the reason turns an ambiguous blank into a
// status the operator can act on, and extends that claim instead of denying it.
//
// The declined alternative was rendering STALE panes with a warning notice:
// that reintroduces exactly the two-truths state — a window showing agents it
// may no longer own — which is the class this bead closed.
const unservedViewerHint = "waiting for window 1 — it decides which agents appear here; reconnecting"

// allHiddenViewerHint is the copy for a viewer whose owned agents are ALL
// globally hidden (ini-uz42, pm 2026-09-12). PM-OWNED STRING -- render it
// verbatim; the count is the only substitution.
//
// THE LOCAL RECOVERY COMES FIRST ON PURPOSE. The blank window already holds a
// working unhide control: the overlay (Option+s) lists every OWNED pane
// including hidden ones, marks them, and a dot-click routes the unhide
// through window 1. It is one click away in the window the operator is
// already looking at, so it leads; the main window's Agents panel is the
// second route for an operator who prefers it.
//
// This state is LEGAL, not a defect. Before uz42 it fell through to the
// unplanned-defect branch, which returned "" -- so the operator got a blank
// window and the log got a warning about a state the product had entered
// correctly.
func allHiddenViewerHint(owned int) string {
	if owned == 1 {
		return "the 1 agent assigned here is hidden — click its dot in the overlay (Option+s) to unhide, or use the main window's Agents panel"
	}
	return fmt.Sprintf("all %d agents assigned here are hidden — click their dots in the overlay (Option+s) to unhide, or use the main window's Agents panel", owned)
}

// awaitingArrivalsViewerHint is the copy for a viewer that owns more agents
// than have arrived, where every one that HAS arrived is hidden (ini-uz42).
// PM-OWNED STRING -- verbatim, counts substituted.
//
// If any arrived agent were visible the plan would be non-empty and no hint
// would render at all, which is why this state only exists when the arrived
// subset is entirely hidden.
func awaitingArrivalsViewerHint(missing, owned int) string {
	return fmt.Sprintf("waiting for %d of %d assigned agents to arrive from window 1", missing, owned)
}

// unplannedViewerHint is the copy for the one state here that IS a defect:
// agents are owned, present and not hidden, and the plan still dropped them
// (ini-uz42). PM-OWNED STRING -- verbatim, count substituted.
//
// It must never be a blank screen. That is ini-x5ob canon applied to the
// failing case: an unserved viewer renders an announced empty state rather
// than an unexplained blank, and a DEFECTIVE one is held to the same rule --
// an operator staring at nothing cannot tell a bug from an empty fleet.
func unplannedViewerHint(unrendered int) string {
	if unrendered == 1 {
		return "1 agent assigned here is not rendering — this is a bug; details are in the log"
	}
	return fmt.Sprintf("%d agents assigned here are not rendering — this is a bug; details are in the log", unrendered)
}

// viewerOwnsNoGroups reports whether this window is a secondary with NOTHING
// assigned to it -- the one state the empty-viewer hint describes. Two
// entrances, one state (ini-xq4r's grooming cross-reference): a first attach
// before any assignment, and the last group being moved away. Deliberately
// NOT len(plan)==0, which is also true pre-connect and when every assigned
// pane is hidden -- in those states the hint's copy would be a lie.
// viewerEmptyExplanation returns the sentence a secondary window must show
// when it is rendering no panes, or "" when it must stay silent.
//
// ONE FUNCTION, ONE QUESTION (ini-x5ob, eng2's invariant). This replaces
// viewerOwnsNoGroups and viewerAwaitsOwnership, which answered "why is this
// window empty" from two DIFFERENT sources: the hint read the ASSIGNMENT
// while the panes came from the SERVED ownership. That is the
// two-consumers-one-question shape, and it produced the worst possible
// screen -- window 2 rendering nothing while the hint, consulting the
// assignment, concluded the window owned groups and stayed silent. A bare
// unexplained window is the exact state ini-9fn exists to prevent, and the
// operator has said so.
//
// The TRIGGER is therefore the plan -- the thing the operator can actually
// see -- and the served map only chooses WHICH true sentence to say:
//
//	not served yet          -> waiting for window 1
//	served, owns nothing    -> no agents are assigned to this window
//	served, owns something  -> silence, deliberately (see below)
//
// The third case is a window that OWNS agents and is still rendering none.
// That is not a state to explain to the operator; it is a defect, and either
// sentence would be a lie that papers over it. It is logged instead, loudly,
// so it surfaces as the bug it is rather than as reassuring copy.
// viewerEmptyDefect describes the one empty-viewer state that IS a defect --
// owned, present, unhidden, and still unplanned -- with the counts a single
// log line needs to carry (ini-4dzh AC3).
type viewerEmptyDefect struct {
	owned, present          []string
	hidden, absent, visible int
	planned                 int
}

// key identifies the defect state for change detection. Two frames with the
// same key are the same incident; a different key is a new one.
func (d viewerEmptyDefect) key() string {
	return fmt.Sprintf("%s|%s|h%d|a%d|v%d|p%d",
		strings.Join(d.owned, ","), strings.Join(d.present, ","),
		d.hidden, d.absent, d.visible, d.planned)
}

// viewerEmptyExplanation returns the sentence a secondary window shows when
// it renders no panes, and warns -- ON TRANSITIONS, NOT FRAMES (ini-4dzh) --
// when that emptiness is the genuine planning defect.
//
// THE GATE LIVES HERE, ONCE. Classification is the pure function below; this
// wrapper owns the only piece of state. The key is cleared on every
// non-defect result rather than at each of classification's returns, because
// a per-return clear is a carry-over list: the next return someone adds would
// forget it, and a re-entry after recovery would go silent.
func (t *TUI) viewerEmptyExplanation() string {
	hint, defect := t.classifyViewerEmpty()
	if defect == nil {
		t.lastEmptyViewerWarn = ""
		return hint
	}
	if k := defect.key(); k != t.lastEmptyViewerWarn {
		LogWarn("ownership", "viewer owns agents but is rendering none",
			"window", t.windowID,
			"owned", strings.Join(defect.owned, ","),
			"panes_present", strings.Join(defect.present, ","),
			"hidden", defect.hidden,
			"absent", defect.absent,
			"visible", defect.visible,
			"plan_panes", defect.planned)
		t.lastEmptyViewerWarn = k
	}
	return hint
}

// classifyViewerEmpty is the pure half: it decides which of the five states
// the viewer is in and returns the hint for it, plus a defect descriptor
// when -- and only when -- the state is the planning defect.
func (t *TUI) classifyViewerEmpty() (string, *viewerEmptyDefect) {
	if t.windowID == WindowOne || len(t.plan.Panes) > 0 {
		return "", nil
	}
	// Not told yet, or told but nothing has arrived: both are "waiting", and
	// the second is NOT the defect branch below. A viewer can be served its
	// partition on the handshake -- fast, one message -- and still have no
	// panes for a while, because the panes come separately over per-agent
	// streams. Under load that gap is seconds, and classifying it as a defect
	// left the window bare and silent for the whole of it. Measured on eng2's
	// six-agent rig, which is heavier than this file's own and reached the
	// state repeatedly.
	if !t.ownershipServed || len(t.panes) == 0 {
		return unservedViewerHint, nil
	}
	if len(ownershipKeysFor(t.paneOwnership, t.windowID)) == 0 {
		return emptyViewerHint, nil
	}
	// THE OWNED SET PARTITIONS INTO THREE, and which part is non-empty is the
	// whole question (ini-uz42). "owned" alone cannot tell them apart, and the
	// first person to hit this in the field spent a round trip discovering it:
	//
	//   present + hidden   -- legal. The operator hid them; nothing is wrong.
	//   absent             -- legal. The stream has not arrived from window 1
	//                         yet; under load that gap is seconds.
	//   present + visible  -- the DEFECT. They arrived, nothing hides them,
	//                         and the plan dropped them anyway.
	//
	// Before this, all three returned "" and all three logged the warning. The
	// two legal states are the common ones, so the operator got a blank window
	// for a fleet he had hidden himself, and the log got a flood about correct
	// behaviour (the two largest sources of ini-4dzh's flood, removed here at
	// the source rather than rate-limited downstream).
	owned := ownershipKeysFor(t.paneOwnership, t.windowID)
	present := make(map[string]bool, len(t.panes))
	var have []string
	for _, p := range t.panes {
		k := agentKey(p)
		present[k] = true
		have = append(have, k)
	}
	sort.Strings(have)

	hiddenCount, visibleCount, absentCount := 0, 0, 0
	for _, k := range owned {
		switch {
		case !present[k]:
			absentCount++
		case t.layoutState.Hidden[k]:
			hiddenCount++
		default:
			visibleCount++
		}
	}

	// Every owned agent is here and hidden: the legal all-hidden state.
	if hiddenCount == len(owned) {
		return allHiddenViewerHint(len(owned)), nil
	}
	// Some have not arrived, and nothing that HAS arrived is visible. A single
	// visible arrival would have produced a non-empty plan and no hint at all.
	if absentCount > 0 && visibleCount == 0 {
		return awaitingArrivalsViewerHint(absentCount, len(owned)), nil
	}

	// Present, unhidden, and still unplanned: the one state that is actually
	// wrong. The warning for it is the wrapper's, gated on this descriptor
	// changing, so an unchanged defect logs once rather than once per frame.
	return unplannedViewerHint(visibleCount), &viewerEmptyDefect{
		owned: owned, present: have,
		hidden: hiddenCount, absent: absentCount, visible: visibleCount,
		planned: len(t.plan.Panes),
	}
}

// liveTickInputs derives the live rotation's universe for THIS window: its
// assigned panes minus hidden, plus the pin set intersected with that universe
// (ini-xq4r AC 3). Pins are GLOBAL state and survive a move -- the agent
// re-pins wherever it now renders -- but the OLD window's slot must release,
// or a dangling reservation rotates nothing forever. Re-derived every tick
// from (global state ∩ window set), never mutated, so there is no per-window
// pin state to fall out of sync.
func (t *TUI) liveTickInputs() ([]PaneView, map[string]int) {
	windowPanes := t.visiblePanesForWindow()
	livePanes := make([]PaneView, 0, len(windowPanes))
	inWindow := make(map[string]bool, len(windowPanes))
	for _, p := range windowPanes {
		inWindow[agentKey(p)] = true
		if !t.layoutState.Hidden[agentKey(p)] {
			livePanes = append(livePanes, p)
		}
	}
	pinned := make(map[string]int, len(t.layoutState.LivePinned))
	for k, slot := range t.layoutState.LivePinned {
		if inWindow[k] {
			pinned[k] = slot
		}
	}
	return livePanes, pinned
}

// connectedWindowSet reports which secondary windows are attached right now.
//
// Window 1 reads it from its own listener. A SECONDARY window has no listener
// and cannot observe its siblings -- but it does not need to: the predicate
// only consults liveness for windows other than the one being asked about,
// and a secondary window renders exactly what is assigned to it. So it
// reports itself as present, which is trivially true (it is running), and
// that is sufficient for the predicate to return the right answer for its own
// agents.
func (t *TUI) connectedWindowSet() map[string]bool {
	if t.windowID != WindowOne {
		return map[string]bool{t.windowID: true}
	}
	return t.windowSrv.connectedWindows()
}

// noticeWindowTransitions raises the session-level fold-back and restore
// notices when a window comes or goes. Called from the render loop, so the
// notice lands in the same frame the panes move.
//
// Per the spec's standing assumption 3, these are SESSION-level notices: they
// describe the session's shape changing, not one agent's activity, so they are
// emitted with no pane attached and render in every window rather than only
// where the agent lives.
func (t *TUI) noticeWindowTransitions() {
	if t.assignment == nil || t.liveness == nil {
		return
	}
	gone, returned := t.liveness.observe(t.connectedWindowSet())
	for _, w := range gone {
		detail := fmt.Sprintf("window %s disconnected; its agents folded back into window 1", w)
		EmitEvent(t.agentEvents, AgentEvent{
			Type:   EventWindowFoldback,
			Detail: detail,
			Time:   time.Now(),
		})
		// Fan out to every OTHER attached window. Raised here, on window 1,
		// because it is the hub -- secondary windows cannot push to each
		// other, so a notice raised in one of them would render in exactly
		// one place (ini-9ka.8).
		t.windowSrv.broadcastSessionNotice(detail)
	}
	for _, w := range returned {
		detail := fmt.Sprintf("window %s reattached; its agents moved back", w)
		EmitEvent(t.agentEvents, AgentEvent{
			Type:   EventWindowRestored,
			Detail: detail,
			Time:   time.Now(),
		})
		t.windowSrv.broadcastSessionNotice(detail)
	}
}

// surfaceSessionNotice renders a notice broadcast by window 1 in THIS window.
// The receiving side of broadcastSessionNotice: a secondary window turns the
// control-stream message back into an ordinary local event, so a session
// notice reaches the operator's eyes in every window rather than only where it
// was raised.
func (t *TUI) surfaceSessionNotice(text string) {
	EmitEvent(t.agentEvents, AgentEvent{
		Type:   EventWindowFoldback,
		Detail: text,
		Time:   time.Now(),
	})
	// A session notice means the session's SHAPE changed -- and for a follower
	// that includes assignment moves made on window 1 (ini-xq4r): its cached
	// WindowAssignment is a startup snapshot, so without this reload the
	// follower's pane plan filters on a stale store forever and a moved group
	// never arrives. Event-driven on the existing notice plumbing rather than
	// polling the file per layout tick; the notice and the plan change land
	// together, which is also what AC 4's mid-glance rule wants.
	t.reloadAssignmentIfFollower()
	t.refreshMembershipIfFollower()
	// The FLEET store (hidden/protected/pins) needs the same treatment the
	// assignment store gets above — re-read AND re-project AND re-plan. The
	// broadcast half of "window 1 broadcasting on change is the shape"
	// (refreshFleetIfFollower's own comment) landed in mutateFleet; this is
	// the receive half. Without it window 2 logged the notice ten times while
	// its modal and panes kept rendering the startup snapshot
	// (operator-observed, 2026-08-15): a doorbell nobody answers.
	if !t.isFleetAuthority() {
		t.refreshFleetIfFollower()
		t.applyFleetProjection()
		t.recalcGrid(false)
		t.applyLayout()
	}
}

// reloadAssignmentIfFollower re-reads the assignment store on a window that
// does not own it, mirroring refreshFleetIfFollower one store over. Window 1's
// in-memory assignment IS the truth (it writes through MoveGroup, which
// persists on every change), so the authority never reloads.
func (t *TUI) reloadAssignmentIfFollower() {
	if t.windowID == WindowOne || t.projectRoot == "" {
		return
	}
	a, err := LoadAssignment(t.projectRoot, t.windowID)
	if err != nil {
		// Keep the cached store: a transient read error must not blank this
		// window's plan. The next notice retries.
		LogWarn("assignment", "follower reload failed; keeping cached store", "err", err)
		return
	}
	t.assignment = a
	t.recalcGrid(false)
	t.applyLayout()
}

// isSecondaryWindowIdentity reports whether a peer_name is one the --window
// flag derives (window-2, window-3, ...). It is how a TUI knows it is a
// secondary window rather than the session owner.
//
// Matching the derived shape rather than "peer_name is non-empty" matters:
// peer_name is also set for ordinary cross-machine peers, and treating one of
// those as a secondary window would make it render only its assigned groups
// and silently drop the rest.
// participatesInMultiWindow reports whether this process is part of a
// multi-window fleet -- either because it SERVES one (window 1, which has a
// WindowListen) or because it IS one (a secondary, whose identity says so).
//
// The distinction matters because the two roles are configured differently:
// viewerProject deliberately clears WindowListen, since a viewer serves
// nothing. Testing WindowListen alone therefore answers "am I window 1?", not
// "is this fleet multi-window?" -- which is why a secondary window rendered no
// monitor tiers at all while window 1 rendered them from the same assignment
// data (ini-6m4). Named once here so the next reader of "is this multi-window"
// cannot pick the wrong half again; tui.go's fleet/assignment loading already
// used this exact pair inline.
func participatesInMultiWindow(p *config.Project) bool {
	if p == nil {
		return false
	}
	return p.WindowListen != "" || isSecondaryWindowIdentity(p.PeerName)
}

func isSecondaryWindowIdentity(peerName string) bool {
	return strings.HasPrefix(peerName, "window-")
}

// noticeAssignmentWriteFailed surfaces a refused or failed assignment write to
// the operator (ini-9ka.9).
//
// The operator asked for a move; a silent no-op would leave them believing it
// applied. That matters most in the read-only-fallback case, where the move is
// refused precisely BECAUSE their real arrangement is still on disk and must
// not be overwritten -- so the notice names the recovery action rather than
// just reporting failure.
//
// Session-level (no pane attached): this describes the session's assignment
// store, not one agent, so it renders in every window.
func (t *TUI) noticeAssignmentWriteFailed(action string, err error) {
	detail := fmt.Sprintf("%s failed: %v", action, err)
	if errors.Is(err, ErrAssignmentReadOnly) {
		detail = fmt.Sprintf("%s was not applied: the assignment store is unreadable. "+
			"Your existing arrangement is preserved on disk and was NOT overwritten. "+
			"Repair or delete .initech/assignments.yaml, then reopen this modal.", action)
	}
	EmitEvent(t.agentEvents, AgentEvent{
		Type:   EventAssignmentWriteRefused,
		Detail: detail,
		Time:   time.Now(),
	})
}

// sessionNoticeAction is the unsolicited control-stream message that carries a
// session-level notice from window 1 out to every attached window (ini-9ka.8).
//
// It rides the transport that already exists rather than adding one:
// gracefulShutdown has always pushed unsolicited messages to every ctrlConns
// entry, and the client's ControlMux already routes ID-less messages to its
// events channel. Only the message type and the raise site were missing.
const sessionNoticeAction = "session_notice"

// identityTakenOverAction is the server's eviction VERDICT (ini-jhm6): sent on
// the OLD client's control stream just before its session is closed, telling
// it another connection took its window identity. The verdict is what makes
// eviction terminal for the loser -- a bare disconnect is indistinguishable
// from a transient drop, and the reconnect loop rightly treats those as
// retryable, which is exactly how two undead --window processes fought a
// 1-second eviction war all afternoon.
const identityTakenOverAction = "identity_taken_over"

// broadcastSessionNotice pushes a session-level notice to every attached
// window. Called on window 1, which is the hub: secondary windows cannot push
// to each other, so a notice raised locally in one of them would render in
// exactly one place -- which is the bug this closes.
//
// Best-effort per client: a window whose control stream is already broken is
// about to be detected as disconnected anyway (ini-9ka.7), and failing the
// whole broadcast because one recipient died would drop the notice for the
// windows that are still there.
func (w *windowServer) broadcastSessionNotice(text string) {
	if w == nil || w.daemon == nil {
		return
	}
	w.daemon.sessionsMu.Lock()
	ctrls := append([]net.Conn(nil), w.daemon.ctrlConns...)
	w.daemon.sessionsMu.Unlock()

	for _, ctrl := range ctrls {
		writeJSON(ctrl, ControlResp{Action: sessionNoticeAction, Text: text}) //nolint:errcheck
	}
	LogDebug("window-server", "session notice broadcast", "windows", len(ctrls), "text", text)
}

// agentStatusAction is the unsolicited control-stream message carrying an
// agent's observed state (beads, session description) from window 1 outward
// (ini-9ka.11). Rides the same channel as sessionNoticeAction.
const agentStatusAction = "agent_status"

// agentStatusSnapshot is the last value broadcast for one agent, used to emit
// only on genuine change.
type agentStatusSnapshot struct {
	beads string // Joined, for cheap comparison only -- the wire carries the slice.
	desc  string
	// waiting is part of the compared state so BOTH EDGES fire (ini-35ak): a
	// raise and a CLEAR are the same detector seeing a different value, not a
	// clear path bolted on beside a raise path. A clear that is a special case
	// is how a stale row survives an answered dialog.
	waiting WaitingState
	// suspended is compared the same both-edges way: park and wake are one
	// detector seeing different values (2026-08-15, "window 2 doesn't show
	// it as suspended").
	suspended bool
}

// broadcastAgentStatusChanges pushes per-agent bead/description updates to
// every attached window, but ONLY for agents whose state actually changed
// since the last call (ini-9ka.11).
//
// The diff is the point. Beads change rarely and discretely, so pushing on
// change is right for them. Session descriptions are re-extracted from the
// cursor row on essentially every frame, so pushing unconditionally would
// flood the control stream at frame rate. Comparing against the last broadcast
// value gets both: bead changes propagate within the render cycle they happen
// in, and descriptions cost nothing while they are merely being recomputed to
// the same string.
//
// Called from the render loop on window 1, which is the sole authority --
// secondary windows cannot push (the ini-9ka.8 topology fact).
func (t *TUI) broadcastAgentStatusChanges() {
	if t.windowSrv == nil {
		return // Single-window session: nobody to tell.
	}
	if t.agentStatus == nil {
		t.agentStatus = make(map[string]agentStatusSnapshot)
	}
	for _, pv := range t.panes {
		p, ok := pv.(*Pane)
		if !ok {
			continue // Only locally-owned agents are ours to report.
		}
		beads := p.BeadIDs()
		desc := p.SessionDesc()
		key := agentKey(p)
		ws := waitingStateOf(p)
		susp := p.IsSuspended()
		next := agentStatusSnapshot{beads: strings.Join(beads, "\x00"), desc: desc, waiting: ws, suspended: susp}
		if prev, seen := t.agentStatus[key]; seen && prev == next {
			continue
		}
		t.agentStatus[key] = next
		t.windowSrv.broadcastAgentStatus(p.Name(), beads, desc, ws, susp)
	}
}

// broadcastAgentStatus pushes one agent's state to every attached window.
// Best-effort per recipient, for the same reason as broadcastSessionNotice: a
// window whose stream is already broken is about to be detected as gone.
func (w *windowServer) broadcastAgentStatus(name string, beads []string, desc string, ws WaitingState, suspended bool) {
	if w == nil || w.daemon == nil {
		return
	}
	w.daemon.sessionsMu.Lock()
	ctrls := append([]net.Conn(nil), w.daemon.ctrlConns...)
	w.daemon.sessionsMu.Unlock()

	primary := ""
	if len(beads) > 0 {
		primary = beads[0]
	}
	for _, ctrl := range ctrls {
		writeJSON(ctrl, ControlResp{ //nolint:errcheck
			Action:       agentStatusAction,
			Name:         name,
			Beads:        beads,
			Bead:         primary, // Wire compatibility, same as AgentStatus.
			Text:         desc,
			WaitingState: ws,
			Suspended:    suspended,
		})
	}
}

// applyAgentStatus updates the named remote pane from a broadcast (ini-9ka.11).
// The receiving half of broadcastAgentStatus.
func (t *TUI) applyAgentStatus(name string, beads []string, desc string, ws WaitingState, suspended bool) {
	for _, pv := range t.panes {
		rp, ok := pv.(*RemotePane)
		if !ok || rp.Name() != name {
			continue
		}
		wasSuspended := rp.IsSuspended()
		rp.ApplyStatus(beads, desc)
		rp.ApplyWaiting(ws)
		rp.ApplySuspended(suspended)
		// WAKE-EDGE GEOMETRY REASSERTION. A wake respawns the pane at
		// whatever size the LAST writer left on its emulator — and both
		// windows write sizes (measured 2026-08-15: window 2 set 37x110,
		// window 1's layout overwrote to 36x104 twelve seconds later, the
		// spawn inherited window 1's numbers; the garbled run was 76-vs-37).
		// Rather than arbitrate the writers, the viewer re-sends ITS size on
		// the wake edge it already receives, so the child snaps to the
		// displaying window's truth within one resize round-trip of booting.
		if wasSuspended && !suspended {
			if cols, rows := rp.region.TerminalSize(); rows > 1 && cols > 1 {
				rp.Resize(rows, cols)
			}
		}
		return
	}
}

// isViewerSession reports whether this process is a secondary window rather
// than the session owner.
//
// Nil-safe on purpose: cfg.Project is nil in several call paths (and in tests),
// and every other read of it in Run is guarded. Round 1 of ini-civ dereferenced
// it directly at the IPC guard, which happened to work only because the paths
// that reach there always set it -- a nil panic waiting for the first caller
// that does not.
func isViewerSession(cfg Config) bool {
	return cfg.Project != nil && isSecondaryWindowIdentity(cfg.Project.PeerName)
}

// broadcastPaneOwnership pushes window 1's ownership decision to every
// attached window (ini-x5ob). Same channel and same best-effort-per-recipient
// reasoning as broadcastSessionNotice: a window whose control stream is
// already broken is about to be detected as disconnected anyway, and failing
// the whole broadcast because one recipient died would strand the windows that
// are still there.
//
// A LOST PUSH IS NOT A HOLE HERE, which is the point of the design. Ownership
// is re-served on every change and at hello, and a window that has not been
// served renders nothing rather than deriving a guess -- so the failure mode
// of a dropped message is "this window is briefly empty", never "two windows
// disagree about who owns an agent".
func (w *windowServer) broadcastPaneOwnership(owner map[string]string) {
	if w == nil || w.daemon == nil {
		return
	}
	w.daemon.sessionsMu.Lock()
	ctrls := append([]net.Conn(nil), w.daemon.ctrlConns...)
	w.daemon.sessionsMu.Unlock()

	for _, ctrl := range ctrls {
		writeJSON(ctrl, ControlResp{Action: paneOwnershipAction, Owner: owner}) //nolint:errcheck
	}
	LogDebug("window-server", "pane ownership broadcast", "windows", len(ctrls), "agents", len(owner))
}
