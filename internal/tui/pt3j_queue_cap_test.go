package tui

import (
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ini-pt3j item 9: one cap on held mail, 500 messages or 1 MiB per pane, for
// every held kind; at the cap the new message is refused with one line and
// nothing held is dropped.

// fillQueue puts n messages of size bytes each on p's queue, as held mail.
func fillQueue(p *Pane, n, size int) {
	body := strings.Repeat("x", size)
	p.mu.Lock()
	for i := 0; i < n; i++ {
		p.messageQueue = append(p.messageQueue, QueuedMessage{Text: itoa(i) + body[len(itoa(i)):], Enter: true, Time: time.Now()})
	}
	p.mu.Unlock()
}

// capKinds sends one message by each held kind's path and returns its verdict.
var capKinds = []struct {
	name, when string
	setup      func(t *testing.T) *Pane
	send       func(p *Pane, text string) SendVerdict
}{
	{"suspended", sendAgainWhenResumes,
		func(t *testing.T) *Pane { p := NewParkedPane(PaneConfig{Name: "a"}, 10, 40); return p },
		func(p *Pane, text string) SendVerdict {
			return p.SendTextVerdictFrom(text, true, sendOrigin{From: "b"})
		}},
	{"do-not-disturb", sendAgainWhenTurnEnds,
		func(t *testing.T) *Pane {
			p, _, _ := dndPane(t, "a")
			dndSetActivity(p, StateRunning)
			p.startDND()
			return p
		},
		func(p *Pane, text string) SendVerdict {
			return p.SendTextVerdictFrom(text, true, sendOrigin{From: "b"})
		}},
	{"dialog", sendAgainWhenDialog,
		func(t *testing.T) *Pane {
			p, _, _ := heldMailPane(t, "a")
			p.latchDialogOpen("Claude needs your permission")
			p.noteDialogSighting()
			return p
		},
		func(p *Pane, text string) SendVerdict {
			return p.SendTextVerdictFrom(text, true, sendOrigin{From: "b"})
		}},
	{"not reading", sendAgainWhenReading,
		func(t *testing.T) *Pane { return &Pane{name: "a"} },
		func(p *Pane, text string) SendVerdict { return holdForStuckInput(p, text, true) }},
}

// AC8/AC9: by count, each kind refuses the 501st with its line and keeps all
// 500 it holds, in order.
func TestQueueCap_EachKindRefusesTheFiveHundredFirstAndDropsNothing(t *testing.T) {
	for _, k := range capKinds {
		t.Run(k.name, func(t *testing.T) {
			p := k.setup(t)
			fillQueue(p, maxMessageQueue, 4)
			v := k.send(p, "[from b] one too many")
			want := queueRefusal("a", "500 messages", k.when)
			if v.Outcome != DeliveryNotDelivered || v.Reason != want {
				t.Fatalf("verdict %+v, want not delivered with %q", v, want)
			}
			q := queueTexts(p)
			if len(q) != maxMessageQueue || q[0] != "0xxx" || q[len(q)-1] != itoa(maxMessageQueue-1)+"x" {
				t.Fatalf("queue changed at the cap: %d messages, first %q last %q", len(q), q[0], q[len(q)-1])
			}
		})
	}
}

// AC8: by bytes, 16 messages of 64 KiB fill 1 MiB and the 17th is refused,
// far below the count.
func TestQueueCap_TheSeventeenthSixtyFourKiBMessageIsRefusedByBytes(t *testing.T) {
	for _, k := range capKinds {
		t.Run(k.name, func(t *testing.T) {
			p := k.setup(t)
			fillQueue(p, 15, 64<<10)
			if v := k.send(p, strings.Repeat("y", 64<<10)); v.Outcome == DeliveryNotDelivered {
				t.Fatalf("the 16th 64 KiB message (exactly 1 MiB) was refused: %+v", v)
			}
			v := k.send(p, strings.Repeat("z", 64<<10))
			if v.Outcome != DeliveryNotDelivered || v.Reason != queueRefusal("a", "1 MiB of messages", k.when) {
				t.Fatalf("17th: %+v, want refused by bytes", v)
			}
			if n := p.QueuedMessageCount(); n != 16 {
				t.Fatalf("queue holds %d, want the 16 it took", n)
			}
		})
	}
}

// Feed it nothing: an empty queue takes an empty message and a message of
// exactly 1 MiB; one byte more is refused.
func TestQueueCap_AnEmptyQueueTakesUpToOneMiB(t *testing.T) {
	p := &Pane{name: "a"}
	if r := p.enqueueOrRefuse(QueuedMessage{}, sendAgainWhenResumes); r != "" {
		t.Fatalf("an empty message was refused: %q", r)
	}
	p = &Pane{name: "a"}
	if r := p.enqueueOrRefuse(QueuedMessage{Text: strings.Repeat("x", maxQueueBytes)}, sendAgainWhenResumes); r != "" {
		t.Fatalf("1 MiB on an empty queue was refused: %q", r)
	}
	p = &Pane{name: "a"}
	if r := p.enqueueOrRefuse(QueuedMessage{Text: strings.Repeat("x", maxQueueBytes+1)}, sendAgainWhenResumes); r == "" || p.QueuedMessageCount() != 0 {
		t.Fatalf("1 MiB + 1 was taken (refusal %q)", r)
	}
}

// The refusal is one line with the do-not-retry words and the when.
func TestQueueCap_TheRefusalIsOneLineNamingWhenToSendAgain(t *testing.T) {
	r := queueRefusal("eng1", "500 messages", sendAgainWhenResumes)
	if r != "eng1 is holding 500 messages (its limit); do not retry in a loop: send again after it resumes" || strings.Contains(r, "\n") {
		t.Fatalf("refusal %q", r)
	}
}

// A do-not-disturb refusal names how the hold ends.
func TestQueueCap_ADNDRefusalNamesHowTheHoldEnds(t *testing.T) {
	p := &Pane{name: "a"}
	p.dndUntilOff = true
	if w := p.dndWhenLocked(); w != "initech dnd off" {
		t.Fatalf("until off: %q", w)
	}
	p.dndUntilOff = false
	p.dndUntil = time.Date(2026, 11, 5, 15, 30, 0, 0, time.Local)
	if w := p.dndWhenLocked(); w != "do-not-disturb ends at 15:30" {
		t.Fatalf("timer: %q", w)
	}
	p.dndUntil = time.Time{}
	if w := p.dndWhenLocked(); w != "its turn ends" {
		t.Fatalf("turn end: %q", w)
	}
}

// Putting back a message the pane already held never trims the queue: past
// the cap it keeps every one.
func TestQueueCap_PuttingBackAHeldMessageDropsNothing(t *testing.T) {
	p := &Pane{name: "a"}
	fillQueue(p, maxMessageQueue, 4)
	p.requeueFront(QueuedMessage{Text: "back", Enter: true})
	q := queueTexts(p)
	if len(q) != maxMessageQueue+1 || q[0] != "back" || q[len(q)-1] != itoa(maxMessageQueue-1)+"x" {
		t.Fatalf("queue %d, first %q last %q: want all kept", len(q), q[0], q[len(q)-1])
	}
}

// AC9 over IPC: a send to a suspended agent at the cap is refused, nothing
// queued, nothing woken.
func TestQueueCap_AnIPCSendToAFullSuspendedAgentIsRefusedAndWakesNothing(t *testing.T) {
	parked := NewParkedPane(PaneConfig{Name: "eng1"}, 10, 40)
	fillQueue(parked, maxMessageQueue, 4)
	tu := newTestTUI(parked)
	var spawned atomic.Int32
	tu.paneSpawner = fakePaneSpawner(t, &spawned)
	resp := ipcCall(t, func(c net.Conn) {
		tu.handleIPCSend(c, IPCRequest{Target: "eng1", Text: "[from b] one too many", Enter: true})
	})
	want := "not delivered to eng1: " + queueRefusal("eng1", "500 messages", sendAgainWhenResumes)
	if resp.OK || resp.Delivery != DeliveryNotDelivered || resp.Error != want {
		t.Fatalf("reply %+v, want %q", resp, want)
	}
	if spawned.Load() != 0 || parked.QueuedMessageCount() != maxMessageQueue {
		t.Fatalf("spawned %d, queued %d: want nothing woken or queued", spawned.Load(), parked.QueuedMessageCount())
	}
}

// AC10: a send that wakes an agent with mail ahead answers once the agent is
// up, before the queue is typed; the queue then drains in order. On the
// bubble's clock, the reply comes before one drain gap could pass.
func TestQueueCap_AWakingSendAnswersBeforeTheQueueIsTyped(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		parked := NewParkedPane(PaneConfig{Name: "eng1"}, 10, 40)
		fillQueue(parked, 3, 4)
		tu := newTestTUI(parked)
		var spawned atomic.Int32
		tu.paneSpawner = fakePaneSpawner(t, &spawned)
		start := time.Now()
		resp := i35wIPC(t, func(c net.Conn) {
			tu.handleIPCSend(c, IPCRequest{Target: "eng1", Text: "[from b] hello", Enter: true})
		})
		took := time.Since(start)
		if !resp.OK || resp.Delivery != DeliveryHeld || resp.Data != "queued for eng1, woken: delivered in order now that it is up (3 ahead)" {
			t.Fatalf("reply %+v, want held with 3 ahead", resp)
		}
		np, ok := tu.panes[0].(*Pane)
		if !ok || np == parked || spawned.Load() != 1 {
			t.Fatalf("not woken (spawned %d)", spawned.Load())
		}
		t.Cleanup(func() { np.Close() })
		// Typing 4 messages takes at least 3 drain gaps; the reply took less
		// than one gap after the agent was up.
		lock := tu.resumeLockFor("eng1")
		for !lock.TryLock() { // held by the drain until it has typed all
			time.Sleep(queueDrainInterval / 10)
		}
		lock.Unlock()
		drained := time.Since(start)
		if drained-took < 3*queueDrainInterval {
			t.Fatalf("reply at %v, drain done at %v: the reply waited for the queue", took, drained)
		}
		if parked.QueuedMessageCount() != 0 {
			t.Fatalf("%d left on the parked pane after the drain", parked.QueuedMessageCount())
		}
	})
}

