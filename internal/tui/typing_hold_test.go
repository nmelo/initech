package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
)

// ini-uibp: a message to a pane the operator is typing in waits for a quiet
// gap (default 2s), at most the cap (default 15s), in arrival order.

type fakeHoldClock struct {
	mu         sync.Mutex
	now        time.Time
	waiters    []fakeHoldWaiter
	registered chan struct{}
}

type fakeHoldWaiter struct {
	at time.Time
	ch chan time.Time
}

func newFakeHoldClock() *fakeHoldClock {
	return &fakeHoldClock{now: time.Unix(1_000_000, 0), registered: make(chan struct{}, 64)}
}

func (c *fakeHoldClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeHoldClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, fakeHoldWaiter{at: c.now.Add(d), ch: ch})
	c.registered <- struct{}{}
	return ch
}

// Advance moves the clock and fires every timer now due, returning how many.
func (c *fakeHoldClock) Advance(d time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	fired := 0
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
			fired++
		} else {
			kept = append(kept, w)
		}
	}
	c.waiters = kept
	return fired
}

// typedAgo stamps an operator key d before the fake now.
func (c *fakeHoldClock) typedAgo(p *Pane, d time.Duration) {
	p.lastOperatorKey.Store(c.Now().Add(-d).UnixNano())
}

func withTypingHold(t *testing.T, quiet, max time.Duration) {
	t.Helper()
	prev := typingHoldConfig.Load()
	setTypingHold(quiet, max)
	t.Cleanup(func() { typingHoldConfig.Store(prev) })
}

func holdPane(t *testing.T) (*Pane, *fakeHoldClock) {
	t.Helper()
	clk := newFakeHoldClock()
	p := &Pane{name: "eng2", alive: true, holdClk: clk}
	return p, clk
}

// startHold runs waitTypingHold on its own goroutine; the channel yields the
// release once the hold ends.
func startHold(p *Pane) <-chan func() {
	out := make(chan func(), 1)
	go func() { out <- p.waitTypingHold() }()
	return out
}

// next waits for the hold either to register a timer or to end.
func next(t *testing.T, clk *fakeHoldClock, done <-chan func()) (release func(), ended bool) {
	t.Helper()
	select {
	case <-clk.registered:
		return nil, false
	case r := <-done:
		return r, true
	case <-time.After(2 * time.Second):
		t.Fatal("the hold neither waited nor ended")
	}
	return nil, false
}

func TestTypingHold_AMessageToAPaneTypedInASecondAgoGoesAfterTwoQuietSeconds(t *testing.T) {
	withTypingHold(t, 2*time.Second, 15*time.Second)
	p, clk := holdPane(t)
	clk.typedAgo(p, time.Second)
	done := startHold(p)
	if _, ended := next(t, clk, done); ended {
		t.Fatal("delivered at once to a pane typed in a second ago")
	}
	if p.HeldMessages() != 1 {
		t.Fatalf("held messages = %d while waiting, want 1 (the ribbon mark reads this)", p.HeldMessages())
	}
	clk.Advance(999 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("went before the quiet window passed")
	case <-time.After(50 * time.Millisecond):
	}
	clk.Advance(time.Millisecond)
	release, ended := next(t, clk, done)
	if !ended {
		t.Fatal("still held after two quiet seconds")
	}
	release()
	if p.HeldMessages() != 0 {
		t.Fatalf("held messages = %d after delivery, want 0", p.HeldMessages())
	}
}

func TestTypingHold_KeysKeepExtendingTheWait(t *testing.T) {
	withTypingHold(t, 2*time.Second, 15*time.Second)
	p, clk := holdPane(t)
	clk.typedAgo(p, time.Second)
	done := startHold(p)
	next(t, clk, done)
	clk.Advance(500 * time.Millisecond)
	p.noteOperatorKey() // typing again at +0.5s
	clk.Advance(500 * time.Millisecond)
	if _, ended := next(t, clk, done); ended {
		t.Fatal("went at the first deadline although the operator typed again")
	}
	clk.Advance(1500 * time.Millisecond) // two seconds after the last key
	release, ended := next(t, clk, done)
	if !ended {
		t.Fatal("still held two quiet seconds after the last key")
	}
	release()
}

