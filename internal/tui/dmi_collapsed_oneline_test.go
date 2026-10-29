package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ini-dmi: a send into a busy pane read "our text was never seen in the box"
// while the text had landed. Measured on Claude Code 2.1.312 into a busy
// Haiku pane (claude --model haiku), 49 columns like the fleet's: a one-line
// body over 800 characters collapses to "[Pasted text #1]", with no "+N
// lines" count, and the placeholder reader needed the count. Ten clean
// samples before the Enter showed it and none read it as ours; after the
// Enter Claude queued the message and the box cleared. The fleet log's six
// such sends that day read the same: torn=0 stable=31 sighted=0.
//
// Fixtures (testdata/footer_corpus, account names replaced by a placeholder
// of the same length): haiku312_busy_collapsed_w49.txt is the rig's last
// sample before the Enter; fleet312_super_busy_w49.txt and
// fleet312_pm_busy_w49.txt are this fleet's busy super and pm panes on 2.1.312
// minutes after two sightings (initech peek, screen text only).

// dmiBody is the exact one-line body sent for haiku312_busy_collapsed_w49.txt.
var dmiBody = "[from b] DMIL1 one long line: " +
	strings.TrimSuffix(strings.Repeat("the landing is on main and CI is green on the sha, qa has it; ", 14), " ") +
	" marker DMIL1 end."

const dmiQueued = "❯ Press up to edit queued messages"

