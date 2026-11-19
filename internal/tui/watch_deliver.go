package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/watch"
)

// Watches 2C (ini-kbpo, seam S3): a fired or missed watch reaches its owner
// as one line, or the rig's super. Window 1, main goroutine.
//
// The write order makes every line go out exactly once across a stop:
// (1) the ending is in watches.yaml (2B); (2) the owner's line is held mail,
// with a receipt and the watch ids, so held.yaml holds it; (3) MarkHanded is
// written; (4) the line is typed and MarkDelivered is written from the
// receipt's typed event. Between (2) and (3) the line is held untyped
// (watchPending), so it is never typed before MarkHanded is on disk. Each
// pass, and the start, resolve an ending by what the files show, never by
// sending again blind:
//   - owed with no line in held mail: handed now (stop after 1);
//   - owed with its line in held mail: adopted, MarkHanded with that
//     receipt, no second copy (stop after 2);
//   - handed with its line still in held mail: typed from there (after 3);
//   - handed, its line gone from held mail with no drop recorded: it reads
//     delivered and is never handed again (stop after typing).

// watchWithdrawnSuffix ends 2B's detail for a send its owner withdrew: the
// one MISSED ending that does not tell the owner's super (pm, ini-20r2).
const watchWithdrawnSuffix = " was withdrawn"

// watchReadyKey is the readiness waiter a STARTING watch line registers.
const watchReadyKey = "watch-line"

// watchLineText is one ending's line in its owner's message.
func watchLineText(w watch.Watch) string {
	state := "FIRED"
	if w.State == watch.Missed {
		state = "MISSED"
	}
	late := ""
	if w.Late {
		late = "late: "
	}
	detail := ""
	if w.Detail != "" {
		detail = " (" + w.Detail + ")"
	}
	return fmt.Sprintf("[from initech, watch %s] %s: %s%s%s", w.ID, state, late, w.Why, detail)
}

// deliverWatches hands every owed ending's line to its owner, or to super,
// tells super what it is owed, and publishes the receipts to keep. Main
// goroutine: after each pass, at each deadline, at start, and when an owner's
// pane becomes ready.
func (t *TUI) deliverWatches(now time.Time) {
	if t.watches == nil {
		return
	}
	var order []string
	byOwner := map[string][]watch.Watch{}
	for _, w := range t.watches.Owed() {
		if _, seen := byOwner[w.Owner]; !seen {
			order = append(order, w.Owner)
		}
		byOwner[w.Owner] = append(byOwner[w.Owner], w)
	}
	for _, owner := range order {
		t.deliverOwner(owner, byOwner[owner], now)
	}
	t.tellSuperOfMisses(now)
	t.tellSuperOfHeld(now)
	t.publishWatchedReceipts(now)
}

// deliverOwner hands one owner's owed endings: adopted from held mail when
// their line is already there, else as one new message, one line each, or
// kept owed while the owner cannot take it now.
func (t *TUI) deliverOwner(owner string, ws []watch.Watch, now time.Time) {
	p := t.findPane(owner)
	if p == nil {
		// Gone from the rig: the line goes to super, with who set it and why.
		for _, w := range ws {
			if err := t.watches.MarkToSuper(w.ID, now); err != nil {
				LogWarn("watch", "line for super not marked; tried again next pass", "watch", w.ID, "err", err)
				continue
			}
			t.sendInitechLine(fmt.Sprintf("for %s, who is no longer in the rig: %s", owner, watchLineText(w)))
		}
		return
	}
	ws = t.adoptHeldWatchLines(p, ws, now)
	if len(ws) == 0 {
		return
	}
	var send []watch.Watch
	for _, w := range ws {
		switch {
		case !p.IsAlive() && !p.IsSuspended():
			// Stopped or exited, still in the rig: held until it runs again;
			// initech never starts it to deliver a line.
		case p.IsSuspended() && w.HoldIfAsleep:
			// Held while asleep, as the owner asked.
		default:
			send = append(send, w)
		}
	}
	if len(send) == 0 {
		return
	}
	if !p.IsSuspended() && !p.whenReady(watchReadyKey, now, func() {
		t.runOnMain(func() { t.deliverWatches(watchNow()) })
	}) {
		return // STARTING: the pane runs delivery once it is ready
	}
	t.handWatchLines(p, send, now)
}

// handWatchLines puts one message for p's owed endings into held mail (2),
// writes MarkHanded (3), and releases it to be typed or woken (4).
func (t *TUI) handWatchLines(p *Pane, ws []watch.Watch, now time.Time) {
	lines := make([]string, 0, len(ws))
	ids := make([]string, 0, len(ws))
	for _, w := range ws {
		lines = append(lines, watchLineText(w))
		ids = append(ids, w.ID)
	}
	m := QueuedMessage{Text: strings.Join(lines, "\n"), Enter: true, From: initechSender, Time: now, WatchIDs: ids, watchPending: true}
	if p.InDND() {
		m.DND = true
	} else if p.agentReader() != noAgentReader {
		m.HeldForAgent = true
	}
	refusal, receipt := p.enqueueReceipt(m, sendAgainWhenResumes)
	if refusal != "" {
		LogWarn("watch", "watch line not held: the owner's queue is full; tried again next pass", "owner", p.Name(), "watches", len(ids))
		return
	}
	t.markHandedAndRelease(p, ids, receipt, now)
}

