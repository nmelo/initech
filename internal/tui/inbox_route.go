// inbox_route.go makes the operator inbox an any-window surface: every window
// lists it and counts it, and every window can act on it, while window 1 stays
// the only writer (ini-3wkl.7).
//
// Built to mirror the fleet-state follower channel (fleet_state.go:564-640)
// piece for piece, because that channel is already proven from a child window
// by fn77's own two-window rig. Management is window 1's; attention is
// everyone's, and the inbox is an attention surface (canon §288).
//
// THREE RULES, each load-bearing:
//
// DECLINE, NEVER IMPROVISE (§291). A child that cannot reach window 1 says so
// and writes nothing. There is no local write that "syncs later" and no
// optimistic answered state: the store's mutate chokepoint refuses a follower
// anyway, so an improvising seam would only fail later and more quietly.
//
// ITS OWN DOORBELL. A change rings inbox_changed, never session_notice.
// surfaceSessionNotice re-reads three stores and re-plans the layout, so a
// post riding it would re-plan every follower on every post (eng2's finding).
// The receive side here re-reads the inbox and nothing else.
//
// A BOUNDED STALENESS, NOT REFRESH-ON-OPEN ALONE. The mux drops unsolicited
// events when its buffer is full (control_mux.go, drop-oldest), so a doorbell
// CAN be lost. Refresh-on-open would then leave the corner count stale exactly
// when nobody opens the panel -- and a stale count is the reason nobody would.
// A child re-reads on a slow cadence, so a dropped doorbell costs at most
// inboxFollowerRefresh.
package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// inboxFollowerRefresh bounds how stale a child window's inbox can be when a
// doorbell is lost (pm default, ini-3wkl.7 AC 4). It is a BOUND, not a
// polling interval anyone should depend on for freshness: the doorbell is the
// fast path and this is the floor under it.
const inboxFollowerRefresh = 30 * time.Second

// inboxChangedAction is the unsolicited control-stream message window 1 sends
// when the inbox changed. Payload-less on purpose: the receiver re-reads the
// store, so there is nothing to get out of order and nothing to reconcile.
const inboxChangedAction = "inbox_changed"

// inboxCmdGoneAction answers a child's act on an item another window already
// resolved. Carried as an ACTION, not matched out of an error string, so the
// child can tell "you lost the race" from every refusal it must report.
const inboxCmdGoneAction = "inbox_cmd_gone"

// Operator acts a child window can ask window 1 to perform.
const (
	inboxOpSeen    = "seen"
	inboxOpDismiss = "dismiss"
	inboxOpReply   = "reply"
	inboxOpAccept  = "accept"
)

// InboxCmd is the control command a SECONDARY window sends to window 1 to act
// on an inbox item. Beside FleetStateCmd, and for the same reason: secondaries
// never write the store, so they command the authority.
type InboxCmd struct {
	ID     string `json:"id,omitempty"`
	Action string `json:"action"` // "inbox_cmd"
	Op     string `json:"op"`     // seen | dismiss | reply | accept
	Item   string `json:"item"`
	Text   string `json:"text,omitempty"` // The typed reply, for "reply".
}

// inboxAct performs one operator act from THIS window: directly on window 1,
// routed to window 1 from a child.
//
// A lost race returns nil on a child. First write wins, and the operator took
// the trade of no "answered from another window" notice -- a raw transition
// error would be exactly that notice, and a misleading one. The item simply
// disappears on the refresh routeInboxCmd already did.
func (t *TUI) inboxAct(op, id, text string) error {
	if t.isFleetAuthority() {
		return t.applyInboxOp(InboxCmd{Op: op, Item: id, Text: text})
	}
	return t.routeInboxCmd(op, id, text)
}

// applyInboxCmd applies a child's inbox command on window 1. The single entry
// point the daemon hands the command to, so a child's reply takes exactly the
// path a keypress on window 1 takes.
//
// MARSHALLED ONTO THE MAIN LOOP for applyFleetStateCmd's reason and one more:
// answerInboxItem is main-goroutine only and reaches the agent's local *Pane.
// A reply from a child therefore runs ENTIRELY on window 1, send and delivery
// verdict included -- which is where the agent processes live.
func (t *TUI) applyInboxCmd(cmd InboxCmd) error {
	if cmd.Item == "" {
		return fmt.Errorf("item is required")
	}
	var err error
	if !t.runOnMain(func() { err = t.applyInboxOp(cmd) }) {
		return fmt.Errorf("window 1 is shutting down; %s on %s was not applied", cmd.Op, cmd.Item)
	}
	return err
}

// applyInboxOp is the switch itself. Main goroutine only.
func (t *TUI) applyInboxOp(cmd InboxCmd) error {
	switch cmd.Op {
	case inboxOpSeen:
		return t.inboxState().Transition(cmd.Item, InboxSeen, actorOperator)
	case inboxOpDismiss:
		return t.inboxState().Transition(cmd.Item, InboxDismissed, actorOperator)
	case inboxOpReply:
		return t.ReplyToInboxItem(cmd.Item, cmd.Text)
	case inboxOpAccept:
		return t.AcceptInboxDefault(cmd.Item)
	default:
		return fmt.Errorf("unknown inbox op %q", cmd.Op)
	}
}

