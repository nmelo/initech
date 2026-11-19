package tui

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/watch"
)

// ini-yjcy (watches seam S4): every send that ends in a hold carries a
// receipt at the end of its held line, whatever held it, and the receipt has
// a state other parts of initech read and are told of. At base a Claude
// pane's do-not-disturb, wake, ceiling, dialog and not-reading holds minted
// none.

// receiptOf is the receipt at the end of a held line, or "".
func receiptOf(words string) string {
	i := strings.LastIndex(words, " (receipt ")
	if i < 0 || !strings.HasSuffix(words, ")") {
		return ""
	}
	return strings.TrimSuffix(words[i+len(" (receipt "):], ")")
}

// fakeBook is a receipt book on a clock the test moves, set on the pane.
func fakeBook(p *Pane, t0 time.Time) (*receiptBook, *time.Time) {
	b := newReceiptBook()
	now := t0
	b.now = func() time.Time { return now }
	p.receipts = b
	return b, &now
}

// AC1: a Claude do-not-disturb hold prints its receipt; the words before it
// are today's. Red at base: no receipt.
func TestReceipts_ClaudeDNDHoldPrintsItsReceipt(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	b, _ := fakeBook(p, time.Now())
	tu := newTestTUI(p)
	p.startDND()
	r := dndSend(t, tu, "eng1", false, "[from eng1] later")
	id := receiptOf(r.Data)
	if r.Delivery != DeliveryHeld || id == "" || !strings.HasPrefix(r.Data, "held: a is in do-not-disturb (") {
		t.Fatalf("reply %+v, want the held line ending with a receipt", r)
	}
	if st := b.state("a", id); st.State != ReceiptHeld || st.From != "eng1" || st.Kind != heldKindDND {
		t.Fatalf("state %+v", st)
	}
	if q := dndQueue(p); len(q) != 1 || q[0].Receipt != id {
		t.Fatalf("queue %+v, want the message with its receipt", q)
	}
}

// AC1: a Claude dialog defer prints its receipt after today's words.
func TestReceipts_ClaudeDialogDeferPrintsItsReceipt(t *testing.T) {
	f := &pkinFake{body: nma5Body, after: "❯ ", takes: func(int) bool { return true }}
	tu, p := v69Rig(t, f)
	p.latchDialogOpen("")
	resp := v69Send(t, tu, "eng2", nma5Body, true)
	if !resp.OK || resp.Delivery != DeliveryDeferred || !strings.HasPrefix(resp.Data, "deferred to eng2 — modal open") || receiptOf(resp.Data) == "" {
		t.Fatalf("reply %+v, want the deferral words ending with a receipt", resp)
	}
	if q := dndQueue(p); len(q) != 1 || q[0].Receipt != receiptOf(resp.Data) {
		t.Fatalf("queue %+v", q)
	}
}

// AC1: a send held because the pane is not reading prints its receipt.
func TestReceipts_NotReadingHoldPrintsItsReceipt(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	v := holdForStuckInput(p, "stuck", true)
	resp := verdictIPCResponse("a", v, "")
	if v.Outcome != DeliveryDeferred || resp.Data != StuckInputDeferral+receiptLine(v.Receipt) || v.Receipt == "" {
		t.Fatalf("verdict %+v reply %+v", v, resp)
	}
}

// AC1: a send to a suspended Claude pane at the machine ceiling prints its
// receipt after the ceiling words, with kind ceiling.
func TestReceipts_CeilingDeferPrintsItsReceipt(t *testing.T) {
	parked := NewParkedPane(PaneConfig{Name: "eng1"}, 10, 40)
	tu := ceilingTUI(t, ceilingAt(t, 99), time.Now(), parked)
	resp := ipcCall(t, func(c net.Conn) {
		tu.handleIPCSend(c, IPCRequest{Target: "eng1", Text: "held-ceiling-message", Enter: true, From: "eng3"})
	})
	id := receiptOf(resp.Data)
	if !resp.OK || resp.Delivery != DeliveryDeferred || resp.Data != CeilingDeferral+receiptLine(id) || id == "" {
		t.Fatalf("reply %+v, want the ceiling words ending with a receipt", resp)
	}
	if st := tu.receiptState("eng1", id); st.State != ReceiptHeld || st.Kind != ReceiptKindCeiling || st.From != "eng3" {
		t.Fatalf("state %+v", st)
	}
}

