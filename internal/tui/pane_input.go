package tui

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/xpty"
)

// A CHILD THAT STOPS READING WEDGES ITS OWN PANE AND NOTHING ELSE (ini-7rd).
//
// A write to a PTY master blocks once the child's input is full, and a child
// that stops reading never empties it. That single blocking write sat under
// everything that talks to a child: every operator key, paste and mouse event
// on the main goroutine, every send (holding sendMu, which the main goroutine's
// suspend then waited on), and the emulator's own replies to terminal queries,
// which readLoop writes from inside emu.Write holding renderMu. Any one of them
// froze the whole rig: hover's main loop sat 7 minutes on one such write.
//
// So exactly one place writes to a child: the drain below, which owns
// ptmx.Write. Everything else queues, and queueing never blocks. responseLoop
// keeps reading the emulator's pipe and hands the bytes here, so emu.SendKey
// and the emulator's replies cannot block either: a full queue drops, it never
// stops the reader. A blocked write cannot be cancelled, only abandoned, so its
// bytes may still land if the child resumes; that is why the send path below
// never resends a body any of which reached the drain.

const (
	// inputQueueCap is how many writes a pane may have waiting. A key is one
	// item and a paste is one item of any size. Past it the pane is wedged and
	// its input is dropped: a pane that is not reading cannot take keys anyway.
	inputQueueCap = 256
	// sendWriteBound is how long a send waits for one of its own writes.
	sendWriteBound = 2 * time.Second
	// inputChunkSize splits a large write, so one huge paste does not hold a
	// single write(2) for its whole length.
	inputChunkSize = 64 * 1024
)

// stoppedReadingVerdictReason is the sender's verdict for a body partly handed
// to a write that never returned. It states the duplicate risk, because that
// risk is why the message is not sent again.
const stoppedReadingVerdictReason = "the pane stopped reading its input while this message was being written; " +
	"it will not be sent again, because a resend could duplicate it if the agent resumes"

var errInputClosed = errors.New("pane input closed")

// childWrite is what became of a write a send waited for.
type childWrite int

const (
	childWritten    childWrite = iota // Every byte reached the child's terminal.
	childNotReached                   // None of it reached the drain: safe to hold and redeliver.
	childPartial                      // It reached the drain and did not finish: never resend.
)

// inputItem is one write to the child.
type inputItem struct {
	data []byte
	done chan struct{} // Closed when the write ended; nil when nobody waits.
	n    int           // Bytes written. Read after done is closed.
	err  error         // Read after done is closed.
}

// paneInput is the pane's input queue. The zero value is ready: the drain
// exists only while there is something to write, so a pane has no goroutine to
// start or stop for it.
type paneInput struct {
	mu       sync.Mutex
	q        []*inputItem
	current  *inputItem // The write in progress.
	draining bool
	closed   bool
	wedged   bool         // Set by a full queue or a send's bound; cleared when the stuck write returns.
	dropped  int          // Items dropped this wedge. Keys and emulator replies share the pipe, so one count.
	pending  atomic.Int32 // Writes on their way here: a paste waits its turn for sendMu off main.
}

// writeInput queues data for the child and returns at once. It never blocks, so
// it is safe on the main goroutine. It reports false when the data was dropped:
// the pane is closed or wedged. data is copied.
func (p *Pane) writeInput(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	return p.pushInput(&inputItem{data: append([]byte(nil), data...)})
}

// writeInputWait queues data and waits for it, at most sendWriteBound, and no
// longer than the pane stays open. It returns what became of the data, and the
// write's own error when it failed rather than stuck. On expiry the pane is
// marked wedged, so nothing is redelivered into it until the stuck write
// returns.
func (p *Pane) writeInputWait(data []byte) (childWrite, error) {
	it := &inputItem{data: data, done: make(chan struct{})}
	if !p.pushInput(it) {
		return childNotReached, nil
	}
	timer := time.NewTimer(sendWriteBound)
	defer timer.Stop()
	select {
	case <-it.done:
		return it.outcome(), it.err
	case <-timer.C:
	case <-p.closingC():
	}
	return p.abandonInput(it)
}

// outcome reads a finished item. Call it after done is closed.
func (it *inputItem) outcome() childWrite {
	switch {
	case it.err == nil:
		return childWritten
	case it.n == 0:
		return childNotReached
	}
	return childPartial
}

// abandonInput ends a send's wait on it: still queued, it is taken back and
// nothing of it reaches the child; in the drain, it can no longer be recalled.
func (p *Pane) abandonInput(it *inputItem) (childWrite, error) {
	in := &p.input
	in.mu.Lock()
	for i, q := range in.q {
		if q == it {
			in.q = append(in.q[:i:i], in.q[i+1:]...)
			// Only a write stuck ahead of it can clear a wedge; with none, and
			// on a close, nothing stopped reading.
			begin := in.current != nil && !in.closed && in.markWedgedLocked()
			in.mu.Unlock()
			p.reportWedge(begin)
			return childNotReached, nil
		}
	}
	if in.current != it {
		in.mu.Unlock() // It finished as the bound expired.
		<-it.done
		return it.outcome(), it.err
	}
	begin := !in.closed && in.markWedgedLocked()
	in.mu.Unlock()
	p.reportWedge(begin)
	return childPartial, nil
}

