package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// fakeInbox is the panel's store seam under test: the panel is built against
// inboxReader, so its tests need no file, no project root and no window-1
// authority (ini-3wkl.4).
type fakeInbox struct {
	items       []InboxItem
	reason      string
	transitions []string
	transErr    error
}

func (f *fakeInbox) Items() []InboxItem { return append([]InboxItem(nil), f.items...) }

func (f *fakeInbox) Item(id string) (InboxItem, bool) {
	for _, it := range f.items {
		if it.ID == id {
			return it, true
		}
	}
	return InboxItem{}, false
}

func (f *fakeInbox) OpenCount(agent string) int {
	n := 0
	for _, it := range f.items {
		if it.Agent == agent && (it.State == InboxUnread || it.State == InboxSeen) {
			n++
		}
	}
	return n
}

func (f *fakeInbox) Transition(id string, to InboxState, by inboxActor) error {
	if f.transErr != nil {
		return f.transErr
	}
	f.transitions = append(f.transitions, id+"->"+string(to))
	for i := range f.items {
		if f.items[i].ID == id {
			f.items[i].State = to
		}
	}
	return nil
}

func (f *fakeInbox) PersistenceReason() string { return f.reason }

func inboxItemAt(id, agent, body string, age time.Duration) InboxItem {
	return InboxItem{ID: id, Agent: agent, Body: body, State: InboxUnread, Created: time.Now().Add(-age)}
}

// ── the list ────────────────────────────────────────────────────────

// Oldest on top: largest attention debt first, as the attention popup does.
func TestInboxList_OldestOnTop(t *testing.T) {
	f := &fakeInbox{items: []InboxItem{
		inboxItemAt("p2", "eng2", "newer", 2*time.Minute),
		inboxItemAt("p1", "eng1", "older", 30*time.Minute),
	}}
	got := inboxListFor(f)
	if len(got) != 2 || got[0].ID != "p1" {
		t.Fatalf("order = %v, want the 30m item first", []string{got[0].ID, got[1].ID})
	}
}

// AC 8 / canon: the list is NOT scoped to the window's own agents. The item's
// agent here is one this window does not display at all.
func TestInboxList_IsNotScopedToThisWindowsAgents(t *testing.T) {
	f := &fakeInbox{items: []InboxItem{inboxItemAt("p9", "qa7", "from an agent this window never shows", time.Minute)}}
	if got := inboxListFor(f); len(got) != 1 {
		t.Fatalf("got %d items, want 1 — attention is never scoped (canon), and the inbox is an attention surface", len(got))
	}
}

// An answered item stays listed until the send path confirms delivery: an
// answer the agent never received is what the operator most needs to see, and
// it is the set the store refuses to prune.
func TestInboxList_KeepsAnAnsweredItemUntilDeliveryIsConfirmed(t *testing.T) {
	undelivered := inboxItemAt("p1", "eng1", "q", time.Minute)
	undelivered.State = InboxAnswered
	delivered := inboxItemAt("p2", "eng2", "q", time.Minute)
	delivered.State = InboxAnswered
	delivered.DeliveryStatus = InboxDelivered
	dismissed := inboxItemAt("p3", "eng3", "q", time.Minute)
	dismissed.State = InboxDismissed

	got := inboxListFor(&fakeInbox{items: []InboxItem{undelivered, delivered, dismissed}})
	if len(got) != 1 || got[0].ID != "p1" {
		t.Fatalf("listed %v, want only the answered-but-undelivered item", got)
	}
}

// ── the corner count ────────────────────────────────────────────────

// AC 5: the count is UNREAD, so opening an item drops it even though the item
// stays listed; and it is zero when nothing is unread, which is what the
// badge uses to render nothing at all.
func TestInboxUnreadCount_CountsUnreadOnlyAndDropsOnSeen(t *testing.T) {
	f := &fakeInbox{items: []InboxItem{
		inboxItemAt("p1", "eng1", "a", time.Minute),
		inboxItemAt("p2", "eng2", "b", time.Minute),
	}}
	if got := inboxUnreadCount(f); got != 2 {
		t.Fatalf("count = %d, want 2", got)
	}
	if err := f.Transition("p1", InboxSeen, actorOperator); err != nil {
		t.Fatal(err)
	}
	if got := inboxUnreadCount(f); got != 1 {
		t.Errorf("count after one item was seen = %d, want 1", got)
	}
	if got := len(inboxListFor(f)); got != 2 {
		t.Errorf("the seen item left the list (%d listed, want 2) — seen drops the COUNT, not the row", got)
	}
}

func TestInboxUnreadCount_IsZeroWhenNothingIsUnread(t *testing.T) {
	seen := inboxItemAt("p1", "eng1", "a", time.Minute)
	seen.State = InboxSeen
	if got := inboxUnreadCount(&fakeInbox{items: []InboxItem{seen}}); got != 0 {
		t.Errorf("count = %d, want 0 so the badge renders nothing", got)
	}
}