// AC1: the wake queue of a suspended Claude pane mints a receipt, and the
// wake reply ends with it.
func TestReceipts_WakeQueuePrintsItsReceipt(t *testing.T) {
	parked := NewParkedPane(PaneConfig{Name: "eng1"}, 10, 40)
	v := parked.queueForWake("wake up", true, sendOrigin{From: "eng3"})
	if v.Receipt == "" || parked.QueueLen() != 1 {
		t.Fatalf("verdict %+v queue %d", v, parked.QueueLen())
	}
	reply := resumedSendReply("eng1", 0)
	reply.Data = withReceipt(reply.Data, v.Receipt)
	if reply.Data != "queued for eng1, woken: delivered in order now that it is up (0 ahead)"+receiptLine(v.Receipt) {
		t.Fatalf("reply %q", reply.Data)
	}
	if st := parked.book().state("eng1", v.Receipt); st.State != ReceiptHeld || st.Kind != heldKindSuspended {
		t.Fatalf("state %+v", st)
	}
}

// AC2 and feed-it-nothing: a send to a pane holding nothing keeps today's
// words, with no receipt.
func TestReceipts_ADeliveredSendHasNone(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, _, typed := dndPane(t, "a")
		tu := newTestTUI(p)
		r := dndSend(t, tu, "eng1", false, "[from eng1] now")
		if r.Delivery == DeliveryHeld || r.Delivery == DeliveryDeferred || strings.Contains(r.Data, "(receipt ") || !strings.Contains(typed(), "now") {
			t.Fatalf("reply %+v typed %q", r, typed())
		}
	})
}

// AC3: on a fake clock the receipt reads held, then typed with its time once
// do-not-disturb ends and it is typed; onReceipt is told of each change.
func TestReceipts_HeldThenTypedAndEachChangeIsTold(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, events, typed := dndPane(t, "a")
		t0 := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
		b, now := fakeBook(p, t0)
		tu := newTestTUI(p)
		var told []ReceiptState
		onReceipt = func(r ReceiptState) { told = append(told, r) }
		defer func() { onReceipt = nil }()
		p.startDND()
		id := receiptOf(dndSend(t, tu, "eng1", false, "[from eng1] later").Data)
		if st := b.state("a", id); st.State != ReceiptHeld || !st.At.IsZero() {
			t.Fatalf("held state %+v", st)
		}
		*now = t0.Add(3 * time.Minute)
		dndSet(t, tu, "a", "off")
		if !dndWait(15*time.Second, func() bool { return strings.Contains(typed(), "later") && b.state("a", id).State == ReceiptTyped }) {
			t.Fatalf("not typed: state %+v typed %q", b.state("a", id), typed())
		}
		if st := b.state("a", id); !st.At.Equal(t0.Add(3*time.Minute)) || st.Since.IsZero() || st.Since.After(st.At) {
			t.Fatalf("typed at %v (held %v), want the fake clock's typed time after the hold", st.At, st.Since)
		}
		for drained := false; !drained; {
			select {
			case ev := <-events:
				tu.handleAgentEvent(ev)
			default:
				drained = true
			}
		}
		if len(told) != 2 || told[0].State != ReceiptHeld || told[1].State != ReceiptTyped || told[1].ID != id {
			t.Fatalf("told %+v, want held then typed", told)
		}
	})
}

