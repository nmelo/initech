package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-qmbi: the inbox panel's two modes (spec 8b0db47). Measured before the
// fix, in COMMAND mode with no reply mode at all: typing "add" both accepted
// the default and dismissed the item; "add a flag" left replyBuf = "  flg"
// with every 'a' and 'd' stripped out of the sentence, and the following
// Enter sent nothing because the dismissed item had left the list.

func inboxType(t *TUI, s string) {
	for _, r := range s {
		t.handleInboxKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
}

func inboxKey(t *TUI, k tcell.Key) { t.handleInboxKey(tcell.NewEventKey(k, 0, 0)) }

// THE BEAD'S NEGATIVE CONTROL: the sentence that used to accept, dismiss and
// mangle itself must land intact and change nothing.
func TestInboxPanel_TypingASentenceWithAAndDLandsIntactAndChangesNoItem(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "should I ship?", "ship at 5pm")
	tui.openInboxPanel()
	before, _ := tui.inboxState().Item(item.ID)

	inboxType(tui, "r")
	inboxType(tui, "add a flag")

	if got := string(tui.inbox.replyBuf); got != "add a flag" {
		t.Errorf("replyBuf = %q, want %q -- the 'a' and 'd' were consumed as commands", got, "add a flag")
	}
	after, _ := tui.inboxState().Item(item.ID)
	if after.State != before.State {
		t.Errorf("typing changed the item state from %q to %q", before.State, after.State)
	}
	if after.ReplyText != "" {
		t.Errorf("typing stored a reply %q -- the default was accepted mid-sentence", after.ReplyText)
	}
}

// End to end, the bead's own check: type it, send it, and the agent's --check
// shows exactly that sentence.
func TestInboxPanel_TypedSentenceIsWhatTheAgentSeesInCheck(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "should I ship?", "ship at 5pm")
	tui.openInboxPanel()

	inboxType(tui, "r")
	inboxType(tui, "add a flag")
	inboxKey(tui, tcell.KeyEnter)

	awaitStatus(t, tui, item.ID, InboxDelivered)
	line := checkLine(t, tui, item.ID)
	if !strings.Contains(line, "answered: ") || !strings.Contains(line, "add a flag") {
		t.Errorf("--check = %q, want the operator's own sentence", line)
	}
	if strings.Contains(line, "go with your default") {
		t.Errorf("--check = %q: the agent was sent its own default instead of the reply", line)
	}
}

// Every letter reaches the line, including the two that were commands.
func TestInboxPanel_ReplyModeTakesEveryPrintableRune(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	tui := replyTUI(t, p)
	postItem(t, tui, "eng1", "question", "a default")
	tui.openInboxPanel()
	const alphabet = "abcdefghijklmnopqrstuvwxyz"

	inboxType(tui, "r")
	inboxType(tui, alphabet)

	if got := string(tui.inbox.replyBuf); got != alphabet {
		t.Errorf("replyBuf = %q, want the whole alphabet", got)
	}
}

// Both entry keys, because Enter is the one everyone tries first.
func TestInboxPanel_ReplyModeIsEnteredByRAndByEnter(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*TUI)
	}{
		{"r", func(tu *TUI) { inboxType(tu, "r") }},
		{"Enter", func(tu *TUI) { inboxKey(tu, tcell.KeyEnter) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := inboxLivePane(t, "eng1")
			tui := replyTUI(t, p)
			postItem(t, tui, "eng1", "question", "")
			tui.openInboxPanel()
			if tui.inbox.replying {
				t.Fatal("the panel must open in command mode")
			}
			tc.open(tui)
			if !tui.inbox.replying {
				t.Fatal("did not enter reply mode")
			}
			inboxType(tui, "d")
			if got := string(tui.inbox.replyBuf); got != "d" {
				t.Errorf("replyBuf = %q; the rune was taken as a command", got)
			}
		})
	}
}

// Two-stage Esc: discard the draft and return to command mode, then close.
func TestInboxPanel_EscDiscardsTheDraftThenClosesThePanel(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "question", "")
	tui.openInboxPanel()
	inboxType(tui, "r")
	inboxType(tui, "half typed")

	inboxKey(tui, tcell.KeyEscape)
	if tui.inbox.replying {
		t.Error("Esc did not leave reply mode")
	}
	if !tui.inbox.active {
		t.Error("Esc in reply mode closed the panel; it must only discard the draft")
	}
	if got := string(tui.inbox.replyBuf); got != "" {
		t.Errorf("draft %q survived Esc", got)
	}
	stored, _ := tui.inboxState().Item(item.ID)
	if stored.State == InboxAnswered {
		t.Error("Esc answered the item")
	}

	inboxKey(tui, tcell.KeyEscape)
	if tui.inbox.active {
		t.Error("Esc in command mode did not close the panel")
	}
}

