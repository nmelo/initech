package tui

// A dialog versus a QUOTATION of one, and a latch that heals itself (ini-9gvn).
//
// THE FIXTURES ARE REAL CAPTURES, not invented text. specimenQuotation is
// copied from eng2's live pane at the moment the fleet was stalled -- the
// evidence captured read-only before the operator's relaunch cleared it -- and
// realPermissionDialog is the shape a live Claude 2.1.233 renders, measured
// during ini-d7a7 round 2. Inventing either would have designed the guard
// against what I imagined a screen looks like, which is the mistake this arc
// has now paid for five times.

import (
	"strings"
	"testing"
	"time"
)

// specimenQuotation is the pane content that held the fleet's mail for ~30
// minutes. It is prose ABOUT a dialog: an agent reporting that compacted text
// reads "Doyouwanttoproceed?". Because isModalPrompt compacts whitespace out of
// both sides, this matches the permission prompt exactly.
const specimenQuotation = ` eDoyouwanttoproceed?. Only
  two of my own outputs
  contradicting each other
  exposed it.

  I raised one thing as a
  question, not a proposal:
  every gated rig we own that
  spawns a real Claude has
  this same structure.

  eng2 idle pending eng1's
  l5sy timing.`

// realPermissionDialog is what a live permission prompt renders: the question
// with something to answer directly under it.
const realPermissionDialog = ` ❯ Run the shell command: date +%s

  /permissions to let auto mode decide

  Do you want to proceed?
  ❯ 1. Yes
    2. Yes, and don't ask again
    3. No, and tell Claude what to do differently`

// TestScreenShowsLiveDialog_QuotationIsNotADialog is the bug, as a cell.
func TestScreenShowsLiveDialog_QuotationIsNotADialog(t *testing.T) {
	if !isModalPrompt(specimenQuotation) {
		t.Fatal("precondition: the specimen no longer matches the prompt matcher at all, " +
			"so this cell would pass without discriminating anything -- the fixture has " +
			"drifted from the incident it was captured from")
	}
	if screenShowsLiveDialog(specimenQuotation) {
		t.Error("prose QUOTING a dialog is read as a dialog.\n\nThis is the live incident: " +
			"every send to that pane deferred with \"target has a modal open\" while nothing " +
			"was open, and the fleet's coordination sat in queues for half an hour.")
	}
}

// TestScreenShowsLiveDialog_RealDialogStillDefers is the direction that must
// never widen. A missed dialog forges an operator answer.
func TestScreenShowsLiveDialog_RealDialogStillDefers(t *testing.T) {
	if !screenShowsLiveDialog(realPermissionDialog) {
		t.Fatal("a REAL permission dialog is no longer recognised.\n\nThis is the forbidden " +
			"direction: a false \"no modal\" pastes a message into an option picker and the " +
			"submit key answers a destructive default the operator never saw (ini-2jpo).")
	}
}

// TestScreenOffersAnswer_BothHalvesAreRequired pins WHY the discriminator
// works, so a future edit cannot keep the name and lose the meaning.
func TestScreenOffersAnswer_BothHalvesAreRequired(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"question with numbered options", "Do you want to proceed?\n❯ 1. Yes\n  2. No", true},
		{"question with confirm footer", "Do you want to proceed?\nEnter to confirm · Esc to cancel", true},
		{"question alone, nothing to answer with", "Do you want to proceed?", false},
		{"options alone, no question", "❯ 1. Yes\n  2. No", false},
		{"prose mentioning the words", "the guard matched Do you want to proceed in my report", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := screenShowsLiveDialog(tc.text); got != tc.want {
				t.Errorf("screenShowsLiveDialog = %v, want %v for:\n%s", got, tc.want, tc.text)
			}
		})
	}
}

// TestAuditDialogLatch_UncorroboratedRaiseHealsItself is AC4: a false raise
// must be self-healing, not operator-healing.
func TestAuditDialogLatch_UncorroboratedRaiseHealsItself(t *testing.T) {
	p := testPane("eng2")
	p.latchDialogOpen()
	if !p.dialogLatched() {
		t.Fatal("precondition: the latch did not raise")
	}

	// Not yet: a dialog that was declared a moment ago may simply not have
	// rendered yet, and downgrading that would race a real dialog.
	if p.auditDialogLatch(time.Now()) {
		t.Error("a latch raised moments ago was downgraded; a real dialog renders a frame " +
			"or two after its announcement and this would race it")
	}

	// Past the window, with sight never confirming it.
	if !p.auditDialogLatch(time.Now().Add(latchCorroborationWindow + time.Second)) {
		t.Fatal("a latch that sight NEVER corroborated survived the audit.\n\nThat is the " +
			"live specimen: raised falsely, never clearable (a false raise can never earn " +
			"its clear because no dialog ever renders to be seen closing), so it survived a " +
			"restart and held the fleet's mail until a human typed into the pane.")
	}
	if p.dialogLatched() {
		t.Error("auditDialogLatch reported a downgrade but the latch is still raised")
	}
}