// pushInput adds it to the queue and starts the drain if none is running.
func (p *Pane) pushInput(it *inputItem) bool {
	in := &p.input
	in.mu.Lock()
	if in.closed {
		in.mu.Unlock()
		return false
	}
	if len(in.q) >= inputQueueCap {
		begin := in.markWedgedLocked()
		in.dropped++
		in.mu.Unlock()
		p.reportWedge(begin)
		return false
	}
	in.q = append(in.q, it)
	start := !in.draining
	in.draining = true
	in.mu.Unlock()
	if start {
		go p.drainInput()
	}
	return true
}

// markWedgedLocked marks the pane wedged and reports whether this began a
// wedge. The caller holds in.mu and calls reportWedge after releasing it.
func (in *paneInput) markWedgedLocked() bool {
	begin := !in.wedged
	in.wedged = true
	return begin
}

// reportWedge is the one log line and the one operator notice of a wedge.
func (p *Pane) reportWedge(begin bool) {
	if !begin {
		return
	}
	LogWarn("input", "pane stopped reading its input; dropping input until it reads again",
		"pane", p.Name(), "queue_cap", inputQueueCap)
	p.reportEvent(AgentEvent{
		Type:   EventAgentStalled,
		Pane:   p.Name(),
		Detail: p.Name() + " stopped reading its input; keys and messages to it are not going in",
	})
}

// drainInput writes the queue in order and exits when it is empty. It is the
// only code that writes to a child, and at most one runs for a pane.
func (p *Pane) drainInput() {
	in := &p.input
	for {
		in.mu.Lock()
		if len(in.q) == 0 {
			in.draining = false
			in.mu.Unlock()
			return
		}
		it := in.q[0]
		in.q = in.q[1:]
		in.current = it
		in.mu.Unlock()

		it.n, it.err = p.writeChild(it.data)

		in.mu.Lock()
		in.current = nil
		wasWedged, dropped := in.wedged, in.dropped
		in.wedged, in.dropped = false, 0
		in.mu.Unlock()
		if it.done != nil {
			close(it.done)
		}
		if wasWedged {
			// No notice: the stuck write returning is the pane taking input
			// again. The counts are this wedge's record, keys and emulator
			// replies together because they arrive on one pipe.
			LogInfo("input", "pane reads its input again", "pane", p.Name(),
				"keys_and_replies_dropped", dropped)
			p.maybeDrainModalQueue() // Held mail goes in now.
		}
	}
}

// writeChild writes data to the child's terminal, in chunks.
func (p *Pane) writeChild(data []byte) (int, error) {
	ptmx := p.ptyHandle()
	if ptmx == nil {
		return 0, errInputClosed
	}
	written := 0
	for len(data) > 0 {
		n := min(len(data), inputChunkSize)
		w, err := ptmx.Write(data[:n])
		written += w
		if err != nil {
			return written, err
		}
		data = data[n:]
	}
	return written, nil
}

// closeInput ends the pane's input: what is queued is dropped, so no byte is
// written after a close, and senders waiting on it are released. A write
// already in progress cannot be recalled.
func (p *Pane) closeInput() {
	in := &p.input
	in.mu.Lock()
	in.closed = true
	q := in.q
	in.q = nil
	in.mu.Unlock()
	for _, it := range q {
		it.err = errInputClosed
		if it.done != nil {
			close(it.done)
		}
	}
}

// inputWedged reports that nothing should be sent into the pane yet: a write is
// stuck, or the pane is closing.
func (p *Pane) inputWedged() bool {
	in := &p.input
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.wedged || in.closed
}

// awaitInputDrained waits until everything queued has been written, at most d,
// and reports whether it was. For code that must know the bytes went out.
func (p *Pane) awaitInputDrained(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		p.input.mu.Lock()
		idle := len(p.input.q) == 0 && p.input.current == nil && p.input.pending.Load() == 0
		p.input.mu.Unlock()
		if idle {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// closingC returns the channel closed when the pane starts closing.
func (p *Pane) closingC() <-chan struct{} {
	p.closingOnce.Do(func() { p.closingCh = make(chan struct{}) })
	return p.closingCh
}

// isClosing reports that the pane has started closing.
func (p *Pane) isClosing() bool {
	select {
	case <-p.closingC():
		return true
	default:
		return false
	}
}

// ptyHandle reads the PTY field. Teardown clears it off the main goroutine, so
// code outside sendMu reads it here. ptyMu is a leaf lock: resize calls this
// holding the emulator's lock and renderMu, and nothing waits under ptyMu.
func (p *Pane) ptyHandle() xpty.Pty {
	p.ptyMu.Lock()
	defer p.ptyMu.Unlock()
	return p.ptmx
}

// hasPTY reports whether the pane still has a PTY.
func (p *Pane) hasPTY() bool { return p.ptyHandle() != nil }

// markClosing tells a send in flight that the pane is going, and ends its
// input. It is safe to call twice, and it never blocks.
func (p *Pane) markClosing() {
	p.closingC()
	p.closedOnce.Do(func() { close(p.closingCh) })
	p.closeInput()
}
