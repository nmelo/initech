package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nmelo/initech/internal/watch"
)

// Watches 2C (ini-kbpo): a fired or missed watch reaches its owner as one
// line, or the rig's super, by the diagram, exactly once across a stop.

var wdT0 = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// deliverRig is window 1 with an owner pane (eng4), a super pane, and a
// watch store under root, loaded as window 1 loads it.
type deliverRig struct {
	tu     *TUI
	owner  *Pane
	typed  func() string
	events chan AgentEvent
	root   string
	lines  *lineRecorder
}

func newDeliverRig(t *testing.T, root string) *deliverRig {
	t.Helper()
	return newDeliverRigWith(t, root, nil, nil)
}

// newDeliverRigWith is newDeliverRig as a fresh window 1: the owner's own
// receipt book (a restart starts with none), held mail put back by before
// (held.yaml loads first), then the watch store.
func newDeliverRigWith(t *testing.T, root string, lines *lineRecorder, before func(owner *Pane)) *deliverRig {
	t.Helper()
	owner, events, typed := dndPane(t, "eng4")
	owner.receipts = newReceiptBook()
	sup, _, _ := dndPane(t, "super")
	// The panes close with the test, so a startup-gate watcher stops.
	t.Cleanup(owner.markClosing)
	t.Cleanup(sup.markClosing)
	tu := newTestTUI(owner, sup)
	if lines == nil {
		lines = recordInitechLines(t)
	}
	r := &deliverRig{tu: tu, owner: owner, typed: typed, events: events, root: root, lines: lines}
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0o700); err != nil {
		t.Fatal(err)
	}
	if before != nil {
		before(owner)
	}
	stubAfter(t)
	tu.loadWatches(root, wdT0)
	t.Cleanup(func() { onReceipt = nil })
	return r
}

// end adds a watch for owner and ends it as state.
func (r *deliverRig) end(t *testing.T, owner string, state watch.State, detail string, hold bool) string {
	t.Helper()
	id, err := r.tu.watches.Add(watch.Watch{Owner: owner, Kind: watch.KindDeadline, Why: "check the build",
		Deadline: wdT0.Add(time.Hour), HoldIfAsleep: hold}, wdT0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.tu.watches.End(id, state, detail, wdT0, wdT0); err != nil {
		t.Fatal(err)
	}
	return id
}

// pump hands the receipt events to window 1's loop, as Run does.
func (r *deliverRig) pump() {
	for {
		select {
		case ev := <-r.events:
			r.tu.handleAgentEvent(ev)
		default:
			return
		}
	}
}

func (r *deliverRig) watch(id string) watch.Watch {
	for _, w := range r.tu.watches.Snapshot(wdT0) {
		if w.ID == id {
			return w
		}
	}
	return watch.Watch{}
}

func (r *deliverRig) superLines() []string {
	var out []string
	for _, l := range r.lines.all() {
		if strings.HasPrefix(l, "super|") {
			out = append(out, l)
		}
	}
	return out
}

// settle lets the drains and the receipt events finish: two seconds of the
// bubble's fake clock (no real time passes), pumping events as they come.
func (r *deliverRig) settle() {
	for i := 0; i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		r.pump()
	}
}

// At a stop: one line typed, with the reason; delivered from its receipt.
func TestWatchDeliver_AtAStopTheLineIsTypedAndDelivered(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		r := newDeliverRig(t, t.TempDir())
		id := r.end(t, "eng4", watch.Fired, "its deadline came", false)
		r.tu.judgeWatchPass(wdT0)
		r.settle()
		if n := strings.Count(r.typed(), "[from initech, watch "+id+"] FIRED: check the build (its deadline came)"); n != 1 {
			t.Fatalf("typed %d times: %q", n, r.typed())
		}
		if w := r.watch(id); w.Delivery.Handed.IsZero() || w.Delivery.Delivered.IsZero() || w.Delivery.Receipt == "" {
			t.Fatalf("delivery %+v, want handed and delivered with a receipt", w.Delivery)
		}
	})
}

