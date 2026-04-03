// Tests for PTY byte fan-out subscriber registry (Subscribe/Unsubscribe/broadcast).
package tui

import (
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// TestSubscribe_ReceivesBytes verifies that a subscriber receives broadcasted data.
func TestSubscribe_ReceivesBytes(t *testing.T) {
	p := &Pane{name: "test", emu: vt.NewSafeEmulator(80, 24)}
	ch := p.Subscribe("ws-1")
	defer p.Unsubscribe("ws-1")

	p.broadcastToSubscribers([]byte("hello"))

	select {
	case got := <-ch:
		if string(got) != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast")
	}
}

// TestSubscribe_MultipleSubscribers verifies all subscribers receive the same data.
func TestSubscribe_MultipleSubscribers(t *testing.T) {
	p := &Pane{name: "test", emu: vt.NewSafeEmulator(80, 24)}
	ch1 := p.Subscribe("ws-1")
	ch2 := p.Subscribe("ws-2")
	defer p.Unsubscribe("ws-1")
	defer p.Unsubscribe("ws-2")

	p.broadcastToSubscribers([]byte("data"))

	for _, tc := range []struct {
		name string
		ch   chan []byte
	}{
		{"ws-1", ch1},
		{"ws-2", ch2},
	} {
		select {
		case got := <-tc.ch:
			if string(got) != "data" {
				t.Errorf("%s: got %q, want %q", tc.name, got, "data")
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: timed out waiting for broadcast", tc.name)
		}
	}
}

// TestSubscribe_DataIsCopied verifies subscribers get independent copies,
// not references to the readLoop buffer.
func TestSubscribe_DataIsCopied(t *testing.T) {
	p := &Pane{name: "test", emu: vt.NewSafeEmulator(80, 24)}
	ch := p.Subscribe("ws-1")
	defer p.Unsubscribe("ws-1")

	orig := []byte("original")
	p.broadcastToSubscribers(orig)

	// Mutate the original after broadcast.
	orig[0] = 'X'

	got := <-ch
	if string(got) != "original" {
		t.Errorf("subscriber got mutated data: %q", got)
	}
}

// TestUnsubscribe_ClosesChannel verifies the channel is closed on unsubscribe.
func TestUnsubscribe_ClosesChannel(t *testing.T) {
	p := &Pane{name: "test", emu: vt.NewSafeEmulator(80, 24)}
	ch := p.Subscribe("ws-1")
	p.Unsubscribe("ws-1")

	// Channel should be closed; receive should return zero value immediately.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out; channel not closed")
	}
}

// TestUnsubscribe_DoubleUnsubscribe does not panic.
func TestUnsubscribe_DoubleUnsubscribe(t *testing.T) {
	p := &Pane{name: "test", emu: vt.NewSafeEmulator(80, 24)}
	p.Subscribe("ws-1")
	p.Unsubscribe("ws-1")
	p.Unsubscribe("ws-1") // Should not panic.
}

// TestUnsubscribe_UnknownID does not panic.
func TestUnsubscribe_UnknownID(t *testing.T) {
	p := &Pane{name: "test", emu: vt.NewSafeEmulator(80, 24)}
	p.Unsubscribe("never-subscribed") // Should not panic.
}

// TestBroadcast_ZeroSubscribers does not panic.
func TestBroadcast_ZeroSubscribers(t *testing.T) {
	p := &Pane{name: "test", emu: vt.NewSafeEmulator(80, 24)}
	p.broadcastToSubscribers([]byte("no one listening")) // Should not panic.
}

// TestBroadcast_FullChannelDropsOldest verifies that when a subscriber channel
// is full, the oldest entry is dropped and the new one is delivered.
func TestBroadcast_FullChannelDropsOldest(t *testing.T) {
	p := &Pane{name: "test", emu: vt.NewSafeEmulator(80, 24)}
	ch := p.Subscribe("ws-1")
	defer p.Unsubscribe("ws-1")

	// Fill the channel to capacity.
	for i := 0; i < subscriberBufSize; i++ {
		p.broadcastToSubscribers([]byte{byte(i % 256)})
	}

	// Channel is now full. Next broadcast must not block and must drop oldest.
	done := make(chan struct{})
	go func() {
		p.broadcastToSubscribers([]byte("overflow"))
		close(done)
	}()

	select {
	case <-done:
		// Good: broadcast did not block.
	case <-time.After(2 * time.Second):
		t.Fatal("broadcastToSubscribers blocked on full channel")
	}

	// Drain all but last; last entry should be "overflow".
	for i := 0; i < subscriberBufSize-1; i++ {
		<-ch
	}
	got := <-ch
	if string(got) != "overflow" {
		t.Errorf("last entry = %q, want %q", got, "overflow")
	}
}

// TestBroadcast_ConcurrentSafety exercises subscribe, unsubscribe, and
// broadcast from multiple goroutines simultaneously.
func TestBroadcast_ConcurrentSafety(t *testing.T) {
	p := &Pane{name: "test", emu: vt.NewSafeEmulator(80, 24)}

	var wg sync.WaitGroup
	const numWriters = 4
	const numCycles = 100

	// Concurrent broadcasters.
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numCycles; j++ {
				p.broadcastToSubscribers([]byte("concurrent"))
			}
		}()
	}

	// Concurrent subscribe/unsubscribe cycles.
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numCycles; j++ {
				sid := "sub-" + string(rune('A'+id))
				p.Subscribe(sid)
				p.Unsubscribe(sid)
			}
		}(i)
	}

	wg.Wait() // Must not panic or deadlock.
}