// ── delivery, in the send path's words ──────────────────────────────

// The panel never says "sent" on its own authority: blank is unconfirmed.
func TestInboxDeliveryLine_BlankIsUnconfirmedNeverSuccess(t *testing.T) {
	it := inboxItemAt("p1", "eng1", "q", time.Minute)
	it.State = InboxAnswered
	got := inboxDeliveryLine(it)
	if !strings.Contains(got, "unconfirmed") {
		t.Errorf("blank delivery status renders %q, want it reported as unconfirmed", got)
	}
	for _, forbidden := range []string{"delivered", "sent"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("blank delivery status renders %q, which reads as success the panel has no authority to claim", got)
		}
	}
}

// A reported status is rendered VERBATIM, in the path's own vocabulary.
func TestInboxDeliveryLine_RendersThePathsOwnWords(t *testing.T) {
	for _, status := range []string{
		"held — agent has a dialog open; delivers when it clears",
		"typed, awaiting submit",
		"queued — agent suspended; wakes and delivers",
		"not running — stored",
		InboxDelivered,
	} {
		it := inboxItemAt("p1", "eng1", "q", time.Minute)
		it.State = InboxAnswered
		it.DeliveryStatus = status
		if got := inboxDeliveryLine(it); got != "delivery: "+status {
			t.Errorf("status %q rendered as %q — the panel must not reword the path's verdict", status, got)
		}
	}
}

// An item that is not answered has nothing to say about delivery.
func TestInboxDeliveryLine_SaysNothingBeforeThereIsAReply(t *testing.T) {
	if got := inboxDeliveryLine(inboxItemAt("p1", "eng1", "q", time.Minute)); got != "" {
		t.Errorf("unanswered item reports delivery %q, want nothing", got)
	}
}

// ── rows ────────────────────────────────────────────────────────────

// AC 5: the per-agent open count renders only above one.
func TestInboxRowText_ShowsThePerAgentCountOnlyAboveOne(t *testing.T) {
	one := &fakeInbox{items: []InboxItem{inboxItemAt("p1", "eng1", "only one", time.Minute)}}
	if got := inboxRowText(one.items[0], one, 8, time.Now()); strings.Contains(got, "(1)") {
		t.Errorf("row = %q, want no count for a single open item", got)
	}
	many := &fakeInbox{items: []InboxItem{
		inboxItemAt("p1", "eng2", "first", time.Minute),
		inboxItemAt("p2", "eng2", "second", time.Minute),
	}}
	if got := inboxRowText(many.items[0], many, 8, time.Now()); !strings.Contains(got, "eng2 (2)") {
		t.Errorf("row = %q, want the open count when above one", got)
	}
}

// AC 3: the re-post marker renders for a flagged item and NOT for one the
// store did not flag (an item matching an ANSWERED item is never flagged).
func TestInboxRowText_RePostMarkerOnlyWhenTheStoreFlaggedIt(t *testing.T) {
	flagged := inboxItemAt("p1", "eng1", "same question", time.Minute)
	flagged.RePostOfDismissed = true
	if got := inboxRowText(flagged, &fakeInbox{}, 6, time.Now()); !strings.Contains(got, inboxRePostMarker) {
		t.Errorf("row = %q, want the re-post marker", got)
	}
	plain := inboxItemAt("p2", "eng1", "same question", time.Minute)
	if got := inboxRowText(plain, &fakeInbox{}, 6, time.Now()); strings.Contains(got, inboxRePostMarker) {
		t.Errorf("row = %q, want NO marker: a post matching an answered item is not a re-post", got)
	}
}

func TestInboxRowText_ChimeMarkOnlyWhenFlagged(t *testing.T) {
	chimed := inboxItemAt("p1", "eng1", "urgent", time.Minute)
	chimed.Chime = true
	if !strings.Contains(inboxRowText(chimed, &fakeInbox{}, 6, time.Now()), inboxChimeMark) {
		t.Error("a chimed item's row has no chime mark")
	}
	if strings.Contains(inboxRowText(inboxItemAt("p2", "eng1", "calm", time.Minute), &fakeInbox{}, 6, time.Now()), inboxChimeMark) {
		t.Error("an unflagged item's row carries a chime mark")
	}
}

// The row shows the FIRST LINE only, however long the body is (AC 6's row
// half; the detail pane's scroll is the other half).
func TestInboxRowText_ShowsTheFirstLineOnlyOfALongBody(t *testing.T) {
	body := "the first line\n" + strings.Repeat("another line\n", 39)
	it := inboxItemAt("p1", "eng1", body, time.Minute)
	got := inboxRowText(it, &fakeInbox{}, 6, time.Now())
	if !strings.Contains(got, "the first line") {
		t.Fatalf("row = %q, want the first line", got)
	}
	if strings.Contains(got, "another line") {
		t.Errorf("row = %q, want only the first line of a 40-line body", got)
	}
}