// Endings for one owner together go in one message, one line each.
func TestWatchDeliver_EndingsTogetherGoInOneMessage(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		r := newDeliverRig(t, t.TempDir())
		r.owner.startDND() // hold it so the queued message can be read
		a := r.end(t, "eng4", watch.Fired, "one", false)
		b := r.end(t, "eng4", watch.Fired, "two", false)
		r.tu.judgeWatchPass(wdT0)
		q := dndQueue(r.owner)
		if len(q) != 1 || len(q[0].WatchIDs) != 2 || strings.Count(q[0].Text, "\n") != 1 ||
			!strings.Contains(q[0].Text, "watch "+a+"]") || !strings.Contains(q[0].Text, "watch "+b+"]") {
			t.Fatalf("queue %+v, want one message with two lines", q)
		}
	})
}

// Mid-turn, typing or do-not-disturb: held like a send, delivered at the
// next stop, never before.
func TestWatchDeliver_DoNotDisturbHoldsItUntilTheNextStop(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		r := newDeliverRig(t, t.TempDir())
		r.owner.startDND()
		id := r.end(t, "eng4", watch.Fired, "", false)
		r.tu.judgeWatchPass(wdT0)
		r.settle()
		if strings.Contains(r.typed(), "watch "+id) || !r.watch(id).Delivery.Delivered.IsZero() {
			t.Fatalf("typed or delivered during do-not-disturb: %q", r.typed())
		}
		if r.watch(id).Delivery.Handed.IsZero() {
			t.Fatal("not handed: the line is not in held mail")
		}
		dndSet(t, r.tu, "eng4", "off")
		r.settle()
		if strings.Count(r.typed(), "watch "+id+"]") != 1 || r.watch(id).Delivery.Delivered.IsZero() {
			t.Fatalf("after dnd off: typed %q, delivery %+v", r.typed(), r.watch(id).Delivery)
		}
	})
}

// AC-S, red-first: a watch fires while its owner's pane is starting; the
// line waits in STARTING, is never refused, and is delivered once the pane
// is ready.
func TestWatchDeliver_StartingWaitsAndDeliversOnceReady(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		r := newDeliverRig(t, t.TempDir())
		r.owner.mu.Lock()
		r.owner.startedAt, r.owner.startupSettled = time.Now(), false
		r.owner.mu.Unlock()
		id := r.end(t, "eng4", watch.Fired, "", false)
		r.tu.judgeWatchPass(time.Now())
		r.settle()
		if r.owner.QueueLen() != 0 || strings.Contains(r.typed(), "watch "+id) || !r.watch(id).Delivery.Handed.IsZero() {
			t.Fatalf("handed while starting: queue %d typed %q", r.owner.QueueLen(), r.typed())
		}
		if len(r.superLines()) != 0 {
			t.Fatalf("super told while starting: %v", r.superLines())
		}
		r.owner.openStartupGate(time.Now().Add(startupGateWindow)) // the pane is ready
		r.settle()
		if strings.Count(r.typed(), "watch "+id+"]") != 1 || r.watch(id).Delivery.Delivered.IsZero() {
			t.Fatalf("once ready: typed %q, delivery %+v", r.typed(), r.watch(id).Delivery)
		}
	})
}

// pm's row: STARTING that never ends counts toward the held bound: super is
// told once after 30 minutes.
func TestWatchDeliver_StartingThatNeverEndsTellsSuperOnce(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		r := newDeliverRig(t, t.TempDir())
		r.end(t, "eng4", watch.Fired, "", false)
		// The pane reads starting at each pass: its gate has not opened.
		at := func(now time.Time) {
			r.owner.mu.Lock()
			r.owner.startedAt, r.owner.startupSettled = now.Add(-time.Second), false
			r.owner.mu.Unlock()
			r.tu.deliverWatches(now)
		}
		at(wdT0.Add(29 * time.Minute))
		r.settle()
		if n := len(r.superLines()); n != 0 || r.owner.QueueLen() != 0 {
			t.Fatalf("before the bound: super %d, queue %d; want the line waiting in STARTING", n, r.owner.QueueLen())
		}
		at(wdT0.Add(30 * time.Minute))
		at(wdT0.Add(40 * time.Minute))
		r.settle()
		if s := r.superLines(); len(s) != 1 || !strings.Contains(s[0], "eng4's watch line has been held") || r.owner.QueueLen() != 0 {
			t.Fatalf("super lines %v, queue %d; want one held line, still not handed", s, r.owner.QueueLen())
		}
	})
}

