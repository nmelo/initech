//go:build linux || darwin

package tui

// ini-7rd: a child that stops reading its input wedges its own pane and
// nothing else. Real children, real ptys: the child sets raw mode and never
// reads, and its terminal's input is filled until a write really blocks. Each
// path below runs on the main goroutine in production; the test stands in for
// it with a goroutine and a deadline, because a blocked main loop has no other
// way to report.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/gdamore/tcell/v2"
)

// wedgeBound is what every main-goroutine path must return within.
const wedgeBound = 2 * time.Second

// wedgeChild is a child in raw mode that asks for mouse reports and then never
// reads. In raw mode the terminal cannot discard input, so a writer blocks once
// the terminal's input is full.
const wedgeChild = `stty raw -echo -ixon && printf '\033[?1000hREADY933' && exec sleep 600`

// wedgedPane starts the child and fills its input until a write stops
// returning, so anything written after this blocks in write(2).
func wedgedPane(t *testing.T, script string) (*Pane, func() string) {
	t.Helper()
	p, screen := dyvPane(t, script)
	if !stashWaitFor(screen, "READY933", 5*time.Second) {
		t.Fatalf("the child never got ready. Screen:\n%s", screen())
	}
	ptmx := p.ptmx
	stuck := make(chan struct{})
	go func() {
		defer close(stuck)
		ptmx.Write(make([]byte, 1<<20)) //nolint:errcheck // Ends with the child.
	}()
	select {
	case <-stuck:
		t.Fatal("a 1 MB write into a child that never reads returned: the fixture wedged nothing")
	case <-time.After(300 * time.Millisecond):
	}
	return p, screen
}

// mustReturn runs fn the way the main loop would and fails if it is still
// blocked at the deadline.
func mustReturn(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(wedgeBound):
		t.Errorf("%s did not return within %s with a child that stopped reading: on the main "+
			"goroutine that is a frozen rig", what, wedgeBound)
	}
}

func wedgeKey() *tcell.EventKey { return tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone) }

// TestWedge_MainPathsReturn is AC1's operator half: keys, a paste and a mouse
// event into a wedged pane, with no send anywhere. The first key takes the only
// place the pipe's reader had; the later ones are what froze the loop.
func TestWedge_MainPathsReturn(t *testing.T) {
	for _, c := range []struct {
		name string
		fn   func(p *Pane)
	}{
		{"keys", func(p *Pane) {
			for i := 0; i < 3; i++ {
				p.SendKey(wedgeKey())
			}
		}},
		{"paste markers", func(p *Pane) {
			p.SendPaste(true)
			p.SendPaste(false)
		}},
		{"a paste", func(p *Pane) { p.FlushPaste([]byte("pasted text")) }},
		{"mouse", func(p *Pane) {
			for i := 0; i < 3; i++ {
				p.ForwardMouse(uv.MouseClickEvent{X: 1, Y: 1, Button: uv.MouseLeft})
			}
		}},
	} {
		t.Run(strings.ReplaceAll(c.name, " ", "_"), func(t *testing.T) {
			p, _ := wedgedPane(t, wedgeChild)
			mustReturn(t, c.name, func() { c.fn(p) })
		})
	}
}

// TestWedge_AQueryFromTheChildDoesNotBlockRendering: the emulator answers a
// device-attributes query from inside emu.Write, which readLoop calls holding
// renderMu, so a wedged pipe would hold the renderer with no input at all.
func TestWedge_AQueryFromTheChildDoesNotBlockRendering(t *testing.T) {
	const script = `stty raw -echo -ixon && printf '\033[?1000hREADY933' && sleep 1 && printf '\033[c' && exec sleep 600`
	p, _ := wedgedPane(t, script)
	mustReturn(t, "a key", func() { p.SendKey(wedgeKey()) })
	time.Sleep(1200 * time.Millisecond) // The child's query lands here.
	mustReturn(t, "taking renderMu", func() {
		p.renderMu.Lock()
		p.renderMu.Unlock() //nolint:staticcheck // Only the wait is under test.
	})
}