// TestCloseAllSubscribers_ClosesChannels verifies teardown closes all channels.
func TestCloseAllSubscribers_ClosesChannels(t *testing.T) {
	p := &Pane{name: "test", emu: vt.NewSafeEmulator(80, 24)}
	ch1 := p.Subscribe("ws-1")
	ch2 := p.Subscribe("ws-2")

	p.closeAllSubscribers()

	for _, tc := range []struct {
		name string
		ch   chan []byte
	}{
		{"ws-1", ch1},
		{"ws-2", ch2},
	} {
		select {
		case _, ok := <-tc.ch:
			if ok {
				t.Errorf("%s: channel not closed", tc.name)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: timed out; channel not closed", tc.name)
		}
	}
}

// TestCloseAllSubscribers_ClearsMap verifies that after teardown, the map is nil
// and new subscriptions work on a fresh map.
func TestCloseAllSubscribers_ClearsMap(t *testing.T) {
	p := &Pane{name: "test", emu: vt.NewSafeEmulator(80, 24)}
	p.Subscribe("ws-1")
	p.closeAllSubscribers()

	// Map should be nil.
	p.subscriberMu.Lock()
	if p.subscribers != nil {
		t.Error("subscribers map not nil after closeAllSubscribers")
	}
	p.subscriberMu.Unlock()

	// Subscribe again should work.
	ch := p.Subscribe("ws-2")
	defer p.Unsubscribe("ws-2")
	p.broadcastToSubscribers([]byte("after-teardown"))

	select {
	case got := <-ch:
		if string(got) != "after-teardown" {
			t.Errorf("got %q, want %q", got, "after-teardown")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

// TestSubscribe_ReplayOnConnect verifies that a new subscriber receives
// buffered PTY history as the first message before any live data.
func TestSubscribe_ReplayOnConnect(t *testing.T) {
	p := &Pane{
		name:      "test",
		emu:       vt.NewSafeEmulator(80, 24),
		replayBuf: NewRingBuf(DefaultRingBufSize),
	}

	// Simulate PTY output that was captured before any subscriber connects.
	p.replayBuf.Write([]byte("previous output"))

	ch := p.Subscribe("ws-1")
	defer p.Unsubscribe("ws-1")

	// First message should be the replay snapshot.
	select {
	case got := <-ch:
		if string(got) != "previous output" {
			t.Errorf("replay = %q, want %q", got, "previous output")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replay")
	}

	// Live data should still arrive after replay.
	p.broadcastToSubscribers([]byte("live"))
	select {
	case got := <-ch:
		if string(got) != "live" {
			t.Errorf("live = %q, want %q", got, "live")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live data")
	}
}

// TestSubscribe_NoReplayBuf does not panic when replayBuf is nil.
func TestSubscribe_NoReplayBuf(t *testing.T) {
	p := &Pane{name: "test", emu: vt.NewSafeEmulator(80, 24)}
	ch := p.Subscribe("ws-1")
	defer p.Unsubscribe("ws-1")

	// Should not have any replay message. Send live data to confirm channel works.
	p.broadcastToSubscribers([]byte("live"))
	select {
	case got := <-ch:
		if string(got) != "live" {
			t.Errorf("got %q, want %q", got, "live")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

// TestSubscribe_EmptyReplayBuf sends no replay when buffer is empty.
func TestSubscribe_EmptyReplayBuf(t *testing.T) {
	p := &Pane{
		name:      "test",
		emu:       vt.NewSafeEmulator(80, 24),
		replayBuf: NewRingBuf(DefaultRingBufSize),
	}

	ch := p.Subscribe("ws-1")
	defer p.Unsubscribe("ws-1")

	// No replay expected. Send live data to prove channel is clean.
	p.broadcastToSubscribers([]byte("live"))
	select {
	case got := <-ch:
		if string(got) != "live" {
			t.Errorf("got %q, want %q", got, "live")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}
