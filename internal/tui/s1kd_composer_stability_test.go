package tui

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ini-s1kd: a sample is whole when the composer rows it reads held still,
// not the whole screen. A busy agent repaints its answer, its spinner and its
// status row between any two reads, so the whole-screen rule tore every
// sample of a streaming pane, before the Enter as well as after, and the
// sighting never fired: 95 of 100 "could not confirm" in one loaded morning
// read "our text was never seen in the box". These cells draw Claude's
// layout (the answer at the top, the box pinned at the bottom between its
// rules, a hint row under it) and stream into everything but the box.

var s1kdRule = strings.Repeat("─", 60)

// s1kdScreen is a Claude-shaped screen around the given composer row.
func s1kdScreen(composer string) string {
	return "\x1b[2J\x1b[H● Here is the long answer, streaming in" +
		"\x1b[36;1H✻ Working" +
		"\x1b[37;1H" + s1kdRule +
		"\x1b[38;1H" + composer +
		"\x1b[39;1H" + s1kdRule +
		"\x1b[40;1H  ⏵⏵ bypass permissions on"
}

// s1kdStream lands a repaint inside every sample, between its ❯-row read and
// its box read, on the answer, the spinner and the hint row: everything on
// screen except the composer rows. It returns how many samples it streamed
// through.
func s1kdStream(t *testing.T, p *Pane, also func(k int)) *atomic.Int32 {
	t.Helper()
	n := &atomic.Int32{}
	old := submitSampleMidpoint
	submitSampleMidpoint = func() {
		k := int(n.Add(1))
		p.emu.Write([]byte("\x1b[" + strconv.Itoa(2+k%30) + ";1H\x1b[2Kstreamed token " + strconv.Itoa(k) +
			"\x1b[36;1H\x1b[2K✻ Working " + strconv.Itoa(k) +
			"\x1b[40;1H\x1b[2K  ⏵⏵ bypass permissions on · " + strconv.Itoa(k)))
		if also != nil {
			also(k)
		}
	}
	t.Cleanup(func() { submitSampleMidpoint = old })
	return n
}

// s1kdLog points the logger at a temp root and returns a reader for its file.
func s1kdLog(t *testing.T) func() string {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(InitLogger(root, slog.LevelInfo))
	return func() string {
		b, _ := os.ReadFile(filepath.Join(root, ".initech", logFileName))
		return string(b)
	}
}

// s1kdCensus reads the census fields off the one UNKNOWN line in the log.
func s1kdCensus(t *testing.T, log string) map[string]int {
	t.Helper()
	var line string
	for _, l := range strings.Split(log, "\n") {
		if strings.Contains(l, "submit outcome UNKNOWN") {
			if line != "" {
				t.Fatalf("two UNKNOWN lines for one send:\n%s", log)
			}
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no UNKNOWN line:\n%s", log)
	}
	got := map[string]int{}
	for _, k := range []string{"samples_before", "samples_after", "torn", "stable", "sighted"} {
		m := regexp.MustCompile(`\b` + k + `=(\d+)`).FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("the UNKNOWN line has no %s field: %s", k, line)
		}
		got[k], _ = strconv.Atoi(m[1])
	}
	return got
}

// Criterion 2, from the real entry point: an agent streaming a long answer
// while our text sits in its box. The box held still through the wait, so
// the body is sighted before the Enter, and the box clearing confirms it.
// Before ini-s1kd every sample tore and this ended "could not confirm".
func TestS1kd_AStreamingAgentIsConfirmed(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	f := &pkinFake{body: nma5Body, after: "❯ ", takes: func(int) bool { return true }, screen: s1kdScreen}
	p, events := pkinSendRig(t, f)
	n := s1kdStream(t, p, nil)
	pkinSend(p, nma5Body)
	pkinNoEvent(t, events)
	if n.Load() == 0 {
		t.Fatal("fixture: no sample ran through the stream")
	}
	if e := f.enters.Load(); e != 1 {
		t.Fatalf("sent %d Enters, want 1", e)
	}
}

// Criterion 2 at the sighter: our body in a box that holds still is sighted
// during the wait however fast the answer above it streams.
func TestS1kdSighting_FiresWhileTheAnswerStreams(t *testing.T) {
	p, _, _ := proofPane(t)
	p.emu.Write([]byte(s1kdScreen("❯ " + nma5Body)))
	s1kdStream(t, p, nil)
	s := startPreEnterSighting(p, newPendingSubmit(nma5Body, "bracketed", ""))
	time.Sleep(3 * composerEchoPoll)
	if !s.wait() {
		t.Fatal("our body in a still box under a streaming answer: not sighted")
	}
}

// Criterion 2 at the watch: with the sighting carried, the first sample of
// a cleared box confirms while the answer goes on streaming.
func TestS1kdWatch_ACarriedSightingConfirmsOnTheFirstClearedSampleWhileStreaming(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	p, _, _ := proofPane(t)
	p.emu.Write([]byte(s1kdScreen("❯ ")))
	s1kdStream(t, p, nil)
	ps := newPendingSubmit(nma5Body, "bracketed", "")
	ps.seenInBox = true
	start := time.Now()
	if got := confirmSubmit(p, ps, start); got != submitTaken {
		t.Fatalf("a cleared box under a streaming answer: outcome %d, want taken", got)
	}
	if el := time.Since(start); el >= composerEchoPoll {
		t.Fatalf("confirmed after %s, want the first sample", el)
	}
}

