package tui

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"golang.org/x/term"
)

// submitWatcher drains the emulator's host-bound stream and counts Enter keys,
// which is how a submit is observable without a child (the pattern
// TestInjectText_StashSkipsRetry established).
type submitWatcher struct {
	mu     sync.Mutex
	enters int
}

func (w *submitWatcher) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enters
}

func watchSubmits(emu *vt.SafeEmulator) *submitWatcher {
	w := &submitWatcher{}
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				w.mu.Lock()
				for _, b := range buf[:n] {
					if b == '\r' {
						w.enters++
					}
				}
				w.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return w
}

// retryPane builds a pane with a real emulator and a buffered event channel.
func retryPane(t *testing.T) (*Pane, *vt.SafeEmulator, *submitWatcher, chan AgentEvent) {
	t.Helper()
	emu := vt.NewSafeEmulator(80, 24)
	ch := make(chan AgentEvent, 16)
	p := &Pane{name: "eng1", emu: emu, eventCh: ch}
	return p, emu, watchSubmits(emu), ch
}

func drainRetryEvents(ch chan AgentEvent) []AgentEvent {
	var out []AgentEvent
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

// composerShowing paints a composer row with the given content.
func composerShowing(emu *vt.SafeEmulator, content string) {
	_, _ = emu.Write([]byte("\x1b[2J\x1b[24;1H❯ " + content))
}

// The AC's first case: the message ARRIVES once the composer repaints holding
// our text. This is the whole point of the fix -- before it, a withheld submit
// was the end of the message.
func TestWithheldSubmit_DeliversOnceTheComposerRepaintsHoldingOurText(t *testing.T) {
	p, emu, w, ch := retryPane(t)
	captureLogs(t)

	composerShowing(emu, "") // busy agent: composer present, body not rendered
	withholdSubmit(p, "deploy the thing", "bracketed")
	if p.pendingSubmit == nil {
		t.Fatal("withholding did not arm a retry, so nothing can ever deliver it")
	}
	drainRetryEvents(ch)

	// The agent finally repaints, and our text is there.
	composerShowing(emu, "deploy the thing")
	p.maybeRetryWithheldSubmit()

	time.Sleep(150 * time.Millisecond)
	if got := w.count(); got != 1 {
		t.Errorf("want exactly 1 submit after the composer repainted holding our text, got %d", got)
	}
	if p.pendingSubmit != nil {
		t.Error("pendingSubmit still armed after delivery: it would fire again")
	}
	if evs := drainRetryEvents(ch); len(evs) == 0 || !strings.Contains(evs[0].Detail, "delivered") {
		t.Errorf("delivery not surfaced to the operator: %+v", evs)
	}
}

// The AC's second case: a composer that changed to something that is NOT ours.
// We do not know whether our text is still there, and a submit into an unknown
// composer is a FORGED submit -- the exact thing the belt exists to prevent.
func TestWithheldSubmit_NeverForgesASubmitWhenTheComposerChanged(t *testing.T) {
	p, emu, w, ch := retryPane(t)
	captureLogs(t)

	composerShowing(emu, "")
	withholdSubmit(p, "deploy the thing", "bracketed")
	drainRetryEvents(ch)

	// The operator started typing, or the agent cleared and reused the composer.
	composerShowing(emu, "something the operator is typing")
	p.maybeRetryWithheldSubmit()

	time.Sleep(150 * time.Millisecond)
	if got := w.count(); got != 0 {
		t.Errorf("FORGED SUBMIT: sent %d Enter(s) into a composer that no longer holds our text", got)
	}
	if p.pendingSubmit != nil {
		t.Error("pendingSubmit still armed: it would keep watching a composer that moved on")
	}
	evs := drainRetryEvents(ch)
	if len(evs) == 0 || !strings.Contains(evs[0].Detail, "NOT delivered") {
		t.Errorf("an undeliverable message was not surfaced loudly: %+v", evs)
	}
}

// While nothing has changed, the retry waits rather than giving up or guessing.
func TestWithheldSubmit_KeepsWaitingWhileTheComposerHasNotRepainted(t *testing.T) {
	p, emu, w, _ := retryPane(t)
	captureLogs(t)

	composerShowing(emu, "")
	withholdSubmit(p, "deploy the thing", "bracketed")
	p.maybeRetryWithheldSubmit()

	time.Sleep(100 * time.Millisecond)
	if got := w.count(); got != 0 {
		t.Errorf("submitted %d time(s) while the composer had not repainted", got)
	}
	if p.pendingSubmit == nil {
		t.Error("gave up while still inside the window: the busy agent it is waiting for had not answered yet")
	}
}

// Bounded: it does not wait forever, and when it gives up it says so.
func TestWithheldSubmit_GivesUpLoudlyAfterTheWindow(t *testing.T) {
	p, emu, w, ch := retryPane(t)
	captureLogs(t)

	composerShowing(emu, "")
	withholdSubmit(p, "deploy the thing", "bracketed")
	drainRetryEvents(ch)

	p.mu.Lock()
	p.pendingSubmit.deadline = time.Now().Add(-time.Second)
	p.mu.Unlock()
	p.maybeRetryWithheldSubmit()

	time.Sleep(100 * time.Millisecond)
	if got := w.count(); got != 0 {
		t.Errorf("submitted %d time(s) after giving up", got)
	}
	if p.pendingSubmit != nil {
		t.Error("still armed after the window expired: it would spin")
	}
	if evs := drainRetryEvents(ch); len(evs) == 0 || !strings.Contains(evs[0].Detail, "NOT delivered") {
		t.Errorf("giving up was not surfaced: %+v", evs)
	}
}

// THE DOUBLE-DELIVERY GUARD. The withheld thing is the SUBMIT; the body is
// already in the composer. A retry that re-pastes the body delivers the message
// twice, which is why enqueue-the-whole-message was the wrong shape here.
func TestWithheldSubmit_RetryNeverRewritesTheBodyToThePTY(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	// RAW MODE, or this test is blind: the tty defaults to CANONICAL mode, where
	// a read only returns on a complete line. Both the control write and any
	// re-pasted body are newline-free, so without this every read returns
	// nothing and the test passes no matter what the code does. This is the
	// same MakeRaw the existing inject tests do, and skipping it is what let the
	// re-paste mutant survive.
	oldState, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	defer term.Restore(int(tty.Fd()), oldState)

	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{name: "eng1", emu: emu, eventCh: make(chan AgentEvent, 16), ptmx: &filePty{ptmx}}
	captureLogs(t)
	watchSubmits(emu)

	composerShowing(emu, "")
	withholdSubmit(p, "deploy the thing", "bracketed")
	composerShowing(emu, "deploy the thing")
	p.maybeRetryWithheldSubmit()
	time.Sleep(200 * time.Millisecond)

	// POSITIVE CONTROL FIRST. Without it this test cannot fail: if reading the
	// tty side does not work, "the body never appeared" is satisfied by an
	// instrument that observes nothing at all, and a mutant that re-pastes the
	// body survives. It did survive, before this control was added.
	const sentinel = "SENTINEL-instrument-is-live"
	if _, err := ptmx.Write([]byte(sentinel)); err != nil {
		t.Fatalf("control write failed: %v", err)
	}
	seen := readTTY(t, tty, 700*time.Millisecond)
	if !strings.Contains(seen, sentinel) {
		t.Fatalf("INSTRUMENT DEAD: a control write to the PTY was not observed on the tty side, so "+
			"this test cannot distinguish 'no body written' from 'nothing readable'. Saw: %q", seen)
	}
	if strings.Contains(seen, "deploy the thing") {
		t.Errorf("DOUBLE DELIVERY: the retry re-pasted the body to the PTY. The body was already "+
			"in the composer; only the submit was withheld. Saw: %q", seen)
	}
}

// readTTY accumulates whatever is readable within d.
//
// The read runs on its own goroutine because a tty read can block past its
// deadline on some platforms, and a blocked read on the TEST goroutine hangs
// the whole run rather than failing it -- which is what happened the first
// time this was written.
func readTTY(t *testing.T, tty *os.File, d time.Duration) string {
	t.Helper()
	var mu sync.Mutex
	var sb strings.Builder
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			select {
			case <-done:
				return
			default:
			}
			n, err := tty.Read(buf)
			if n > 0 {
				mu.Lock()
				sb.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	time.Sleep(d)
	close(done)
	mu.Lock()
	defer mu.Unlock()
	return sb.String()
}