// Name padding is by DISPLAY WIDTH, not rune count: the badge next door paid
// this lesson already (ini-ug62), and a CJK agent name would misalign every
// row without it.
func TestInboxNameWidth_MeasuresDisplayWidthNotRunes(t *testing.T) {
	f := &fakeInbox{items: []InboxItem{inboxItemAt("p1", "日本語", "wide name", time.Minute)}}
	if got := inboxNameWidth(f.items, f); got != 6 {
		t.Errorf("width of 日本語 = %d, want 6 columns (three runes, six columns)", got)
	}
}

// ── rendered output (the bead's tier: in-process render) ────────────

// inboxTUI builds a TUI with a simulation screen and a planted inbox store,
// so the render tests assert what is ON SCREEN rather than what a helper
// returned.
func inboxTUI(t *testing.T, items ...InboxItem) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	tui, s := newTestTUIWithScreen("eng1")
	// LoadInbox, not newInbox: LoadInbox is what sets memoryOnly for a
	// rootless store, and newInbox leaves it false — a store that then SAVES
	// to a relative path, dropping .initech/inbox.yaml into the package
	// directory. My first fixture did exactly that and broke a sibling test
	// that exists to catch precisely this (inbox_test.go's rootless case).
	ib, err := LoadInbox("", true)
	if err != nil {
		t.Fatal(err)
	}
	ib.items = append(ib.items, items...)
	tui.inboxStore = ib
	tui.inboxOnce.Do(func() {})
	return tui, s
}