// adoptHeldWatchLines finds held mail on p that already carries some of ws's
// ids (a stop after step 2, or a MarkHanded that failed), marks them handed
// with that line's receipt and releases it. It returns the endings with no
// line yet.
func (t *TUI) adoptHeldWatchLines(p *Pane, ws []watch.Watch, now time.Time) []watch.Watch {
	owed := map[string]bool{}
	for _, w := range ws {
		owed[w.ID] = true
	}
	type held struct {
		receipt string
		ids     []string
	}
	var found []held
	p.mu.Lock()
	for _, m := range p.messageQueue {
		var hit []string
		for _, id := range m.WatchIDs {
			if owed[id] {
				hit = append(hit, id)
			}
		}
		if len(hit) > 0 && m.Receipt != "" {
			found = append(found, held{m.Receipt, hit})
			for _, id := range hit {
				delete(owed, id)
			}
		}
	}
	p.mu.Unlock()
	for _, h := range found {
		LogInfo("watch", "watch line found in held mail; adopted, not sent again", "owner", p.Name(), "watches", len(h.ids))
		t.markHandedAndRelease(p, h.ids, h.receipt, now)
	}
	var rest []watch.Watch
	for _, w := range ws {
		if owed[w.ID] {
			rest = append(rest, w)
		}
	}
	return rest
}

// markHandedAndRelease writes MarkHanded for ids, then lets the held line be
// typed: a drain for a pane that is up, a wake for a suspended one. A failed
// write leaves the line held untyped for the next pass to adopt.
func (t *TUI) markHandedAndRelease(p *Pane, ids []string, receipt string, now time.Time) {
	for _, id := range ids {
		if err := t.watches.MarkHanded(id, receipt, now); err != nil {
			LogWarn("watch", "watch line not handed; held untyped for the next pass", "watch", id, "err", err)
			return
		}
	}
	p.mu.Lock()
	for i := range p.messageQueue {
		if p.messageQueue[i].Receipt == receipt {
			p.messageQueue[i].watchPending = false
		}
	}
	suspended, cb := p.suspended, p.onSuspendedMessage
	p.mu.Unlock()
	if suspended {
		if cb != nil {
			cb(p) // woken by default; the resume drain waits for it to be ready
		}
		return
	}
	p.startModalDrain()
}

// tellSuperOfMisses tells the owner's super once of each MISSED ending, but
// not of a send its owner withdrew, nor when the owner is super itself or
// the line already went to super. The mark is written before the line, so a
// restart never tells it twice.
func (t *TUI) tellSuperOfMisses(now time.Time) {
	for _, w := range t.watches.Snapshot(now) {
		if w.State != watch.Missed || w.Delivery.SuperToldMissed || w.Delivery.ToSuper ||
			w.Owner == superPaneName || strings.HasSuffix(w.Detail, watchWithdrawnSuffix) {
			continue
		}
		if err := t.watches.MarkSuperToldMissed(w.ID, now); err != nil {
			LogWarn("watch", "super not told of a miss; tried again next pass", "watch", w.ID, "err", err)
			continue
		}
		t.sendInitechLine(fmt.Sprintf("%s's watch missed: %s", w.Owner, watchLineText(w)))
	}
}

// tellSuperOfHeld tells the owner's super once of a line not delivered
// HeldTellSuper after its ending: held, starting, asleep or stopped.
func (t *TUI) tellSuperOfHeld(now time.Time) {
	for _, w := range t.watches.Snapshot(now) {
		d := w.Delivery
		if (w.State != watch.Fired && w.State != watch.Missed) || !d.Delivered.IsZero() || d.ToSuper || d.Dropped != "" ||
			d.SuperToldHeld || now.Sub(w.Ended) < watch.HeldTellSuper {
			continue
		}
		if err := t.watches.MarkSuperToldHeld(w.ID, now); err != nil {
			continue
		}
		t.sendInitechLine(fmt.Sprintf("%s's watch line has been held %s: %s", w.Owner, shortAge(now.Sub(w.Ended)), watchLineText(w)))
	}
}

// watchReceiptChanged is onReceipt for window 1 (main goroutine): a handed
// line's receipt typed marks its watches delivered at the typed time;
// dropped marks them dropped, and super is told once.
func (t *TUI) watchReceiptChanged(r ReceiptState) {
	if t.watches == nil || (r.State != ReceiptTyped && r.State != ReceiptDropped) {
		return
	}
	now := watchNow()
	told := false
	for _, w := range t.watches.Snapshot(now) {
		if w.Delivery.Receipt != r.ID || w.Delivery.Handed.IsZero() || !w.Delivery.Delivered.IsZero() || w.Delivery.Dropped != "" {
			continue
		}
		if r.State == ReceiptTyped {
			at := r.At
			if at.IsZero() {
				at = now
			}
			_ = t.watches.MarkDelivered(w.ID, at)
			continue
		}
		_ = t.watches.MarkDropped(w.ID, r.Why, now)
		if !told {
			told = true
			t.sendInitechLine(fmt.Sprintf("%s's watch line was dropped (%s): %s", w.Owner, r.Why, watchLineText(w)))
		}
	}
	t.publishWatchedReceipts(now)
}

// resolveHandedAtStart is the start's read of each handed, undelivered line
// (after held mail is back): one whose receipt is held again waits to be
// typed; one whose line is gone from held mail with no drop recorded was
// typed before the stop, and reads delivered. Main goroutine.
func (t *TUI) resolveHandedAtStart(now time.Time) {
	for _, w := range t.watches.Snapshot(now) {
		d := w.Delivery
		if d.Handed.IsZero() || !d.Delivered.IsZero() || d.ToSuper || d.Dropped != "" || d.Receipt == "" {
			continue
		}
		if t.receiptState(w.Owner, d.Receipt).State == ReceiptHeld {
			continue
		}
		LogInfo("watch", "handed line gone from held mail with no drop: delivered before the stop", "watch", w.ID)
		_ = t.watches.MarkDelivered(w.ID, now)
	}
}
