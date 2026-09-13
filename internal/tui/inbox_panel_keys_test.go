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