// TestWedge_ASuspendWithASendInFlightReturns is the incident: a 1 KB send into
// a pane whose child stopped reading, then a suspend on the main goroutine.
func TestWedge_ASuspendWithASendInFlightReturns(t *testing.T) {
	p, _ := wedgedPane(t, wedgeChild)
	sent := make(chan SendVerdict, 1)
	go func() {
		p.sendMu.Lock()
		defer p.sendMu.Unlock()
		sent <- sendPaneTextLocked(p, strings.Repeat("m", 1024), true)
	}()
	time.Sleep(200 * time.Millisecond) // The send is in flight.

	mustReturn(t, "the suspend", func() { parkPaneSuspended(p) })
	if !p.IsSuspended() {
		t.Error("the pane does not read suspended")
	}
	select {
	case v := <-sent:
		t.Logf("the send ended: %+v", v)
	case <-time.After(wedgeBound + time.Second):
		t.Error("the send did not end after the suspend")
	}
}

// resumingChild goes raw, stays away from its input for delay seconds, then
// copies everything it receives into the file named by $1.
const resumingChild = `stty raw -echo -ixon && printf 'READY933' && sleep %d && exec cat > "$1"`

// resumingPane starts that child and returns a reader of the file.
func resumingPane(t *testing.T, delay int) (*Pane, func() string) {
	t.Helper()
	shortSubmitDelays(t)
	out := filepath.Join(t.TempDir(), "received")
	p, screen := stashTestPane(t, fmt.Sprintf(resumingChild, delay), out)
	if !stashWaitFor(screen, "READY933", 5*time.Second) {
		t.Fatalf("the child never got ready. Screen:\n%s", screen())
	}
	return p, func() string {
		b, _ := os.ReadFile(out)
		return string(b)
	}
}

// TestWedge_ABodyThatNeverReachedTheWriterIsHeldAndDeliveredOnce: a write is
// stuck ahead of the send, so none of the body reaches the writer. It is held,
// and goes in once, and only once, when the child reads again.
func TestWedge_ABodyThatNeverReachedTheWriterIsHeldAndDeliveredOnce(t *testing.T) {
	p, received := resumingPane(t, 4)
	p.writeInput(make([]byte, 1<<20)) // Stuck ahead of the send.

	p.sendMu.Lock()
	v := sendPaneTextLocked(p, "held933", true)
	p.sendMu.Unlock()
	if v.Outcome != DeliveryDeferred || v.Reason != StuckInputDeferral {
		t.Fatalf("the send answered %+v, want deferred with the not-reading words, not a dialog's", v)
	}
	if n := p.QueuedMessageCount(); n != 1 {
		t.Fatalf("queue holds %d, want the 1 held message", n)
	}
	if !stashWaitFor(received, "held933", 15*time.Second) {
		t.Fatalf("the held message never arrived after the child read again (queue %d)", p.QueuedMessageCount())
	}
	time.Sleep(time.Second) // A second copy would land here.
	if n := strings.Count(received(), "held933"); n != 1 {
		t.Fatalf("the held message arrived %d times, want once", n)
	}
	if n := p.QueuedMessageCount(); n != 0 {
		t.Fatalf("queue still holds %d", n)
	}
}

// TestWedge_ABodyPartlyWrittenIsNeverResent: the body itself is the stuck
// write. The sender is told, with the risk stated, nothing is held, and when
// the child resumes the bytes land once.
func TestWedge_ABodyPartlyWrittenIsNeverResent(t *testing.T) {
	p, received := resumingPane(t, 4)
	body := "part933" + strings.Repeat("b", 1<<20)

	p.sendMu.Lock()
	v := sendPaneTextLocked(p, body, true)
	p.sendMu.Unlock()
	if v.Outcome != DeliveryUnconfirmed || !strings.Contains(v.Reason, "stopped reading") ||
		!strings.Contains(v.Reason, "not be sent again") {
		t.Fatalf("the send answered %+v, want unconfirmed with the stopped-reading words", v)
	}
	if n := p.QueuedMessageCount(); n != 0 {
		t.Fatalf("queue holds %d: a partly written body must not be held", n)
	}
	if !stashWaitFor(received, "bbbbbbbb\x1b[201~", 15*time.Second) {
		t.Fatalf("the body's bytes never finished landing (%d bytes)", len(received()))
	}
	time.Sleep(time.Second)
	if n := strings.Count(received(), "part933"); n != 1 {
		t.Fatalf("the body arrived %d times, want once", n)
	}
}