func inboxScreen(t *testing.T, s tcell.SimulationScreen) string {
	t.Helper()
	w, h := s.Size()
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ch, _, _, _ := s.GetContent(x, y)
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// AC 2: the empty panel teaches the affordance instead of showing a blank box.
func TestInboxPanel_EmptyStateTeachesHowItemsArrive(t *testing.T) {
	tui, s := inboxTUI(t)
	tui.openInboxPanel()
	tui.renderInboxPanel()
	if got := inboxScreen(t, s); !strings.Contains(got, "nothing posted") || !strings.Contains(got, "initech post") {
		t.Errorf("empty panel does not teach the affordance:\n%s", got)
	}
}

// AC 1: selecting marks the item seen AND the corner count drops, asserted
// together because they are one act.
func TestInboxPanel_SelectingMarksSeenAndTheCountDropsInTheSameFrame(t *testing.T) {
	tui, _ := inboxTUI(t,
		inboxItemAt("p1", "eng1", "first", 10*time.Minute),
		inboxItemAt("p2", "eng2", "second", 5*time.Minute),
	)
	if got := tui.inboxUnreadForBadge(); got != 2 {
		t.Fatalf("unread before opening = %d, want 2", got)
	}
	tui.openInboxPanel() // anchors on the oldest and marks it seen
	it, _ := tui.inboxState().Item("p1")
	if it.State != InboxSeen {
		t.Errorf("opening did not mark the selected item seen: state = %s", it.State)
	}
	if got := tui.inboxUnreadForBadge(); got != 1 {
		t.Errorf("count after the selection = %d, want 1 — seen and the count are one act", got)
	}
	if got := len(inboxListFor(tui.inboxState())); got != 2 {
		t.Errorf("the seen item left the list (%d listed, want 2)", got)
	}
}

// AC 5: the corner count renders only above zero.
func TestInboxPanel_CornerCountRendersOnlyWhenSomethingIsUnread(t *testing.T) {
	tui, s := inboxTUI(t, inboxItemAt("p1", "eng1", "unread", time.Minute))
	tui.projectName = "initech"
	tui.renderProjectBadge()
	if got := inboxScreen(t, s); !strings.Contains(got, "✉ 1") {
		t.Errorf("badge has no unread mark:\n%s", strings.SplitN(got, "\n", 2)[0])
	}

	tui2, s2 := inboxTUI(t)
	tui2.projectName = "initech"
	tui2.renderProjectBadge()
	if got := inboxScreen(t, s2); strings.Contains(got, "✉") {
		t.Errorf("badge shows an envelope with nothing unread:\n%s", strings.SplitN(got, "\n", 2)[0])
	}
}

// AC 4 + the delivery rule: the detail pane renders the default and the send
// path's own delivery word, and never a word of the panel's own.
func TestInboxPanel_DetailRendersTheDefaultAndTheDeliveryVerdict(t *testing.T) {
	it := inboxItemAt("p1", "eng1", "should I update both docs?", time.Minute)
	it.DefaultText = "update both"
	it.State = InboxAnswered
	it.DeliveryStatus = "held — agent has a dialog open; delivers when it clears"
	tui, s := inboxTUI(t, it)
	tui.openInboxPanel()
	tui.renderInboxPanel()
	got := inboxScreen(t, s)
	if !strings.Contains(got, "default: update both") {
		t.Errorf("detail pane does not render the default:\n%s", got)
	}
	if !strings.Contains(got, "held — agent has a dialog") {
		t.Errorf("detail pane does not render the send path's delivery verdict:\n%s", got)
	}
}

// AC 4: `a` without a default says why, never silence.
func TestInboxPanel_AcceptWithoutADefaultSaysWhy(t *testing.T) {
	tui, s := inboxTUI(t, inboxItemAt("p1", "eng1", "a question with no default", time.Minute))
	tui.openInboxPanel()
	tui.handleInboxKey(tcell.NewEventKey(tcell.KeyRune, 'a', 0))
	tui.renderInboxPanel()
	if got := inboxScreen(t, s); !strings.Contains(got, inboxNoDefault) {
		t.Errorf("pressing a on an item with no default said nothing:\n%s", got)
	}
}

// AC 4: `a` WITH a default invokes child D's accept path, with the item id.
func TestInboxPanel_AcceptWithADefaultInvokesTheAcceptPath(t *testing.T) {
	it := inboxItemAt("p1", "eng1", "q", time.Minute)
	it.DefaultText = "update both"
	tui, _ := inboxTUI(t, it)
	tui.openInboxPanel()
	var accepted string
	tui.inbox.onAccept = func(id string) { accepted = id }
	tui.handleInboxKey(tcell.NewEventKey(tcell.KeyRune, 'a', 0))
	if accepted != "p1" {
		t.Errorf("accept path invoked with %q, want p1", accepted)
	}
}

// AC 3: the re-post marker renders on its own row.
func TestInboxPanel_RePostMarkerRendersOnTheRow(t *testing.T) {
	it := inboxItemAt("p1", "eng1", "asked before", time.Minute)
	it.RePostOfDismissed = true
	tui, s := inboxTUI(t, it)
	tui.openInboxPanel()
	tui.renderInboxPanel()
	if got := inboxScreen(t, s); !strings.Contains(got, inboxRePostMarker) {
		t.Errorf("the re-post marker is not on screen:\n%s", got)
	}
}

// AC 6: a 40-line body scrolls in the detail pane while the row still shows
// one line.
func TestInboxPanel_LongBodyScrollsWhileTheRowShowsOneLine(t *testing.T) {
	body := "first line of the post\n"
	for i := 2; i <= 40; i++ {
		body += fmt.Sprintf("body line %d\n", i)
	}
	tui, s := inboxTUI(t, inboxItemAt("p1", "eng1", body, time.Minute))
	tui.openInboxPanel()
	tui.renderInboxPanel()
	got := inboxScreen(t, s)
	if !strings.Contains(got, "more lines") {
		t.Errorf("a 40-line body does not report that it scrolls:\n%s", got)
	}
	if strings.Count(got, "body line") > 4 {
		t.Errorf("the detail pane rendered the whole body rather than a window:\n%s", got)
	}
}

// AC 7: the persistence-fallback line renders when the store cannot save, and
// is absent otherwise.
func TestInboxPanel_PersistenceFallbackLineRendersOnlyWhenItApplies(t *testing.T) {
	tui, s := inboxTUI(t, inboxItemAt("p1", "eng1", "q", time.Minute))
	tui.openInboxPanel()
	tui.renderInboxPanel()
	// The fixture store is memory-only (no project root), which is exactly
	// one of the two conditions A reports.
	if got := inboxScreen(t, s); !strings.Contains(got, InboxNotPersistingPrefix) {
		t.Errorf("a memory-only store renders no fallback line:\n%s", got)
	}

	rooted, s2 := inboxTUI(t, inboxItemAt("p1", "eng1", "q", time.Minute))
	persisting, err := LoadInbox(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	persisting.items = append(persisting.items, inboxItemAt("p1", "eng1", "q", time.Minute))
	rooted.inboxStore = persisting
	rooted.openInboxPanel()
	rooted.renderInboxPanel()
	if got := inboxScreen(t, s2); strings.Contains(got, InboxNotPersistingPrefix) {
		t.Errorf("a persisting store still renders the fallback line:\n%s", got)
	}
}

// AC 9: the chord is discoverable. An undiscoverable chord is an unused one,
// exactly as an undocumented command is.
func TestInboxPanel_ChordAppearsInTheHelpCard(t *testing.T) {
	found := false
	for _, line := range getHelpLines() {
		if strings.Contains(line, "+i") && strings.Contains(strings.ToLower(line), "inbox") {
			found = true
		}
	}
	if !found {
		t.Error("the inbox chord is not in the help card")
	}
}
