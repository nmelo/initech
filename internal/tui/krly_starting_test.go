package tui

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ini-krly: a send to an agent still starting is held, not refused, on the
// readiness wait watches' STARTING uses (one mechanism), and typed once the
// pane is ready.

// startingPane is a Claude pane that reads starting: started now, not yet
// seen quiet.
func startingPane(t *testing.T, name string) (*Pane, func() string) {
	t.Helper()
	p, _, typed := dndPane(t, name)
	p.receipts = newReceiptBook()
	p.mu.Lock()
	p.startedAt, p.startupSettled = time.Now(), false
	p.mu.Unlock()
	if !p.startingNow(time.Now()) {
		t.Fatal("the fixture pane does not read starting")
	}
	// The pane closes with the test, so its startup-gate watcher stops and
	// nothing outlives it.
	t.Cleanup(p.markClosing)
	return p, typed
}

// bootingClock makes a starting pane a booting agent for the test's span
// (the bubble's clock): never quiet long enough to settle, and a drain's
// bounded wait longer than the test. Set once, first, before any pane, so no
// drain reads it while it changes.
func bootingClock(t *testing.T) {
	t.Helper()
	i35wGateClock(t, time.Hour, 10*time.Minute)
}

// settleFor lets the drains run on the bubble's clock.
func settleFor(p *Pane) {
	for i := 0; i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
	}
}

// AC1, red first (refused at base): a send from a pane, through IPC, is held
// with the held line and its receipt, exit 0, and nothing is typed.
func TestStartingSend_IsHeldWithAReceipt(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		bootingClock(t)
		p, typed := startingPane(t, "a")
		tu := newTestTUI(p)
		r := dndSend(t, tu, "eng1", false, "[from eng1] hello")
		id := receiptOf(r.Data)
		if !r.OK || r.Delivery != DeliveryHeld || !strings.HasPrefix(r.Data, "held for a: still starting, 0 ahead (receipt ") || id == "" {
			t.Fatalf("reply %+v, want the held line with a receipt", r)
		}
		if q := dndQueue(p); len(q) != 1 || q[0].Receipt != id || q[0].From != "eng1" {
			t.Fatalf("queue %+v", q)
		}
		settleFor(p)
		if typed() != "" {
			t.Fatalf("typed while starting: %q", typed())
		}
	})
}

// AC1: from a second window (the forwarded send) and through initech serve.
func TestStartingSend_IsHeldFromASecondWindowAndThroughServe(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		bootingClock(t)
		p, _ := startingPane(t, "a")
		tu := newTestTUI(p)
		v, err := tu.deliverForwardedSend("a", "[from eng1] far", true, sendOrigin{From: "eng1"})
		if err != nil || v.Outcome != DeliveryHeld || v.Receipt == "" {
			t.Fatalf("forwarded: %+v %v", v, err)
		}
		resp := servedSend(p, "[from eng1] served", true, sendOrigin{From: "eng1"})
		if !resp.OK || resp.Delivery != DeliveryHeld || receiptOf(resp.Data) == "" {
			t.Fatalf("served: %+v", resp)
		}
		if p.QueueLen() != 2 {
			t.Fatalf("queue %d, want both held", p.QueueLen())
		}
	})
}

// AC1: the held send is in the held file.
func TestStartingSend_IsInTheHeldFile(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		bootingClock(t)
		p, _ := startingPane(t, "a")
		path := filepath.Join(t.TempDir(), "held.yaml")
		w := &heldWriter{path: path, panes: map[*Pane][]heldEntry{}, ready: true}
		p.setHeldHook(w)
		v := p.holdWhileStarting("[from eng1] keep me", true, sendOrigin{From: "eng1"})
		b, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(b), v.Receipt) || !strings.Contains(string(b), "keep me") {
			t.Fatalf("held file %q (%v), want the message and its receipt", b, err)
		}
	})
}

