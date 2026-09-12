package tui

// The operator inbox store (ini-3wkl.2).
//
// This is the bead everything else in the epic depends on, so the cells below
// are about the properties the other children will build against: the state
// machine cannot be walked into a state that cannot happen, open items outlive
// a restart, and the two detections teach once per run rather than nagging.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func inboxRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func mustLoadInbox(t *testing.T, root string) *Inbox {
	t.Helper()
	ib, err := LoadInbox(root, true)
	if err != nil {
		t.Fatalf("LoadInbox: %v", err)
	}
	return ib
}

func mustPost(t *testing.T, ib *Inbox, agent, body, runKey string) InboxPostResult {
	t.Helper()
	res, err := ib.Post(InboxItem{Agent: agent, Body: body}, runKey)
	if err != nil {
		t.Fatalf("Post(%q): %v", body, err)
	}
	return res
}

// ── AC 11: the state machine, both directions ────────────────────────

// TestInbox_LegalTransitionsAreAllowed is the table's positive half. A
// transition REMOVED from the table must red this.
func TestInbox_LegalTransitionsAreAllowed(t *testing.T) {
	cases := []struct {
		from, to InboxState
		by       inboxActor
	}{
		{InboxUnread, InboxSeen, actorOperator},
		{InboxUnread, InboxAnswered, actorOperator},
		{InboxUnread, InboxDismissed, actorOperator},
		{InboxUnread, InboxWithdrawn, actorAgent},
		{InboxSeen, InboxAnswered, actorOperator},
		{InboxSeen, InboxDismissed, actorOperator},
		{InboxSeen, InboxWithdrawn, actorAgent},
	}
	for _, c := range cases {
		ib := mustLoadInbox(t, inboxRoot(t))
		id := mustPost(t, ib, "eng2", "body", "run1").ID
		if c.from == InboxSeen {
			if err := ib.Transition(id, InboxSeen, actorOperator); err != nil {
				t.Fatalf("setup %s: %v", c.from, err)
			}
		}
		if err := ib.Transition(id, c.to, c.by); err != nil {
			t.Errorf("%s -> %s by %s was refused: %v", c.from, c.to, c.by, err)
		}
	}
}

// TestInbox_ForbiddenTransitionsAreRefused is the table's negative half, and
// the half that keeps the panel honest: a transition ADDED to the table must
// red this. The user story's second clause is "the inbox never shows a state
// that cannot happen".
func TestInbox_ForbiddenTransitionsAreRefused(t *testing.T) {
	cases := []struct {
		name     string
		from, to InboxState
		by       inboxActor
	}{
		{"terminal states do not reopen", InboxDismissed, InboxSeen, actorOperator},
		{"answered does not reopen", InboxAnswered, InboxUnread, actorOperator},
		{"withdrawn does not reopen", InboxWithdrawn, InboxSeen, actorOperator},
		{"seen does not go back to unread", InboxSeen, InboxUnread, actorOperator},
		{"an item cannot be answered twice", InboxAnswered, InboxAnswered, actorOperator},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ib := mustLoadInbox(t, inboxRoot(t))
			id := mustPost(t, ib, "eng2", "body", "run1").ID
			// Walk to the starting state through legal moves only.
			switch c.from {
			case InboxSeen:
				_ = ib.Transition(id, InboxSeen, actorOperator)
			case InboxDismissed:
				_ = ib.Transition(id, InboxDismissed, actorOperator)
			case InboxAnswered:
				_ = ib.Answer(id, "reply")
			case InboxWithdrawn:
				_ = ib.Transition(id, InboxWithdrawn, actorAgent)
			}
			if err := ib.Transition(id, c.to, c.by); err == nil {
				t.Errorf("%s -> %s was ALLOWED; the panel can now show a state the "+
					"machine says cannot happen", c.from, c.to)
			}
		})
	}
}

// TestInbox_WithdrawIsTheAgentsActAlone pins the actor half of the guard --
// the part a table keyed only on states would lose.
func TestInbox_WithdrawIsTheAgentsActAlone(t *testing.T) {
	ib := mustLoadInbox(t, inboxRoot(t))
	id := mustPost(t, ib, "eng2", "body", "run1").ID

	if err := ib.Transition(id, InboxWithdrawn, actorOperator); err == nil {
		t.Error("the OPERATOR withdrew an item; withdraw is the agent saying it resolved " +
			"the question itself, and an operator doing it would erase the item with no " +
			"record of either answering or dismissing it")
	}
	if err := ib.Transition(id, InboxAnswered, actorAgent); err == nil {
		t.Error("an AGENT answered its own item")
	}
	if err := ib.Transition(id, InboxWithdrawn, actorAgent); err != nil {
		t.Errorf("the agent could not withdraw its own item: %v", err)
	}
}