// TestWedge_OneLogLineAndOneNoticePerWedge: however much is dropped. When the
// stuck write returns the pane takes input again, with no notice, and a later
// wedge is a new one.
func TestWedge_OneLogLineAndOneNoticePerWedge(t *testing.T) {
	logs := captureLogs(t)
	p, received := resumingPane(t, 3)
	events := make(chan AgentEvent, inputQueueCap*2)
	p.eventCh = events
	p.writeInput(make([]byte, 1<<20))

	dropped := 0
	for i := 0; i < inputQueueCap+50; i++ {
		if !p.writeInput([]byte("k")) {
			dropped++
		}
	}
	if dropped < 49 {
		t.Fatalf("%d of %d writes dropped past a queue of %d", dropped, inputQueueCap+50, inputQueueCap)
	}
	const line = "pane stopped reading its input"
	if n := strings.Count(logs.String(), line); n != 1 {
		t.Fatalf("logged %d times for %d dropped writes, want once", n, dropped)
	}
	if !stashWaitFor(received, strings.Repeat("k", inputQueueCap-5), 10*time.Second) {
		t.Fatalf("the queue did not drain when the child resumed (%d bytes)", len(received()))
	}
	if !p.writeInput([]byte("again933")) || !stashWaitFor(received, "again933", 5*time.Second) {
		t.Fatal("the pane did not take input again after the stuck write returned")
	}
	if !strings.Contains(logs.String(), "keys_and_replies_dropped=") {
		t.Fatal("the wedge's end did not count what was dropped")
	}
	notices := 0
	for len(events) > 0 {
		if ev := <-events; ev.Type == EventAgentStalled {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("%d notices for one wedge and its end, want 1", notices)
	}
}

// TestWedge_AFullQueueNeverStopsThePipeReader is the bound on responseLoop:
// past a full queue the pipe's reader keeps reading and dropping, so a key and
// an emulator reply can never block on it.
func TestWedge_AFullQueueNeverStopsThePipeReader(t *testing.T) {
	p, _ := wedgedPane(t, wedgeChild)
	mustReturn(t, "keys past the queue", func() {
		for i := 0; i < inputQueueCap*3; i++ {
			p.SendKey(wedgeKey())
		}
	})
}

// TestInputWriter_KeysAndPastesKeepTodaysBytes is the positive twin: through
// the writer a child gets the same bytes, in the same order.
func TestInputWriter_KeysAndPastesKeepTodaysBytes(t *testing.T) {
	p, received := stashRecordingPane(t, "raw -echo -ixon")
	stashAwaitMode(t, p, true)
	step := func(want string, fn func()) {
		t.Helper()
		fn()
		if !stashWaitFor(received, want, 5*time.Second) {
			t.Fatalf("got %q, want it to contain %q", received(), want)
		}
	}
	step("x", func() { p.SendKey(wedgeKey()) })
	step("x\x1b[200~", func() { p.SendPaste(true) })
	step("x\x1b[200~\x1b[201~", func() { p.SendPaste(false) })
	want := "x\x1b[200~\x1b[201~\x1b[200~pasted\x1b[201~"
	step(want, func() { p.FlushPaste([]byte("pasted")) })
	want += "\x1b[13;2u"
	step(want, func() { p.SendKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModShift)) })
	if got := received(); got != want {
		t.Fatalf("the child received %q, want exactly %q", got, want)
	}
}

// TestPark_StateIsSynchronousAndTeardownFinishes: AC6. The park reads done when
// it returns; the join finishes on its own, waited on by effect.
func TestPark_StateIsSynchronousAndTeardownFinishes(t *testing.T) {
	p, _ := dyvPane(t, "sleep 600")
	parkPaneSuspended(p)
	if !p.IsSuspended() || p.Activity() != StateSuspended || p.IsAlive() {
		t.Fatalf("after the park: suspended %v, activity %v, alive %v", p.IsSuspended(), p.Activity(), p.IsAlive())
	}
	if !p.awaitTeardown(teardownWaitBound) {
		t.Fatal("the teardown did not finish within its bound with no send in flight")
	}
	if p.hasPTY() {
		t.Fatal("the teardown left the PTY set")
	}
	if p.Activity() != StateSuspended {
		t.Fatalf("the teardown moved the activity to %v", p.Activity())
	}
}

