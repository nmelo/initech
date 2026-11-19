package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// ini-79oo: a pane in do-not-disturb shows how many messages it is holding,
// "dnd (N)" on its ribbon, from the one count initech status and the
// sender's "N messages ahead" use. Nothing held reads plain "dnd"; off reads
// today's ribbon.

// AC1, AC2, AC5: the count follows the queue at the next render, and plain
// "dnd" while nothing is held.
func TestDNDCount_TheRibbonShowsHowManyAreHeld(t *testing.T) {
	p := dndRibbonPane()
	p.startDNDHold(time.Now(), 0, true)
	if got := dndRibbon(t, p); !strings.Contains(got, "3 eng1 dnd ") || strings.Contains(got, "(") {
		t.Fatalf("nothing held: %q, want plain \"3 eng1 dnd\"", got)
	}
	for i, want := range []string{"3 eng1 dnd (1)", "3 eng1 dnd (2)", "3 eng1 dnd (3)"} {
		if _, held := p.holdIfDND("message", true, "eng2", ""); !held {
			t.Fatalf("send %d was not held", i)
		}
		if got := dndRibbon(t, p); !strings.Contains(got, want) {
			t.Fatalf("after %d held: %q, want %q", i+1, got, want)
		}
	}
	p.mu.Lock()
	p.messageQueue = p.messageQueue[1:] // one leaves the queue
	p.mu.Unlock()
	if got := dndRibbon(t, p); !strings.Contains(got, "3 eng1 dnd (2)") {
		t.Errorf("after one left: %q, want \"3 eng1 dnd (2)\"", got)
	}
}

// AC4: with do-not-disturb off the ribbon is today's, mail in the queue or not.
func TestDNDCount_OffTheRibbonIsUnchanged(t *testing.T) {
	empty := dndRibbon(t, dndRibbonPane())
	p := dndRibbonPane()
	p.mu.Lock()
	p.messageQueue = []QueuedMessage{{Text: "a", HeldForDialog: true}, {Text: "b", HeldForDialog: true}}
	p.mu.Unlock()
	if got := dndRibbon(t, p); got != empty {
		t.Errorf("off with 2 queued: %q, want today's %q", got, empty)
	}
}

// AC3: on a Codex pane whose screen shows two messages Codex queued itself,
// the ribbon, the list reply's queued_count and the next sender's "N
// messages ahead" are one number, and a withdrawal moves all of them.
func TestDNDCount_RibbonStatusAndSenderAgree(t *testing.T) {
	tu, p, _ := agentCaptureRig(t, "codex", agentCapture(t, "local-codex-0170-queued-two"))
	p.region = Region{X: 0, Y: 0, W: 120, H: 48}
	p.startDNDHold(time.Now(), 0, true)

	agree := func(when string, want int) {
		t.Helper()
		look, _ := p.chromeLook(false, 3)
		if mark := " dnd (" + strconv.Itoa(want) + ")"; !strings.Contains(look.title, mark) {
			t.Fatalf("%s: ribbon %q, want %q", when, look.title, mark)
		}
		seats, ok := tu.AllPanes()
		if !ok || len(seats) != 1 || seats[0].QueuedCount != want {
			t.Fatalf("%s: list reply %+v, want queued_count %d", when, seats, want)
		}
	}
	v, held := p.holdIfDND("first", true, "eng2", "")
	if !held || !strings.Contains(v.Reason, "2 messages ahead") {
		t.Fatalf("first send: %+v, want held behind the 2 Codex queued", v)
	}
	agree("one held, two native", 3)
	v2, _ := p.holdIfDND("second", true, "eng2", "")
	if !strings.Contains(v2.Reason, "3 messages ahead") {
		t.Fatalf("second send: %q, want 3 ahead, the number the ribbon showed", v2.Reason)
	}
	agree("two held, two native", 4)
	_, receipt, _ := strings.Cut(v.Reason, "(receipt ")
	receipt = strings.TrimSuffix(receipt, ")")
	if _, err := p.withdrawReceipt(receipt, "eng2"); err != nil {
		t.Fatalf("withdraw %q: %v", receipt, err)
	}
	agree("one withdrawn", 3)

	// A screen whose lock is held keeps the last reading: the count never
	// drops the native two for a frame.
	p.renderMu.Lock()
	look, _ := p.chromeLook(false, 3)
	p.renderMu.Unlock()
	if !strings.Contains(look.title, " dnd (3)") {
		t.Errorf("screen busy: ribbon %q, want the last count (3)", look.title)
	}
}

// Narrow tiles: the number wins over the word, and the name gives way only
// after the word has gone. Nothing held keeps today's rule exactly.
func TestDNDCount_NarrowTheNumberWinsOverTheWord(t *testing.T) {
	prefix := " 3 eng1"
	for _, tc := range []struct {
		width, held int
		want        string
	}{
		{60, 0, " 3 eng1 dnd "},
		{11, 0, " 3 eng dnd "},
		{4, 0, "dnd "},
		{60, 3, " 3 eng1 dnd (3) "},
		{16, 3, " 3 eng1 dnd (3) "},
		{15, 3, " 3 eng1 (3) "},
		{12, 3, " 3 eng1 (3) "},
		{11, 3, " 3 eng (3) "},
		{5, 3, "(3) "},
		{60, 12, " 3 eng1 dnd (12) "},
		{13, 12, " 3 eng1 (12) "},
	} {
		if got := dndTitle([]rune(prefix), tc.width, tc.held); got != tc.want {
			t.Errorf("width %d, %d held: %q, want %q", tc.width, tc.held, got, tc.want)
		}
	}
}
