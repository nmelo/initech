package tui

// attention_chime_test.go covers the chime (ini-2x8.3).
//
// EVERY assertion here counts calls through the Chimer seam, never tcell
// returning nil. SimulationScreen.Beep() is a literal no-op, so "no error" is
// what a swallowed BEL looks like -- and a swallowed BEL is a silent detection,
// the exact failure this feature exists to end. TestScreenChimer_UsesTcellsOwn
// WritePath covers the production path separately.

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nmelo/initech/internal/config"
)

// countingChimer records chimes instead of making a sound.
type countingChimer struct{ n int }

func (c *countingChimer) Chime() { c.n++ }

// chimeTUI builds a TUI wired for chime tests: window one, sound on, a counting
// chimer in place of the terminal.
func chimeTUI(panes ...*Pane) (*TUI, *countingChimer) {
	c := &countingChimer{}
	t := newTestTUI(panes...)
	t.windowID = WindowOne
	t.chime = c
	t.attentionSound = "bell"
	t.chimeSeen = make(map[string]chimeState)
	return t, c
}

// waitingPaneAt builds a pane already waiting, at a chosen tier and age.
func waitingPaneAt(name string, tier WaitingTier, age time.Duration) *Pane {
	p := testPane(name)
	p.SetWaitingInputTier("a question for the operator", tier)
	p.mu.Lock()
	p.waitingSince = time.Now().Add(-age)
	p.mu.Unlock()
	return p
}

// ── Rising edge ─────────────────────────────────────────────────────

func TestAttentionChimes_FiresOnceOnTheRisingEdge(t *testing.T) {
	p := testPane("super")
	tu, c := chimeTUI(p)
	now := time.Now()

	// Not waiting yet.
	if got := tu.attentionChimes(now); got != 0 {
		t.Fatalf("chimed %d times before anyone was waiting", got)
	}

	p.SetWaitingInputTier("ship v2.6.0 now, or wait?", WaitingTierChime)
	if got := tu.attentionChimes(now); got != 1 {
		t.Fatalf("rising edge chimed %d times, want 1", got)
	}
	if c.n != 1 {
		t.Fatalf("chimer called %d times, want 1", c.n)
	}
}

func TestAttentionChimes_DoesNotRepeatWhileTheSameWaitPersists(t *testing.T) {
	// The operator's stated failure mode: a chime that cries wolf trains him to
	// ignore it, recreating the original problem with added noise.
	p := waitingPaneAt("super", WaitingTierChime, 0)
	tu, c := chimeTUI(p)
	now := time.Now()

	tu.attentionChimes(now)
	for i := 0; i < 200; i++ {
		tu.attentionChimes(now.Add(time.Duration(i) * time.Second))
	}

	if c.n > 2 {
		t.Errorf("chimed %d times across 200 ticks; want at most 2 (edge + one reminder)", c.n)
	}
}

func TestAttentionChimes_RingsAgainForAGenuinelyNewWait(t *testing.T) {
	// Answered, then asked again. Tracking the wait's START TIME rather than a
	// per-pane boolean is what makes this ring twice instead of being swallowed
	// as "already chimed for this agent".
	p := testPane("pm")
	tu, c := chimeTUI(p)
	now := time.Now()

	p.SetWaitingInputTier("stripe or paypal first?", WaitingTierChime)
	tu.attentionChimes(now)

	p.ClearWaitingInput()
	tu.attentionChimes(now.Add(time.Second))

	p.SetWaitingInputTier("and which currency?", WaitingTierChime)
	tu.attentionChimes(now.Add(2 * time.Second))

	if c.n != 2 {
		t.Errorf("chimer called %d times, want 2 (one per distinct wait)", c.n)
	}
}

// ── The single reminder ─────────────────────────────────────────────

func TestAttentionChimes_RemindsOnceAtTwoMinutes(t *testing.T) {
	p := waitingPaneAt("super", WaitingTierChime, 0)
	tu, c := chimeTUI(p)
	start := time.Now()

	tu.attentionChimes(start) // rising edge
	if c.n != 1 {
		t.Fatalf("setup: rising edge chimed %d times, want 1", c.n)
	}

	// Just before two minutes: still one.
	tu.attentionChimes(start.Add(chimeReminderDelay - time.Second))
	if c.n != 1 {
		t.Errorf("reminder fired early: %d chimes before the 2-minute mark", c.n)
	}

	// At two minutes: the reminder.
	tu.attentionChimes(start.Add(chimeReminderDelay))
	if c.n != 2 {
		t.Errorf("chimes at the 2-minute mark = %d, want 2", c.n)
	}

	// Then silence, however long the row persists.
	for i := 1; i <= 60; i++ {
		tu.attentionChimes(start.Add(chimeReminderDelay + time.Duration(i)*time.Minute))
	}
	if c.n != 2 {
		t.Errorf("chimes after an hour of waiting = %d, want 2 (edge + one reminder, then silence)", c.n)
	}
}

