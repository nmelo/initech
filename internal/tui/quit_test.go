package tui

import (
	"net"
	"sync"
	"testing"
	"time"
)

// TestHandleIPCQuit_ConcurrentSafe verifies that calling handleIPCQuit from
// multiple goroutines simultaneously does not panic (close of closed channel).
func TestHandleIPCQuit_ConcurrentSafe(t *testing.T) {
	quitCh := make(chan struct{})
	tui := &TUI{quitCh: quitCh}

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c1, c2 := net.Pipe()
			// Drain the response pipe so handleIPCQuit's writes don't block.
			go func() {
				buf := make([]byte, 256)
				for {
					_, err := c2.Read(buf)
					if err != nil {
						return
					}
				}
			}()
			tui.handleIPCQuit(c1)
			c1.Close()
			c2.Close()
		}()
	}
	wg.Wait()

	// quitCh must be closed after concurrent quit calls.
	select {
	case <-quitCh:
		// Good.
	case <-time.After(time.Second):
		t.Error("quitCh was not closed after concurrent quit calls")
	}
}

// TestHandleIPCQuit_IdempotentOnSingleCall verifies that a single quit call
// closes quitCh normally.
func TestHandleIPCQuit_IdempotentOnSingleCall(t *testing.T) {
	quitCh := make(chan struct{})
	tui := &TUI{quitCh: quitCh}

	c1, c2 := net.Pipe()
	go func() {
		buf := make([]byte, 256)
		for {
			_, err := c2.Read(buf)
			if err != nil {
				return
			}
		}
	}()
	tui.handleIPCQuit(c1)
	c1.Close()
	c2.Close()

	select {
	case <-quitCh:
		// Good.
	default:
		t.Error("quitCh should be closed after handleIPCQuit")
	}
}