// AC12: the restore at start uses the same cap by bytes: of 17 held
// messages of 64 KiB, the 16 oldest come back and the 17th's sender is told.
func TestQueueCap_TheRestoreAtStartKeepsOneMiBAndSaysTheRest(t *testing.T) {
	p := &Pane{name: "eng1"}
	var es []heldEntry
	for i := 0; i < 17; i++ {
		es = append(es, heldEntry{Pane: "eng1", Text: itoa(i) + strings.Repeat("x", 64<<10-len(itoa(i))), Enter: true,
			Time: heldT0.Add(time.Duration(i) * time.Second), Kind: heldKindSuspended, From: "eng2", State: heldStateHeld})
	}
	lines := requeueHeld(es, func(string) *Pane { return p }, heldT0.Add(time.Hour))
	if n := p.QueuedMessageCount(); n != 16 || !strings.HasPrefix(queueTexts(p)[0], "0x") || !strings.HasPrefix(queueTexts(p)[15], "15x") {
		t.Fatalf("restored %d, want the 16 oldest in order", n)
	}
	if len(lines) != 1 || lines[0].to != "eng2" || !strings.Contains(lines[0].text, "is holding 1 MiB of messages (its limit); do not retry in a loop") {
		t.Fatalf("lines %+v, want one to eng2 naming the byte limit", lines)
	}
}