// Enter with nothing typed never answers, and says why without leaving reply
// mode.
func TestInboxPanel_EnterOnAnEmptyReplyDoesNotAnswerTheItem(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "should I ship?", "")
	var replies []string
	tui.openInboxPanel()
	tui.inbox.onReply = func(id, r string) { replies = append(replies, r) }

	inboxType(tui, "r")
	inboxKey(tui, tcell.KeyEnter)

	if len(replies) != 0 {
		t.Errorf("Enter on an empty reply line sent %q; nothing to send is not an act", replies)
	}
	stored, _ := tui.inboxState().Item(item.ID)
	if stored.State == InboxAnswered {
		t.Errorf("the item was answered with reply text %q by an empty Enter", stored.ReplyText)
	}
	if !tui.inbox.replying {
		t.Error("an empty Enter left reply mode; the operator is still composing")
	}
	if tui.inbox.note != inboxEmptyReply {
		t.Errorf("note = %q, want %q", tui.inbox.note, inboxEmptyReply)
	}
}

// The selected item can leave the list under the operator. The reply must not
// go to whatever took its place, and the draft must survive.
func TestInboxPanel_EnterDoesNotSendToWhateverTookThePlaceOfAVanishedItem(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	tui := replyTUI(t, p)
	gone := postItem(t, tui, "eng1", "first question", "")
	other := postItem(t, tui, "eng1", "second question", "")
	type rec struct{ id, reply string }
	var replies []rec
	tui.openInboxPanel()
	tui.inbox.onReply = func(id, r string) { replies = append(replies, rec{id, r}) }
	if tui.inbox.selected != gone.ID {
		t.Fatalf("fixture: selection is %q, want the first item %q", tui.inbox.selected, gone.ID)
	}

	inboxType(tui, "r")
	inboxType(tui, "my answer")
	if err := tui.inboxState().Transition(gone.ID, InboxDismissed, actorOperator); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	inboxKey(tui, tcell.KeyEnter)

	for _, r := range replies {
		if r.id == other.ID {
			t.Errorf("the reply went to %q, an item the operator never selected", other.ID)
		}
	}
	if len(replies) == 0 {
		if got := string(tui.inbox.replyBuf); got != "my answer" {
			t.Errorf("nothing was sent and the draft was discarded (replyBuf=%q)", got)
		}
		if tui.inbox.note != inboxItemGone {
			t.Errorf("note = %q, want %q", tui.inbox.note, inboxItemGone)
		}
	}
}

// The one-key accept the operator approved still works, from command mode.
func TestInboxPanel_AcceptStillWorksFromCommandMode(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	tui := replyTUI(t, p)
	item := postItem(t, tui, "eng1", "should I ship?", "ship at 5pm")
	tui.openInboxPanel()

	inboxType(tui, "a")

	awaitStatus(t, tui, item.ID, InboxDelivered)
	if line := checkLine(t, tui, item.ID); !strings.Contains(line, "go with your default: ship at 5pm") {
		t.Errorf("--check = %q, want the accepted default", line)
	}
}

// A paste goes to the reply line, never to the pane behind the panel.
func TestInboxPanel_PasteGoesToTheReplyLineNotThePaneBehind(t *testing.T) {
	p := inboxLivePane(t, "eng1")
	tui := replyTUI(t, p)
	postItem(t, tui, "eng1", "should I ship?", "")
	tui.openInboxPanel()
	if !tui.modalActive() {
		t.Error("the open inbox panel does not count as a modal")
	}

	drained := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := p.ptmx.Read(buf)
		drained <- buf[:n]
	}()
	tui.pasting = true
	tui.pasteBuf = []byte("pasted reply text")
	tui.handlePaste(false)

	if got := string(tui.inbox.replyBuf); got != "pasted reply text" {
		t.Errorf("reply line = %q, want the pasted text", got)
	}
	select {
	case got := <-drained:
		t.Errorf("the pane behind the panel received %q from a paste made into the inbox", got)
	case <-time.After(300 * time.Millisecond):
	}
}