// Criterion 3: the real tear is still a tear. The first sample's repaint
// lands IN the composer rows, between its ❯-row read and its box read: the
// sample is stitched from a frame with our body and one with no composer.
// Accepted, its "a composer without our body" would confirm a send whose
// text is still in the box; the second sample's repaint puts it back, and
// every later sample shows it held.
func TestS1kd_ARepaintOfTheComposerRowsIsStillATear(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	p.emu.Write([]byte(s1kdScreen("❯ " + nma5Body)))
	n := s1kdStream(t, p, func(k int) {
		switch k {
		case 1:
			p.emu.Write([]byte("\x1b[38;1H\x1b[2KLoading conversation..."))
		case 2:
			p.emu.Write([]byte("\x1b[38;1H\x1b[2K❯ " + nma5Body))
		}
	})
	ps := newPendingSubmit(nma5Body, "bracketed", "")
	ps.seenInBox = true
	if got := confirmSubmit(p, ps, time.Now()); got != submitNotTaken {
		t.Fatalf("a sample torn across a repaint of the box: outcome %d, want not taken (our body is still there)", got)
	}
	if n.Load() < 3 {
		t.Fatalf("only %d samples ran; the torn ones must be followed by whole ones", n.Load())
	}
}

// Criterion 1: the census on the UNKNOWN line, from the real entry point.
// Every sample, before the Enter and after, lands a repaint on the box's
// bottom border, so every one tears and none can sight our text.
func TestS1kdCensus_EverySampleTornIsCountedTorn(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	log := s1kdLog(t)
	f := &pkinFake{body: nma5Body, after: "❯ ", takes: func(int) bool { return true }, screen: s1kdScreen}
	p, events := pkinSendRig(t, f)
	s1kdStream(t, p, func(k int) {
		p.emu.Write([]byte("\x1b[39;1H\x1b[2K" + s1kdRule + " " + strconv.Itoa(k)))
	})
	pkinSend(p, nma5Body)
	select {
	case ev := <-events:
		if !strings.HasPrefix(ev.Detail, unconfirmedSubmitPrefix) {
			t.Fatalf("reported %q, want could not confirm", ev.Detail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a send whose every sample tore was not reported")
	}
	c := s1kdCensus(t, log())
	if c["samples_before"] == 0 || c["samples_after"] == 0 {
		t.Fatalf("census %v: want samples both before and after the Enter", c)
	}
	if c["torn"] != c["samples_before"]+c["samples_after"] || c["stable"] != 0 || c["sighted"] != 0 {
		t.Fatalf("census %v: want every sample torn, none stable, none sighted", c)
	}
}

// Criterion 1, feed it nothing: no composer anywhere. Every sample is whole
// (nothing moved) and none sighted our text.
func TestS1kdCensus_NoComposerIsStableAndNeverSighted(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	log := s1kdLog(t)
	p, _, events := proofPane(t)
	nma5Paint(p, "Loading conversation...")
	confirmAndRetrySubmit(p, newPendingSubmit(nma5Body, "bracketed", ""))
	if ev, got := nma5Undelivered(events, time.Second); !got || !strings.Contains(ev.Detail, "no composer visible") {
		t.Fatalf("reported %q (got=%v), want could not confirm, no composer visible", ev.Detail, got)
	}
	c := s1kdCensus(t, log())
	if c["samples_before"] != 0 || c["samples_after"] == 0 {
		t.Fatalf("census %v: want no samples before (no sighter ran) and some after", c)
	}
	if c["stable"] != c["samples_after"] || c["torn"] != 0 || c["sighted"] != 0 {
		t.Fatalf("census %v: want every sample stable, none torn, none sighted", c)
	}
}

// composerSpan is what a sample is compared on: the box and where it sits.
func TestS1kdComposerSpan_IsThePromptRowThroughTheBottomRule(t *testing.T) {
	rows := []string{"● answer", "✻ Working", s1kdRule, "❯ hello", "  world", s1kdRule, "  ⏵⏵ hint"}
	top, span := composerSpan(rows)
	if top != 3 || strings.Join(span, "|") != "❯ hello|  world|"+s1kdRule {
		t.Fatalf("got top %d span %q, want the prompt row through the bottom rule", top, span)
	}
	// The prompt row scrolled off: the rows between the last two rules.
	rows = []string{"wrapped body line one", "wrapped body line two", s1kdRule, "  ⏵⏵ hint"}
	if top, span = composerSpan(rows); top != 0 || len(span) != 3 {
		t.Fatalf("scrolled-off prompt: got top %d span %q, want rows 0-2", top, span)
	}
	// Neither: nothing to compare.
	if top, span = composerSpan([]string{"Loading conversation...", ""}); top != -1 || span != nil {
		t.Fatalf("no composer: got top %d span %q, want none", top, span)
	}
}