func TestTypingHold_GoesAtTheCapWhileTheOperatorNeverPauses(t *testing.T) {
	withTypingHold(t, 2*time.Second, 15*time.Second)
	p, clk := holdPane(t)
	p.noteOperatorKey()
	start := clk.Now()
	done := startHold(p)
	next(t, clk, done)
	for i := 0; i < 60; i++ {
		p.noteOperatorKey() // a key every half second, never a pause
		if clk.Advance(500*time.Millisecond) == 0 {
			continue
		}
		release, ended := next(t, clk, done)
		if ended {
			if got := clk.Now().Sub(start); got != 15*time.Second {
				t.Fatalf("went after %s of continuous typing, want exactly the 15s cap", got)
			}
			release()
			return
		}
	}
	t.Fatal("never went although the operator never paused")
}

func TestTypingHold_TwoMessagesGoInArrivalOrder(t *testing.T) {
	withTypingHold(t, 2*time.Second, 15*time.Second)
	p, clk := holdPane(t)
	p.noteOperatorKey()
	first := startHold(p)
	next(t, clk, first) // first is waiting on the quiet window
	second := startHold(p)
	time.Sleep(50 * time.Millisecond) // let second reach the line
	if p.HeldMessages() != 2 {
		t.Fatalf("held messages = %d, want 2", p.HeldMessages())
	}
	clk.Advance(2 * time.Second)
	releaseFirst, ended := next(t, clk, first)
	if !ended {
		t.Fatal("first message still held after the quiet window")
	}
	select {
	case <-second:
		t.Fatal("second message went while the first was still being delivered")
	case <-time.After(50 * time.Millisecond):
	}
	releaseFirst()
	select {
	case r := <-second:
		r()
	case <-time.After(2 * time.Second):
		t.Fatal("second message never went after the first was delivered")
	}
}

func TestTypingHold_QuietZeroDeliversAtOnceAsBefore(t *testing.T) {
	withTypingHold(t, 0, 15*time.Second)
	p, clk := holdPane(t)
	p.noteOperatorKey()
	release, ended := next(t, clk, startHold(p))
	if !ended {
		t.Fatal("quiet 0 must disable the hold")
	}
	release()
}

func TestTypingHold_APaneNobodyTypedInGoesAtOnce(t *testing.T) {
	withTypingHold(t, 2*time.Second, 15*time.Second)
	p, clk := holdPane(t)
	release, ended := next(t, clk, startHold(p))
	if !ended {
		t.Fatal("held a message to a pane nobody typed in")
	}
	release()
	clk.typedAgo(p, 3*time.Second) // typed, but long enough ago
	release, ended = next(t, clk, startHold(p))
	if !ended {
		t.Fatal("held a message although the operator paused past the quiet window")
	}
	release()
}

func TestStreamInput_MouseAndFocusReportsAreNotTyping(t *testing.T) {
	for _, in := range []string{"\x1b[<64;10;5M", "\x1b[M !!", "\x1b[I", "\x1b[O"} {
		if !isMouseOrFocusReport([]byte(in)) {
			t.Errorf("%q counted as typing", in)
		}
	}
	for _, in := range []string{"a", "hello", "\x1b[A", "\r"} {
		if isMouseOrFocusReport([]byte(in)) {
			t.Errorf("key %q not counted as typing", in)
		}
	}
}

func TestSendKey_StampsTheTypingClock(t *testing.T) {
	p, clk := holdPane(t)
	p.emu = newEmuPane("x", 80, 24).emu
	emu := p.emu
	go func() { // drain the key encoding; ends when the emulator closes
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = emu.Close() })
	p.SendKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	if d, ok := p.sinceOperatorKey(clk.Now()); !ok || d != 0 {
		t.Fatalf("SendKey did not record the keystroke (since %s, typed %v)", d, ok)
	}
}

