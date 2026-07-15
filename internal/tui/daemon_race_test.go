package tui

import (
	"sync"
	"testing"
	"time"
)

// TestDaemon_ConcurrentPaneAccess_NoRace is the ini-sz46 regression: every
// reader of d.panes/ringBufs/multiSinks must be synchronized with the
// hot-add/remove writers (startPushedPane/removePane). Run under -race; before
// the fix this trips a data race on the d.panes slice and "concurrent map read
// and map write" on ringBufs/multiSinks when a configure_agent/reconnect races
// a peek/list/stream.
func TestDaemon_ConcurrentPaneAccess_NoRace(t *testing.T) {
	d := newTestDaemon(t)

	// Seed a stable pane the readers always look up.
	d.panesMu.Lock()
	d.panes = append(d.panes, &Pane{name: "eng1"})
	d.ringBufs["eng1"] = NewRingBuf(1024)
	d.multiSinks["eng1"] = NewMultiSink()
	d.panesMu.Unlock()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer: hot-add then remove a transient pane, mirroring
	// startPushedPane/removePane (both hold panesMu).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			d.panesMu.Lock()
			d.panes = append(d.panes, &Pane{name: "tmp"})
			d.ringBufs["tmp"] = NewRingBuf(1024)
			d.multiSinks["tmp"] = NewMultiSink()
			d.panesMu.Unlock()
			d.removePane("tmp")
		}
	}()

	// Readers: exercise the synchronized accessors concurrently.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = d.findPane("eng1")
				_, _ = d.AllPanes()
				_ = d.ringBufFor("eng1")
				_ = d.multiSinkFor("eng1")
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}