// The owner no longer in the rig: the line goes to super, with who set it.
func TestWatchDeliver_AGoneOwnersLineGoesToSuper(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		r := newDeliverRig(t, t.TempDir())
		id := r.end(t, "eng9", watch.Fired, "", false)
		r.tu.judgeWatchPass(wdT0)
		r.settle()
		s := r.superLines()
		if len(s) != 1 || !strings.Contains(s[0], "for eng9, who is no longer in the rig: [from initech, watch "+id+"] FIRED") || !r.watch(id).Delivery.ToSuper {
			t.Fatalf("super lines %v delivery %+v", s, r.watch(id).Delivery)
		}
	})
}

// pm's row: a stopped owner still in the rig is held, not sent to super at
// once; super is told after 30 minutes; it is delivered when it runs again.
func TestWatchDeliver_AStoppedOwnerIsHeldNotSentToSuper(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		r := newDeliverRig(t, t.TempDir())
		r.owner.mu.Lock()
		r.owner.alive = false
		r.owner.mu.Unlock()
		id := r.end(t, "eng4", watch.Fired, "", false)
		r.tu.deliverWatches(wdT0)
		r.settle()
		if len(r.superLines()) != 0 || r.watch(id).Delivery.ToSuper || r.owner.QueueLen() != 0 {
			t.Fatalf("stopped owner: super %v, delivery %+v, queue %d", r.superLines(), r.watch(id).Delivery, r.owner.QueueLen())
		}
		r.tu.deliverWatches(wdT0.Add(30 * time.Minute))
		r.settle()
		if len(r.superLines()) != 1 {
			t.Fatalf("super not told once after the bound: %v", r.superLines())
		}
		r.owner.mu.Lock()
		r.owner.alive = true
		r.owner.mu.Unlock()
		r.tu.deliverWatches(wdT0.Add(31 * time.Minute))
		r.settle()
		if strings.Count(r.typed(), "watch "+id+"]") != 1 {
			t.Fatalf("not delivered once it runs again: %q", r.typed())
		}
	})
}

// A suspended owner with --hold-if-asleep keeps its line owed, not woken.
func TestWatchDeliver_HoldIfAsleepKeepsItOwed(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		r := newDeliverRig(t, t.TempDir())
		woke := 0
		r.owner.mu.Lock()
		r.owner.suspended = true
		r.owner.onSuspendedMessage = func(*Pane) { woke++ }
		r.owner.mu.Unlock()
		held := r.end(t, "eng4", watch.Fired, "", true)
		r.tu.deliverWatches(wdT0)
		if woke != 0 || !r.watch(held).Delivery.Handed.IsZero() {
			t.Fatalf("hold-if-asleep: woke %d, delivery %+v", woke, r.watch(held).Delivery)
		}
		wake := r.end(t, "eng4", watch.Fired, "", false)
		r.tu.deliverWatches(wdT0)
		if woke != 1 || r.watch(wake).Delivery.Handed.IsZero() || r.owner.QueueLen() != 1 {
			t.Fatalf("default: woke %d, delivery %+v, queue %d", woke, r.watch(wake).Delivery, r.owner.QueueLen())
		}
	})
}

// pm's row and lead1's note: a MISSED ending tells the owner's super exactly
// once, across a restart between the ending and the line; a send the owner
// withdrew tells it nothing.
func TestWatchDeliver_AMissTellsSuperOnceAcrossARestart(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		root := t.TempDir()
		r := newDeliverRig(t, root)
		r.owner.startDND() // keep the owner's line held; super is the point
		id := r.end(t, "eng4", watch.Missed, "its deadline passed", false)
		r.end(t, "eng4", watch.Missed, "your send to eng2"+watchWithdrawnSuffix, false)
		r.tu.deliverWatches(wdT0)
		r.settle()
		if s := r.superLines(); len(s) != 1 || !strings.Contains(s[0], "eng4's watch missed: [from initech, watch "+id+"] MISSED") {
			t.Fatalf("super lines %v, want one for the miss and none for the withdrawal", s)
		}
		// Window 1 restarts: the store is read back from its file.
		r2 := newDeliverRig(t, root)
		r2.tu.deliverWatches(wdT0.Add(time.Minute))
		r2.settle()
		if s := r2.superLines(); len(s) != 0 {
			t.Fatalf("super told again after the restart: %v", s)
		}
	})
}

