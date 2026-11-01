package tui

import (
	"testing"
	"time"
)

// endDrainsWithTest makes the test wait, when it ends, until no modal drain is
// running on p (ini-ns4). A drain is its own goroutine (startModalDrain) and
// sends while it runs; one left running after its test reached the next
// test's package seams while they were swapped, a data race reported in the
// next test's name. Register it after the fixture's own cleanups: cleanups run
// last-in first-out, so the wait runs while the pane's pipe and emulator are
// still open and the drain can finish its send. The wait is bounded; a drain
// still running at the bound fails the test.
func endDrainsWithTest(t *testing.T, p *Pane) {
	t.Helper()
	t.Cleanup(func() {
		deadline := time.Now().Add(15 * time.Second)
		for {
			p.mu.Lock()
			draining := p.modalDraining
			p.mu.Unlock()
			if !draining {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("a modal drain on %s is still running 15s after the test ended: it would outlive the test", p.name)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}
