package tui

import (
	"strings"
	"testing"
	"time"
)

// ini-slqv: do-not-disturb held other agents' messages but let any send from
// outside a pane straight in, because the CLI stamped such a send as the
// operator's. A script in a plain shell (a commit watcher) ran a turn in
// super's quiet pane. Now a send from outside a pane is held like an agent's;
// the operator's own hand (typing, pastes, an inbox reply, the companion,
// which stamps the operator itself) still goes in, and --urgent from outside
// a pane breaks through as super's does.

// AC1: a send from outside a pane is held with the held line and released on
// dnd off in order, behind what was held before it. Red at base: typed at once.
func TestDNDOutside_AScriptsSendIsHeldAndReleasedInOrder(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, _, typed := dndPane(t, "a")
		tu := newTestTUI(p)
		p.startDND()
		p.SendTextVerdictFrom("[from b] first", true, sendOrigin{From: "b"})
		r := dndSend(t, tu, outsideSender, false, "[watcher] main moved")
		if !r.OK || r.Delivery != DeliveryHeld || !strings.HasPrefix(r.Data, "held: a is in do-not-disturb (") {
			t.Fatalf("reply %+v, want the held line", r)
		}
		if strings.Contains(typed(), "main moved") {
			t.Fatalf("typed during do-not-disturb: %q", typed())
		}
		if q := dndQueue(p); len(q) != 2 || q[1].From != outsideSender {
			t.Fatalf("queue %+v", q)
		}
		dndSet(t, tu, "a", "off")
		// In the bubble, dndWait's clock is the bubble's: no real time passes.
		if !dndWait(15*time.Second, func() bool { return p.QueueLen() == 0 && strings.Contains(typed(), "main moved") }) {
			t.Fatalf("not released: queue %d typed %q", p.QueueLen(), typed())
		}
		out := typed()
		first, second := strings.Index(out, "[from b] first"), strings.Index(out, "[watcher] main moved")
		if first < 0 || second < first {
			t.Fatalf("released out of order or not at all: %q", out)
		}
	})
}

// AC2, R3: --urgent from outside a pane goes straight in and is logged with
// its source; the rest stays held.
func TestDNDOutside_UrgentFromATerminalBreaksThroughAndIsLogged(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		logs := captureLogs(t)
		p, events, typed := dndPane(t, "a")
		tu := newTestTUI(p)
		p.startDND()
		p.SendTextVerdictFrom("[from b] waiting", true, sendOrigin{From: "b"})
		r := dndSend(t, tu, outsideSender, true, "[operator] stop now")
		if !r.OK || r.Delivery == DeliveryHeld || !strings.Contains(typed(), "[operator] stop now") {
			t.Fatalf("reply %+v typed %q, want delivered now", r, typed())
		}
		const line = "urgent from outside a pane broke do-not-disturb on a"
		if n := countLines(logs, "[dnd] "+line); n != 1 {
			t.Errorf("%d log lines %q, want 1", n, line)
		}
		if n := dndEvents(events, line); n != 1 {
			t.Errorf("%d events %q, want 1", n, line)
		}
		if q := dndQueue(p); len(q) != 1 || !p.InDND() {
			t.Errorf("queue %+v dnd %v: the break must leave the rest held", q, p.InDND())
		}
	})
}

// AC2: --urgent from an agent other than super is still refused.
func TestDNDOutside_UrgentFromAnAgentIsStillRefused(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	p.startDND()
	r := dndSend(t, tu, "eng1", true, "[from eng1] let me in")
	if r.OK || r.Delivery != DeliveryNotDelivered || r.Error != "not delivered to a: only super may break do-not-disturb" {
		t.Fatalf("reply %+v, want the refusal", r)
	}
	if strings.Contains(typed(), "let me in") || p.QueueLen() != 0 {
		t.Fatalf("refused message typed or queued: %q, %d", typed(), p.QueueLen())
	}
}

// AC3, R2: the operator's own hand still goes in: the companion and the
// inbox reply stamp the operator, not "outside a pane".
func TestDNDOutside_TheOperatorsOwnSendStillGoesIn(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, _, typed := dndPane(t, "a")
		tu := newTestTUI(p)
		p.startDND()
		r := dndSend(t, tu, operatorSender, false, "[companion] look")
		if !r.OK || r.Delivery == DeliveryHeld || !strings.Contains(typed(), "[companion] look") {
			t.Fatalf("reply %+v typed %q, want delivered now", r, typed())
		}
	})
}

// Feed-it-nothing: from outside a pane to an agent not in do-not-disturb is
// delivered as before, with nothing held.
func TestDNDOutside_ToAnAgentNotInItIsDelivered(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		p, _, typed := dndPane(t, "a")
		tu := newTestTUI(p)
		r := dndSend(t, tu, outsideSender, false, "[watcher] main moved")
		if !r.OK || r.Delivery == DeliveryHeld || !strings.Contains(typed(), "[watcher] main moved") || p.QueueLen() != 0 {
			t.Fatalf("reply %+v typed %q queue %d", r, typed(), p.QueueLen())
		}
	})
}

// A line owed to a sender outside a pane is a notice in window 1, as the
// operator's is, never typed into super as an unknown sender.
func TestDNDOutside_ALineToTheSenderIsANoticeInWindowOne(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	tu := newTestTUI(p)
	if got := tu.tellSenderPane(outsideSender); got != "" {
		t.Fatalf("tellSenderPane = %q, want the operator's notice", got)
	}
	tu.tellSender(outsideSender, "the message to a was not delivered")
	if len(tu.notifications) != 1 || tu.notifications[0].event.Detail != "the message to a was not delivered" {
		t.Fatalf("notifications %+v", tu.notifications)
	}
}

// A receipt held before an upgrade (stamped as the operator) is withdrawn by
// the same person after it (stamped outside a pane), and the reverse.
func TestDNDOutside_WithdrawAcrossTheTwoNames(t *testing.T) {
	for _, tc := range []struct{ held, by string }{{operatorSender, outsideSender}, {outsideSender, operatorSender}} {
		p, _, _ := dndPane(t, "a")
		p.startDND()
		p.mu.Lock()
		p.messageQueue = append(p.messageQueue, QueuedMessage{Receipt: "R1", Text: "x", From: tc.held, DND: true, Time: time.Now()})
		p.mu.Unlock()
		if _, err := p.withdrawReceipt("R1", tc.by); err != nil {
			t.Fatalf("held by %s, withdrawn by %s: %v", tc.held, tc.by, err)
		}
		p.mu.Lock()
		p.messageQueue = append(p.messageQueue, QueuedMessage{Receipt: "R2", Text: "y", From: tc.held, DND: true, Time: time.Now()})
		p.mu.Unlock()
		if _, err := p.withdrawReceipt("R2", "eng1"); err == nil {
			t.Fatalf("an agent withdrew the operator's receipt")
		}
	}
}