// Rule 3: a change event dropped on a full channel loses no state: the next
// read (a watch's next pass) sees it, and drops are logged at most once a
// minute.
func TestReceipts_ADroppedEventKeepsTheState(t *testing.T) {
	logs := captureLogs(t)
	p, _, _ := dndPane(t, "a")
	t0 := time.Now()
	b, now := fakeBook(p, t0)
	full := make(chan AgentEvent) // unbuffered, no reader: every emit drops
	p.eventCh = full
	m := QueuedMessage{Receipt: "R1", From: "eng1", DND: true, Time: t0}
	p.noteReceipt(m, ReceiptHeld)
	p.noteReceipt(m, ReceiptTyped)
	if st := b.state("a", "R1"); st.State != ReceiptTyped {
		t.Fatalf("state %+v after dropped events, want typed", st)
	}
	if n := countLines(logs, "receipt change event dropped"); n != 1 {
		t.Fatalf("%d drop lines in a minute, want 1", n)
	}
	*now = t0.Add(time.Minute)
	p.noteReceipt(QueuedMessage{Receipt: "R2", DND: true}, ReceiptHeld)
	if n := countLines(logs, "receipt change event dropped"); n != 2 {
		t.Fatalf("%d drop lines after a minute, want 2", n)
	}
}

// Rule 2: ended receipts are bounded at maxEndedReceipts, oldest first, but
// never one a waiting watch names (keep). Held ones are never pruned.
func TestReceipts_EndedAreBoundedButAKeptOneStays(t *testing.T) {
	b := newReceiptBook()
	b.keep = func(id string) bool { return id == "e1" }
	b.set("a", "held", "", heldKindDND, ReceiptHeld, "", time.Now())
	for i := 0; i <= maxEndedReceipts+1; i++ {
		b.set("a", "e"+itoa(i), "", heldKindDND, ReceiptTyped, "", time.Now())
	}
	if st := b.state("a", "e0"); st.State != ReceiptUnknown {
		t.Fatalf("oldest ended not pruned: %+v", st)
	}
	if st := b.state("a", "e1"); st.State != ReceiptTyped {
		t.Fatalf("a kept receipt was pruned: %+v", st)
	}
	if st := b.state("a", "e2"); st.State != ReceiptUnknown {
		t.Fatalf("the next oldest not pruned in the kept one's place: %+v", st)
	}
	if st := b.state("a", "held"); st.State != ReceiptHeld {
		t.Fatalf("a held receipt was pruned: %+v", st)
	}
	if n := len(b.byID); n != maxEndedReceipts+1 {
		t.Fatalf("%d receipts kept, want %d ended plus the held one", n, maxEndedReceipts+1)
	}
}

// AC4: after a restart a held receipt reads held, one being typed reads
// unknown, one whose pane is gone reads dropped, and an older entry with no
// receipt gets one.
func TestReceipts_ARestartRestoresThem(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	b, _ := fakeBook(p, time.Now())
	old := receipts
	receipts = b // restoredReceipt writes the process's book
	defer func() { receipts = old }()
	now := time.Now()
	entries := []heldEntry{
		{Receipt: "H1", Pane: "a", Text: "x", Enter: true, Time: now.Add(-time.Hour), Kind: heldKindDND, From: "eng1"},
		{Receipt: "T1", Pane: "a", Text: "y", Enter: true, Time: now.Add(-time.Hour), Kind: heldKindDND, From: "eng1", State: heldStateTyping},
		{Receipt: "G1", Pane: "gone", Text: "z", Enter: true, Time: now.Add(-time.Hour), Kind: heldKindDND, From: "eng1"},
		{Pane: "a", Text: "old", Enter: true, Time: now.Add(-time.Hour), Kind: heldKindDND, From: "eng1"},
	}
	p.startDND() // keep the drain from typing them
	requeueHeld(entries, func(n string) *Pane {
		if n == "a" {
			return p
		}
		return nil
	}, now)
	for id, want := range map[string]string{"H1": ReceiptHeld, "T1": ReceiptUnknown, "G1": ReceiptDropped, "never": ReceiptUnknown} {
		if st := b.state("", id); st.State != want {
			t.Errorf("%s reads %s, want %s", id, st.State, want)
		}
	}
	// The one being typed is recorded, not merely absent: a watch reads its
	// sender and pane to check whose it is.
	if st := b.state("", "G1"); st.Why != "gone no longer exists" {
		t.Errorf("the dropped entry names no reason: %+v", st)
	}
	if st := b.state("", "T1"); st.Pane != "a" || st.From != "eng1" {
		t.Errorf("the typing entry was not recorded: %+v", st)
	}
	q := dndQueue(p)
	if len(q) != 2 || q[1].Receipt == "" || b.state("a", q[1].Receipt).State != ReceiptHeld {
		t.Fatalf("queue %+v: the older entry got no receipt", q)
	}
}

