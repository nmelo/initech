package tui

// The inbox chime, its rate limit, and the line the limiter alone can write
// (ini-3wkl.5).
//
// Every cell here counts Chime() on a fake Chimer rather than asserting on the
// terminal, which is why the Chimer is an interface at all. And every silence
// assertion is made on a TUI where sound is genuinely CONFIGURED -- asserting
// quiet on a fixture with no chimer passes for the wrong reason, measuring the
// fixture's missing sound instead of the rule. That trap is already recorded
// in TestAttentionChimes_OncePerHost; this file reuses its shape rather than
// rediscovering it.

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func chimeIdentity(agent, run string) postIdentity {
	return postIdentity{agent: agent, runKey: run}
}

// inboxChimeTUI is window 1 with sound on and a counting chimer.
func inboxChimeTUI(t *testing.T) (*TUI, *countingChimer) {
	t.Helper()
	tui, c := chimeTUI()
	tui.projectRoot = inboxRoot(t)
	return tui, c
}

// ── AC 1: rings once for a flagged post, never for an unflagged one ──

func TestInboxChime_FlaggedPostRingsOnce(t *testing.T) {
	tui, c := inboxChimeTUI(t)
	fired, notice := tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p1", time.Now())
	if !fired || c.n != 1 {
		t.Fatalf("fired=%v chimes=%d for a flagged post, want true and 1; without the bell "+
			"the operator must be LOOKING at the panel to learn an agent is blocked",
			fired, c.n)
	}
	if notice != "" {
		t.Errorf("a post that DID ring produced a suppression line: %q", notice)
	}
}

func TestInboxChime_UnflaggedPostNeverRings(t *testing.T) {
	tui, c := inboxChimeTUI(t)
	fired, notice := tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), false, "p1", time.Now())
	if fired || c.n != 0 {
		t.Errorf("an UNFLAGGED post rang (fired=%v chimes=%d). The bell means an agent "+
			"judged this worth interrupting the operator for; ringing for every post is "+
			"how it stops meaning that", fired, c.n)
	}
	if notice != "" {
		t.Errorf("an unflagged post produced a teaching line: %q", notice)
	}
}

// ── AC 2: the rate limit, both directions ────────────────────────────

func TestInboxChime_SecondPostInsideTheIntervalIsSilent(t *testing.T) {
	tui, c := inboxChimeTUI(t)
	now := time.Now()
	tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p1", now)

	fired, notice := tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p2",
		now.Add(4*time.Minute))
	if fired || c.n != 1 {
		t.Errorf("a second flagged post 4m later rang again (chimes=%d, want 1)", c.n)
	}
	if !strings.Contains(notice, "chime suppressed") || !strings.Contains(notice, "4m") {
		t.Errorf("the suppression line does not name how long ago the last chime was: %q.\n\n"+
			"The limiter is the only thing that knows it, which is why the line is built "+
			"there rather than by the caller", notice)
	}
}

func TestInboxChime_AfterTheIntervalItRingsAgain(t *testing.T) {
	tui, c := inboxChimeTUI(t)
	now := time.Now()
	tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p1", now)

	fired, _ := tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p2",
		now.Add(inboxChimeInterval+time.Second))
	if !fired || c.n != 2 {
		t.Errorf("a flagged post past the interval did not ring (fired=%v chimes=%d); the "+
			"limit shapes the noise, it does not end it", fired, c.n)
	}
}

// TestInboxChime_AtExactlyTheIntervalItRings pins the boundary. Found by a
// surviving mutant (<= for <), which is NOT equivalent: it differs at exactly
// inboxChimeInterval. The contract is "at most one per 10 minutes", and a
// chime exactly 10 minutes later is one per 10 minutes -- so it rings. Left
// unasserted, either reading would have passed.
func TestInboxChime_AtExactlyTheIntervalItRings(t *testing.T) {
	tui, c := inboxChimeTUI(t)
	now := time.Now()
	tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p1", now)

	fired, notice := tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p2",
		now.Add(inboxChimeInterval))
	if !fired || c.n != 2 {
		t.Errorf("a chime at exactly the interval was suppressed (fired=%v chimes=%d)", fired, c.n)
	}
	if notice != "" {
		t.Errorf("a chime that rang produced a suppression line: %q", notice)
	}
}

func TestInboxChime_TheLimitIsPerAgent(t *testing.T) {
	tui, c := inboxChimeTUI(t)
	now := time.Now()
	tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p1", now)

	if fired, _ := tui.chimeForInboxPost(chimeIdentity("eng4", "run1"), true, "p2", now); !fired {
		t.Error("eng4 was silenced by eng2's chime; the limit is per agent, and one noisy " +
			"agent must not mute a different one's genuine interrupt")
	}
	if c.n != 2 {
		t.Errorf("chimes=%d, want 2", c.n)
	}
}