// A message held for typing that then meets a dialog takes the dialog path:
// deferred to the queue, never pasted.
func TestTypingHold_AHeldMessageThatMeetsADialogIsDeferredNotPasted(t *testing.T) {
	withTypingHold(t, 2*time.Second, 15*time.Second)
	p, r := i35wFreshPane(t, "eng2")
	clk := newFakeHoldClock()
	p.holdClk = clk
	p.mu.Lock()
	p.startupSettled = true
	p.mu.Unlock()
	clk.typedAgo(p, time.Second)

	sent := make(chan struct{})
	go func() { p.SendText("MARKER_UIBP_DIALOG", true); close(sent) }()
	awaitHoldTimer(t, clk)
	p.mu.Lock()
	p.dialogOpen = true // a dialog opened while the message waited
	p.mu.Unlock()
	clk.Advance(time.Second)
	select {
	case <-sent:
	case <-time.After(2 * time.Second):
		t.Fatal("send never returned")
	}
	if got := p.QueuedMessageCount(); got != 1 {
		t.Fatalf("queued = %d, want the held message deferred by the dialog path", got)
	}
	if got := readPipeFor(t, r, 100*time.Millisecond); got != "" {
		t.Fatalf("pasted %q into a pane with an open dialog", got)
	}
}

// The sender's initech send answers only after the held message is written.
func TestHandleIPCSend_AnswersOnlyAfterTheHeldMessageIsWritten(t *testing.T) {
	withTypingHold(t, 2*time.Second, 15*time.Second)
	p, r := i35wFreshPane(t, "eng2")
	clk := newFakeHoldClock()
	p.holdClk = clk
	p.mu.Lock()
	p.startupSettled = true
	p.mu.Unlock()
	clk.typedAgo(p, time.Second)
	tui := &TUI{panes: toPaneViews([]*Pane{p})}

	server, client := net.Pipe()
	defer client.Close()
	go tui.handleIPCSend(server, IPCRequest{Target: "eng2", Text: "MARKER_UIBP_IPC", Enter: true})
	answered := make(chan IPCResponse, 1)
	go func() {
		sc := bufio.NewScanner(client)
		if sc.Scan() {
			var resp IPCResponse
			_ = json.Unmarshal(sc.Bytes(), &resp)
			answered <- resp
		}
	}()

	awaitHoldTimer(t, clk)
	select {
	case resp := <-answered:
		t.Fatalf("sender answered %+v while the message was still held", resp)
	case <-time.After(100 * time.Millisecond):
	}
	if got := readPipeFor(t, r, 50*time.Millisecond); got != "" {
		t.Fatalf("wrote %q during the hold", got)
	}
	clk.Advance(time.Second)
	written := readPipeUntil(t, r, "MARKER_UIBP_IPC", 2*time.Second)
	if !strings.Contains(written, "MARKER_UIBP_IPC") {
		t.Fatalf("after the hold the pane got %q, want the message", written)
	}
	select {
	case resp := <-answered:
		if !resp.OK {
			t.Fatalf("sender told %+v, want delivered", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sender never answered after delivery")
	}
}

// readPipeFor collects whatever reaches the pane's PTY within d.
func readPipeFor(t *testing.T, r *os.File, d time.Duration) string {
	t.Helper()
	_ = r.SetReadDeadline(time.Now().Add(d))
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return b.String()
		}
	}
}

// A cross-machine forward is confirmed only after the peer's delivery, which
// may wait out the peer's typing hold: the forward wait must outlast the
// default cap with room for the belt.
func TestForwardSendTimeout_OutlastsTheDefaultTypingHold(t *testing.T) {
	cap := config.DefaultTypingHoldMaxS * time.Second
	if forwardSendTimeout < cap+20*time.Second {
		t.Fatalf("forward wait %s cannot outlast a %s hold plus delivery", forwardSendTimeout, cap)
	}
	if forwardSendTimeout >= 45*time.Second {
		t.Fatalf("forward wait %s reaches the sender's 45s budget; the sender would time out first", forwardSendTimeout)
	}
}