// AC5: --withdraw works on a receipt from each Claude hold kind, refuses
// another agent's, and the receipt reads withdrawn.
func TestReceipts_WithdrawWorksForEveryKind(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	b, _ := fakeBook(p, time.Now())
	p.startDND()
	dnd, _ := p.holdIfDND("dnd", true, "eng1", "")
	p.mu.Lock()
	p.sendFrom = "eng1"
	p.mu.Unlock()
	_, dialog := p.enqueueHeldReceipt("dialog", true, sendAgainWhenDialog)
	stuck := holdForStuckInput(p, "stuck", true)
	p.mu.Lock()
	p.sendFrom = ""
	p.mu.Unlock()
	parked := NewParkedPane(PaneConfig{Name: "a"}, 10, 40)
	parked.receipts = b
	wake := parked.queueForWake("wake", true, sendOrigin{From: "eng1"})
	for _, c := range []struct {
		pane *Pane
		id   string
	}{{p, dnd.Receipt}, {p, dialog}, {p, stuck.Receipt}, {parked, wake.Receipt}} {
		if c.id == "" {
			t.Fatal("a hold minted no receipt")
		}
		if _, err := c.pane.withdrawReceipt(c.id, "eng2"); err == nil || !strings.Contains(err.Error(), "another sender") {
			t.Fatalf("%s: another agent withdrew it: %v", c.id, err)
		}
		if line, err := c.pane.withdrawReceipt(c.id, "eng1"); err != nil || line != "withdrawn: "+c.id {
			t.Fatalf("%s: %q %v", c.id, line, err)
		}
		if st := b.state("", c.id); st.State != ReceiptWithdrawn {
			t.Fatalf("%s reads %+v", c.id, st)
		}
	}
}

