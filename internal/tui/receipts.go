package tui

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nmelo/initech/internal/watch"
)

// Receipts (ini-yjcy, watches seam S4): every send that ends in a hold mints
// a receipt and prints it at the end of its held line, whatever held it
// (do-not-disturb, the wake queue of a suspended pane, the machine ceiling, a
// dialog, --after, a pane not reading input). A delivered, unconfirmed,
// unchecked or refused send gets none: the sender already has its answer.
//
// The book is one per process, so a pane replaced by a restart or a resume
// shares it with its successor, and a receipt survives the swap. A message
// re-sent from a queue (a drain, a resume, the restart carry) keeps the
// receipt it was held with: a hold only mints when the message has none.

// The states and ReceiptState are declared with the watch checks that read
// them (watch_check.go): Since is when the send was held, At when it ended
// (typed, withdrawn or dropped), Why the reason initech dropped it.

// ReceiptKindCeiling is the kind of a hold for a suspended pane that the
// machine ceiling kept asleep. The other kinds are the held file's.
const ReceiptKindCeiling = "ceiling"

// maxEndedReceipts bounds the receipts kept after they end (typed, withdrawn
// or dropped). Held receipts are all kept: held mail has its own limit.
const maxEndedReceipts = 1000

// receiptDropLogEvery is the most often a dropped change event is logged.
const receiptDropLogEvery = time.Minute

// onReceipt is told of every receipt change, on the main goroutine (window
// 1's event loop). A change event can be dropped when the event channel is
// full; the state never is, so a reader also reads receiptState.
var onReceipt func(ReceiptState)

// receiptBook holds every receipt this process minted or restored.
type receiptBook struct {
	mu    sync.Mutex
	byID  map[string]*ReceiptState
	ended []string // ended receipts, oldest first, for the bound
	now   func() time.Time
	// keep reports a receipt that must not be pruned: one a waiting watch
	// names. nil keeps none past the bound.
	keep func(id string) bool
	// lastDropLog rate-limits the dropped-event log line.
	lastDropLog time.Time
}

func newReceiptBook() *receiptBook {
	return &receiptBook{byID: map[string]*ReceiptState{}, now: time.Now}
}

// receipts is this process's book. Its keep reads the receipts waiting watches
// name, as the main goroutine last published them (watchedReceipts).
var receipts = func() *receiptBook {
	b := newReceiptBook()
	b.keep = keepWatchedReceipt
	return b
}()

// watchedReceipts is the set of receipts waiting send watches name. Only the
// main goroutine publishes it (publishWatchedReceipts); the book's keep, which
// runs under the book's lock on any goroutine, only loads it.
var watchedReceipts atomic.Pointer[map[string]bool]

func keepWatchedReceipt(id string) bool {
	m := watchedReceipts.Load()
	return m != nil && (*m)[id]
}

// publishWatchedReceipts publishes the receipts that window 1's waiting send
// watches name, so the bound never prunes one (pm, ini-yjcy rule 2). Main
// goroutine: at load, after a watch is set, and after each pass.
func (t *TUI) publishWatchedReceipts(now time.Time) {
	set := map[string]bool{}
	if t.watches != nil {
		for _, w := range t.watches.Snapshot(now) {
			if w.State == watch.Waiting && (w.Kind == watch.KindSendDelivered || w.Kind == watch.KindSendHeld) && w.Params["receipt"] != "" {
				set[w.Params["receipt"]] = true
			}
		}
	}
	watchedReceipts.Store(&set)
}

// SplitReceipt splits a held or deferred line into its words and the receipt
// at its end ("" when it carries none), for a reader that matches the words.
func SplitReceipt(line string) (words, id string) {
	i := strings.LastIndex(line, " (receipt ")
	if i < 0 || !strings.HasSuffix(line, ")") {
		return line, ""
	}
	id = strings.TrimSuffix(line[i+len(" (receipt "):], ")")
	if id == "" || strings.ContainsAny(id, " ()") {
		return line, ""
	}
	return line[:i], id
}

// book is the pane's receipt book: its own in a test that set one, else the
// process's.
func (p *Pane) book() *receiptBook {
	if p.receipts != nil {
		return p.receipts
	}
	return receipts
}

// state returns a copy of the receipt's state, or unknown for an id the book
// does not hold.
func (b *receiptBook) state(pane, id string) ReceiptState {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r, ok := b.byID[id]; ok && (pane == "" || r.Pane == pane) {
		return *r
	}
	return ReceiptState{Pane: pane, ID: id, State: ReceiptUnknown}
}

