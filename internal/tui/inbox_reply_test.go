package tui

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"golang.org/x/term"
)

// ini-3wkl.6. Two claims are load-bearing here and both were bought by
// someone else's measurement, so each has a cell written against the shape
// that actually failed:
//
//   - The reply takes the SAME path a message takes. injectText has neither
//     the suspension guard nor the wake callback, so the cell that catches a
//     swap is written against a SUSPENDED pane -- on a live one the two are
//     indistinguishable, which is exactly why the bug survived review.
//   - Accept composes text and then takes the ORDINARY reply write. eng1's
//     store saw an Answer() that marked an item answered without storing the
//     text survive a whole first-pass suite, so the assertion is on --check's
//     OUTPUT, never on the keypress.

// replyTUI builds a TUI with a real inbox store over a temp root.
func replyTUI(t *testing.T, panes ...*Pane) *TUI {
	t.Helper()
	f := inboxFixtureFor(t)
	tui := newTestTUI(panes...)
	tui.projectRoot = f.root
	tui.windowID = WindowOne
	// Joined by the fixture before the root is removed (ini-yxlh), which
	// replaces the join this constructor used to carry itself (ini-5rvq).
	return f.track(tui)
}

// postItem puts an item in the store the way child B's post does.
func postItem(t *testing.T, tui *TUI, agent, body, def string) InboxItem {
	t.Helper()
	item := InboxItem{
		Agent:       agent,
		Body:        body,
		DefaultText: def,
		State:       InboxUnread,
		Created:     time.Now(),
	}
	res, err := tui.inboxState().Post(item, agent+"-run")
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	got, ok := tui.inboxState().Item(res.ID)
	if !ok {
		t.Fatalf("item %q not in the store after post", res.ID)
	}
	return got
}

// checkLine is what the agent's --check prints for an item -- the assertion
// surface for everything about a stored answer.
func checkLine(t *testing.T, tui *TUI, id string) string {
	t.Helper()
	item, ok := tui.inboxState().Item(id)
	if !ok {
		t.Fatalf("item %q missing", id)
	}
	return formatInboxStatus(item, time.Now())
}

// awaitStatus waits for the delivery goroutine to record its verdict.
func awaitStatus(t *testing.T, tui *TUI, id string, want string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		if item, ok := tui.inboxState().Item(id); ok {
			got = item.DeliveryStatus
			if got == want {
				return got
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return got
}

// livePane returns a pane on a real PTY whose screen has no composer glyph,
// so the never-submit belt stands down and a submit actually goes out.
func inboxLivePane(t *testing.T, name string) *Pane {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	t.Cleanup(func() { ptmx.Close(); tty.Close() })
	oldState, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	t.Cleanup(func() { term.Restore(int(tty.Fd()), oldState) })
	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				_, _ = ptmx.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	// Drain the master so a write never blocks.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := ptmx.Read(buf); err != nil {
				return
			}
		}
	}()
	return &Pane{
		name:    name,
		emu:     emu,
		ptmx:    &filePty{ptmx},
		alive:   true,
		visible: true,
		eventCh: make(chan AgentEvent, 16),
	}
}