// AC2 and AC4: once the pane reads ready the line is typed once, in order
// behind earlier held mail, never while starting; its receipt reads typed
// (a --delivered watch fires on that).
func TestStartingSend_IsTypedOnceReadyInOrder(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		bootingClock(t)
		p, typed := startingPane(t, "a")
		p.enqueue(QueuedMessage{Text: "[from b] earlier", Enter: true, Time: time.Now(), From: "b"})
		v := p.holdWhileStarting("[from eng1] later", true, sendOrigin{From: "eng1"})
		p.startModalDrain() // a drain trigger while starting takes nothing
		settleFor(p)
		if typed() != "" {
			t.Fatalf("typed while starting: %q", typed())
		}
		p.openStartupGate(time.Now().Add(startupGateWindow))
		settleFor(p)
		out := typed()
		a, b := strings.Index(out, "earlier"), strings.Index(out, "later")
		if a < 0 || b < a || strings.Count(out, "later") != 1 {
			t.Fatalf("typed %q, want earlier then later, once each", out)
		}
		// The verdict waits out the submit window (the fixture draws no
		// composer): wait on the receipt, on the bubble's clock.
		if !dndWait(30*time.Second, func() bool { return p.book().state("a", v.Receipt).State == ReceiptTyped }) {
			t.Fatalf("receipt %+v, want typed", p.book().state("a", v.Receipt))
		}
	})
}

// AC4: --withdraw removes a starting hold, and only its sender can.
func TestStartingSend_WithdrawRemovesIt(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		bootingClock(t)
		p, _ := startingPane(t, "a")
		v := p.holdWhileStarting("[from eng1] oops", true, sendOrigin{From: "eng1"})
		if _, err := p.withdrawReceipt(v.Receipt, "eng2"); err == nil {
			t.Fatal("another agent withdrew it")
		}
		if line, err := p.withdrawReceipt(v.Receipt, "eng1"); err != nil || line != "withdrawn: "+v.Receipt || p.QueueLen() != 0 {
			t.Fatalf("withdraw: %q %v, queue %d", line, err, p.QueueLen())
		}
	})
}

// AC3: assign's ready check answers ready for a starting agent (its dispatch
// is then held), and still refuses a stopped one.
func TestStartingSend_AssignsReadyCheckHoldsAndRefusesStopped(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		bootingClock(t)
		p, _ := startingPane(t, "a")
		tu := newTestTUI(p)
		ready := func() IPCResponse {
			return dndCall(t, func(c net.Conn) { dispatchIPC(tu, c, IPCRequest{Action: "send_ready", Target: "a"}, nil) })
		}
		if r := ready(); !r.OK {
			t.Fatalf("send_ready to a starting agent: %+v, want ready", r)
		}
		p.mu.Lock()
		p.alive, p.closed = false, true
		p.mu.Unlock()
		if r := ready(); r.OK || !strings.HasPrefix(r.Error, ErrPaneNotRunning.Error()) {
			t.Fatalf("send_ready to a stopped agent: %+v, want refused", r)
		}
	})
}

// AC5: a restart while held: the line comes back from the held file and is
// typed exactly once, once the new pane is ready.
func TestStartingSend_ARestartDeliversItOnce(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		bootingClock(t)
		p, _ := startingPane(t, "a")
		v := p.holdWhileStarting("[from eng1] across", true, sendOrigin{From: "eng1"})
		entry := heldEntryOf("a", dndQueue(p)[0], heldStateHeld)
		p2, typed := startingPane(t, "a")
		requeueHeld([]heldEntry{entry}, func(string) *Pane { return p2 }, time.Now())
		settleFor(p2)
		if typed() != "" {
			t.Fatalf("typed while the new pane starts: %q", typed())
		}
		p2.openStartupGate(time.Now().Add(startupGateWindow))
		settleFor(p2)
		if n := strings.Count(typed(), "across"); n != 1 || dndQueue(p2) != nil && len(dndQueue(p2)) != 0 {
			t.Fatalf("typed %d times, queue %d", n, len(dndQueue(p2)))
		}
		if !dndWait(30*time.Second, func() bool { return p2.book().state("a", v.Receipt).State == ReceiptTyped }) {
			t.Fatalf("receipt after the restart: %+v", p2.book().state("a", v.Receipt))
		}
	})
}

