package tui

import (
	"io"
	"os/exec"
	"runtime"
	"testing"
	"testing/synctest"
	"time"

	"github.com/charmbracelet/x/vt"
)

// memPty is filePty without a file descriptor (ini-8yrl): a pane's child end
// over io.Pipe, so a send-path test can run inside a testing/synctest bubble.
// A goroutine blocked on an os.Pipe read is not durably blocked, so the
// bubble's clock would never advance past it; one blocked on io.Pipe is, as
// io.Pipe is channels and a mutex. Everything else matches filePty: the same
// size and name, and the same raw-mode answer, so the send path makes the
// same stash decision on both (TestMemPty_SameStashDecisionAsFilePty).
type memPty struct {
	*io.PipeWriter
	// turn serializes writers on a channel. io.Pipe's own lock is a
	// sync.Mutex, and a goroutine waiting on a mutex is not durably blocked:
	// with two writers and no reader (the stash key and the body) the bubble
	// clock would never advance. Waiting on a channel is durable.
	turn chan struct{}
}

func (m *memPty) Write(b []byte) (int, error) {
	m.turn <- struct{}{}
	defer func() { <-m.turn }()
	return m.PipeWriter.Write(b)
}

// Fd: there is no descriptor. Nothing on the send path reads it from a pane
// that answers TermRawMode (stash_mode.go reads that first).
func (m *memPty) Fd() uintptr                { return ^uintptr(0) }
func (m *memPty) Read([]byte) (int, error)   { return 0, io.EOF }
func (m *memPty) Resize(int, int) error      { return nil }
func (m *memPty) Size() (int, int, error)    { return 80, 24, nil }
func (m *memPty) Name() string               { return "test" }
func (m *memPty) Start(*exec.Cmd) error      { return nil }
func (m *memPty) TermRawMode() (bool, error) { return true, nil }

// paneWithMemPipe is paneWithPipe over memPty: the pane writes into the pipe
// and the returned reader is the child's side.
func paneWithMemPipe(t *testing.T) (*Pane, *io.PipeReader) {
	t.Helper()
	r, w := memPipe(t)
	return &Pane{ptmx: w}, r
}

// TestMemPty_SameStashDecisionAsFilePty is inference (2a) of ini-8yrl: the send
// path reads a pane's terminal mode to decide whether to type the stash key,
// and the in-memory fake must answer as the os.Pipe fake does, or a converted
// test would exercise a different send.
func TestMemPty_SameStashDecisionAsFilePty(t *testing.T) {
	fp, _ := paneWithPipe(t)
	mp, _ := paneWithMemPipe(t)
	fRaw, fErr := ptyRawMode(fp.ptmx)
	mRaw, mErr := ptyRawMode(mp.ptmx)
	if fRaw != mRaw || (fErr == nil) != (mErr == nil) {
		t.Fatalf("raw mode: filePty (%v, %v), memPty (%v, %v): the stash decision differs", fRaw, fErr, mRaw, mErr)
	}
	if fp.stashKeyAllowed() != mp.stashKeyAllowed() {
		t.Fatalf("stash allowed: filePty %v, memPty %v", fp.stashKeyAllowed(), mp.stashKeyAllowed())
	}
	if !mRaw {
		t.Fatal("memPty reads not raw: the send would skip the stash a Claude pane gets")
	}
}

