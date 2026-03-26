//go:build !windows

package roles

import (
	"sync"
	"testing"
	"time"
)

// TestRunSelector_DoneChannelUnblocksGoroutine verifies the done-channel
// pattern used by RunSelector's key-reader goroutine. The goroutine must exit
// promptly when done is closed, even when keyCh is full and no new reads are
// draining it (ini-a1e.18).
//
// RunSelector itself cannot be exercised in a unit test (it requires /dev/tty),
// so this test validates the pattern in isolation.
func TestRunSelector_DoneChannelUnblocksGoroutine(t *testing.T) {
	keyCh := make(chan keyPress, 4)
	done := make(chan struct{})

	// Feed more events than keyCh can hold so the goroutine will block on the
	// channel send without the done-channel escape hatch.
	pending := make([]keyPress, 10)
	for i := range pending {
		pending[i] = keyPress{kind: keyChar, ch: byte('a' + i)}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, kp := range pending {
			select {
			case keyCh <- kp:
			case <-done:
				return
			}
		}
	}()

	// Drain two events so the goroutine makes some progress, then simulate
	// the Enter-confirm path by closing done.
	<-keyCh
	<-keyCh
	close(done)

	// The goroutine must exit within a generous timeout; without the done
	// channel it would block forever on the full keyCh.
	exited := make(chan struct{})
	go func() {
		wg.Wait()
		close(exited)
	}()

	select {
	case <-exited:
		// goroutine exited cleanly
	case <-time.After(2 * time.Second):
		t.Error("key-reader goroutine did not exit after done was closed (goroutine leak)")
	}
}

// TestRunSelector_DoneChannelEarlyClose verifies the goroutine exits immediately
// when done is closed before any reads from keyCh — the zero-drain scenario.
func TestRunSelector_DoneChannelEarlyClose(t *testing.T) {
	keyCh := make(chan keyPress, 1)
	done := make(chan struct{})

	// Fill the buffer so the very first send will block.
	keyCh <- keyPress{kind: keyChar, ch: 'x'}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, kp := range []keyPress{{kind: keyChar, ch: 'y'}, {kind: keyChar, ch: 'z'}} {
			select {
			case keyCh <- kp:
			case <-done:
				return
			}
		}
	}()

	// Close done immediately — goroutine should unblock from the full channel.
	close(done)

	exited := make(chan struct{})
	go func() {
		wg.Wait()
		close(exited)
	}()

	select {
	case <-exited:
		// goroutine exited cleanly
	case <-time.After(2 * time.Second):
		t.Error("key-reader goroutine did not exit after early done close")
	}
}
