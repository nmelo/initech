package tui

// REGRESSION ONLY -- these cells are NOT the evidence for ini-hbj4.
//
// The claim "a wake delivers into a listening process" rests on the LIVE A/B:
// arm B 6/6 delivered against arm A losing 4 of 6, measured on the real
// product with real agents. These cells guard waitForOutputQuiescence's own
// contract so a later edit cannot silently change it -- nothing more.
//
// The distinction is the reason this bead was reverted once on false
// evidence. The reverted series carried a unit fixture whose child was a
// shell echo loop; the claim is about Claude Code flushing stdin at raw-mode
// entry while still rendering a large transcript, and `sh` cannot produce
// that. Its green was evidence about the fixture. So: no cell here asserts
// delivery, and none should be added that does. If the quiescence design is
// ever questioned again, the answer is another live A/B, not a green suite.

import (
	"testing"
	"time"
)

func quiescencePane(lastOutput time.Time) *Pane {
	p := &Pane{name: "eng9"}
	p.lastOutputTime = lastOutput
	return p
}

// TestWaitForOutputQuiescence_ReturnsOnceTheChildHasBeenQuiet: a pane whose
// last output is already older than the stable window settles immediately.
func TestWaitForOutputQuiescence_ReturnsOnceTheChildHasBeenQuiet(t *testing.T) {
	tui := &TUI{quitCh: make(chan struct{})}
	pane := quiescencePane(time.Now().Add(-2 * time.Second))

	start := time.Now()
	if !tui.waitForOutputQuiescence(pane, 600*time.Millisecond, 15*time.Second) {
		t.Fatal("a child quiet for 2s did not read as settled")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v to notice an already-quiet child; the wake pays this on every "+
			"resume, so it must not idle through the stable window it has already met", elapsed)
	}
}

// TestWaitForOutputQuiescence_WaitsWhileTheChildIsStillTalking is the
// behaviour the whole gate exists for: output still arriving means not ready.
func TestWaitForOutputQuiescence_WaitsWhileTheChildIsStillTalking(t *testing.T) {
	tui := &TUI{quitCh: make(chan struct{})}
	pane := quiescencePane(time.Now())

	done := make(chan bool, 1)
	go func() { done <- tui.waitForOutputQuiescence(pane, 400*time.Millisecond, 5*time.Second) }()

	// Keep it "talking" past the stable window.
	for i := 0; i < 6; i++ {
		time.Sleep(100 * time.Millisecond)
		pane.mu.Lock()
		pane.lastOutputTime = time.Now()
		pane.mu.Unlock()
	}
	select {
	case <-done:
		t.Fatal("settled while the child was still producing output every 100ms; delivering " +
			"there is delivering into boot, which is the defect")
	default:
	}

	// Stop talking: it must then settle.
	select {
	case ok := <-done:
		if !ok {
			t.Error("reported not-settled after the output stopped")
		}
	case <-time.After(3 * time.Second):
		t.Error("never settled after the child went quiet")
	}
}

// TestWaitForOutputQuiescence_CapExpiryReportsFalse pins the fallback's
// SIGNAL. The caller delivers anyway on false and logs loudly -- a cap expiry
// must never look like a settled child.
//
// NOTE: this fallback has never fired on live traffic (the live A/B saw
// quiescence_warn=0 across all six wakes), so its end-to-end behaviour is
// unmeasured; this cell covers the helper's return value only.
func TestWaitForOutputQuiescence_CapExpiryReportsFalse(t *testing.T) {
	tui := &TUI{quitCh: make(chan struct{})}
	pane := quiescencePane(time.Now())

	done := make(chan bool, 1)
	go func() { done <- tui.waitForOutputQuiescence(pane, time.Second, 300*time.Millisecond) }()
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				pane.mu.Lock()
				pane.lastOutputTime = time.Now()
				pane.mu.Unlock()
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()
	defer close(stop)

	select {
	case ok := <-done:
		if ok {
			t.Error("a cap expiry reported SETTLED; the caller would log nothing and the " +
				"operator would never learn the wake delivered without waiting")
		}
	case <-time.After(3 * time.Second):
		t.Error("cap never expired")
	}
}

// TestWaitForOutputQuiescence_QuitStopsTheWait: shutdown must not be held up
// by a child that never goes quiet.
func TestWaitForOutputQuiescence_QuitStopsTheWait(t *testing.T) {
	quit := make(chan struct{})
	tui := &TUI{quitCh: quit}
	pane := quiescencePane(time.Now())

	done := make(chan bool, 1)
	go func() { done <- tui.waitForOutputQuiescence(pane, time.Hour, time.Hour) }()
	time.Sleep(100 * time.Millisecond)
	close(quit)

	select {
	case ok := <-done:
		if ok {
			t.Error("reported settled on quit; the caller would then deliver into a pane " +
				"during shutdown")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("quit did not stop the wait; a shutdown would block on a talkative child")
	}
}