// ── Tier gate ───────────────────────────────────────────────────────

func TestAttentionChimes_ListOnlyTierNeverMakesASound(t *testing.T) {
	p := waitingPaneAt("qa4", WaitingTierListOnly, 0)
	tu, c := chimeTUI(p)
	start := time.Now()

	for i := 0; i <= 10; i++ {
		tu.attentionChimes(start.Add(time.Duration(i) * time.Minute))
	}

	if c.n != 0 {
		t.Errorf("a list-only detection chimed %d times; it must be visible and silent", c.n)
	}
}

func TestAttentionChimes_SilentTierIsTheZeroValue(t *testing.T) {
	// A detector that says nothing about confidence gets silence, not noise.
	// This is what stops a future detector from becoming audible by omission.
	p := testPane("newdetector")
	p.SetWaitingInput("something a new detector found")
	tu, c := chimeTUI(p)

	tu.attentionChimes(time.Now())

	if c.n != 0 {
		t.Errorf("default tier chimed %d times; the zero value must be silent", c.n)
	}
	if p.WaitingTierOf() != WaitingTierListOnly {
		t.Errorf("default tier = %v, want WaitingTierListOnly", p.WaitingTierOf())
	}
}

func TestAttentionChimes_UpgradedTierDoesNotChimeForAWaitAlreadyOnScreen(t *testing.T) {
	// A heuristic surfaces the row silently; a better signal later confirms it.
	// The operator has already been looking at that row, so confirming it is not
	// news and must not ring.
	p := testPane("codexpane")
	p.SetWaitingInputTier("dialog detected", WaitingTierListOnly)
	tu, c := chimeTUI(p)
	now := time.Now()

	tu.attentionChimes(now)
	p.SetWaitingInputTier("Do you want to proceed?", WaitingTierChime)
	tu.attentionChimes(now.Add(time.Second))

	if c.n != 0 {
		t.Errorf("upgrading a visible row chimed %d times, want 0", c.n)
	}
}

// ── Config ──────────────────────────────────────────────────────────

func TestAttentionChimes_SoundNoneStaysSilent(t *testing.T) {
	p := waitingPaneAt("super", WaitingTierChime, 0)
	tu, c := chimeTUI(p)
	tu.attentionSound = "none"

	for i := 0; i <= 5; i++ {
		tu.attentionChimes(time.Now().Add(time.Duration(i) * time.Minute))
	}

	if c.n != 0 {
		t.Errorf("attention.sound=none chimed %d times, want 0", c.n)
	}
}

func TestAttentionChimes_TurningSoundOnMidSessionDoesNotReplayOldWaits(t *testing.T) {
	// Switching sound on must announce NEW waits, not everything already on
	// screen -- a burst of chimes for questions the operator is already reading
	// is precisely the cry-wolf failure.
	p := waitingPaneAt("super", WaitingTierChime, 5*time.Minute)
	tu, c := chimeTUI(p)
	tu.attentionSound = "none"
	now := time.Now()

	tu.attentionChimes(now) // silent, but bookkeeping runs

	tu.attentionSound = "bell"
	tu.attentionChimes(now.Add(time.Second))

	if c.n != 0 {
		t.Errorf("enabling sound replayed %d chimes for a wait already in progress, want 0", c.n)
	}

	// A genuinely new wait still rings.
	p2 := testPane("eng1")
	p2.SetWaitingInputTier("Bash: rm -rf build/", WaitingTierChime)
	tu.panes = append(tu.panes, p2)
	tu.attentionChimes(now.Add(2 * time.Second))
	if c.n != 1 {
		t.Errorf("a new wait after enabling sound chimed %d times, want 1", c.n)
	}
}