// routeInboxCmd sends one act to window 1 and reports what happened to it.
//
// Every failure is an error the caller shows: the operator asked for a reply,
// and a silent no-op would leave him believing it was sent. The one exception
// is the lost race, which is not a failure of this act -- the item was already
// resolved -- and returns nil after the refresh.
func (t *TUI) routeInboxCmd(op, id, text string) error {
	mux := t.windowOneMux()
	if mux == nil {
		return fmt.Errorf("not connected to window 1; %s not sent", op)
	}
	resp, err := mux.RequestRaw(InboxCmd{Action: "inbox_cmd", Op: op, Item: id, Text: text})
	if err != nil {
		return fmt.Errorf("could not reach window 1; %s not sent: %w", op, err)
	}
	if resp.Action == inboxCmdGoneAction {
		t.refreshInboxIfFollower()
		return nil
	}
	if !resp.OK {
		return fmt.Errorf("window 1 refused %s: %s", op, resp.Error)
	}
	// Window 1 applied and persisted it; re-read so this window stops showing
	// the state it just asked to change, whether or not the doorbell arrives.
	t.refreshInboxIfFollower()
	return nil
}

// handleInboxCmd applies a secondary window's inbox act on window 1.
func (d *Daemon) handleInboxCmd(line []byte) ControlResp {
	var cmd InboxCmd
	if err := json.Unmarshal(line, &cmd); err != nil {
		return ControlResp{Error: fmt.Sprintf("invalid inbox_cmd payload: %v", err)}
	}
	if d.onInboxCmd == nil {
		return ControlResp{ID: cmd.ID, Error: "this session does not own the inbox"}
	}
	if err := d.onInboxCmd(cmd); err != nil {
		if errors.Is(err, ErrInboxBadTransition) || errors.Is(err, ErrInboxNoSuchItem) {
			return ControlResp{ID: cmd.ID, Action: inboxCmdGoneAction, Error: err.Error(), Target: cmd.Item}
		}
		return ControlResp{ID: cmd.ID, Error: err.Error()}
	}
	return ControlResp{ID: cmd.ID, OK: true, Action: "inbox_cmd_ok", Target: cmd.Item}
}

// ── the doorbell ────────────────────────────────────────────────────

// ringInboxDoorbell is window 1's store change hook. It runs inside the
// store's mutate, under ib.mu, so it must not block: the network write goes
// to its own goroutine. Payload-less doorbells commute, so their order does
// not matter.
func (t *TUI) ringInboxDoorbell() {
	ws := t.windowSrv
	if ws == nil {
		return // Single-window session: nobody to tell.
	}
	// safeGo, not a bare go: a panic in a broadcast on an arbitrary goroutine
	// would take the process, and window 1's process is the whole fleet's
	// display.
	t.safeGo(ws.broadcastInboxChanged)
}

// broadcastInboxChanged pushes the doorbell to every attached window.
// Best-effort per recipient, for broadcastSessionNotice's reason; a lost one is
// what inboxFollowerRefresh bounds.
func (w *windowServer) broadcastInboxChanged() {
	if w == nil || w.daemon == nil {
		return
	}
	w.daemon.sessionsMu.Lock()
	ctrls := append([]net.Conn(nil), w.daemon.ctrlConns...)
	w.daemon.sessionsMu.Unlock()
	for _, ctrl := range ctrls {
		writeJSON(ctrl, ControlResp{Action: inboxChangedAction}) //nolint:errcheck
	}
}

// ── the follower's refresh ──────────────────────────────────────────

// refreshInboxIfFollower re-reads the inbox on a window that does not own it.
//
// THE INBOX AND NOTHING ELSE. No recalcGrid, no applyLayout, no other store:
// a post changes what the panel lists, never where panes sit (AC 3).
func (t *TUI) refreshInboxIfFollower() {
	t.refreshInboxIfFollowerAt(time.Now())
}

// refreshInboxIfFollowerAt is refreshInboxIfFollower with the clock injected,
// so the staleness bound can be asserted without sleeping through it.
//
// A read error KEEPS the last good store: a transient failure must not blank
// a count that was right a moment ago (§290's never-an-unexplained-blank). The
// clock still advances, so a persistent failure retries on the cadence rather
// than every frame.
func (t *TUI) refreshInboxIfFollowerAt(now time.Time) {
	if t.isFleetAuthority() || t.projectRoot == "" {
		return
	}
	t.inboxState() // Establish the initial load, so a refresh never races it.
	ib, err := LoadInbox(t.projectRoot, false)
	t.inboxStoreMu.Lock()
	defer t.inboxStoreMu.Unlock()
	t.lastInboxRefresh = now
	if err != nil {
		LogWarn("inbox", "follower refresh failed; keeping the last good store", "err", err)
		return
	}
	t.inboxStore = ib
}

// refreshInboxOnCadence is the staleness floor, called from the main loop's
// housekeeping tick.
func (t *TUI) refreshInboxOnCadence(now time.Time) {
	if t.isFleetAuthority() || t.projectRoot == "" {
		return
	}
	t.inboxStoreMu.Lock()
	due := now.Sub(t.lastInboxRefresh) >= inboxFollowerRefresh
	t.inboxStoreMu.Unlock()
	if due {
		t.refreshInboxIfFollowerAt(now)
	}
}