// AC9 through the send: a full do-not-disturb queue's refusal names how
// that hold ends, until off or at its time.
func TestQueueCap_AFullDNDQueueSaysWhenItsHoldEnds(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(p *Pane)
		when string
	}{
		{"until off", func(p *Pane) { p.dndUntilOff = true }, "initech dnd off"},
		{"timer", func(p *Pane) { p.dndUntil = time.Now().Add(time.Hour) }, "do-not-disturb ends at " + time.Now().Add(time.Hour).Local().Format("15:04")},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, _, _ := dndPane(t, "a")
			dndSetActivity(p, StateRunning)
			p.startDND()
			p.mu.Lock()
			c.set(p)
			p.mu.Unlock()
			fillQueue(p, maxMessageQueue, 4)
			v := p.SendTextVerdictFrom("[from b] one too many", true, sendOrigin{From: "b"})
			if want := queueRefusal("a", "500 messages", c.when); v.Outcome != DeliveryNotDelivered || v.Reason != want {
				t.Fatalf("verdict %+v, want %q", v, want)
			}
		})
	}
}

// A pane suspended while a message waited at the typing hold, with its queue
// full: the message is refused with the cap's line, nothing written, nothing
// queued, nothing woken.
func TestQueueCap_APaneSuspendedDuringTheHoldWithAFullQueueRefuses(t *testing.T) {
	withTypingHold(t, 2*time.Second, 15*time.Second)
	p, r, clk := heldPaneOnPipe(t)
	woke := make(chan struct{}, 1)
	p.SetOnSuspendedMessage(func(*Pane) { woke <- struct{}{} })
	fillQueue(p, maxMessageQueue, 4)

	sent := make(chan SendVerdict, 1)
	go func() { sent <- p.SendTextVerdictFrom("MARKER_PT3J_SUSP", true, sendOrigin{From: "b"}) }()
	awaitHoldTimer(t, clk)
	p.mu.Lock()
	p.suspended = true
	p.mu.Unlock()
	clk.Advance(time.Second)
	v := <-sent // the test's -timeout bounds this wait
	if want := queueRefusal(p.name, "500 messages", sendAgainWhenResumes); v.Outcome != DeliveryNotDelivered || v.Reason != want {
		t.Fatalf("verdict %+v, want %q", v, want)
	}
	if n := p.QueuedMessageCount(); n != maxMessageQueue {
		t.Fatalf("queued %d, want the %d it held", n, maxMessageQueue)
	}
	select {
	case <-woke:
		t.Fatal("a refused message woke the pane")
	default:
	}
	// Nothing written: with the send returned, close the pane's end and read
	// the pipe to its end, no clock involved.
	p.ptmx.Close()
	if got, _ := io.ReadAll(r); len(got) != 0 {
		t.Fatalf("wrote %q to a suspended pane", got)
	}
}