// TestMemPty_AChildThatNeverReadsEndsTheSendOnTheBubbleClock is ini-8yrl's
// feed-it-nothing: a pane whose child never reads holds the body's write, and
// the send ends on its own bound (sendWriteBound), on the bubble's clock and
// never as a hang, with nothing reported delivered.
func TestMemPty_AChildThatNeverReadsEndsTheSendOnTheBubbleClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p, _ := paneWithMemPipe(t) // nobody reads the child's side
		p.name = "eng2"
		p.alive = true
		p.emu = vt.NewSafeEmulator(80, 24)
		// The emulator's replies (the stash key) go to the child, as
		// responseLoop sends them; that write blocks too, nobody reading.
		go func() {
			buf := make([]byte, 256)
			for {
				n, err := p.emu.Read(buf)
				if n > 0 {
					_, _ = p.ptmx.Write(buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()
		t.Cleanup(func() {
			if w, ok := p.emu.InputPipe().(*io.PipeWriter); ok {
				w.CloseWithError(io.EOF)
			}
			_ = p.ptmx.Close()
		})
		start := time.Now()
		v := pkinSend(p, "nobody reads this")
		if v.Outcome == DeliveryDelivered {
			t.Fatalf("a body the child never read answered %+v", v)
		}
		if waited := time.Since(start); waited < sendWriteBound {
			t.Fatalf("the send gave up after %v, before its bound %v", waited, sendWriteBound)
		}
		t.Logf("answered %+v after %v on the bubble clock", v, time.Since(start))
	})
}

// memPipe is a pane's child end for a rig built from parts: the reader is the
// child's side and the memPty is what the pane writes to.
func memPipe(t *testing.T) (*io.PipeReader, *memPty) {
	t.Helper()
	r, w := io.Pipe()
	t.Cleanup(func() { r.Close(); w.Close() })
	return r, &memPty{PipeWriter: w, turn: make(chan struct{}, 1)}
}

// sendBubble runs a send-path test on a synctest bubble's clock (ini-8yrl):
// the send's delays and windows (the submit delay, the confirm and echo
// windows, the drain gap) run as written without real time passing. Every
// converted test goes through it, so one mutant here withholds the clock from
// all of them.
func sendBubble(t *testing.T, f func(t *testing.T)) {
	t.Helper()
	// yamux pools its timers in a sync.Pool, so a timer made in one bubble
	// would be handed to the next test, in a bubble or not, and no code may
	// touch another bubble's timer (fatal, measured both ways, ini-t0y1).
	// Two collections empty a sync.Pool: before, and after.
	emptyPools := func() { runtime.GC(); runtime.GC() }
	emptyPools()
	defer emptyPools()
	synctest.Test(t, f)
}

// TestMemPty_ASendDeliversTheSameBytesAsOverOSPipe is ini-8yrl's fidelity
// check: a send in a synctest bubble over memPty reaches the fake child as the
// same bytes, and answers the same verdict, as that send over os.Pipe on the
// real clock, the rig the converted tests used before.
func TestMemPty_ASendDeliversTheSameBytesAsOverOSPipe(t *testing.T) {
	bodies := map[string]string{
		"one line":  nma5Body,
		"two lines": "[from b] first line of a report\nsecond line, marker 8YRL end",
		"hostile":   "hello\x1b[201~ls -la\r",
	}
	for name, body := range bodies {
		clean, _ := cleanMessage(body)
		send := func(t *testing.T, pipeFor func(*testing.T) (*Pane, io.ReadCloser)) (string, SendVerdict) {
			f := &pkinFake{body: clean, after: "❯ ", takes: func(int) bool { return true }}
			shortSubmitDelays(t)
			p, _ := pkinSendRigOn(t, f, 120, 40, pipeFor)
			v := pkinSend(p, body)
			all, _, _ := f.stream()
			return all, v
		}
		var overOS, overMem string
		var vOS, vMem SendVerdict
		t.Run(name+"/os.Pipe", func(t *testing.T) {
			overOS, vOS = send(t, func(t *testing.T) (*Pane, io.ReadCloser) { return paneWithPipe(t) })
		})
		t.Run(name+"/memPty in a bubble", func(t *testing.T) {
			sendBubble(t, func(t *testing.T) {
				overMem, vMem = send(t, func(t *testing.T) (*Pane, io.ReadCloser) { return paneWithMemPipe(t) })
			})
		})
		if overOS == "" || overOS != overMem {
			t.Errorf("%s: the child got %q over os.Pipe and %q over memPty", name, overOS, overMem)
		}
		if vOS.Outcome != vMem.Outcome || vOS.Reason != vMem.Reason {
			t.Errorf("%s: verdict %+v over os.Pipe, %+v over memPty", name, vOS, vMem)
		}
		t.Logf("%s: %d bytes, %q, verdict %s on both", name, len(overOS), overOS, vOS.Outcome)
	}
}
