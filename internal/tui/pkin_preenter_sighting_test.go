package tui

import (
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// ini-pkin: the sighting of our body is taken during the wait before the
// Enter and carried into the watch after it, so a busy agent whose every
// post-Enter frame is mid-repaint no longer ends "could not confirm". These
// cells drive the whole send (sendPaneTextLocked) against a fake Claude on
// the far side of the PTY.

// pkinWrite is one read of what the send path wrote to the PTY.
type pkinWrite struct {
	at time.Time
	b  string
}

// pkinFake stands in for Claude: it reads the PTY, paints our body into the
// box when the paste ends, and on each Enter clears the box (or not) the way
// its test says.
type pkinFake struct {
	p       *Pane
	body    string
	shows   string                       // the box once the paste ends; "" is "❯ " + body
	after   string                       // the box after a taken Enter
	takes   func(n int) bool             // whether Enter number n is taken
	busy    bool                         // tear every post-Enter sample while the box holds our text
	screen  func(composer string) string // the whole screen around the box; nil draws the box alone at the top
	mu      sync.Mutex
	writes  []pkinWrite
	samples atomic.Int32 // samples through the hook (busy only)
	torn    atomic.Bool  // a sample after the sighter's was torn
	taken   atomic.Bool  // an Enter was taken
	cleared atomic.Bool
	clear   sync.Once
	enters  atomic.Int32
	frame   atomic.Int32
}

func (f *pkinFake) paint(composer string) {
	if f.screen != nil {
		f.p.emu.Write([]byte(f.screen(composer)))
		return
	}
	f.p.emu.Write([]byte("\x1b[2J\x1b[H" + strings.Repeat("─", 40) + "\r\n" + composer + "\r\n" + strings.Repeat("─", 40)))
}

func (f *pkinFake) saw(b string) {
	f.mu.Lock()
	f.writes = append(f.writes, pkinWrite{time.Now(), b})
	f.mu.Unlock()
	if strings.Contains(b, "\x1b[201~") {
		if f.shows != "" {
			f.paint(f.shows)
		} else {
			f.paint("❯ " + f.body)
		}
	}
	if strings.ContainsAny(b, "\r\n") {
		n := int(f.enters.Add(1))
		if f.takes(n) {
			if f.busy {
				f.taken.Store(true)
				f.maybeClear()
				return
			}
			// Claude takes an Enter within one sample (869 of 1,088 measured
			// confirmations landed at 50-99 ms).
			go func() {
				time.Sleep(20 * time.Millisecond)
				f.paint(f.after)
			}()
		}
	}
}

// maybeClear clears a busy agent's box 20 ms after it has both taken an
// Enter and had a sample torn, whichever came second. Never on a timer of
// its own: on a loaded host a timer can clear the box before any sample ran,
// and the fixture would no longer be a busy agent.
func (f *pkinFake) maybeClear() {
	if !f.taken.Load() || !f.torn.Load() {
		return
	}
	f.clear.Do(func() {
		go func() {
			time.Sleep(20 * time.Millisecond)
			f.paint(f.after)
			f.cleared.Store(true)
		}()
	})
}

func (f *pkinFake) stream() (all string, bodyAt, enterAt time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, w := range f.writes {
		all += w.b
		if strings.Contains(w.b, "\x1b[201~") {
			bodyAt = w.at
		}
		if strings.ContainsAny(w.b, "\r\n") && enterAt.IsZero() {
			enterAt = w.at
		}
	}
	return all, bodyAt, enterAt
}

// pkinSendRig builds a pane whose PTY is read by a fake Claude, with the
// emulator's own replies (the stash key, the Enter) forwarded to the PTY the
// way responseLoop does.
func pkinSendRig(t *testing.T, f *pkinFake) (*Pane, <-chan AgentEvent) {
	t.Helper()
	p, r := paneWithPipe(t)
	p.name = "eng2"
	p.alive = true
	p.emu = vt.NewSafeEmulator(120, 40)
	events := make(chan AgentEvent, 8)
	p.eventCh = events
	p.claudeVersion = measuredOnlyVersionProbe()
	f.p = p
	f.paint("❯ ")

	if f.busy {
		old := submitSampleMidpoint
		submitSampleMidpoint = func() {
			// The first sample is the sighter's, during the wait: the one
			// stable frame a busy agent shows. Every later sample, which is
			// the watch after the Enter, is torn while the box holds our
			// text. Counting samples, not reading when the fake saw the
			// Enter, keeps that true under any scheduling. The repaint lands
			// on the box's bottom border: only the composer rows tear a
			// sample (ini-s1kd), so a spinner elsewhere would leave these
			// samples whole and the carried sighting untested.
			if f.samples.Add(1) == 1 || f.cleared.Load() {
				return
			}
			p.emu.Write([]byte("\x1b[3;45H✻ Working " + strconv.Itoa(int(f.frame.Add(1)))))
			f.torn.Store(true)
			f.maybeClear()
		}
		t.Cleanup(func() { submitSampleMidpoint = old })
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := p.emu.Read(buf)
			if n > 0 {
				_, _ = p.ptmx.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				f.saw(string(buf[:n]))
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		if w, ok := p.emu.InputPipe().(*io.PipeWriter); ok {
			w.CloseWithError(io.EOF)
		}
		_ = p.ptmx.Close()
		_ = r.Close()
		wg.Wait()
	})
	return p, events
}

func pkinSend(p *Pane, text string) SendVerdict {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	return sendPaneTextLocked(p, text, true)
}

// pkinBusyBound is the confirm window for the busy-agent cells: a bound, not
// a clock (ini-jyh3). The watch returns the moment the box clears after the
// torn sample, so a passing run never reaches it; a 300 ms window here was
// the clock a loaded CI host ran out before the fake's clear landed, and the
// cell read "could not confirm" for a send that was taken.
const pkinBusyBound = 5 * time.Second

// pkinDeliveredByEffect asserts what the send itself answered, delivered,
// and that the fixture really was the busy agent: a sample after the
// sighter's was torn and the box then cleared. The verdict is the effect;
// no window of silence afterwards stands in for it.
func pkinDeliveredByEffect(t *testing.T, f *pkinFake, v SendVerdict) {
	t.Helper()
	if v.Outcome != DeliveryDelivered {
		t.Fatalf("the send answered %q (%s) for a message the fake took", v.Outcome, v.Reason)
	}
	if !f.torn.Load() || !f.cleared.Load() {
		t.Fatalf("torn %v, cleared %v: the fixture did not produce the busy agent", f.torn.Load(), f.cleared.Load())
	}
}

func pkinNoEvent(t *testing.T, events <-chan AgentEvent) {
	t.Helper()
	select {
	case ev := <-events:
		t.Fatalf("reported %q for a message the fake took", ev.Detail)
	case <-time.After(100 * time.Millisecond):
	}
}

// Criterion 1: delivery unchanged. The PTY sees the stash key, the bracketed
// body, then ONE Enter, and the Enter goes out after the 500 ms wait, the
// same as before the sighting existed. Holds at 4af6701 and after.
func TestPkin_DeliveryBytesAndEnterMomentAreUnchanged(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	f := &pkinFake{body: nma5Body, after: "❯ ", takes: func(int) bool { return true }, busy: true}
	p, _ := pkinSendRig(t, f)
	pkinSend(p, nma5Body)

	all, bodyAt, enterAt := f.stream()
	if want := "\x13\x1b[200~" + nma5Body + "\x1b[201~\r"; all != want {
		t.Fatalf("PTY bytes %q, want %q", all, want)
	}
	gap := enterAt.Sub(bodyAt)
	if gap < bracketedPasteSubmitDelay || gap > bracketedPasteSubmitDelay+250*time.Millisecond {
		t.Fatalf("Enter went out %s after the body, want the %s wait (plus the belt's first poll)", gap, bracketedPasteSubmitDelay)
	}
}

// Criterion 2, from the real entry point: a busy agent tears every sample
// after the Enter while our text is in the box, then clears it. The body
// was in plain view for the whole wait before the Enter; that sighting
// completes the transition. Before ini-pkin this ended "could not confirm".
func TestPkin_ABusyAgentThatTakesTheEnterIsConfirmed(t *testing.T) {
	nma5Window(t, pkinBusyBound)
	f := &pkinFake{body: nma5Body, after: "❯ ", takes: func(int) bool { return true }, busy: true}
	p, _ := pkinSendRig(t, f)
	pkinDeliveredByEffect(t, f, pkinSend(p, nma5Body))
	if n := f.enters.Load(); n != 1 {
		t.Fatalf("sent %d Enters, want 1", n)
	}
	if f.frame.Load() == 0 {
		t.Fatal("no post-Enter sample was torn: the fixture did not produce the busy agent")
	}
}

// Criterion 2: Claude's queued-message line in the box is delivery.
func TestPkin_ABusyAgentThatQueuesTheMessageIsConfirmed(t *testing.T) {
	nma5Window(t, pkinBusyBound)
	f := &pkinFake{body: nma5Body, after: "❯ Press up to edit queued messages", takes: func(int) bool { return true }, busy: true}
	p, _ := pkinSendRig(t, f)
	pkinDeliveredByEffect(t, f, pkinSend(p, nma5Body))
}

// Criterion 3: a box that keeps our text after the Enter still gets one
// resend and then the NOT delivered report, from the real entry point.
func TestPkin_TextLeftInTheBoxIsStillResentOnceThenNotDelivered(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	f := &pkinFake{body: nma5Body, after: "❯ ", takes: func(int) bool { return false }}
	p, events := pkinSendRig(t, f)
	pkinSend(p, nma5Body)
	select {
	case ev := <-events:
		if !strings.HasPrefix(ev.Detail, undeliveredSubmitPrefix) || !strings.Contains(ev.Detail, "Enter not taken twice") {
			t.Fatalf("reported %q, want NOT delivered", ev.Detail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a message left in the box was never reported")
	}
	if n := f.enters.Load(); n != 2 {
		t.Fatalf("sent %d Enters, want 2 (the Enter and one resend)", n)
	}
}

// Criterion 4, from the real entry point: the box changed when the paste
// ended (so the belt let the Enter go) but never showed our text. That is
// not a sighting, so the cleared box after the Enter stays unconfirmed.
func TestPkin_ABoxThatOnlyChangedIsNeverConfirmed(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	f := &pkinFake{body: nma5Body, shows: "❯ lunch?", after: "❯ ", takes: func(int) bool { return true }}
	p, events := pkinSendRig(t, f)
	pkinSend(p, nma5Body)
	select {
	case ev := <-events:
		if !strings.HasPrefix(ev.Detail, unconfirmedSubmitPrefix) || !strings.Contains(ev.Detail, "never seen") {
			t.Fatalf("reported %q, want could not confirm, never seen", ev.Detail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a box that never showed our text was passed over in silence")
	}
	if n := f.enters.Load(); n != 1 {
		t.Fatalf("sent %d Enters, want 1 (unknown presses nothing)", n)
	}
}

// ── the sighting and the carried watch, cell by cell ─────────────────

func pkinSight(t *testing.T, screen, body string) bool {
	t.Helper()
	p, _, _ := proofPane(t)
	nma5Paint(p, screen)
	s := startPreEnterSighting(p, newPendingSubmit(body, "bracketed", ""))
	time.Sleep(3 * composerEchoPoll)
	return s.wait()
}

func TestPkinSighting_FiresOnOurBodyInTheBox(t *testing.T) {
	if !pkinSight(t, "❯ "+nma5Body, nma5Body) {
		t.Fatal("our body in plain view during the wait: not sighted")
	}
	if !pkinSight(t, "❯ ok?", "ok?") {
		t.Fatal("a short body whole in the box: not sighted")
	}
	if !pkinSight(t, "❯ [Pasted text #1 +59 lines]", longBody(60)) {
		t.Fatal("Claude's placeholder with our exact count: not sighted")
	}
}

// Criterion 4: only our text is a sighting. A box that changed to other
// text, a placeholder of another count, a picker, an empty body and a
// screen with no composer all leave it unseen.
func TestPkinSighting_NeverFiresWithoutOurText(t *testing.T) {
	for name, c := range map[string]struct{ screen, body string }{
		"box changed to other text":    {"❯ half-typed note from the operator about lunch", nma5Body},
		"placeholder of another count": {"❯ [Pasted text #1 +12 lines]", longBody(60)},
		"picker swallowed the paste":   {"Do you want to proceed?\r\n❯ 1. Yes\r\n  2. No", nma5Body},
		"empty body":                   {"❯ ", ""},
		"no composer":                  {"Loading conversation...", nma5Body},
	} {
		if pkinSight(t, c.screen, c.body) {
			t.Errorf("%s: sighted", name)
		}
	}
}

// Criterion 2: with the body seen before the Enter, the first stable sample
// showing a composer without it confirms: an empty box, Claude's queued
// line, or the operator's restored draft.
func TestPkinWatch_ACarriedSightingConfirmsOnTheFirstClearedSample(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	for name, box := range map[string]string{
		"empty box":      "❯ ",
		"queued line":    "❯ Press up to edit queued messages",
		"restored draft": "❯ half-typed note from the operator about lunch",
	} {
		p, _, _ := proofPane(t)
		nma5Paint(p, box)
		ps := newPendingSubmit(nma5Body, "bracketed", "")
		ps.seenInBox = true
		start := time.Now()
		if got := confirmSubmit(p, ps, start); got != submitTaken {
			t.Errorf("%s: outcome %d, want taken", name, got)
		}
		if el := time.Since(start); el >= composerEchoPoll {
			t.Errorf("%s: confirmed after %s, want the first sample", name, el)
		}
		// The twin: the same box with no sighting stays unknown.
		if got := confirmSubmit(p, newPendingSubmit(nma5Body, "bracketed", ""), time.Now()); got != submitUnknown {
			t.Errorf("%s, never seen: outcome %d, want unknown", name, got)
		}
	}
}

// Criterion 3 at the cell: a carried sighting never overrides a box that
// still holds our body, and no composer at all stays unknown.
func TestPkinWatch_ACarriedSightingNeverConfirmsAHeldOrMissingBox(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	nma5Paint(p, "❯ "+nma5Body)
	ps := newPendingSubmit(nma5Body, "bracketed", "")
	ps.seenInBox = true
	if got := confirmSubmit(p, ps, time.Now()); got != submitNotTaken {
		t.Fatalf("our body still in the box: outcome %d, want not taken", got)
	}
	nma5Paint(p, "Loading conversation...")
	if got := confirmSubmit(p, ps, time.Now()); got != submitUnknown {
		t.Fatalf("no composer: outcome %d, want unknown", got)
	}
}

// A dialog that opened during the wait takes the Enter, and our body sits
// under it, still unsent. The picker's ❯ row is not a composer: a carried
// sighting must not read it as "the box cleared".
func TestPkinWatch_ADialogAfterTheSightingIsNotTheBoxClearing(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	p.emu.Write([]byte("\x1b[2J\x1b[20;1HDo you want to proceed?\r\n❯ 1. Yes\r\n  2. No"))
	ps := newPendingSubmit(nma5Body, "bracketed", "")
	ps.seenInBox = true
	if got := confirmSubmit(p, ps, time.Now()); got == submitTaken {
		t.Fatal("a permission dialog on screen after the sighting: confirmed as taken")
	}
}