// TestPark_ADescendantHoldingTheTerminalDoesNotBlockTheMainGoroutine: killing
// the child leaves the terminal open in its background job, so the teardown can
// wait; the park must not. The job ends itself.
func TestPark_ADescendantHoldingTheTerminalDoesNotBlockTheMainGoroutine(t *testing.T) {
	p, screen := dyvPane(t, "sleep 6 & echo READY933; exec sleep 600")
	if !stashWaitFor(screen, "READY933", 5*time.Second) {
		t.Fatalf("the child never got ready. Screen:\n%s", screen())
	}
	mustReturn(t, "the park", func() { parkPaneSuspended(p) })
	if !p.IsSuspended() {
		t.Fatal("the pane does not read suspended")
	}
}

// TestPark_TwiceAndOnAPaneWithNothingIsSafe feeds the close nothing: a second
// park, a pane with no PTY, and a key into a suspended pane.
func TestPark_TwiceAndOnAPaneWithNothingIsSafe(t *testing.T) {
	p, _ := dyvPane(t, "sleep 600")
	parkPaneSuspended(p)
	mustReturn(t, "a second park", func() { parkPaneSuspended(p) })
	mustReturn(t, "a key into a suspended pane", func() { p.SendKey(wedgeKey()) })

	bare := &Pane{name: "bare"}
	mustReturn(t, "a park of a pane with no PTY", func() { parkPaneSuspended(bare) })
	if bare.writeInput([]byte("x")) {
		t.Fatal("a closed pane accepted input")
	}
	if bare.writeInput(nil) != true {
		t.Fatal("an empty write is nothing to drop")
	}
}

// TestRace_ASendRacingAParkAndAKeyRacingAClose is the -race proof: the send
// reads the PTY and the emulator while the teardown clears them.
func TestRace_ASendRacingAParkAndAKeyRacingAClose(t *testing.T) {
	for i := 0; i < 24; i++ {
		p, _ := dyvPane(t, "stty raw -echo -ixon && exec cat > /dev/null")
		time.Sleep(20 * time.Millisecond)
		stop := make(chan struct{})
		var pump sync.WaitGroup
		pump.Add(1)
		go func() { // Keeps the writer reading the PTY field through the teardown.
			defer pump.Done()
			for {
				select {
				case <-stop:
					return
				default:
					p.writeInput([]byte("k"))
				}
			}
		}()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			p.sendMu.Lock()
			defer p.sendMu.Unlock()
			sendPaneTextLocked(p, "racing", true)
		}()
		go func() {
			defer wg.Done()
			p.SendKey(wedgeKey())
		}()
		if i%2 == 0 {
			parkPaneSuspended(p)
		} else {
			p.Close()
		}
		wg.Wait()
		close(stop)
		pump.Wait()
		if !p.awaitTeardown(teardownWaitBound) {
			t.Fatal("the teardown did not finish")
		}
	}
}

// gatePty is a pty whose writes block until the gate opens, and record what
// reached it: a stuck write the test can release.
type gatePty struct {
	filePty
	gate    chan struct{}
	started chan struct{}
	mu      sync.Mutex
	got     strings.Builder
}

func newGatePty() *gatePty {
	return &gatePty{gate: make(chan struct{}), started: make(chan struct{}, 8)}
}

func (g *gatePty) Write(b []byte) (int, error) {
	select {
	case g.started <- struct{}{}:
	default:
	}
	<-g.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	g.got.Write(b)
	return len(b), nil
}

func (g *gatePty) Close() error { return nil }

func (g *gatePty) String() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.got.String()
}

// TestWedge_NothingQueuedIsWrittenAfterTheClose: a write in progress cannot be
// recalled, but what waits behind it is dropped at the close.
func TestWedge_NothingQueuedIsWrittenAfterTheClose(t *testing.T) {
	g := newGatePty()
	p := &Pane{name: "eng1", ptmx: g}
	p.writeInput([]byte("first"))
	<-g.started
	p.writeInput([]byte("second"))
	p.markClosing()
	close(g.gate)
	if !p.awaitInputDrained(2 * time.Second) {
		t.Fatal("the input did not drain")
	}
	if got := g.String(); got != "first" {
		t.Fatalf("the child received %q, want only the write in progress at the close", got)
	}
}