func ribbonText(s tcell.SimulationScreen, r Region) string {
	var b strings.Builder
	for x := r.X; x < r.X+r.W; x++ {
		mainc, _, _ := s.Get(x, r.Y+r.H-1)
		b.WriteString(mainc)
	}
	return b.String()
}

func TestRibbon_ShowsTheMailMarkOnlyWhileAMessageIsHeld(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.applyLayout()
	tui.layoutState.Overlay = false
	p := tui.panes[0].(*Pane)

	tui.render()
	if strings.Contains(ribbonText(s, p.region), "✉") {
		t.Fatal("mark shown with nothing held")
	}
	p.heldCount.Add(1)
	tui.render()
	if !strings.Contains(ribbonText(s, p.region), "✉") {
		t.Fatalf("no mark while a message is held: %q", ribbonText(s, p.region))
	}
	p.heldCount.Add(-1)
	tui.render()
	if strings.Contains(ribbonText(s, p.region), "✉") {
		t.Fatal("mark still shown after delivery")
	}
}

// Typing in another window reaches the pane through the daemon's stream pump:
// a key there counts as typing, a mouse report does not.
func TestStreamPump_AKeyFromAnotherWindowCountsAsTypingAMouseReportDoesNot(t *testing.T) {
	p, r := paneWithPipe(t)
	p.name = "eng2"
	p.alive = true
	d := &Daemon{panes: []*Pane{p}, ringBufs: map[string]*RingBuf{}, multiSinks: map[string]*MultiSink{}}
	server, client := net.Pipe()
	defer client.Close()
	go d.streamAgentLive(p, server, "window2")

	arrived := func(want string) {
		t.Helper()
		if got := readPipeUntil(t, r, want, 2*time.Second); !strings.Contains(got, want) {
			t.Fatalf("pane got %q, want %q", got, want)
		}
	}
	mouse := "\x1b[<64;10;5M"
	if _, err := client.Write([]byte(mouse)); err != nil {
		t.Fatal(err)
	}
	arrived(mouse)
	if _, typed := p.sinceOperatorKey(time.Now()); typed {
		t.Fatal("a mouse report from another window counted as typing")
	}
	if _, err := client.Write([]byte("k")); err != nil {
		t.Fatal(err)
	}
	arrived("k")
	if _, typed := p.sinceOperatorKey(time.Now()); !typed {
		t.Fatal("a key typed in another window did not count as typing")
	}
}

// readPipeUntil collects PTY bytes until want appears or d passes.
func readPipeUntil(t *testing.T, r *os.File, want string, d time.Duration) string {
	t.Helper()
	end := time.Now().Add(d)
	var b strings.Builder
	buf := make([]byte, 4096)
	for time.Now().Before(end) && !strings.Contains(b.String(), want) {
		_ = r.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
		n, _ := r.Read(buf)
		b.Write(buf[:n])
	}
	return b.String()
}

// Once a message is held, a later send waits its turn even when the operator
// has stopped typing by the time it arrives: nothing overtakes a held message.
func TestTypingHold_ALaterSendNeverOvertakesAHeldOne(t *testing.T) {
	withTypingHold(t, 2*time.Second, 15*time.Second)
	p, clk := holdPane(t)
	p.noteOperatorKey()
	first := startHold(p)
	next(t, clk, first)
	clk.Advance(2 * time.Second)
	releaseFirst, ended := next(t, clk, first)
	if !ended {
		t.Fatal("first message still held after the quiet window")
	}
	// First is now delivering; the operator is quiet. A new send arrives.
	later := startHold(p)
	select {
	case <-later:
		t.Fatal("a later send went while the held message was still being delivered")
	case <-time.After(50 * time.Millisecond):
	}
	releaseFirst()
	select {
	case r := <-later:
		r()
	case <-time.After(2 * time.Second):
		t.Fatal("the later send never went")
	}
}

// awaitHoldTimer waits for the hold to set its timer, failing instead of
// hanging when no hold happens.
func awaitHoldTimer(t *testing.T, clk *fakeHoldClock) {
	t.Helper()
	select {
	case <-clk.registered:
	case <-time.After(2 * time.Second):
		t.Fatal("the message was not held")
	}
}
