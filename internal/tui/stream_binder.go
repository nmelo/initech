package tui

// The viewer's side of stream-on-create (ini-c5jv). Window 1 opens a new
// yamux stream for an agent it adds after attach and announces it with
// stream_added{stream_id, name}. The two travel separately, the stream on
// the session and the announcement on the control stream, so either may
// arrive first. They are paired BY STREAM ID, as the handshake pairs its
// streams with the stream map. Pairing by arrival order meant one lost
// announcement bound agent A's stream to agent B's name, and keys typed into
// the "B" pane went to A's terminal for the rest of the connection.
//
// An unpaired stream or announcement waits streamBindTimeout and is then
// logged and dropped. A pane is created only from a pair, never from a guess.

import (
	"net"
	"sync"
	"time"
)

// streamBindTimeout bounds how long a stream and its announcement wait for
// each other. A var so tests can shorten it.
var streamBindTimeout = 10 * time.Second

// streamBinder pairs the streams window 1 opens after attach with their
// stream_added announcements, by stream id.
type streamBinder struct {
	peer string
	bind func(name string, stream net.Conn) // called once per pair, outside mu

	mu      sync.Mutex
	streams map[uint32]net.Conn // accepted, not yet announced
	anns    map[uint32]string   // announced, stream not yet accepted
	bound   map[uint32]string   // paired; an id is never paired twice
	timers  map[uint32]*time.Timer
	closed  bool
}

func newStreamBinder(peer string, bind func(name string, stream net.Conn)) *streamBinder {
	return &streamBinder{
		peer:    peer,
		bind:    bind,
		streams: map[uint32]net.Conn{},
		anns:    map[uint32]string{},
		bound:   map[uint32]string{},
		timers:  map[uint32]*time.Timer{},
	}
}

// offerStream takes a stream window 1 opened. It binds at once if its
// announcement already arrived, and otherwise waits for it.
func (b *streamBinder) offerStream(id uint32, stream net.Conn) {
	if id == 0 {
		LogWarn("remote", "stream with id 0 refused", "peer", b.peer)
		stream.Close()
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		stream.Close()
		return
	}
	if name, ok := b.bound[id]; ok {
		b.mu.Unlock()
		LogWarn("remote", "second stream for an already bound id refused", "peer", b.peer, "id", id, "agent", name)
		stream.Close()
		return
	}
	if name, ok := b.anns[id]; ok {
		b.pairLocked(id, name)
		b.mu.Unlock()
		b.bind(name, stream)
		return
	}
	b.streams[id] = stream
	b.timers[id] = time.AfterFunc(streamBindTimeout, func() { b.expire(id) })
	b.mu.Unlock()
}

// announce takes a stream_added. It binds at once if the stream already
// arrived, and otherwise waits for it. An empty name, id 0, or an id already
// bound is refused: a bound pane is never rebound.
func (b *streamBinder) announce(id uint32, name string) {
	if name == "" || id == 0 {
		LogWarn("remote", "stream_added refused: needs a name and a stream id", "peer", b.peer, "agent", name, "id", id)
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	if prev, ok := b.bound[id]; ok {
		b.mu.Unlock()
		LogWarn("remote", "stream_added refused: id already bound", "peer", b.peer, "id", id, "agent", name, "bound_to", prev)
		return
	}
	if prev, ok := b.anns[id]; ok {
		b.mu.Unlock()
		LogWarn("remote", "stream_added refused: id already announced", "peer", b.peer, "id", id, "agent", name, "announced_as", prev)
		return
	}
	if stream, ok := b.streams[id]; ok {
		b.pairLocked(id, name)
		b.mu.Unlock()
		b.bind(name, stream)
		return
	}
	b.anns[id] = name
	b.timers[id] = time.AfterFunc(streamBindTimeout, func() { b.expire(id) })
	b.mu.Unlock()
}

// forget drops a pending announcement for name: the agent was removed
// before its stream arrived, so the stream must not create a pane for it.
func (b *streamBinder) forget(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, n := range b.anns {
		if n == name {
			b.stopLocked(id)
			delete(b.anns, id)
		}
	}
}

// close ends the binder with its connection: pending streams are closed and
// nothing binds after it.
func (b *streamBinder) close() {
	b.mu.Lock()
	b.closed = true
	streams := b.streams
	b.streams = map[uint32]net.Conn{}
	for id := range b.timers {
		b.stopLocked(id)
	}
	b.anns = map[uint32]string{}
	b.mu.Unlock()
	for _, s := range streams {
		s.Close()
	}
}

// expire drops whichever half of id is still waiting.
func (b *streamBinder) expire(id uint32) {
	b.mu.Lock()
	stream, isStream := b.streams[id]
	name, isAnn := b.anns[id]
	delete(b.streams, id)
	delete(b.anns, id)
	delete(b.timers, id)
	b.mu.Unlock()
	if isStream {
		LogWarn("remote", "stream never announced; no pane created", "peer", b.peer, "id", id, "waited", streamBindTimeout)
		stream.Close()
	}
	if isAnn {
		LogWarn("remote", "announced agent's stream never arrived; no pane created", "peer", b.peer, "id", id, "agent", name, "waited", streamBindTimeout)
	}
}

// pairLocked records id as bound to name. b.mu is held.
func (b *streamBinder) pairLocked(id uint32, name string) {
	b.stopLocked(id)
	delete(b.streams, id)
	delete(b.anns, id)
	b.bound[id] = name
}

func (b *streamBinder) stopLocked(id uint32) {
	if t := b.timers[id]; t != nil {
		t.Stop()
		delete(b.timers, id)
	}
}