// TestWedge_NothingGoesInWhileAWriteIsStuck: once a send has run out its bound,
// the next one does not wait 2 s beside the stuck write and cannot interleave
// with it: it is held at once.
func TestWedge_NothingGoesInWhileAWriteIsStuck(t *testing.T) {
	p, _ := resumingPane(t, 30)
	p.writeInput(make([]byte, 1<<20))
	if !func() bool {
		p.sendMu.Lock()
		defer p.sendMu.Unlock()
		return sendPaneTextLocked(p, "one933", true).Outcome == DeliveryDeferred
	}() {
		t.Fatal("the first send was not held")
	}
	start := time.Now()
	p.sendMu.Lock()
	v := sendPaneTextLocked(p, "two933", true)
	p.sendMu.Unlock()
	if v.Outcome != DeliveryDeferred {
		t.Fatalf("the second send answered %+v, want held", v)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("the second send took %s: it went at the stuck pane instead of being held", took)
	}
	if n := p.QueuedMessageCount(); n != 2 {
		t.Fatalf("queue holds %d, want both messages", n)
	}
}

// TestWedge_ASendOfNothingAndASendToAClosedPane feeds the send path nothing.
func TestWedge_ASendOfNothingAndASendToAClosedPane(t *testing.T) {
	p, _ := dyvPane(t, "sleep 600")
	time.Sleep(50 * time.Millisecond)
	p.sendMu.Lock()
	empty := sendPaneTextLocked(p, "", false)
	p.sendMu.Unlock()
	if empty.Outcome != DeliveryDelivered {
		t.Fatalf("an empty body answered %+v", empty)
	}
	p.Close()
	p.sendMu.Lock()
	closed := sendPaneTextLocked(p, "late", true)
	p.sendMu.Unlock()
	if closed.Outcome != DeliveryNotDelivered {
		t.Fatalf("a send to a closed pane answered %+v, want not delivered", closed)
	}
}

// TestWedge_ACloseEndsASendAtOnce: a write that stays stuck past the close (a
// descendant still holds the terminal) must not cost the sender its full bound.
func TestWedge_ACloseEndsASendAtOnce(t *testing.T) {
	g := newGatePty()
	t.Cleanup(func() { close(g.gate) })
	p := &Pane{name: "eng1", ptmx: g}
	ended := make(chan childWrite, 1)
	go func() {
		wrote, _ := p.writeInputWait([]byte("body"))
		ended <- wrote
	}()
	<-g.started
	p.markClosing()
	select {
	case wrote := <-ended:
		if wrote != childPartial {
			t.Fatalf("the send read %d, want partial: the body reached the stuck write", wrote)
		}
	case <-time.After(sendWriteBound / 4):
		t.Fatal("the send kept waiting after the close")
	}
}

// TestPark_TheBusyGraceRunsOffTheMainGoroutine: a running agent gets SIGTERM and
// two seconds. A child that ignores it makes that wait real, and the main
// goroutine must not be the one waiting.
func TestPark_TheBusyGraceRunsOffTheMainGoroutine(t *testing.T) {
	p, screen := dyvPane(t, `trap '' TERM; echo READY933; while :; do sleep 1; done`)
	if !stashWaitFor(screen, "READY933", 5*time.Second) {
		t.Fatalf("the child never got ready. Screen:\n%s", screen())
	}
	p.mu.Lock()
	p.activity = StateRunning
	p.mu.Unlock()

	start := time.Now()
	parkPaneSuspended(p)
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("the park held its caller %s: the grace ran on the main goroutine", took)
	}
	if !p.awaitTeardown(teardownWaitBound) {
		t.Fatal("the teardown did not finish within its bound")
	}
}

// TestRace_TheDrainAndTheTeardownShareThePTYFieldSafely runs the two head on,
// with no other lock between them: only -race can fail this, and it does if the
// drain reads the field without the state lock.
func TestRace_TheDrainAndTheTeardownShareThePTYFieldSafely(t *testing.T) {
	sink, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	for i := 0; i < 200; i++ {
		p := &Pane{name: "eng1", ptmx: &filePty{sink}}
		done := make(chan struct{})
		go func() {
			defer close(done)
			p.writeChild([]byte("x")) //nolint:errcheck // Either end is fine.
		}()
		p.releaseOnce.Do(p.releaseTerminal)
		<-done
	}
}
