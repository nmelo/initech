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

// visiblePanesForWindow filters the TUI's panes down to those this window
// should render, consulting rendersInWindow with the current assignment and
// the live connected-window set (ini-9ka.6 wires what ini-9ka.7 decided).
//
// Returns the pane list unchanged when no assignment store is loaded, which
// is every ordinary single-window session: window 1 renders everything, via
// the same slice it always did. That keeps multi-monitor from being a second
// code path for users who never enabled it.
func (t *TUI) visiblePanesForWindow() []PaneView {
	if t.assignment == nil {
		return t.panes
	}
	connected := t.connectedWindowSet()
	groupOf := t.layoutState.GroupOf

	out := make([]PaneView, 0, len(t.panes))
	for _, p := range t.panes {
		if rendersInWindow(paneKey(p), t.windowID, t.assignment, groupOf, connected) {
			out = append(out, p)
		}
	}
	return out
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
		key := paneKey(p)
		next := agentStatusSnapshot{beads: strings.Join(beads, "\x00"), desc: desc}
		if prev, seen := t.agentStatus[key]; seen && prev == next {
			continue
		}
		t.agentStatus[key] = next
		t.windowSrv.broadcastAgentStatus(p.Name(), beads, desc)
	}
}

// broadcastAgentStatus pushes one agent's state to every attached window.
// Best-effort per recipient, for the same reason as broadcastSessionNotice: a
// window whose stream is already broken is about to be detected as gone.
func (w *windowServer) broadcastAgentStatus(name string, beads []string, desc string) {
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
			Action: agentStatusAction,
			Name:   name,
			Beads:  beads,
			Bead:   primary, // Wire compatibility, same as AgentStatus.
			Text:   desc,
		})
	}
}

// applyAgentStatus updates the named remote pane from a broadcast (ini-9ka.11).
// The receiving half of broadcastAgentStatus.
func (t *TUI) applyAgentStatus(name string, beads []string, desc string) {
	for _, pv := range t.panes {
		rp, ok := pv.(*RemotePane)
		if !ok || rp.Name() != name {
			continue
		}
		rp.ApplyStatus(beads, desc)
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
