package tui

import (
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
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

// spawnPty is a woken agent's pane end in memory, for the fake spawners
// (ini-t0y1): the child has said "up" (the read loop sees output, so the wake
// settles) and then says nothing until closed. A write fails as a write to
// the read end of the os.Pipe the spawners used before does (EBADF), so a
// send behaves as before; capturePty overrides Write to record instead.
// Every wait on it is durable, so resume tests can run in a synctest bubble.
type spawnPty struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func newSpawnPty(t *testing.T) *spawnPty {
	t.Helper()
	r, w := io.Pipe()
	go func() { _, _ = w.Write([]byte("up\r\n")) }() // ends when read, or at Close
	t.Cleanup(func() { w.Close() })
	return &spawnPty{r: r, w: w}
}

func (s *spawnPty) Read(b []byte) (int, error) { return s.r.Read(b) }
func (s *spawnPty) Write([]byte) (int, error) {
	return 0, &os.PathError{Op: "write", Path: "test", Err: syscall.EBADF}
}
func (s *spawnPty) Close() error {
	s.w.Close()
	return s.r.Close()
}
func (s *spawnPty) Fd() uintptr                { return ^uintptr(0) }
func (s *spawnPty) Resize(int, int) error      { return nil }
func (s *spawnPty) Size() (int, int, error)    { return 80, 24, nil }
func (s *spawnPty) Name() string               { return "test" }
func (s *spawnPty) Start(*exec.Cmd) error      { return nil }
func (s *spawnPty) TermRawMode() (bool, error) { return true, nil }

// pipeRecord reads a child's side as the pane writes, as an os.Pipe's buffer
// did for the tests that read it later (ini-t0y1).
type pipeRecord struct {
	mu   sync.Mutex
	b    strings.Builder
	read int
	done chan struct{}
}

func recordPipe(t *testing.T, r io.Reader) *pipeRecord {
	t.Helper()
	rec := &pipeRecord{done: make(chan struct{})}
	go func() {
		defer close(rec.done)
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			rec.mu.Lock()
			rec.b.Write(buf[:n])
			rec.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return rec
}

// takeAfter waits d, then returns what arrived since the last take: what
// readPipeFor read from an os.Pipe with a d read deadline.
func (rec *pipeRecord) takeAfter(d time.Duration) string {
	time.Sleep(d)
	return rec.take()
}

// takeUntil returns what arrived since the last take once it holds want, or
// at d: what readPipeUntil read from an os.Pipe.
func (rec *pipeRecord) takeUntil(want string, d time.Duration) string {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		rec.mu.Lock()
		has := strings.Contains(rec.b.String()[rec.read:], want)
		rec.mu.Unlock()
		if has {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return rec.take()
}

func (rec *pipeRecord) take() string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	s := rec.b.String()[rec.read:]
	rec.read = len(rec.b.String())
	return s
}

// i35wFreshMemPane is i35wFreshPane in memory, for a test in a synctest
// bubble: the child's side is recorded as it is written.
func i35wFreshMemPane(t *testing.T, name string) (*Pane, *pipeRecord) {
	t.Helper()
	p := newEmuPane(name, 80, 24)
	r, w := memPipe(t)
	rec := recordPipe(t, r)
	t.Cleanup(func() {
		// End newEmuPane's drain and the recorder, so nothing outlives the
		// bubble.
		if pw, ok := p.emu.InputPipe().(*io.PipeWriter); ok {
			pw.CloseWithError(io.EOF)
		}
		w.Close()
		<-rec.done
	})
	p.ptmx = w
	p.cfg.NoBracketedPaste = true
	p.mu.Lock()
	p.alive = true
	p.startedAt = time.Now()
	p.lastOutputTime = time.Now()
	p.mu.Unlock()
	return p, rec
}

// TestPkinFake_ANewlineInsideThePasteIsNotAnEnter (ini-vlgq): the fake
// counted the newline in a two-line paste as an Enter and cleared the box
// 20 ms after painting the body; the os.Pipe half of the fidelity check then
// withheld its submit whenever its first echo sample missed those 20 ms.
func TestPkinFake_ANewlineInsideThePasteIsNotAnEnter(t *testing.T) {
	for chunk, want := range map[string]int32{
		"\x1b[200~first line\nsecond line\x1b[201~":   0,
		"\x1b[200~first line\nsecond line\x1b[201~\r": 1,
		"\r":                         1,
		"\x1b[200~one line\x1b[201~": 0,
	} {
		f := &pkinFake{body: "first line\nsecond line", after: "❯ ", takes: func(int) bool { return false },
			screen: func(string) string { return "" }}
		f.p = &Pane{emu: vt.NewSafeEmulator(40, 5)}
		f.saw(chunk)
		if got := f.enters.Load(); got != want {
			t.Errorf("%q: %d Enters, want %d", chunk, got, want)
		}
	}
}
