package tui

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// setSampleMidpoint runs fn inside each confirmation sample of p, and of no
// other pane, until the returned restore runs or the test ends. Restoring is
// idempotent.
func setSampleMidpoint(t *testing.T, p *Pane, fn func()) (restore func()) {
	t.Helper()
	h := func(pane *Pane) {
		if pane == p {
			fn()
		}
	}
	old := submitSampleMidpoint.Swap(&h)
	var once sync.Once
	restore = func() { once.Do(func() { submitSampleMidpoint.Store(old) }) }
	t.Cleanup(restore)
	return restore
}

// A send that another test left running samples its own pane while this test
// sets and restores the sample seam (rigs 41334196849: a modal drain started
// by a daemon-maintenance test was still sending when the ini-dmi test swapped
// the seam). The swap must not race that send's read, and the leftover send
// must not run this test's hook, which acts on this test's pane only.
func TestSampleSeam_ALeftoverSendNeitherRacesTheSwapNorRunsThisPanesHook(t *testing.T) {
	other, _, _ := proofPane(t)
	nma5Paint(other, "❯ "+nma5Body)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ps := newPendingSubmit(nma5Body, "bracketed", "")
		for {
			select {
			case <-stop:
				return
			default:
				submitSample(other, ps)
			}
		}
	}()
	p, _, _ := proofPane(t)
	var calls atomic.Int32
	for i := 0; i < 50; i++ {
		restore := setSampleMidpoint(t, p, func() { calls.Add(1) })
		time.Sleep(time.Millisecond)
		restore()
	}
	close(stop)
	<-done
	if n := calls.Load(); n != 0 {
		t.Errorf("another pane's samples ran this test's hook %d times", n)
	}
	// The positive twin: the hook does run for this test's own pane.
	setSampleMidpoint(t, p, func() { calls.Add(1) })
	nma5Paint(p, "❯ "+nma5Body)
	submitSample(p, newPendingSubmit(nma5Body, "bracketed", ""))
	if n := calls.Load(); n != 1 {
		t.Errorf("this pane's sample ran its hook %d times, want 1", n)
	}
}
