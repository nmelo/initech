package tui

import (
	"bytes"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// The typing hold: an agent message to a pane the operator is typing in
// waits for a quiet gap, so the draft is not stashed and restored mid-word.
// It waits at most max from the message's arrival, then is delivered anyway,
// through the unchanged send order (dialog guard, stash, paste, belt).
//
// Several messages to one pane wait in a FIFO line and go one after another;
// once a line exists, every later send to that pane joins it, typing or not,
// so nothing overtakes a held message.

// typingHoldSettings is the effective quiet window and cap. quiet 0 disables.
type typingHoldSettings struct {
	quiet, max time.Duration
}

// typingHoldConfig is set once at TUI or daemon start from initech.yaml.
// Unset means the defaults, the same as absent keys.
var typingHoldConfig atomic.Pointer[typingHoldSettings]

// applyTypingHold sets the hold from a loaded project and logs one warning per
// key that fell back. A nil project leaves the defaults.
func applyTypingHold(p *config.Project) {
	if p == nil {
		return
	}
	quiet, max, warnings := p.TypingHold()
	for _, w := range warnings {
		LogWarn("config", "typing hold setting ignored", "detail", w)
	}
	setTypingHold(time.Duration(quiet)*time.Second, time.Duration(max)*time.Second)
}

func setTypingHold(quiet, max time.Duration) {
	typingHoldConfig.Store(&typingHoldSettings{quiet: quiet, max: max})
}

func currentTypingHold() typingHoldSettings {
	if s := typingHoldConfig.Load(); s != nil {
		return *s
	}
	return typingHoldSettings{
		quiet: config.DefaultTypingHoldQuietS * time.Second,
		max:   config.DefaultTypingHoldMaxS * time.Second,
	}
}

// holdClock is the time source for the hold. Nil on a pane means real time;
// tests swap in a fake.
type holdClock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realHoldClock struct{}

func (realHoldClock) Now() time.Time                         { return time.Now() }
func (realHoldClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (p *Pane) holdClockOrReal() holdClock {
	if p.holdClk != nil {
		return p.holdClk
	}
	return realHoldClock{}
}

// holdLine is a pane's FIFO of held messages: tickets in arrival order, one
// served at a time. The served message keeps its turn through delivery.
type holdLine struct {
	mu            sync.Mutex
	cond          *sync.Cond
	next, serving uint64
}

// noteOperatorKey records that a key the operator pressed reached this pane,
// from this window or any other. Mouse input is not typing and never calls it.
func (p *Pane) noteOperatorKey() {
	p.lastOperatorKey.Store(p.holdClockOrReal().Now().UnixNano())
}

// sinceOperatorKey is how long ago the operator last typed into this pane,
// and false when they never have.
func (p *Pane) sinceOperatorKey(now time.Time) (time.Duration, bool) {
	ns := p.lastOperatorKey.Load()
	if ns == 0 {
		return 0, false
	}
	return now.Sub(time.Unix(0, ns)), true
}

// HeldMessages is how many messages this pane is holding for the operator's
// typing, including one being delivered at the end of its hold. The ribbon
// shows a mark while it is above zero.
func (p *Pane) HeldMessages() int {
	return int(p.heldCount.Load())
}

// waitTypingHold waits out the typing hold for one message and returns the
// function that ends its turn, to be called after delivery. A send to a pane
// nobody is typing in, with no line, returns at once.
func (p *Pane) waitTypingHold() (release func()) {
	s := currentTypingHold()
	if s.quiet <= 0 {
		return func() {}
	}
	clk := p.holdClockOrReal()
	arrival := clk.Now()

	l := &p.hold
	l.mu.Lock()
	if l.cond == nil {
		l.cond = sync.NewCond(&l.mu)
	}
	since, typed := p.sinceOperatorKey(arrival)
	if l.next == l.serving && (!typed || since >= s.quiet) {
		l.mu.Unlock()
		return func() {}
	}
	ticket := l.next
	l.next++
	p.heldCount.Add(1)
	for l.serving != ticket {
		l.cond.Wait()
	}
	l.mu.Unlock()

	capReached := false
	for p.alivePane() {
		now := clk.Now()
		since, typed := p.sinceOperatorKey(now)
		if !typed || since >= s.quiet {
			break
		}
		waited := now.Sub(arrival)
		if waited >= s.max {
			capReached = true
			break
		}
		<-clk.After(min(s.quiet-since, s.max-waited))
	}
	LogInfo("send", "message held", "pane", p.name, "reason", "operator typing",
		"held_s", clk.Now().Sub(arrival).Round(100*time.Millisecond).Seconds(), "cap_reached", capReached)

	return func() {
		p.heldCount.Add(-1)
		l.mu.Lock()
		l.serving++
		l.cond.Broadcast()
		l.mu.Unlock()
	}
}

// isMouseOrFocusReport reports whether raw input from another window's stream
// is a mouse or focus report rather than a key: those are not typing.
func isMouseOrFocusReport(b []byte) bool {
	return bytes.HasPrefix(b, []byte("\x1b[<")) || bytes.HasPrefix(b, []byte("\x1b[M")) ||
		bytes.Equal(b, []byte("\x1b[I")) || bytes.Equal(b, []byte("\x1b[O"))
}