// SUPPRESSION IS OF THE BELL ONLY -- the item still posts and still lists.
// This is the difference between the guard and a cap, and the spec is explicit
// that there is no cap.
func TestInboxChime_ASuppressedItemStillPostsAndLists(t *testing.T) {
	tui, _ := inboxChimeTUI(t)
	ib := tui.inboxState()
	now := time.Now()
	id := chimeIdentity("eng2", "run1")

	first, err := ib.Post(InboxItem{Agent: "eng2", Body: "first", Chime: true}, id.runKey)
	if err != nil {
		t.Fatal(err)
	}
	tui.chimeForInboxPost(id, true, first.ID, now)

	second, err := ib.Post(InboxItem{Agent: "eng2", Body: "second", Chime: true}, id.runKey)
	if err != nil {
		t.Fatal(err)
	}
	tui.chimeForInboxPost(id, true, second.ID, now.Add(time.Minute))

	if _, ok := ib.Item(second.ID); !ok {
		t.Fatal("the suppressed item is not in the store. The limiter silences the BELL; " +
			"an item it swallowed entirely would be a cap, and the spec says there is none")
	}
	if got := ib.OpenCount("eng2"); got != 2 {
		t.Errorf("open count is %d, want 2 -- both items must list", got)
	}
}

// ── AC 3: the teaching line, once per agent process ──────────────────

func TestInboxChime_SuppressionTeachesOncePerRunAndResets(t *testing.T) {
	tui, _ := inboxChimeTUI(t)
	now := time.Now()
	tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p1", now)

	_, first := tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p2", now.Add(time.Minute))
	if !strings.Contains(first, "chime suppressed") {
		t.Fatalf("first suppression taught nothing: %q", first)
	}
	_, second := tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p3", now.Add(2*time.Minute))
	if second != "" {
		t.Errorf("the same condition taught twice in one run: %q", second)
	}

	// A RESTARTED agent has fresh context and is taught again.
	_, afterRestart := tui.chimeForInboxPost(chimeIdentity("eng2", "run2"), true, "p4", now.Add(3*time.Minute))
	if !strings.Contains(afterRestart, "chime suppressed") {
		t.Errorf("a restarted agent was not re-taught: %q. Its context is fresh -- it has "+
			"never seen this rule", afterRestart)
	}
}

// ── AC 4: the rejected reminder must not arrive by inheritance ───────

// TestInboxChime_NeverReachesTheAttentionReminder is the negative control the
// bead asks for by name.
//
// chimeState carries chimeReminderDelay, the single 2-minute reminder the
// operator REJECTED for inbox items. An inbox chime must not create or touch
// that bookkeeping, or the reminder arrives by inheritance rather than by
// anyone deciding it should.
func TestInboxChime_NeverReachesTheAttentionReminder(t *testing.T) {
	tui, c := inboxChimeTUI(t)
	now := time.Now()

	if fired, _ := tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p1", now); !fired {
		t.Fatal("setup: the inbox chime did not ring, so this proves nothing")
	}
	if len(tui.chimeSeen) != 0 {
		t.Errorf("an inbox chime wrote %d entries into chimeSeen; that map is keyed to "+
			"WAITING EPISODES and carries the 2-minute reminder", len(tui.chimeSeen))
	}

	// Well past the reminder delay, the attention system must have nothing to
	// remind about -- there is no waiting pane and no episode was created.
	before := c.n
	tui.attentionChimes(now.Add(chimeReminderDelay * 2))
	if c.n != before {
		t.Errorf("a reminder followed an inbox chime (%d -> %d). The operator rejected "+
			"reminders for inbox items; inheriting one from the attention machine is "+
			"exactly how a rejected behaviour ships anyway", before, c.n)
	}
}

// ── AC 5: the window gate, on a fixture where sound is real ──────────

func TestInboxChime_SameHostChildWindowNeverRings(t *testing.T) {
	viewer, c := inboxChimeTUI(t)
	viewer.windowID = "window-2"

	fired, notice := viewer.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p1", time.Now())
	if fired || c.n != 0 {
		t.Errorf("a same-host child window rang (fired=%v chimes=%d). On one host the "+
			"windows share speakers, so a viewer chime is a duplicate of window 1's, not "+
			"a second notification (§289). Cross-machine is ini-tagj's and is not claimed "+
			"here", fired, c.n)
	}
	if notice != "" {
		t.Errorf("the viewer produced a teaching line for a bell it was never going to "+
			"ring: %q", notice)
	}
}