// CONSTRAINT 1, THE CELL THAT CATCHES THE INJECT SWAP. A suspended agent's
// reply must queue and wake it. TUI.injectText does neither: it reaches
// sendPaneTextLocked, which returns at once because a suspended pane's ptmx
// is nil, so the reply would vanish while the item read "answered".
func TestInboxReply_SuspendedAgentQueuesAndWakes(t *testing.T) {
	p := &Pane{name: "eng1", suspended: true, alive: false, eventCh: make(chan AgentEvent, 8)}
	var woke int
	var mu sync.Mutex
	p.SetOnSuspendedMessage(func(*Pane) { mu.Lock(); woke++; mu.Unlock() })
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "should I update both docs?", "")

	if err := tui.ReplyToInboxItem(item.ID, "yes, both"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if got := awaitStatus(t, tui, item.ID, inboxDeliveryQueuedSuspended); got != inboxDeliveryQueuedSuspended {
		t.Fatalf("delivery status = %q, want %q", got, inboxDeliveryQueuedSuspended)
	}
	if n := p.QueuedMessageCount(); n != 1 {
		t.Errorf("queued %d messages; the reply did not reach the suspension guard "+
			"(a lower-level inject would have dropped it into a nil ptmx)", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if woke == 0 {
		t.Error("the wake callback never fired; the reply took a path with no resume-on-message")
	}
}

// AC 4: the quoted reply reaches the agent and --check reports it.
func TestInboxReply_QuotedReplyIsDeliveredAndRecorded(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "should I update both docs?\nthere are two places", "")

	if err := tui.ReplyToInboxItem(item.ID, "yes, both"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if got := awaitStatus(t, tui, item.ID, InboxDelivered); got != InboxDelivered {
		t.Fatalf("delivery status = %q, want %q", got, InboxDelivered)
	}
	line := checkLine(t, tui, item.ID)
	if !strings.HasPrefix(line, "answered: ") {
		t.Fatalf("--check = %q, want an answered line", line)
	}
	if !strings.Contains(line, `re "should I update both docs?" — yes, both`) {
		t.Errorf("--check = %q, want the quoted first line and the reply", line)
	}
	if !strings.Contains(line, "delivery: "+InboxDelivered) {
		t.Errorf("--check = %q, want the delivery status", line)
	}
}

// AC 4, other half: dismiss delivers nothing at all.
func TestInboxReply_DismissDeliversNothing(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "heads up: the rig is flaky", "")

	if err := tui.inboxState().Transition(item.ID, InboxDismissed, actorOperator); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if got := checkLine(t, tui, item.ID); got != string(InboxDismissed) {
		t.Errorf("--check = %q, want %q", got, InboxDismissed)
	}
	if n := p.QueuedMessageCount(); n != 0 {
		t.Errorf("dismiss queued %d messages; it must deliver nothing", n)
	}
	stored, _ := tui.inboxState().Item(item.ID)
	if stored.ReplyText != "" || stored.DeliveryStatus != "" {
		t.Errorf("dismiss wrote reply=%q delivery=%q; it sends nothing and records nothing",
			stored.ReplyText, stored.DeliveryStatus)
	}
}

// AC 16 / super's seam: accept composes the text and then takes the ORDINARY
// reply write. Asserted on --check's OUTPUT, never on the keypress.
func TestInboxAccept_ComposedTextIsStoredAndReadBackByCheck(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "should I update both docs?", "update both")

	if err := tui.AcceptInboxDefault(item.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	awaitStatus(t, tui, item.ID, InboxDelivered)
	line := checkLine(t, tui, item.ID)
	if !strings.Contains(line, "go with your default: update both") {
		t.Fatalf("--check = %q, want the composed accept text stored on the item; "+
			"an accept that marks answered without the ordinary write reports "+
			"'answered:' with nothing after it", line)
	}
}

// Accept on an item with no default still composes through the same seam --
// the panel refuses it (child C), and nothing here invents a second path.
func TestInboxAccept_UsesTheSameWriteAsATypedReply(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	tui := replyTUI(t, p)
	typed := postItem(t, tui, "eng1", "question one", "")
	accepted := postItem(t, tui, "eng1", "question two", "do the thing")

	if err := tui.ReplyToInboxItem(typed.ID, "answer one"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if err := tui.AcceptInboxDefault(accepted.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	for _, id := range []string{typed.ID, accepted.ID} {
		stored, _ := tui.inboxState().Item(id)
		if stored.State != InboxAnswered {
			t.Errorf("item %s state = %q, want answered", id, stored.State)
		}
		if stored.ReplyText == "" {
			t.Errorf("item %s stored no reply text; both paths must take the ordinary write", id)
		}
	}
}

// CONSTRAINT 2: an agent that is not running. The operator's act is recorded
// and the reply is retrievable; nothing claims it was delivered.
func TestInboxReply_NotRunningAgentStoresTheAnswerUndelivered(t *testing.T) {
	p := &Pane{name: "eng1", alive: false, eventCh: make(chan AgentEvent, 8)}
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "question", "")

	if err := tui.ReplyToInboxItem(item.ID, "answer"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if got := awaitStatus(t, tui, item.ID, inboxDeliveryNotRunning); got != inboxDeliveryNotRunning {
		t.Fatalf("delivery status = %q, want %q", got, inboxDeliveryNotRunning)
	}
	stored, _ := tui.inboxState().Item(item.ID)
	if stored.State != InboxAnswered || stored.ReplyText == "" {
		t.Error("the operator's act must be recorded even when nothing was delivered")
	}
	if inboxPrunable(stored) {
		t.Error("an answered item whose delivery is unconfirmed must never prune -- "+
			"its stored reply may be the only copy of the answer", stored.DeliveryStatus)
	}
}

// CONSTRAINT 2: the panel never says delivered on its own authority. An
// unknown pane is not a delivery.
func TestInboxReply_MissingPaneIsNotDelivered(t *testing.T) {
	tui := replyTUI(t)
	item := postItem(t, tui, "ghost", "question", "")

	if err := tui.ReplyToInboxItem(item.ID, "answer"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if got := awaitStatus(t, tui, item.ID, inboxDeliveryNotRunning); got == InboxDelivered {
		t.Fatalf("delivery status = %q; a pane that is not there was reported delivered", got)
	}
}

// CONSTRAINT 2, the late corrections: the send path's own reports update the
// item long after the keypress.
func TestInboxReply_LatePathReportsUpdateTheDeliveryStatus(t *testing.T) {
	cases := []struct {
		name   string
		detail string
		want   string
	}{
		{"withheld submit surfaced", undeliveredSubmitPrefix + "claude 1.2) after 1m30s (the composer no longer holds our text), re-send it: hello", inboxDeliveryNotDeliveredPrefix + "after 1m30s (the composer no longer holds our text)"},
		{"wake failed", "Resume failed: boom. 1 message(s) still queued; the next message retries.", inboxDeliveryWakeFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Pane{name: "eng1", suspended: true, eventCh: make(chan AgentEvent, 8)}
			p.SetOnSuspendedMessage(func(*Pane) {})
			tui := replyTUI(t, p)
			item := postItem(t, tui, "eng1", "question", "")
			if err := tui.ReplyToInboxItem(item.ID, "answer"); err != nil {
				t.Fatalf("reply: %v", err)
			}
			awaitStatus(t, tui, item.ID, inboxDeliveryQueuedSuspended)

			tui.noteInboxDeliveryEvent(AgentEvent{Type: EventAgentStalled, Pane: "eng1", Detail: tc.detail, Time: time.Now()})

			stored, _ := tui.inboxState().Item(item.ID)
			if stored.DeliveryStatus != tc.want {
				t.Errorf("delivery status = %q, want %q", stored.DeliveryStatus, tc.want)
			}
			if !strings.Contains(checkLine(t, tui, item.ID), tc.want) {
				t.Errorf("--check does not carry the corrected status: %q", checkLine(t, tui, item.ID))
			}
		})
	}
}

// THE HOOK IS WIRED, not merely correct. Every other cell here calls
// noteInboxDeliveryEvent directly, which proves the function works and says
// nothing about production reaching it -- and the one line that does reach it
// lives in another file, which is exactly how it got left out of this bead's
// first commit. This drives the real handler.
func TestInboxReply_LateReportArrivesThroughTheAgentEventHandler(t *testing.T) {
	p := &Pane{name: "eng1", suspended: true, eventCh: make(chan AgentEvent, 8)}
	p.SetOnSuspendedMessage(func(*Pane) {})
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "question", "")
	if err := tui.ReplyToInboxItem(item.ID, "answer"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	awaitStatus(t, tui, item.ID, inboxDeliveryQueuedSuspended)

	tui.handleAgentEvent(AgentEvent{Type: EventAgentStalled, Pane: "eng1",
		Detail: "Resume failed: boom. 1 message(s) still queued; the next message retries.", Time: time.Now()})

	stored, _ := tui.inboxState().Item(item.ID)
	if stored.DeliveryStatus != inboxDeliveryWakeFailed {
		t.Errorf("delivery status = %q, want %q -- the agent event handler does not reach the inbox",
			stored.DeliveryStatus, inboxDeliveryWakeFailed)
	}
}

// The late report lands on the item it belongs to. Keyed by the dispatched id
// in order, not by "the most recent answered item", which would put the first
// reply's verdict on the second.
func TestInboxReply_LateReportLandsOnTheOldestOutstandingItem(t *testing.T) {
	p := &Pane{name: "eng1", suspended: true, eventCh: make(chan AgentEvent, 8)}
	p.SetOnSuspendedMessage(func(*Pane) {})
	tui := replyTUI(t, p)
	first := postItem(t, tui, "eng1", "question one", "")
	second := postItem(t, tui, "eng1", "question two", "")

	if err := tui.ReplyToInboxItem(first.ID, "answer one"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	awaitStatus(t, tui, first.ID, inboxDeliveryQueuedSuspended)
	if err := tui.ReplyToInboxItem(second.ID, "answer two"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	awaitStatus(t, tui, second.ID, inboxDeliveryQueuedSuspended)

	tui.noteInboxDeliveryEvent(AgentEvent{Type: EventAgentStalled, Pane: "eng1",
		Detail: "Resume failed: boom. 2 message(s) still queued; the next message retries.", Time: time.Now()})

	gotFirst, _ := tui.inboxState().Item(first.ID)
	gotSecond, _ := tui.inboxState().Item(second.ID)
	if gotFirst.DeliveryStatus != inboxDeliveryWakeFailed {
		t.Errorf("first item = %q, want the late report on the OLDEST outstanding delivery", gotFirst.DeliveryStatus)
	}
	if gotSecond.DeliveryStatus != inboxDeliveryQueuedSuspended {
		t.Errorf("second item = %q; one report resolved two items", gotSecond.DeliveryStatus)
	}
}

// An event that is not a delivery verdict must not consume an outstanding
// delivery -- otherwise an unrelated stall would silently mark a reply.
func TestInboxReply_UnrelatedEventLeavesTheDeliveryAlone(t *testing.T) {
	p := &Pane{name: "eng1", suspended: true, eventCh: make(chan AgentEvent, 8)}
	p.SetOnSuspendedMessage(func(*Pane) {})
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "question", "")
	if err := tui.ReplyToInboxItem(item.ID, "answer"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	awaitStatus(t, tui, item.ID, inboxDeliveryQueuedSuspended)

	tui.noteInboxDeliveryEvent(AgentEvent{Type: EventAgentStalled, Pane: "eng1",
		Detail: "Message queue full, oldest message dropped.", Time: time.Now()})
	tui.noteInboxDeliveryEvent(AgentEvent{Type: EventMessageSent, Pane: "eng1", Time: time.Now()})

	stored, _ := tui.inboxState().Item(item.ID)
	if stored.DeliveryStatus != inboxDeliveryQueuedSuspended {
		t.Errorf("delivery status = %q; an unrelated event consumed the outstanding delivery", stored.DeliveryStatus)
	}
}

// SUPER'S CONSTRAINT 4: the keypress must not wait on delivery. Recording the
// operator's act promptly is a different requirement from not blocking, and
// this asserts the second one.
func TestInboxReply_KeypressDoesNotWaitForDelivery(t *testing.T) {
	release := make(chan struct{})
	p := &Pane{name: "eng1", suspended: true, eventCh: make(chan AgentEvent, 8)}
	p.SetOnSuspendedMessage(func(*Pane) { <-release }) // a wake that blocks, as a real respawn does
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "question", "")
	// Released in cleanup: registered after the fixture, so it runs before the
	// fixture's join (ini-yxlh), which replaces 87b17a0's in-body release.
	t.Cleanup(func() { close(release) })

	done := make(chan error, 1)
	go func() { done <- tui.ReplyToInboxItem(item.ID, "answer") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reply: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the keypress waited on delivery; a blocking wake froze the key/render loop")
	}

	stored, _ := tui.inboxState().Item(item.ID)
	if stored.State != InboxAnswered || stored.ReplyText == "" {
		t.Error("the operator's act was not recorded synchronously")
	}
	if stored.DeliveryStatus == InboxDelivered {
		t.Error("an in-flight delivery was reported delivered")
	}

}

// PRUNE COUPLING with child A: only a confirmed delivery lets an answered
// item go. This is why "delivered" is set from the path's own record and
// never from "we called SendText and it did not error".
func TestInboxReply_OnlyConfirmedDeliveryLetsAnAnsweredItemPrune(t *testing.T) {
	for _, tc := range []struct {
		status string
		prune  bool
	}{
		{InboxDelivered, true},
		{inboxDeliveryQueuedSuspended, false},
		{inboxDeliveryAwaitingSubmit, false},
		{inboxDeliveryHeldDialog, false},
		{inboxDeliveryNotRunning, false},
		{inboxDeliveryNotDeliveredPrefix + "after 1m", false},
		{inboxDeliveryWakeFailed, false},
		{"", false},
	} {
		item := InboxItem{State: InboxAnswered, DeliveryStatus: tc.status}
		if got := inboxPrunable(item); got != tc.prune {
			t.Errorf("prunable(%q) = %v, want %v", tc.status, got, tc.prune)
		}
	}
}

// The composed text is the spec's shape, and a long first line is truncated
// rather than pasted whole into the agent's pane.
func TestInboxReply_ComposedTextQuotesAndTruncatesTheFirstLine(t *testing.T) {
	long := strings.Repeat("x", 200)
	item := InboxItem{Body: long + "\nsecond line", DefaultText: "the default"}
	got := inboxComposeReply(item, "my answer")
	if !strings.HasPrefix(got, `re "`) || !strings.HasSuffix(got, "— my answer") {
		t.Errorf("composed reply = %q", got)
	}
	if len(got) > 120 {
		t.Errorf("composed reply is %d bytes; the first line must be truncated", len(got))
	}
	if strings.Contains(got, "second line") {
		t.Error("the quote must be the FIRST line only")
	}
	acc := inboxComposeAccept(item)
	if !strings.HasSuffix(acc, "— go with your default: the default") {
		t.Errorf("composed accept = %q", acc)
	}
}

// CONSTRAINT 2: a dialog is open. sendPaneTextLocked defers the message into
// the pane's queue and readLoop re-delivers it when the dialog closes, so the
// honest report is "held", never "delivered".
func TestInboxReply_DialogOpenIsHeldNotDelivered(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	p.mu.Lock()
	p.dialogOpen = true // the send guard's latch, the same term paneHasModal reads
	p.dialogOpenAt = time.Now()
	p.mu.Unlock()
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "question", "")

	if err := tui.ReplyToInboxItem(item.ID, "answer"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if got := awaitStatus(t, tui, item.ID, inboxDeliveryHeldDialog); got != inboxDeliveryHeldDialog {
		t.Fatalf("delivery status = %q, want %q", got, inboxDeliveryHeldDialog)
	}
	if n := p.QueuedMessageCount(); n != 1 {
		t.Errorf("queued %d; the modal guard must defer the reply rather than paste it into a picker", n)
	}
	stored, _ := tui.inboxState().Item(item.ID)
	if inboxPrunable(stored) {
		t.Error("a held reply is not delivered and must never prune")
	}
}

// CONSTRAINT 2: the never-submit belt withheld the terminator. The body is in
// the composer; the message is not delivered, and saying "delivered" here is
// exactly the panel speaking on its own authority.
func TestInboxReply_WithheldSubmitIsAwaitingSubmitNotDelivered(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	// A composer glyph makes the belt apply; nothing echoes our body back into
	// the emulator, so composerAcceptedBody stays false and the submit is held.
	if _, err := p.emu.Write([]byte("\x1b[2J\x1b[24;1H❯ ")); err != nil {
		t.Fatalf("paint composer: %v", err)
	}
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "question", "")

	if err := tui.ReplyToInboxItem(item.ID, "answer"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	got := awaitStatus(t, tui, item.ID, inboxDeliveryAwaitingSubmit)
	if got != inboxDeliveryAwaitingSubmit {
		t.Fatalf("delivery status = %q, want %q (the belt withheld the submit)", got, inboxDeliveryAwaitingSubmit)
	}
	p.mu.Lock()
	held := p.pendingSubmit != nil
	p.mu.Unlock()
	if !held {
		t.Fatal("fixture did not reach the belt: no pendingSubmit on the pane")
	}
	stored, _ := tui.inboxState().Item(item.ID)
	if inboxPrunable(stored) {
		t.Error("text sitting unsubmitted in a composer is not delivery and must never prune")
	}
}

// THE PANEL'S KEYS REACH THIS BEAD'S SEAM. Child C deliberately leaves its
// two callbacks nil and reports rather than pretends, which is what let the
// two beads land independently -- and is exactly the state in which a panel
// looks finished while answering nothing.
func TestInboxPanel_EnterAndAcceptReachTheDeliverySeam(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	tui := replyTUI(t, p)
	typed := postItem(t, tui, "eng1", "question one", "")
	withDefault := postItem(t, tui, "eng1", "question two", "do the thing")

	tui.openInboxPanel()
	if tui.inbox.onReply == nil || tui.inbox.onAccept == nil {
		t.Fatal("opening the panel left the delivery seam unwired; Enter and `a` would report instead of answering")
	}

	tui.inbox.onReply(typed.ID, "answer one")
	awaitStatus(t, tui, typed.ID, InboxDelivered)
	if got := checkLine(t, tui, typed.ID); !strings.Contains(got, "— answer one") {
		t.Errorf("Enter did not reach the seam: --check = %q", got)
	}

	tui.inbox.onAccept(withDefault.ID)
	awaitStatus(t, tui, withDefault.ID, InboxDelivered)
	if got := checkLine(t, tui, withDefault.ID); !strings.Contains(got, "go with your default: do the thing") {
		t.Errorf("`a` did not reach the seam: --check = %q", got)
	}
}

// THE FIXTURE JOINS DELIVERIES BEFORE THE TEMP DIR IS REMOVED (ini-5rvq).
//
// The flake this guards was load-dependent: 30-40% in the full suite, never
// alone, so a repeat-the-suite check can only ever say "not seen this time".
// This cell turns the property into something observable on every run.
//
// A delivery released in cleanup that finishes AFTER t.TempDir's RemoveAll
// does not fail quietly: Inbox.save does MkdirAll and an atomic write, so it
// RECREATES .initech/ inside the removed root; one that lands mid-walk makes
// RemoveAll fail. So: run the parked-wake shape in a subtest, then -- after
// every one of its cleanups has run -- join the delivery and require that the
// subtest passed and the root is gone. With the join in replyTUI both hold by
// construction. Without it, a late write shows up here as a recreated root or
// a failed subtest.
func TestReplyTUI_JoinsDeliveriesBeforeTheTempDirIsRemoved(t *testing.T) {
	var tui *TUI
	var root string
	ok := t.Run("parked_wake_released_in_cleanup", func(t *testing.T) {
		release := make(chan struct{})
		p := &Pane{name: "eng1", suspended: true, eventCh: make(chan AgentEvent, 8)}
		p.SetOnSuspendedMessage(func(*Pane) { <-release })
		tui = replyTUI(t, p)
		root = tui.projectRoot
		item := postItem(t, tui, "eng1", "question", "")
		// Registered after replyTUI, so under LIFO it runs BEFORE the fixture's
		// join -- the exact order that raced.
		t.Cleanup(func() { close(release) })
		if err := tui.ReplyToInboxItem(item.ID, "answer"); err != nil {
			t.Fatalf("reply: %v", err)
		}
	})
	if !ok {
		t.Fatal("the subtest failed during cleanup: a delivery was still writing when its temp dir was removed")
	}

	joined := make(chan struct{})
	go func() { tui.inboxDeliveries.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(10 * time.Second):
		t.Fatal("the delivery goroutine never finished")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("the temp root %s exists after its RemoveAll (stat err=%v): a delivery wrote into it "+
			"after the directory was removed, so the fixture did not join before removal", root, err)
	}
}