// TestAuditDialogLatch_SightedDialogIsNeverDowngraded is the half that keeps
// the fix from becoming "clear the latch on a timer".
//
// Without this cell, an implementation that simply expired every latch after 90
// seconds would pass the test above -- and would forge an operator answer into
// a dialog that is genuinely open and merely unanswered, which is the exact
// harm the guard exists to prevent.
func TestAuditDialogLatch_SightedDialogIsNeverDowngraded(t *testing.T) {
	p := testPane("eng1")
	p.latchDialogOpen()
	p.noteDialogSighting() // the screen confirmed it

	if p.auditDialogLatch(time.Now().Add(10 * latchCorroborationWindow)) {
		t.Error("a latch CORROBORATED BY SIGHT was downgraded on age.\n\nA real dialog can " +
			"sit open for hours waiting for the operator; expiring it would paste into an " +
			"open picker and answer a destructive default.")
	}
	if !p.dialogLatched() {
		t.Error("the corroborated latch was cleared")
	}
}

// TestNoteDialogSighting_DoesNotRaiseOnItsOwn keeps the corroboration flag from
// becoming a second way to raise a latch.
func TestNoteDialogSighting_DoesNotRaiseOnItsOwn(t *testing.T) {
	p := testPane("qa1")
	p.noteDialogSighting()
	if p.dialogLatched() {
		t.Error("sighting alone raised the latch; corroboration confirms a declaration, it " +
			"does not make one")
	}
}

// TestModalMaintenance_IdlePaneReleasesHeldMail is AC2: a deferred queue must
// be retried WITHOUT the recipient producing output.
//
// The live incident's second defect: deferral drained only from readLoop, so
// an idle pane held mail indefinitely. The recipient had finished its work and
// gone quiet, which is precisely when it produces no output -- so the messages
// telling it what to do next could never arrive.
func TestModalMaintenance_IdlePaneReleasesHeldMail(t *testing.T) {
	tui := newTestTUI()
	p := testPane("eng2")
	tui.panes = []PaneView{p}

	p.EnqueueMessage("[from super] merge order: you land second", true)
	if p.QueuedMessageCount() != 1 {
		t.Fatalf("precondition: queue holds %d, want 1", p.QueuedMessageCount())
	}

	// No output from the pane at any point: it is idle, which is the case that
	// never recovered.
	tui.modalMaintenance(time.Now())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && p.QueuedMessageCount() > 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if got := p.QueuedMessageCount(); got != 0 {
		t.Errorf("an idle pane still holds %d message(s) after maintenance ran; the queue "+
			"drains only on recipient output, so a finished agent never hears anything again",
			got)
	}
}

// TestModalMaintenance_HeldMailStaysHeldWhileADialogIsOpen is the other
// direction: the re-check must not become a way to paste into an open dialog.
func TestModalMaintenance_HeldMailStaysHeldWhileADialogIsOpen(t *testing.T) {
	tui := newTestTUI()
	p := testPane("eng1")
	tui.panes = []PaneView{p}
	p.latchDialogOpen()
	p.noteDialogSighting() // a REAL dialog: declared and seen

	p.EnqueueMessage("[from super] do not paste this into a picker", true)
	tui.modalMaintenance(time.Now())
	time.Sleep(200 * time.Millisecond)

	if p.QueuedMessageCount() != 1 {
		t.Error("maintenance drained a queue into a pane with an open, sighted dialog -- " +
			"the submit key would read as confirming whatever option was highlighted")
	}
}

// TestOverlayStatus_NamesHeldMail is AC3: held mail must be a visible fact.
// Asserted on the status text the overlay renders, because "the fleet looks
// idle" was the operator's entire experience of a 30-minute stall.
func TestOverlayStatus_NamesHeldMail(t *testing.T) {
	tui, screen := newTestTUIWithScreen("eng1")
	lp, ok := tui.panes[0].(*Pane)
	if !ok {
		t.Fatal("fixture pane is not a local pane")
	}
	lp.EnqueueMessage("held one", true)
	lp.EnqueueMessage("held two", true)

	// Rendered through renderOverlay directly: the overlay's visibility toggle
	// is not what this cell is about, and going through it would make the
	// assertion depend on a keybinding rather than on the status text.
	tui.renderOverlay()
	screen.Show()

	w, h := screen.Size()
	var found bool
	for y := 0; y < h && !found; y++ {
		row := make([]rune, 0, w)
		for x := 0; x < w; x++ {
			ch, _, _, _ := screen.GetContent(x, y)
			row = append(row, ch)
		}
		if strings.Contains(string(row), "2 held") {
			found = true
		}
	}
	if !found {
		t.Error("the overlay does not say the pane is holding messages.\n\nQueued mail was " +
			"invisible everywhere during the incident: the fleet simply looked idle, and " +
			"nothing distinguished 'idle' from 'idle, holding two messages'.")
	}
}