// AC6, feed-it-nothing: a settled pane is typed at once, with no hold; a
// stopped one is refused as today.
func TestStartingSend_ASettledPaneIsTypedAtOnceAndAStoppedOneRefused(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, _, typed := dndPane(t, "a")
		tu := newTestTUI(p)
		r := dndSend(t, tu, "eng1", false, "[from eng1] now")
		if r.Delivery == DeliveryHeld || !strings.Contains(typed(), "now") || p.QueueLen() != 0 {
			t.Fatalf("settled pane: %+v typed %q", r, typed())
		}
		p.mu.Lock()
		p.alive, p.closed = false, true
		p.mu.Unlock()
		if r := dndSend(t, tu, "eng1", false, "[from eng1] stopped"); r.OK || !strings.HasPrefix(r.Error, ErrPaneNotRunning.Error()) {
			t.Fatalf("stopped pane: %+v, want refused", r)
		}
	})
}

// R1 (lead read of c03dee6): a pane never quiet stays held for its whole
// startup window: not typed at 15 s (the send path's settle cap) nor at 59 s,
// and typed once when the window ends at 60 s.
func TestStartingSend_APaneNeverQuietIsTypedWhenItsStartupWindowEnds(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		// Never quiet, and the production settle cap (15 s): the bound the
		// drain must not stop at.
		i35wGateClock(t, time.Hour, StartupSettleBound)
		p, typed := startingPane(t, "a")
		start := time.Now()
		p.mu.Lock()
		p.startedAt = start
		p.mu.Unlock()
		p.holdWhileStarting("[from eng1] patient", true, sendOrigin{From: "eng1"})
		at := func(d time.Duration) { time.Sleep(time.Until(start.Add(d))) }
		at(15 * time.Second)
		if typed() != "" || p.QueueLen() != 1 {
			t.Fatalf("at 15 s: typed %q queue %d; want it still held", typed(), p.QueueLen())
		}
		at(59 * time.Second)
		if typed() != "" || p.QueueLen() != 1 {
			t.Fatalf("at 59 s: typed %q queue %d; want it still held", typed(), p.QueueLen())
		}
		at(61 * time.Second)
		if strings.Count(typed(), "patient") != 1 {
			t.Fatalf("after 60 s: typed %q, want it once", typed())
		}
	})
}

// Rigs 44657324152 (TestSuspendGuard_ForwardSendSite): a suspended pane is
// not "starting" even when its last start is recent. A send to it from a
// second window, or through initech serve, is queued and wakes it, as
// before; a starting hold would never wake it.
func TestStartingSend_ASuspendedPaneIsWokenNotHeldAsStarting(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		bootingClock(t)
		p, _ := startingPane(t, "a")
		woke := 0
		p.mu.Lock()
		p.suspended = true
		p.onSuspendedMessage = func(*Pane) { woke++ }
		p.mu.Unlock()
		if p.startingNow(time.Now()) {
			t.Fatal("a suspended pane reads starting")
		}
		tu := newTestTUI(p)
		v, err := tu.deliverForwardedSend("a", "[from eng1] via-forward", true, sendOrigin{From: "eng1"})
		if err != nil || strings.HasPrefix(v.Reason, "held for a: still starting") || woke != 1 {
			t.Fatalf("forwarded: %+v %v, woke %d; want queued for the wake", v, err, woke)
		}
		resp := servedSend(p, "[from eng1] via-serve", true, sendOrigin{From: "eng1"})
		if strings.HasPrefix(resp.Data, "held for a: still starting") || woke != 2 {
			t.Fatalf("served: %+v, woke %d; want queued for the wake", resp, woke)
		}
	})
}