// Feed-it-nothing: no owed ending, no send; and a pass that runs again does
// not hand a line twice.
func TestWatchDeliver_NothingOwedSendsNothingAndAPassIsIdempotent(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		r := newDeliverRig(t, t.TempDir())
		r.tu.judgeWatchPass(wdT0)
		r.settle()
		if r.typed() != "" || len(r.lines.all()) != 0 {
			t.Fatalf("sent with nothing owed: typed %q lines %v", r.typed(), r.lines.all())
		}
		r.owner.startDND()
		r.end(t, "eng4", watch.Fired, "", false)
		r.tu.deliverWatches(wdT0)
		r.tu.deliverWatches(wdT0)
		if q := dndQueue(r.owner); len(q) != 1 {
			t.Fatalf("queue %d, want the line held once", len(q))
		}
	})
}

// lead1's note: a handed line's receipt is kept from pruning until it is
// delivered.
func TestWatchDeliver_AHandedLinesReceiptIsKept(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		r := newDeliverRig(t, t.TempDir())
		r.owner.startDND()
		id := r.end(t, "eng4", watch.Fired, "", false)
		r.tu.deliverWatches(wdT0)
		rc := r.watch(id).Delivery.Receipt
		if rc == "" || !keepWatchedReceipt(rc) {
			t.Fatalf("handed receipt %q not kept", rc)
		}
	})
}

// The four stop points (the chief of staff's AC): a stop at each, then a restart; the
// line goes out exactly once, never zero, never two.

// Stop after (1): the ending is on disk, nothing handed, held mail empty.
func TestWatchDeliver_StopAfterTheEndingDeliversOnce(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		root := t.TempDir()
		r := newDeliverRig(t, root)
		id := r.end(t, "eng4", watch.Fired, "", false) // stop here: no pass ran
		r2 := newDeliverRig(t, root)                   // restart: loadWatches delivers
		r2.settle()
		if n := strings.Count(r2.typed(), "watch "+id+"]"); n != 1 {
			t.Fatalf("typed %d times after the restart", n)
		}
	})
}

// Stop after (2): held mail holds the line with the watch id; watches.yaml
// has no MarkHanded. It is adopted, not sent again.
func TestWatchDeliver_StopAfterHeldMailAdoptsTheLine(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		root := t.TempDir()
		r := newDeliverRig(t, root)
		id := r.end(t, "eng4", watch.Fired, "", false)
		line := watchLineText(r.watch(id))
		// held.yaml restored the line before the stop could mark it handed.
		r2 := newDeliverRigWith(t, root, nil, func(owner *Pane) {
			owner.startDND()
			requeueHeld([]heldEntry{heldEntryOf("eng4", QueuedMessage{Text: line, Enter: true, From: initechSender, Time: wdT0, Receipt: "HELD1", WatchIDs: []string{id}, DND: true}, heldStateHeld)},
				func(n string) *Pane { return owner }, wdT0)
			owner.startDND()
		})
		if q := dndQueue(r2.owner); len(q) != 1 || r2.watch(id).Delivery.Receipt != "HELD1" {
			t.Fatalf("queue %d, receipt %q: want the held line adopted", len(q), r2.watch(id).Delivery.Receipt)
		}
		dndSet(t, r2.tu, "eng4", "off")
		r2.settle()
		if n := strings.Count(r2.typed(), "watch "+id+"]"); n != 1 {
			t.Fatalf("typed %d times", n)
		}
	})
}