// TestInboxChime_TheItemStillListsInAChildWindow: the sound is pinned to one
// window, the SIGHT is not (§288).
//
// The actor here is WINDOW 1, not the viewer. A child window never posts --
// ErrInboxNotAuthority refuses it at the mutation chokepoint -- so a cell that
// posts through the viewer measures the single-writer rule and says nothing
// about listing. Window 1 writes the file; a viewer store on the SAME ROOT
// reads it back. That is the pairing AC 5 actually claims: the item the
// viewer stayed silent for is still there to be seen.
func TestInboxChime_TheItemStillListsInAChildWindow(t *testing.T) {
	root := inboxRoot(t)

	primary, primaryChime := chimeTUI()
	primary.projectRoot = root
	item, err := primary.inboxState().Post(
		InboxItem{Agent: "eng2", Body: "flagged", Chime: true}, "run1")
	if err != nil {
		t.Fatalf("window 1 could not post: %v", err)
	}
	if fired, _ := primary.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, item.ID, time.Now()); !fired {
		t.Fatal("setup: window 1 did not ring, so the viewer's silence proves nothing")
	}

	viewer, viewerChime := chimeTUI()
	viewer.projectRoot = root
	viewer.windowID = "window-2"

	if _, err := viewer.inboxState().Post(
		InboxItem{Agent: "eng2", Body: "from the viewer", Chime: true}, "run1"); !errors.Is(err, ErrInboxNotAuthority) {
		t.Errorf("a viewer wrote the store directly (err=%v); single-writer is enforced at "+
			"the mutation point, and this cell depends on it", err)
	}
	if got := viewer.inboxState().OpenCount("eng2"); got != 1 {
		t.Errorf("window 1's item does not list in the child window (open=%d, want 1). The "+
			"sound is pinned to one window; the sight travels to all of them", got)
	}
	if viewerChime.n != 0 || primaryChime.n != 1 {
		t.Errorf("chimes: viewer=%d want 0, window 1=%d want 1", viewerChime.n, primaryChime.n)
	}
}

// ── config off ───────────────────────────────────────────────────────

// TestInboxChime_SoundOffIsSilentAndTeachesNothing pins the decision named in
// the PLAN: the suppression line says "you chimed 4m ago", which is FALSE when
// the operator simply muted the bell. Teaching an agent to chime less for that
// reason is a lie about the cause, and the agent would act on it.
func TestInboxChime_SoundOffIsSilentAndTeachesNothing(t *testing.T) {
	tui, c := inboxChimeTUI(t)
	tui.attentionSound = "none"

	fired, notice := tui.chimeForInboxPost(chimeIdentity("eng2", "run1"), true, "p1", time.Now())
	if fired || c.n != 0 {
		t.Errorf("sound is off and it rang anyway (fired=%v chimes=%d)", fired, c.n)
	}
	if notice != "" {
		t.Errorf("muted sound produced a rate-limit teaching line: %q. The agent did not "+
			"chime too often; the operator turned the bell off, and the line would be "+
			"false", notice)
	}
}

// ── the bell-only property, through the real handler ─────────────────

// TestInboxChime_SuppressedPostSucceedsThroughTheHandler exercises the call
// site, not just the limiter.
//
// Everything above this line can be true while the HANDLER still turns a
// suppressed chime into a failed post -- the limiter has no store reference,
// so it structurally cannot drop an item, but the caller can. This is the cell
// that makes "suppression is of the sound only" a property of the product
// path: the second post returns OK, reports its id, and lists.
func TestInboxChime_SuppressedPostSucceedsThroughTheHandler(t *testing.T) {
	tui, c := inboxChimeTUI(t)
	id := chimeIdentity("eng2", "run1")
	now := time.Now()

	first := tui.applyPostRequest(IPCRequest{Action: "post", Text: "blocked on the schema", Chime: true}, id, now)
	if !first.OK {
		t.Fatalf("first post failed: %q", first.Error)
	}

	second := tui.applyPostRequest(IPCRequest{Action: "post", Text: "still blocked", Chime: true}, id, now.Add(time.Minute))
	if !second.OK {
		t.Fatalf("a post whose CHIME was suppressed failed: %q. The limiter silences the "+
			"bell; the post is unaffected", second.Error)
	}
	if !strings.HasPrefix(second.Data, "posted ") {
		t.Errorf("the suppressed post did not report an id: %q", second.Data)
	}
	if got := tui.inboxState().OpenCount("eng2"); got != 2 {
		t.Errorf("open count is %d, want 2 -- both posts must list", got)
	}
	if c.n != 1 {
		t.Errorf("chimes=%d, want 1: the second rang anyway", c.n)
	}
	if !noticeContains(second.Notices, "chime suppressed") {
		t.Errorf("the handler dropped the limiter's teaching line; notices=%q.\n\n"+
			"The limiter is the only thing that knows the chime was suppressed and why, "+
			"so if the caller does not carry the line, nothing else can write it",
			second.Notices)
	}
}

func noticeContains(notices []string, want string) bool {
	for _, n := range notices {
		if strings.Contains(n, want) {
			return true
		}
	}
	return false
}