func TestAttentionSound_DefaultsToBellAndOnlyNoneSilences(t *testing.T) {
	cases := map[string]string{
		"":                  "bell",
		"bell":              "bell",
		"none":              "none",
		"/path/to/ding.wav": "bell", // reserved value: must not silence, must not block load
		"nonsense":          "bell",
	}
	for in, want := range cases {
		got := config.AttentionConfig{Sound: in}.AttentionSound()
		if got != want {
			t.Errorf("attention.sound=%q normalised to %q, want %q", in, got, want)
		}
	}
}

// ── Multi-window ────────────────────────────────────────────────────

func TestAttentionChimes_OnlyWindowOneMakesNoise(t *testing.T) {
	// The list renders in every window; the sound does not multiply. A session
	// with several windows attached must ring once, not once per window.
	start := time.Now()

	p1 := waitingPaneAt("super", WaitingTierChime, 0)
	w1, c1 := chimeTUI(p1)
	w1.windowID = WindowOne

	p2 := waitingPaneAt("super", WaitingTierChime, 0)
	w2, c2 := chimeTUI(p2)
	w2.windowID = "2"

	p3 := waitingPaneAt("super", WaitingTierChime, 0)
	w3, c3 := chimeTUI(p3)
	w3.windowID = "3"

	w1.attentionChimes(start)
	w2.attentionChimes(start)
	w3.attentionChimes(start)

	if total := c1.n + c2.n + c3.n; total != 1 {
		t.Errorf("3 attached windows produced %d chimes for one rising edge, want 1", total)
	}
	if c1.n != 1 {
		t.Errorf("window one chimed %d times, want 1", c1.n)
	}
	if c2.n != 0 || c3.n != 0 {
		t.Errorf("secondary windows chimed (w2=%d w3=%d), want 0 each", c2.n, c3.n)
	}
}

// ── The production write path ───────────────────────────────────────

// TestScreenChimer_UsesTcellsOwnWritePath pins the seam that the counted tests
// above cannot see. tcell owns the terminal fd; writing byte 7 to os.Stdout
// directly lands wherever the current frame happens to be, which is the
// swallowed-BEL failure. Screen.Beep() writes through tcell's own writer.
func TestScreenChimer_UsesTcellsOwnWritePath(t *testing.T) {
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer s.Fini()

	c := screenChimer{screen: s}
	c.Chime() // must route through Screen.Beep, not os.Stdout

	// NOTE: SimulationScreen.Beep() is a no-op, so there is deliberately NO
	// assertion here that a bell was heard -- such an assertion would pass
	// whether or not the real terminal ever rings, which is worse than no test.
	// What this pins is that the chimer holds a tcell.Screen and calls into it;
	// the counted Chimer tests above carry the behavioural assertions.
	var _ tcell.Screen = c.screen
}

func TestScreenChimer_NilScreenDoesNotPanic(t *testing.T) {
	// lint:test-name-allow asserts the nil-screen contract; a panic here would kill the render loop
	c := screenChimer{}
	c.Chime()
}

// TestAttentionChimes_NewWaitBetweenTicksStillRings is qa1's probe from ini-1io,
// lifted verbatim, and it closes a real hole in this file's own coverage.
//
// TestAttentionChimes_RingsAgainForAGenuinelyNewWait ticks once while the pane
// is cleared, so pruneChimeState drops the entry and presence alone is enough to
// pass it. qa1 re-ran the presence-keyed mutation against the committed suite
// and it stayed GREEN -- my ini-2x8.3 DONE comment claimed that mutation went
// red, which was wrong: the mutation I ran also defeated prune, so it never
// isolated the identity comparison.
//
// This is the distinctive case: answered and re-asked BETWEEN two ticks, so
// prune never sees the gap and only comparing the wait's START TIME can tell
// there were two questions. It is also design point 3's own motivating case, and
// render-tick timing makes it rare enough that a regression would ship silently.
func TestAttentionChimes_NewWaitBetweenTicksStillRings(t *testing.T) {
	p := testPane("pm")
	tu, c := chimeTUI(p)
	now := time.Now()

	p.SetWaitingInputTier("stripe or paypal first?", WaitingTierChime)
	tu.attentionChimes(now)

	// Answered and re-asked with NO tick in between: prune never observes the
	// gap, so a presence-keyed edge would swallow the second question.
	p.ClearWaitingInput()
	p.SetWaitingInputTier("and which currency?", WaitingTierChime)

	tu.attentionChimes(now.Add(2 * time.Second))

	if c.n != 2 {
		t.Errorf("chimer called %d times, want 2 -- a question answered and re-asked between "+
			"ticks is two questions, and the second must not be swallowed", c.n)
	}
}