// ── AC 6: persistence and the startup prune ──────────────────────────

// TestInbox_OpenItemsSurviveAReload is the whole reason the store is a file.
func TestInbox_OpenItemsSurviveAReload(t *testing.T) {
	root := inboxRoot(t)
	ib := mustLoadInbox(t, root)
	unread := mustPost(t, ib, "eng2", "still waiting", "run1").ID
	seen := mustPost(t, ib, "eng2", "operator looked", "run1").ID
	if err := ib.Transition(seen, InboxSeen, actorOperator); err != nil {
		t.Fatal(err)
	}

	reloaded := mustLoadInbox(t, root)
	for _, id := range []string{unread, seen} {
		if _, ok := reloaded.Item(id); !ok {
			t.Errorf("open item %s did not survive the reload; an agent's question was "+
				"lost across a restart, which is what the file exists to prevent", id)
		}
	}
}

// TestInbox_TerminalItemsArePrunedAtStartup, and the crash half with it.
func TestInbox_TerminalItemsArePrunedAtStartup(t *testing.T) {
	root := inboxRoot(t)
	ib := mustLoadInbox(t, root)
	answered := mustPost(t, ib, "eng2", "a", "run1").ID
	dismissed := mustPost(t, ib, "eng2", "b", "run1").ID
	withdrawn := mustPost(t, ib, "eng2", "c", "run1").ID
	open := mustPost(t, ib, "eng2", "d", "run1").ID
	if err := ib.Answer(answered, "yes"); err != nil {
		t.Fatal(err)
	}
	// CONFIRMED DELIVERED, or this item would correctly SURVIVE the prune:
	// terminal-by-status is not terminal-by-delivery (PM rework blocker 3).
	// Without this line the cell asserts the pre-rework rule, which is how the
	// first version of this suite locked the bug in place.
	if err := ib.SetDeliveryStatus(answered, InboxDelivered); err != nil {
		t.Fatal(err)
	}
	if err := ib.Transition(dismissed, InboxDismissed, actorOperator); err != nil {
		t.Fatal(err)
	}
	if err := ib.Transition(withdrawn, InboxWithdrawn, actorAgent); err != nil {
		t.Fatal(err)
	}

	// No clean shutdown happens here -- the file is simply on disk, exactly as
	// a kill -9 would leave it. That is the CRASH path, and it must prune
	// identically to a clean one, because the prune is on load and there is no
	// shutdown step to skip.
	next := mustLoadInbox(t, root)
	for _, id := range []string{answered, dismissed, withdrawn} {
		if _, ok := next.Item(id); ok {
			t.Errorf("terminal item %s survived the startup prune", id)
		}
	}
	if _, ok := next.Item(open); !ok {
		t.Error("the prune took an OPEN item with it")
	}

	// AND THE PRUNE IS WRITTEN, asserted on the FILE. Checking the item count
	// after a reload cannot see this: an unwritten prune re-prunes on every
	// load and the count is right every time, while the file grows forever
	// and the operator's inbox.yaml fills with items he closed months ago.
	// A mutant that skipped the save survived the count assertion.
	onDisk, err := os.ReadFile(filepath.Join(root, ".initech", "inbox.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{answered, dismissed, withdrawn} {
		if strings.Contains(string(onDisk), "id: "+id+"\n") {
			t.Errorf("pruned item %s is still IN THE FILE; the prune ran in memory only, "+
				"so the file keeps every terminal item forever", id)
		}
	}
	if !strings.Contains(string(onDisk), "id: "+open+"\n") {
		t.Errorf("the open item %s is not in the file after the prune's rewrite", open)
	}
}

// ── AC 13: re-post marking, both halves ──────────────────────────────

func TestInbox_RePostOfADismissedItemIsMarked(t *testing.T) {
	ib := mustLoadInbox(t, inboxRoot(t))
	first := mustPost(t, ib, "eng2", "Should I update both docs?\nmore body", "run1").ID
	if err := ib.Transition(first, InboxDismissed, actorOperator); err != nil {
		t.Fatal(err)
	}

	// Same first line, different whitespace and case: the production
	// normaliser compacts the space, EqualFold handles the case.
	second := mustPost(t, ib, "eng2", "should i   update both docs?\ndifferent body", "run1").ID
	it, _ := ib.Item(second)
	if !it.RePostOfDismissed {
		t.Error("a re-post of a DISMISSED item was not marked; the operator cannot see " +
			"that the agent is re-asking something already refused")
	}
}

// TestInbox_RePostOfAnAnsweredItemIsNotMarked is AC 13's negative half and the
// guard that keeps the detection honest: a false marker tells the operator to
// ignore something he should read.
func TestInbox_RePostOfAnAnsweredItemIsNotMarked(t *testing.T) {
	ib := mustLoadInbox(t, inboxRoot(t))
	first := mustPost(t, ib, "eng2", "Should I update both docs?", "run1").ID
	if err := ib.Answer(first, "yes, both"); err != nil {
		t.Fatal(err)
	}

	second := mustPost(t, ib, "eng2", "Should I update both docs?", "run1").ID
	it, _ := ib.Item(second)
	if it.RePostOfDismissed {
		t.Error("a post matching an ANSWERED item was marked as a re-post of a dismissal; " +
			"the operator engaged with that question, and marking it teaches the agent " +
			"that asking again after an answer is misuse")
	}
}

func TestInbox_ADifferentAgentsDismissalDoesNotMark(t *testing.T) {
	ib := mustLoadInbox(t, inboxRoot(t))
	other := mustPost(t, ib, "eng4", "Should I update both docs?", "run1").ID
	if err := ib.Transition(other, InboxDismissed, actorOperator); err != nil {
		t.Fatal(err)
	}
	mine := mustPost(t, ib, "eng2", "Should I update both docs?", "run1").ID
	it, _ := ib.Item(mine)
	if it.RePostOfDismissed {
		t.Error("eng2 was marked for a question the operator dismissed from eng4; the " +
			"dismissal was not addressed to it")
	}
}

// ── AC 15: teaching lines, once per condition per RUN ────────────────

func TestInbox_RePostTeachesOncePerRun(t *testing.T) {
	ib := mustLoadInbox(t, inboxRoot(t))
	first := mustPost(t, ib, "eng2", "same question", "run1").ID
	if err := ib.Transition(first, InboxDismissed, actorOperator); err != nil {
		t.Fatal(err)
	}

	one := mustPost(t, ib, "eng2", "same question", "run1")
	if !strings.Contains(one.Notice, "Dismissed means no") {
		t.Fatalf("first re-post taught nothing: %q", one.Notice)
	}
	two := mustPost(t, ib, "eng2", "same question", "run1")
	if two.Notice != "" {
		t.Errorf("the same condition taught twice in one run (%q); nagging trains agents "+
			"to ignore output, which is the spec's stated reason for the latch", two.Notice)
	}

	// A RESTARTED agent has fresh context and must be taught again. This is
	// the half that fails silently if the run key is the agent's NAME.
	three := mustPost(t, ib, "eng2", "same question", "run2")
	if !strings.Contains(three.Notice, "Dismissed means no") {
		t.Errorf("a restarted agent (new run key) was NOT re-taught: %q. Its context is "+
			"fresh -- it has never seen this rule", three.Notice)
	}
}

func TestInbox_ThresholdTeachesOnTheCrossingOnly(t *testing.T) {
	ib := mustLoadInbox(t, inboxRoot(t))
	for i := 0; i < inboxOpenThreshold; i++ {
		if got := mustPost(t, ib, "eng2", "item", "run1").Notice; got != "" {
			t.Fatalf("post %d taught below the threshold: %q", i+1, got)
		}
	}
	crossing := mustPost(t, ib, "eng2", "item", "run1")
	if !strings.Contains(crossing.Notice, "items waiting on the operator") {
		t.Fatalf("crossing the threshold taught nothing: %q", crossing.Notice)
	}
	above := mustPost(t, ib, "eng2", "item", "run1")
	if above.Notice != "" {
		t.Errorf("a post ABOVE the threshold taught again (%q); the spec says once per "+
			"crossing, not per post above it", above.Notice)
	}
}

// ── AC 5: the fallbacks and their reason strings ─────────────────────

// TestInbox_CorruptFileYieldsAReadOnlyStoreThatDoesNotOverwriteIt.
func TestInbox_CorruptFileYieldsAReadOnlyStoreThatDoesNotOverwriteIt(t *testing.T) {
	root := inboxRoot(t)
	path := filepath.Join(root, ".initech", "inbox.yaml")
	corrupt := []byte("items: [this is not: valid: yaml\n")
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}

	ib, err := LoadInbox(root, true)
	if err == nil {
		t.Error("a corrupt store loaded without an error")
	}
	if reason := ib.PersistenceReason(); !strings.Contains(reason, "not being saved") {
		t.Errorf("read-only store's reason is %q; child C renders this in the header and "+
			"an operator who cannot see it loses items silently", reason)
	}

	if _, err := ib.Post(InboxItem{Agent: "eng2", Body: "x"}, "run1"); err == nil {
		t.Error("a read-only store accepted a write")
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(corrupt) {
		t.Error("the refused write TOUCHED THE FILE. A corrupt file is not an absent " +
			"file: overwriting it discards whatever the operator's real items were, and " +
			"presents as a successful reset")
	}
}

func TestInbox_RootlessStoreIsMemoryOnlyAndWritesNoStrayFile(t *testing.T) {
	ib, err := LoadInbox("", true)
	if err != nil {
		t.Fatalf("rootless load: %v", err)
	}
	if reason := ib.PersistenceReason(); !strings.Contains(reason, "this session only") {
		t.Errorf("memory-only reason is %q", reason)
	}
	if _, err := ib.Post(InboxItem{Agent: "eng2", Body: "x"}, "run1"); err != nil {
		t.Errorf("a rootless store REFUSED a write (%v); it must accept in memory, or an "+
			"ad-hoc TUI is worse than useless", err)
	}
	if _, err := os.Stat(filepath.Join(".initech", "inbox.yaml")); err == nil {
		t.Error("a rootless store wrote .initech/inbox.yaml into the CURRENT DIRECTORY; " +
			"inboxPath(\"\") is relative, so this lands wherever initech was launched from")
	}
}

func TestInbox_AHealthyStoreReportsNoPersistenceReason(t *testing.T) {
	if reason := mustLoadInbox(t, inboxRoot(t)).PersistenceReason(); reason != "" {
		t.Errorf("a healthy store reports %q; C would render a warning header on a store "+
			"that is fine", reason)
	}
}

// ── AC 6 (authority): refused at the mutation point ──────────────────

func TestInbox_NonAuthorityIsRefusedAtTheMutationPoint(t *testing.T) {
	root := inboxRoot(t)
	viewer, err := LoadInbox(root, false)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := viewer.Post(InboxItem{Agent: "eng2", Body: "x"}, "run1"); err == nil {
		t.Error("a secondary window wrote the inbox directly; single-writer says it must " +
			"request the mutation through window 1 and never improvise")
	}
	if err := viewer.Answer("p1", "reply"); err == nil {
		t.Error("a secondary window answered an item directly")
	}
	if _, statErr := os.Stat(filepath.Join(root, ".initech", "inbox.yaml")); statErr == nil {
		t.Error("the refused writes created the file anyway")
	}
}

// TestInbox_AnswerStoresTheReplyText is seam 2's named failure mode, asserted
// here rather than trusted.
//
// eng2's breakdown says it plainly: if a reply marks the item answered WITHOUT
// storing the text, --check reports "answered:" with nothing after it, "and it
// will pass every test anyone writes for the panel". It did -- a mutant that
// dropped the assignment survived my whole suite until this cell existed.
// Child D's accept key composes its text and comes through Answer for exactly
// this reason.
func TestInbox_AnswerStoresTheReplyText(t *testing.T) {
	ib := mustLoadInbox(t, inboxRoot(t))
	id := mustPost(t, ib, "eng2", "Should I update both docs?", "run1").ID

	if err := ib.Answer(id, "go with your default: update both"); err != nil {
		t.Fatal(err)
	}
	it, ok := ib.Item(id)
	if !ok {
		t.Fatal("item vanished")
	}
	if it.State != InboxAnswered {
		t.Errorf("state is %q, want answered", it.State)
	}
	if it.ReplyText != "go with your default: update both" {
		t.Errorf("ReplyText is %q; --check would report \"answered:\" with nothing after "+
			"it, and the operator's answer is lost to the agent", it.ReplyText)
	}
}

// TestInbox_AnUndeliveredAnswerSurvivesTheStartupPrune is the PM rework's
// blocker 3, and AC 6's amended half, in its own words: "answer, kill before
// delivery, restart, --check still returns the text".
//
// TERMINAL-BY-STATUS IS NOT TERMINAL-BY-DELIVERY. An answered item whose reply
// never reached the agent holds the ONLY COPY of what the operator said. The
// spec's edge case promises that reply is retrievable when a dead or stopped
// agent returns, and "when it returns" can be a later session -- so pruning it
// on the next startup destroys the answer before its reader ever existed.
//
// THIS CELL REPLACES ONE THAT ASSERTED THE OPPOSITE. The version I shipped in
// b4b4a9b said "Answered is terminal, so it is pruned on the NEXT load" and
// checked exactly that, which locked the pre-rework behaviour into the suite
// (qa1's finding, and the sharper half of it: a test can hold a bug in place
// more firmly than the code does).
func TestInbox_AnUndeliveredAnswerSurvivesTheStartupPrune(t *testing.T) {
	root := inboxRoot(t)
	ib := mustLoadInbox(t, root)
	id := mustPost(t, ib, "eng2", "question", "run1").ID
	if err := ib.Answer(id, "the answer"); err != nil {
		t.Fatal(err)
	}
	// No delivery confirmation: blank is unconfirmed, never delivered. This is
	// the crash-before-delivery shape -- nothing else happens to the store.

	reloaded := mustLoadInbox(t, root)
	it, ok := reloaded.Item(id)
	if !ok {
		t.Fatal("an answered-but-UNDELIVERED item was pruned on restart. Its reply was " +
			"the only copy of the operator's answer, and the agent it was written for " +
			"can never read it -- the failure class this whole epic exists to end")
	}
	if it.ReplyText != "the answer" {
		t.Errorf("the item survived but its reply text did not: %q", it.ReplyText)
	}
}

// TestInbox_ADeliveredAnswerIsPruned is the other direction, without which the
// carve-out could simply never prune answered items at all.
func TestInbox_ADeliveredAnswerIsPruned(t *testing.T) {
	root := inboxRoot(t)
	ib := mustLoadInbox(t, root)
	id := mustPost(t, ib, "eng2", "question", "run1").ID
	if err := ib.Answer(id, "the answer"); err != nil {
		t.Fatal(err)
	}
	if err := ib.SetDeliveryStatus(id, InboxDelivered); err != nil {
		t.Fatal(err)
	}

	if _, ok := mustLoadInbox(t, root).Item(id); ok {
		t.Error("an answer CONFIRMED delivered survived the prune; the inbox would keep " +
			"every resolved item forever")
	}
}

// TestInbox_DismissedAndWithdrawnPruneRegardless: nothing was ever sent for
// them, so delivery has no bearing.
func TestInbox_DismissedAndWithdrawnPruneRegardless(t *testing.T) {
	root := inboxRoot(t)
	ib := mustLoadInbox(t, root)
	dismissed := mustPost(t, ib, "eng2", "a", "run1").ID
	withdrawn := mustPost(t, ib, "eng2", "b", "run1").ID
	if err := ib.Transition(dismissed, InboxDismissed, actorOperator); err != nil {
		t.Fatal(err)
	}
	if err := ib.Transition(withdrawn, InboxWithdrawn, actorAgent); err != nil {
		t.Fatal(err)
	}

	next := mustLoadInbox(t, root)
	for _, id := range []string{dismissed, withdrawn} {
		if _, ok := next.Item(id); ok {
			t.Errorf("%s survived the prune; delivery status has no bearing on an item "+
				"whose reply was never sent", id)
		}
	}
}

// ── AC 13 amended: the similarity rule, and the two cases that define it ──

// TestInbox_SimilarityMatchesAcrossCase is the first of pm's two named cases.
func TestInbox_SimilarityMatchesAcrossCase(t *testing.T) {
	ib := mustLoadInbox(t, inboxRoot(t))
	first := mustPost(t, ib, "eng2", "Update docs?", "run1").ID
	if err := ib.Transition(first, InboxDismissed, actorOperator); err != nil {
		t.Fatal(err)
	}
	second := mustPost(t, ib, "eng2", "update docs?", "run1").ID
	if it, _ := ib.Item(second); !it.RePostOfDismissed {
		t.Error("'update docs?' was not matched against dismissed 'Update docs?'; the rule " +
			"is lowercased, so case alone must not hide a re-post")
	}
}

// TestInbox_SimilarityRespectsWordBoundaries is pm's second case and the one
// that made the borrowed normaliser wrong.
//
// compactPromptText DROPS whitespace rather than collapsing it, so "now here"
// and "nowhere" both became "nowhere" and a different question was marked as a
// re-post. A false marker tells the operator to IGNORE something he should
// read, which the bead's own design principle calls worse than a missed marker.
func TestInbox_SimilarityRespectsWordBoundaries(t *testing.T) {
	ib := mustLoadInbox(t, inboxRoot(t))
	first := mustPost(t, ib, "eng2", "nowhere", "run1").ID
	if err := ib.Transition(first, InboxDismissed, actorOperator); err != nil {
		t.Fatal(err)
	}
	second := mustPost(t, ib, "eng2", "now here", "run1").ID
	if it, _ := ib.Item(second); it.RePostOfDismissed {
		t.Error("'now here' was matched against dismissed 'nowhere'. Dropping whitespace " +
			"instead of collapsing it to a boundary makes two different questions the " +
			"same string, and the operator is told to ignore one he has never seen")
	}
}

// TestInbox_SimilarityCollapsesRunsAndTrims keeps the rest of the rule honest:
// runs collapse to one space, and leading/trailing space does not matter.
func TestInbox_SimilarityCollapsesRunsAndTrims(t *testing.T) {
	ib := mustLoadInbox(t, inboxRoot(t))
	first := mustPost(t, ib, "eng2", "  update   both   docs?  ", "run1").ID
	if err := ib.Transition(first, InboxDismissed, actorOperator); err != nil {
		t.Fatal(err)
	}
	second := mustPost(t, ib, "eng2", "Update both docs?", "run1").ID
	if it, _ := ib.Item(second); !it.RePostOfDismissed {
		t.Error("whitespace runs and trimming changed the match; the rule collapses runs " +
			"to one space and trims")
	}
}

// TestInbox_ThresholdDoesNotTeachAFreshRunThatIsAlreadyAbove pins the
// CROSSING semantics, and the decision behind them.
//
// An agent that restarts while already over the threshold is not taught in its
// new run, because no crossing happens there. Named here because it is a
// judgment call: the alternative teaches "you have 8 items" on every restart,
// which is the nagging the latch exists to prevent. A mutant changing the
// crossing test to a level test (n > threshold) survives every other cell,
// because the per-run latch hides it.
func TestInbox_ThresholdDoesNotTeachAFreshRunThatIsAlreadyAbove(t *testing.T) {
	ib := mustLoadInbox(t, inboxRoot(t))
	for i := 0; i <= inboxOpenThreshold; i++ {
		mustPost(t, ib, "eng2", "item", "run1")
	}
	if got := ib.OpenCount("eng2"); got <= inboxOpenThreshold {
		t.Fatalf("setup: %d open, want above %d", got, inboxOpenThreshold)
	}

	fresh := mustPost(t, ib, "eng2", "item", "run2-after-restart")
	if fresh.Notice != "" {
		t.Errorf("a fresh run posting while ALREADY above the threshold was taught (%q); "+
			"no crossing happened in that run, and teaching here means the line repeats "+
			"on every restart", fresh.Notice)
	}
}

// TestInbox_WithdrawIsOwnerCheckedUnderOneLock covers the primitive child B
// asked for: the owner check and the transition cannot be separated, or the
// operator can answer an item in the gap and the agent's withdraw lands on an
// already-resolved item.
func TestInbox_WithdrawIsOwnerCheckedUnderOneLock(t *testing.T) {
	ib := mustLoadInbox(t, inboxRoot(t))
	mine := mustPost(t, ib, "eng2", "mine", "run1").ID
	theirs := mustPost(t, ib, "eng4", "theirs", "run1").ID

	if err := ib.Withdraw(theirs, "eng2"); err == nil {
		t.Error("eng2 withdrew eng4's item; an agent can erase another agent's question " +
			"from the operator's inbox")
	}
	if err := ib.Withdraw(mine, "eng2"); err != nil {
		t.Errorf("an agent could not withdraw its own item: %v", err)
	}
	if it, _ := ib.Item(mine); it.State != InboxWithdrawn {
		t.Errorf("state after withdraw is %q", it.State)
	}
}