// Stop after (3): handed, and the line is still in held mail: typed from
// there, once.
func TestWatchDeliver_StopAfterHandedTypesFromHeldMail(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		root := t.TempDir()
		r := newDeliverRig(t, root)
		r.owner.startDND()
		id := r.end(t, "eng4", watch.Fired, "", false)
		r.tu.deliverWatches(wdT0)
		q := dndQueue(r.owner)
		if len(q) != 1 {
			t.Fatalf("queue %d", len(q))
		}
		// Restart: held.yaml puts the line back, held, before watches load.
		r2 := newDeliverRigWith(t, root, r.lines, func(owner *Pane) {
			requeueHeld([]heldEntry{heldEntryOf("eng4", q[0], heldStateHeld)}, func(n string) *Pane { return owner }, wdT0)
		})
		r2.settle()
		if n := strings.Count(r2.typed(), "watch "+id+"]"); n != 1 || r2.watch(id).Delivery.Delivered.IsZero() {
			t.Fatalf("typed %d times, delivery %+v", n, r2.watch(id).Delivery)
		}
	})
}

// Stop after typing, before MarkDelivered: handed, the line gone from held
// mail, no drop recorded. It reads delivered and is never handed again.
func TestWatchDeliver_StopAfterTypingReadsDelivered(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		root := t.TempDir()
		r := newDeliverRig(t, root)
		r.owner.startDND()
		id := r.end(t, "eng4", watch.Fired, "", false)
		r.tu.deliverWatches(wdT0) // handed; the stop comes after it was typed
		r2 := newDeliverRig(t, root)
		r2.settle()
		if r2.typed() != "" || r2.watch(id).Delivery.Delivered.IsZero() {
			t.Fatalf("after the restart: typed %q, delivery %+v; want delivered, nothing typed", r2.typed(), r2.watch(id).Delivery)
		}
	})
}

// Between held mail (2) and MarkHanded (3) a watch line is never typed: both
// drains stop at a pending head.
func TestWatchDeliver_APendingLineIsNeverTypedBeforeItIsHanded(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		r := newDeliverRig(t, t.TempDir())
		r.owner.mu.Lock()
		r.owner.messageQueue = []QueuedMessage{{Text: "[from initech, watch w9] FIRED: x", Enter: true, From: initechSender, Receipt: "P1", WatchIDs: []string{"w9"}, watchPending: true}}
		r.owner.mu.Unlock()
		r.owner.startModalDrain()
		r.settle()
		if strings.Contains(r.typed(), "watch w9") || r.owner.QueueLen() != 1 {
			t.Fatalf("a pending line was typed: %q, queue %d", r.typed(), r.owner.QueueLen())
		}
	})
}

// lead1's read of cb940a1: a held-mail write that fails leaves the line in
// memory only. It is not marked handed, so a stop then a restart hands it
// again and it is typed once, never read delivered untyped.
func TestWatchDeliver_AFailedHeldWriteIsNotHanded(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		root := t.TempDir()
		r := newDeliverRig(t, root)
		notADir := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(notADir, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		w := &heldWriter{path: filepath.Join(notADir, "held.yaml"), panes: map[*Pane][]heldEntry{}, ready: true}
		r.owner.setHeldHook(w) // every write fails: its folder is a file
		r.owner.startDND()
		id := r.end(t, "eng4", watch.Fired, "", false)
		r.tu.deliverWatches(wdT0)
		if !r.watch(id).Delivery.Handed.IsZero() {
			t.Fatalf("marked handed with held mail not on disk: %+v", r.watch(id).Delivery)
		}
		// A stop: the line in memory is lost. The restart hands it again.
		r2 := newDeliverRig(t, root)
		r2.settle()
		if n := strings.Count(r2.typed(), "watch "+id+"]"); n != 1 || r2.watch(id).Delivery.Delivered.IsZero() {
			t.Fatalf("after the restart: typed %d, delivery %+v; want typed once and delivered", n, r2.watch(id).Delivery)
		}
	})
}

// qa2's K5: a watch line held while its owner is in do-not-disturb is held
// as do-not-disturb mail, so the held file keeps it as such and a restart
// releases it as dnd off does (not as mail waiting for a wake).
func TestWatchDeliver_ALineHeldInDNDIsDNDMail(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		r := newDeliverRig(t, t.TempDir())
		r.owner.startDND()
		r.end(t, "eng4", watch.Fired, "", false)
		r.tu.deliverWatches(wdT0)
		q := dndQueue(r.owner)
		if len(q) != 1 || !q[0].DND || heldKindOf(q[0]) != heldKindDND {
			t.Fatalf("queue %+v: want the line held as do-not-disturb mail", q)
		}
	})
}