// Rule 2, wired (lead read of f8a72c4): the process book never prunes a
// receipt a waiting send watch names. The main goroutine publishes the set
// (publishWatchedReceipts) and the book's keep reads only that; once the
// watch ends and the set is published again, the receipt prunes like any
// other.
func TestReceipts_AWatchedReceiptIsNeverPruned(t *testing.T) {
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	s, _ := watch.Open(filepath.Join(t.TempDir(), "watches.yaml"), now)
	id, err := s.Add(watch.Watch{Owner: "eng1", Kind: watch.KindSendDelivered, Params: map[string]string{"pane": "eng2", "receipt": "W1"},
		Why: "see my send land", Deadline: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatal(err)
	}
	tu := newTestTUI()
	tu.watches = s
	defer watchedReceipts.Store(nil)
	tu.publishWatchedReceipts(now)
	b := newReceiptBook()
	b.keep = receipts.keep // the process book's wiring, not a test stub
	fill := func() {
		b.set("eng2", "W1", "eng1", heldKindDND, ReceiptTyped, "", now)
		for i := 0; i <= maxEndedReceipts; i++ {
			b.set("eng2", "f"+itoa(i), "", heldKindDND, ReceiptTyped, "", now)
		}
	}
	fill()
	if st := b.state("", "W1"); st.State != ReceiptTyped {
		t.Fatalf("a watched receipt was pruned: %+v", st)
	}
	if err := s.Cancel(id, "eng1", now); err != nil {
		t.Fatal(err)
	}
	tu.publishWatchedReceipts(now)
	b = newReceiptBook()
	b.keep = receipts.keep
	fill()
	if st := b.state("", "W1"); st.State != ReceiptUnknown {
		t.Fatalf("an unwatched receipt was kept past the bound: %+v", st)
	}
}

// qa2's survivors on 70be8ac, each a rule the DONE claimed; pinned here.

// Y1: an ended receipt stays ended: a late held (a stale re-hold racing the
// typing) never revives it.
func TestReceipts_AnEndedReceiptIsNeverRevived(t *testing.T) {
	b := newReceiptBook()
	for _, end := range []string{ReceiptTyped, ReceiptWithdrawn, ReceiptDropped} {
		id := "R-" + end
		b.set("a", id, "eng1", heldKindDND, ReceiptHeld, "", time.Now())
		b.set("a", id, "eng1", heldKindDND, end, "", time.Now())
		if _, changed := b.set("a", id, "eng1", heldKindDND, ReceiptHeld, "", time.Now()); changed || b.state("a", id).State != end {
			t.Errorf("%s revived to %s", end, b.state("a", id).State)
		}
	}
}

// Y3: the bound is pm's 1,000, exactly: 1,000 ended receipts are all kept,
// the 1,001st prunes the oldest.
func TestReceipts_TheBoundIsOneThousand(t *testing.T) {
	if maxEndedReceipts != 1000 {
		t.Fatalf("maxEndedReceipts = %d, pm ruled 1,000", maxEndedReceipts)
	}
	b := newReceiptBook()
	for i := 0; i < 1000; i++ {
		b.set("a", "b"+itoa(i), "", heldKindDND, ReceiptTyped, "", time.Now())
	}
	if st := b.state("a", "b0"); st.State != ReceiptTyped {
		t.Fatalf("pruned at 1,000: %+v", st)
	}
	b.set("a", "b1000", "", heldKindDND, ReceiptTyped, "", time.Now())
	if st := b.state("a", "b0"); st.State != ReceiptUnknown || b.state("a", "b1").State != ReceiptTyped {
		t.Fatalf("the 1,001st did not prune exactly the oldest: b0 %+v", st)
	}
}

// Y4: one mint rule: a message that already has a receipt keeps it through
// every hold a re-send can meet (do-not-disturb, the wake queue, a dialog or
// not-reading hold), so the sender's receipt stays the one that names it.
func TestReceipts_AReSentMessageKeepsItsReceipt(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	fakeBook(p, time.Now())
	p.startDND()
	if v, _ := p.holdIfDND("dnd", true, "eng1", "KEEP-DND"); v.Receipt != "KEEP-DND" || dndQueue(p)[0].Receipt != "KEEP-DND" {
		t.Fatalf("dnd re-hold minted %q", v.Receipt)
	}
	p.setSendReceipt("KEEP-DIALOG")
	_, r := p.enqueueHeldReceipt("dialog", true, sendAgainWhenDialog)
	p.setSendReceipt("")
	if r != "KEEP-DIALOG" {
		t.Fatalf("dialog re-hold minted %q", r)
	}
	parked := NewParkedPane(PaneConfig{Name: "w"}, 10, 40)
	if v := parked.queueForWake("wake", true, sendOrigin{From: "eng1", Receipt: "KEEP-WAKE"}); v.Receipt != "KEEP-WAKE" {
		t.Fatalf("wake re-hold minted %q", v.Receipt)
	}
	m := QueuedMessage{Receipt: "KEEP"}
	if mintReceipt(&m); m.Receipt != "KEEP" {
		t.Fatalf("mintReceipt replaced %q", m.Receipt)
	}
}

// Y6: a re-send the send path refuses is dropped, with its reason; a held
// or deferred one stays held; any other verdict is typed.
func TestReceipts_ARefusedReSendIsDropped(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	b, _ := fakeBook(p, time.Now())
	for _, c := range []struct {
		v    SendVerdict
		want string
	}{
		{SendVerdict{Outcome: DeliveryNotDelivered, Reason: "the agent is gone"}, ReceiptDropped},
		{SendVerdict{Outcome: DeliveryHeld}, ReceiptHeld},
		{SendVerdict{Outcome: DeliveryDeferred}, ReceiptHeld},
		{SendVerdict{Outcome: DeliveryUnconfirmed}, ReceiptTyped},
	} {
		m := QueuedMessage{Receipt: "S-" + c.v.Outcome, From: "eng1", DND: true, Time: time.Now()}
		p.noteReceipt(m, ReceiptHeld)
		p.settleReceipt(m, c.v)
		st := b.state("a", m.Receipt)
		if st.State != c.want || (c.want == ReceiptDropped && st.Why != "the agent is gone") {
			t.Errorf("%s: %+v, want %s", c.v.Outcome, st, c.want)
		}
	}
}