// set records a receipt's state and returns the new value, and whether it
// changed. heldAt is when the send was held (Since); an ending stamps At with
// the book's clock, and why for a drop. A receipt that ended stays ended: a
// late held never revives it.
func (b *receiptBook) set(pane, id, from, kind, state, why string, heldAt time.Time) (ReceiptState, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if heldAt.IsZero() {
		heldAt = now
	}
	r, ok := b.byID[id]
	if !ok {
		r = &ReceiptState{Pane: pane, ID: id, From: from, Kind: kind, Since: heldAt}
		b.byID[id] = r
	}
	if ok && r.State == state && r.Pane == pane && (kind == "" || kind == r.Kind) {
		return *r, false
	}
	if ok && ended(r.State) && state == ReceiptHeld {
		return *r, false
	}
	r.Pane, r.State = pane, state
	if kind != "" {
		r.Kind = kind
	}
	if from != "" {
		r.From = from
	}
	if ended(state) || state == ReceiptUnknown {
		r.At = now
	}
	if state == ReceiptDropped {
		r.Why = why
	}
	if ended(state) {
		b.ended = append(b.ended, id)
		b.pruneLocked()
	}
	return *r, true
}

func ended(state string) bool {
	return state == ReceiptTyped || state == ReceiptWithdrawn || state == ReceiptDropped
}

// pruneLocked drops the oldest ended receipts past maxEndedReceipts, never
// one keep names, and never one that is no longer ended.
func (b *receiptBook) pruneLocked() {
	over := len(b.ended) - maxEndedReceipts
	if over <= 0 {
		return
	}
	kept := b.ended[:0:0]
	for _, id := range b.ended {
		r, ok := b.byID[id]
		switch {
		case !ok || !ended(r.State):
			// Gone, or held again: not an ended entry any more.
		case over > 0 && (b.keep == nil || !b.keep(id)):
			delete(b.byID, id)
			over--
		default:
			kept = append(kept, id)
		}
	}
	b.ended = kept
}

// mintReceipt gives m a receipt when it has none.
func mintReceipt(m *QueuedMessage) {
	if m.Receipt == "" {
		m.Receipt = newAgentReceipt()
	}
}

// noteReceipt records m's receipt in state and tells the window of the
// change. A message with no receipt is not recorded.
func (p *Pane) noteReceipt(m QueuedMessage, state string) {
	if m.Receipt == "" {
		return
	}
	p.noteReceiptKind(m, heldKindOf(m), state, "")
}

// dropReceipt records that initech dropped m, and why.
func (p *Pane) dropReceipt(m QueuedMessage, why string) {
	if m.Receipt == "" {
		return
	}
	p.noteReceiptKind(m, heldKindOf(m), ReceiptDropped, why)
}

// noteReceiptKind is noteReceipt with the kind named, and why for a drop.
func (p *Pane) noteReceiptKind(m QueuedMessage, kind, state, why string) {
	if m.Receipt == "" {
		return
	}
	b := p.book()
	r, changed := b.set(p.name, m.Receipt, m.From, kind, state, why, m.Time)
	if !changed {
		return
	}
	b.emit(p.eventCh, r)
}

// settleReceipt ends the receipt of a message a drain has handed to the send
// path, by the send's verdict: refused is dropped; held or deferred again
// keeps it held (the re-hold kept the receipt); anything else was typed.
func (p *Pane) settleReceipt(m QueuedMessage, v SendVerdict) {
	switch v.Outcome {
	case DeliveryHeld, DeliveryDeferred:
		return
	case DeliveryNotDelivered:
		p.dropReceipt(m, v.Reason)
	default:
		p.noteReceipt(m, ReceiptTyped)
	}
}

// emit sends one change on the event channel without blocking. A full
// channel drops the event, logged at most once a minute; the state is
// already in the book.
func (b *receiptBook) emit(ch chan<- AgentEvent, r ReceiptState) {
	if ch == nil {
		return
	}
	rr := r
	select {
	case ch <- AgentEvent{Type: EventMessageSent, Pane: r.Pane, Receipt: &rr, Time: b.now()}:
	default:
		b.mu.Lock()
		log := b.now().Sub(b.lastDropLog) >= receiptDropLogEvery
		if log {
			b.lastDropLog = b.now()
		}
		b.mu.Unlock()
		if log {
			LogWarn("receipt", "receipt change event dropped: the event channel is full; the state is kept", "pane", r.Pane, "state", r.State)
		}
	}
}

// receiptState is a receipt's state as watches read it (seam S4), on the
// main goroutine. A receipt this process does not hold reads unknown.
func (t *TUI) receiptState(pane, id string) ReceiptState {
	if p, ok := t.findPaneByName(pane).(*Pane); ok {
		return p.book().state(pane, id)
	}
	return receipts.state(pane, id)
}

// receiptLine is the end every held line carries.
func receiptLine(id string) string {
	if id == "" {
		return ""
	}
	return " (receipt " + id + ")"
}