func dmiScreen(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "footer_corpus", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// dmiWithBox is a captured screen with its input box's prompt row replaced.
func dmiWithBox(t *testing.T, name, composer string) []string {
	t.Helper()
	rows := dmiScreen(t, name)
	top, _, ok := lowestComposerFrame(rows)
	if !ok {
		t.Fatalf("%s: no input box", name)
	}
	out := append([]string{}, rows...)
	out[top+1] = composer
	return out
}

// dmiBusyFake is the fake Claude around a captured busy screen: the paste
// shows as shows, a taken Enter queues the message and the box reads
// Claude's queued placeholder.
func dmiBusyFake(t *testing.T, name, body, shows string, takes func(int) bool) *pkinFake {
	t.Helper()
	return &pkinFake{
		body:  body,
		shows: shows,
		after: dmiQueued,
		takes: takes,
		screen: func(composer string) string {
			return "\x1b[2J\x1b[H" + strings.Join(dmiWithBox(t, name, composer), "\r\n")
		},
	}
}

func dmiBound(t *testing.T) { nma5Window(t, pkinBusyBound) }

// The fixture is what the bead is about: a busy pane (a spinner above the
// box) whose box holds Claude's count-free placeholder, and our body is a
// one-line body past the collapse length.
func TestCollapsedOneLine_TheFixtureIsTheBusyCollapsedBox(t *testing.T) {
	joined := strings.Join(dmiScreen(t, "haiku312_busy_collapsed_w49.txt"), "\n")
	// Claude draws a no-break space after its ❯.
	if !strings.Contains(joined, "❯\u00a0[Pasted text #1]\n") || !strings.Contains(joined, "Skedaddling…") {
		t.Fatalf("the fixture is not a busy pane with a collapsed one-line paste:\n%s", joined)
	}
	if len([]rune(dmiBody)) <= collapsedOneLineRunes || strings.Contains(dmiBody, "\n") {
		t.Fatalf("the body is %d runes with newlines %v: not the measured one-line collapse", len([]rune(dmiBody)), strings.Contains(dmiBody, "\n"))
	}
}

// AC1: the captured busy screen with our collapsed text in the box is seen.
func TestCollapsedOneLine_TheCapturedBusyBoxIsOurs(t *testing.T) {
	p, _, _ := proofPane(t)
	lvxwPaint(p, dmiScreen(t, "haiku312_busy_collapsed_w49.txt"))
	ps := newPendingSubmit(dmiBody, "bracketed", "")
	tail, found := composerTail(p)
	if !submitSighted(p, tail, found, ps) {
		t.Fatalf("our collapsed one-line paste in the busy box was not seen (tail %q)", tail)
	}
}

// AC1 from the real entry point: the send into the captured busy pane, whose
// box shows our placeholder and then Claude's queued line, reads delivered.
// Both fleet captures carry the same box.
func TestCollapsedOneLine_ASendIntoTheBusyPaneIsDelivered(t *testing.T) {
	for _, name := range []string{"haiku312_busy_collapsed_w49.txt", "fleet312_super_busy_w49.txt", "fleet312_pm_busy_w49.txt"} {
		t.Run(name, func(t *testing.T) {
			dmiBound(t)
			f := dmiBusyFake(t, name, dmiBody, "❯ [Pasted text #1]", func(int) bool { return true })
			p, events := pkinSendRig(t, f)
			if v := pkinSend(p, dmiBody); v.Outcome != DeliveryDelivered {
				t.Fatalf("a long one-line send into the busy pane answered %q (%s)", v.Outcome, v.Reason)
			}
			if n := f.enters.Load(); n != 1 {
				t.Errorf("%d Enters for a send taken at the first", n)
			}
			pkinNoEvent(t, events)
		})
	}
}

// AC1, the existing captures keep their verdicts: a 621-character one-line
// body, which read delivered on the rig, still does in the busy pane; a
// two-line body's end in the busy box is still seen (the fake reads any
// newline as an Enter, so this one is read from the screen); and the fleet's
// busy screens, whose boxes are empty, are not our text.
func TestCollapsedOneLine_ShorterSendsStillDelivered(t *testing.T) {
	mid := "[from b] DMIL4 one line of about six hundred: " +
		strings.TrimSuffix(strings.Repeat("all five rigs are back on the release and the timings are in; ", 9), " ") + " marker DMIL4 end."
	dmiBound(t)
	// The box of a 49-column pane caps at a few rows and keeps the end in
	// view (ini-lvxw).
	f := dmiBusyFake(t, "haiku312_busy_collapsed_w49.txt", mid, "❯ "+mid[len(mid)-40:], func(int) bool { return true })
	p, _ := pkinSendRig(t, f)
	if v := pkinSend(p, mid); v.Outcome != DeliveryDelivered {
		t.Fatalf("a 621-character send answered %q (%s)", v.Outcome, v.Reason)
	}
	two := "[from b] DMI02 two lines: first line says the rig moved to the new build and every agent restarted cleanly.\nSecond line: no action, keep looping; marker DMI02 end."
	q, _, _ := proofPane(t)
	lvxwPaint(q, dmiWithBox(t, "haiku312_busy_collapsed_w49.txt", "❯ Second line: no action, keep looping;"))
	tail, found := composerTail(q)
	if !submitSighted(q, tail, found, newPendingSubmit(two, "bracketed", "")) {
		t.Error("the end of a two-line body in the busy box was not seen")
	}
	for _, name := range []string{"fleet312_super_busy_w49.txt", "fleet312_pm_busy_w49.txt"} {
		p, _, _ := proofPane(t)
		lvxwPaint(p, dmiScreen(t, name))
		tail, found := composerTail(p)
		if submitSighted(p, tail, found, newPendingSubmit(dmiBody, "bracketed", "")) {
			t.Errorf("%s: the empty busy box read as our text", name)
		}
	}
}

// AC3: Claude never takes the Enter, the placeholder stays in the box
// through the resend: not delivered, two Enters, never "could not confirm".
func TestCollapsedOneLine_LeftInTheBoxIsNotDelivered(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	f := dmiBusyFake(t, "haiku312_busy_collapsed_w49.txt", dmiBody, "❯ [Pasted text #1]", func(int) bool { return false })
	p, _ := pkinSendRig(t, f)
	v := pkinSend(p, dmiBody)
	if v.Outcome != DeliveryNotDelivered {
		t.Fatalf("our placeholder left in the box answered %q (%s), want not delivered", v.Outcome, v.Reason)
	}
	if n := f.enters.Load(); n != 2 {
		t.Errorf("%d Enters, want the send and one resend", n)
	}
}

var dmiNeverSeen = regexp.MustCompile(`^our text was never seen in the box: (\d+) samples; the box before the Enter showed (.+), after it (.+)$`)

// AC3: a box that never shows our text still reads never seen, and the
// report names the samples and what the box showed before and after.
func TestCollapsedOneLine_NeverSeenNamesItsSamples(t *testing.T) {
	cases := []struct {
		name, body, shows, before, after string
	}{
		// Somebody else's text in the box the whole time.
		{"other text", dmiBody, "❯ lunch order: two of the usual", `"lunch order: two of the usual"`, `"Press up to edit queued messages"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dmiBound(t)
			f := dmiBusyFake(t, "haiku312_busy_collapsed_w49.txt", c.body, c.shows, func(int) bool { return true })
			p, _ := pkinSendRig(t, f)
			v := pkinSend(p, c.body)
			if v.Outcome == DeliveryDelivered {
				t.Fatalf("a send whose text was never in the box read delivered")
			}
			m := dmiNeverSeen.FindStringSubmatch(v.Reason)
			if m == nil {
				t.Fatalf("reason %q is not the never-seen report", v.Reason)
			}
			if m[1] == "0" || m[2] != c.before || m[3] != c.after {
				t.Errorf("samples %s, before %s, after %s; want >0, %s, %s", m[1], m[2], m[3], c.before, c.after)
			}
		})
	}
}

// AC4, feed it nothing: the box stays empty in every sample before the
// Enter (the belt withholds such a send at the entry point, so the sighting
// and the watch are driven directly).
func TestCollapsedOneLine_AnEmptyBoxEverySampleIsNeverSeen(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	p, _, _ := proofPane(t)
	lvxwPaint(p, dmiWithBox(t, "haiku312_busy_collapsed_w49.txt", "❯ "))
	s := startPreEnterSighting(p, newPendingSubmit(dmiBody, "bracketed", ""))
	time.Sleep(3 * composerEchoPoll)
	ps := newPendingSubmit(dmiBody, "bracketed", "")
	ps.seenInBox, ps.census = s.wait(), s.census
	v := confirmAndRetrySubmit(p, ps)
	m := dmiNeverSeen.FindStringSubmatch(v.Reason)
	if v.Outcome != DeliveryUnconfirmed || m == nil || m[2] != "an empty box" || m[3] != "an empty box" {
		t.Fatalf("an empty box in every sample answered %q (%s)", v.Outcome, v.Reason)
	}
}

// What a bare placeholder is NOT: ours only when our body has no newline and
// is longer than Claude's measured one-line limit. A shorter body never
// collapses, so the placeholder is somebody else's paste, and a body with
// newlines collapses with a count.
func TestCollapsedOneLine_ABarePlaceholderIsOursOnlyWhenOursCollapses(t *testing.T) {
	at := func(n int) string { return strings.Repeat("x", n) }
	for _, c := range []struct {
		name string
		body string
		ours bool
	}{
		{"801 runes, one line", at(801), true},
		{"800 runes, one line: shown in full, never collapsed", at(800), false},
		{"a short one-line body", "[from super] ok, carry on", false},
		{"an empty body", "", false},
		// Counted in characters, not bytes: 600 two-byte characters is a
		// body Claude shows in full.
		{"600 accented characters, 1200 bytes", strings.Repeat("é", 600), false},
		{"801 accented characters", strings.Repeat("é", 801), true},
		{"long with one newline: collapses as +1 lines", at(900) + "\n" + at(10), false},
		{"long with a trailing newline", at(900) + "\n", false},
	} {
		p, _, _ := proofPane(t)
		nma5Paint(p, "❯ [Pasted text #3]")
		ps := newPendingSubmit(c.body, "bracketed", "")
		if got := composerHoldsOurBody(p, "[Pasted text #3]", ps); got != c.ours {
			t.Errorf("%s: read as ours %v, want %v", c.name, got, c.ours)
		}
	}
}

// The counted form is unchanged and still exact.
func TestCollapsedOneLine_TheCountedFormIsUnchanged(t *testing.T) {
	p, _, _ := proofPane(t)
	body := longBody(60)
	ps := newPendingSubmit(body, "bracketed", "")
	if !composerHoldsOurBody(p, "[Pasted text #1 +59 lines]", ps) {
		t.Error("our exact count no longer reads as ours")
	}
	if composerHoldsOurBody(p, "[Pasted text #1 +58 lines]", ps) || composerHoldsOurBody(p, "[Pasted text #1]", ps) {
		t.Error("another count, or no count, read as our 60-line paste")
	}
}

// The belt's retry presses Enter on a proven paste: an operator's long
// one-line paste turning up in the box of a withheld short send is not
// proof of ours, so nothing is pressed.
func TestCollapsedOneLine_TheBeltNeverTakesSomebodyElsesBarePaste(t *testing.T) {
	p, _, _ := proofPane(t)
	nma5Paint(p, "❯ [Pasted text #2]")
	ps := newPendingSubmit("[from super] short note for you", "bracketed", "")
	if composerProvesOurPaste(p, "[Pasted text #2]", ps) {
		t.Fatal("a bare placeholder proved a short withheld body was ours")
	}
	ps = newPendingSubmit(dmiBody, "bracketed", "")
	if !composerProvesOurPaste(p, "[Pasted text #2]", ps) {
		t.Fatal("the twin: our long one-line paste repainted as the placeholder was not proven")
	}
}

// The mismatch line for a bare placeholder that is not ours says so in
// words, never "+ lines".
func TestCollapsedOneLine_ABareMismatchReadsInWords(t *testing.T) {
	short := placeholderMismatch("[Pasted text #1]", newPendingSubmit("[from super] short", "bracketed", ""))
	multi := placeholderMismatch("[Pasted text #1]", newPendingSubmit(longBody(5), "bracketed", ""))
	if short != "the box showed a collapsed one-line paste; ours is short enough to show in full" {
		t.Errorf("short body: %q", short)
	}
	if multi != "the box showed a collapsed one-line paste; ours would read +4 lines" {
		t.Errorf("five-line body: %q", multi)
	}
	if got := placeholderMismatch("❯ plain text", newPendingSubmit("x", "bracketed", "")); got != "" {
		t.Errorf("no placeholder: %q", got)
	}
}

// AC4, feed it nothing: the report's own inputs at their edges.
func TestCollapsedOneLine_TheReportWithNothingToRead(t *testing.T) {
	empty, queued := "", "Press up to edit queued messages"
	for _, c := range []struct {
		name string
		c    submitCensus
		want string
	}{
		{"no box before the Enter", submitCensus{before: 10, after: 21, boxAfter: &queued},
			`our text was never seen in the box: 31 samples; the box before the Enter showed no box, after it "Press up to edit queued messages"`},
		{"empty both times", submitCensus{before: 1, after: 1, boxBefore: &empty, boxAfter: &empty},
			"our text was never seen in the box: 2 samples; the box before the Enter showed an empty box, after it an empty box"},
	} {
		if got := unconfirmedReason("", true, false, c.c); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	if got := unconfirmedReason("", false, false, submitCensus{}); got != "no composer visible" {
		t.Errorf("no box on screen at all: %q", got)
	}
	long := strings.Repeat("y", 200)
	got := unconfirmedReason("", true, false, submitCensus{after: 1, boxAfter: &long})
	if !strings.HasSuffix(got, `after it "`+strings.Repeat("y", 57)+`..."`) {
		t.Errorf("a long box line is not bounded like the log's: %q", got)
	}
}

// AC4, a zero-length window: the watch still takes its one sample, so our
// placeholder still in the box reads held, and an empty box reads never
// seen with the sample it took.
func TestCollapsedOneLine_AZeroWindow(t *testing.T) {
	nma5Window(t, 0)
	p, _, _ := proofPane(t)
	lvxwPaint(p, dmiScreen(t, "haiku312_busy_collapsed_w49.txt"))
	if got := confirmSubmit(p, newPendingSubmit(dmiBody, "bracketed", ""), time.Now()); got != submitNotTaken {
		t.Errorf("our placeholder in the box with no window read %v, want not taken", got)
	}
	lvxwPaint(p, dmiWithBox(t, "haiku312_busy_collapsed_w49.txt", "❯ "))
	out, why := watchSubmit(p, newPendingSubmit(dmiBody, "bracketed", ""), time.Now())
	if m := dmiNeverSeen.FindStringSubmatch(why); out != submitUnknown || m == nil || m[1] != "1" {
		t.Errorf("an empty box with no window read %v %q, want unknown after 1 sample", out, why)
	}
}

// The report's "before" line comes from the sampler: a screen with no box
// during the wait reads "no box", not an empty one.
func TestCollapsedOneLine_NoBoxBeforeTheEnterReadsNoBox(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	p, _, _ := proofPane(t)
	lvxwPaint(p, []string{"  Running 1 shell command…", "  ⎿  $ sleep 4"})
	s := startPreEnterSighting(p, newPendingSubmit(dmiBody, "bracketed", ""))
	time.Sleep(3 * composerEchoPoll)
	ps := newPendingSubmit(dmiBody, "bracketed", "")
	ps.seenInBox, ps.census = s.wait(), s.census
	lvxwPaint(p, dmiWithBox(t, "haiku312_busy_collapsed_w49.txt", "❯ "))
	v := confirmAndRetrySubmit(p, ps)
	m := dmiNeverSeen.FindStringSubmatch(v.Reason)
	if m == nil || m[2] != "no box" || m[3] != "an empty box" {
		t.Fatalf("no box during the wait, an empty one after: %q", v.Reason)
	}
}

// The "before" line comes only from a reading that held still: when every
// reading before the Enter is torn mid-read, none of their passing frames is
// reported as what the box showed.
func TestCollapsedOneLine_ATornReadingIsNeverTheBeforeLine(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	p, _, _ := proofPane(t)
	frame := func(n int) []string {
		return dmiWithBox(t, "haiku312_busy_collapsed_w49.txt", "❯ passing frame "+strconv.Itoa(n))
	}
	lvxwPaint(p, frame(0))
	var n atomic.Int32
	old := submitSampleMidpoint
	submitSampleMidpoint = func() { lvxwPaint(p, frame(int(n.Add(1)))) }
	t.Cleanup(func() { submitSampleMidpoint = old })
	s := startPreEnterSighting(p, newPendingSubmit(dmiBody, "bracketed", ""))
	time.Sleep(3 * composerEchoPoll)
	ps := newPendingSubmit(dmiBody, "bracketed", "")
	ps.seenInBox, ps.census = s.wait(), s.census
	submitSampleMidpoint = old
	if ps.census.torn == 0 || ps.census.stable != 0 {
		t.Fatalf("torn %d stable %d: the readings before the Enter were not all torn", ps.census.torn, ps.census.stable)
	}
	lvxwPaint(p, dmiWithBox(t, "haiku312_busy_collapsed_w49.txt", "❯ "))
	v := confirmAndRetrySubmit(p, ps)
	if m := dmiNeverSeen.FindStringSubmatch(v.Reason); m == nil || m[2] != "no box" {
		t.Fatalf("every reading before the Enter torn: %q, want the before line no box", v.Reason)
	}
}