// The footer tells the operator which keys are live, per mode, verbatim.
func TestInboxPanel_FooterShowsTheKeysOfTheCurrentMode(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.projectRoot = t.TempDir()
	tui.windowID = WindowOne
	postItem(t, tui, "eng1", "question", "a default")
	tui.openInboxPanel()
	tui.render()
	if got := inboxScreen(t, s); !strings.Contains(got, strings.TrimSpace(inboxFooterCommand)) {
		t.Errorf("command-mode footer missing:\n%s", got)
	}

	inboxType(tui, "r")
	tui.render()
	got := inboxScreen(t, s)
	if !strings.Contains(got, strings.TrimSpace(inboxFooterReply)) {
		t.Errorf("reply-mode footer missing:\n%s", got)
	}
	if strings.Contains(got, "d dismiss") {
		t.Errorf("reply-mode footer still offers the command keys:\n%s", got)
	}
}

// ── ini-qgij scope add: the selection advances after an act ────────

// threeItemInbox stubs the two delivery hooks so an act changes nothing but
// the selection, and returns the panel with the FIRST item selected.
func threeItemInbox(t *testing.T) (*TUI, []InboxItem) {
	t.Helper()
	items := []InboxItem{
		inboxItemAt("p1", "eng1", "first question", 3*time.Minute),
		inboxItemAt("p2", "eng2", "second question", 2*time.Minute),
		inboxItemAt("p3", "eng3", "third question", time.Minute),
	}
	for i := range items {
		items[i].DefaultText = "yes"
	}
	tui, _ := inboxTUI(t, items...)
	tui.inbox.onAccept = func(string) {}
	tui.inbox.onReply = func(string, string) {}
	tui.openInboxPanel()
	if tui.inbox.selected != "p1" {
		t.Fatalf("fixture: selection is %q, want p1", tui.inbox.selected)
	}
	return tui, items
}

// Before the fix a, d and Enter left the selection on the acted item (gone
// after a dismiss), so the detail fell back to row 0 while r said "that item
// is gone" — Down was needed before every r.
func TestInboxPanel_AcceptDismissAndReplyAdvanceToTheNextItem(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  func(*TUI)
	}{
		{"accept", func(tui *TUI) { inboxType(tui, "a") }},
		{"dismiss", func(tui *TUI) { inboxType(tui, "d") }},
		{"reply", func(tui *TUI) { inboxType(tui, "r"); inboxType(tui, "done"); inboxKey(tui, tcell.KeyEnter) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tui, _ := threeItemInbox(t)
			tc.act(tui)
			if tui.inbox.note != "" {
				t.Fatalf("the act itself failed: %q", tui.inbox.note)
			}
			if tui.inbox.selected != "p2" {
				t.Fatalf("after %s the selection is %q, want the next item p2", tc.name, tui.inbox.selected)
			}
			// The advanced selection is real: r acts on it without a gone-note.
			inboxType(tui, "r")
			if tui.inbox.note == inboxItemGone || !tui.inbox.replying {
				t.Errorf("r after %s did not open a reply on the next item (note=%q replying=%v)", tc.name, tui.inbox.note, tui.inbox.replying)
			}
		})
	}
}

// A dismissed last item has no next: the previous one is selected. An
// answered last item is still listed until delivery confirms, so it stays.
func TestInboxPanel_ActOnTheLastItemFallsBackSensibly(t *testing.T) {
	tui, _ := threeItemInbox(t)
	tui.inbox.selected = "p3"
	inboxType(tui, "d")
	if tui.inbox.selected != "p2" {
		t.Errorf("dismissing the last item selected %q, want the previous item p2", tui.inbox.selected)
	}

	tui, _ = threeItemInbox(t)
	tui.inbox.selected = "p3"
	inboxType(tui, "a")
	if tui.inbox.selected != "p3" {
		t.Errorf("accepting the last item moved the selection to %q; the item is still listed and should stay selected", tui.inbox.selected)
	}
}

// Dismissing the only item leaves nothing selected — and no stale-ID note on
// the next keypress, since there is nothing to act on.
func TestInboxPanel_DismissingTheOnlyItemLeavesNothingSelected(t *testing.T) {
	tui, s := inboxTUI(t, inboxItemAt("p1", "eng1", "only question", time.Minute))
	tui.openInboxPanel()
	inboxType(tui, "d")
	if tui.inbox.selected != "" {
		t.Errorf("selection after dismissing the only item = %q, want none", tui.inbox.selected)
	}
	tui.renderInboxPanel()
	if got := inboxScreen(t, s); !strings.Contains(got, "nothing posted") {
		t.Errorf("empty panel does not show the empty state:\n%s", got)
	}
}
