// multisink_stall_test.go instruments the stall a slow-but-alive window 2
// imposes on window 1's rendering through writeComplete's retry budget
// (ini-tk7z), pins the budget against the z8o wedge-detection constants, and
// caps the short-write spin qa1 found.
package tui

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/hashicorp/yamux"
)

// slowWriter is the worst-case slow-but-alive window: every attempt blocks
// for delay and then reports the session's send window full. It records how
// many attempts each MultiSink.Write cost it.
type slowWriter struct {
	delay    time.Duration
	mu       sync.Mutex
	attempts int
}

func (s *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(s.delay)
	s.mu.Lock()
	s.attempts++
	s.mu.Unlock()
	return 0, yamux.ErrConnectionWriteTimeout
}

func (s *slowWriter) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

// The bound, derived from the constants rather than read off a comment:
// the worst-case stall one chunk can impose on readLoop must stay ABOVE the
// z8o worst-case wedge detection (so a genuinely wedged window is torn down
// by keepalive before writeComplete gives up on it) and must not exceed 14s
// (two attempts at the session write timeout) -- the bead's tightening.
func TestWriteComplete_StallBound_AboveWedgeDetection_AtMost14s(t *testing.T) {
	worstStall := time.Duration(maxTransientRetries+1) * windowConnectionWriteTimeout
	wedgeDetect := windowKeepAliveInterval + windowConnectionWriteTimeout
	if worstStall <= wedgeDetect {
		t.Fatalf("worst-case stall %v must exceed the z8o wedge detection %v, or a wedged window is dropped here before keepalive can tell wedged from busy", worstStall, wedgeDetect)
	}
	if worstStall > 14*time.Second {
		t.Fatalf("worst-case stall %v exceeds 14s: maxTransientRetries = %d, want 1 (two attempts)", worstStall, maxTransientRetries)
	}
}

// THE INSTRUMENT: the real Pane.readLoop over a pipe PTY, tee'd into a
// MultiSink holding one worst-case slow writer. Chunk A's sink write stalls
// the loop for (attempts × delay); chunk B cannot reach window 1's EMULATOR
// until that returns. The attempt count is the deterministic assertion; the
// measured latency is logged and recorded on the bead (timing under CI load
// is an instrument, not a claim).
func TestReadLoop_SlowWindow2StallsWindow1Frame_Measured(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pipe-PTY timing instrument in short mode")
	}
	const delay = 100 * time.Millisecond
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	emu := vt.NewSafeEmulator(80, 10)
	p := &Pane{name: "eng1", emu: emu, alive: true, ptmx: &filePty{r}}
	slow := &slowWriter{delay: delay}
	ms := NewMultiSink()
	ms.Add(slow)
	p.SetNetworkSink(ms)

	done := make(chan struct{})
	go func() { p.readLoop(); close(done) }()

	if _, err := w.Write([]byte("AAAA")); err != nil {
		t.Fatal(err)
	}
	// Let chunk A be read (it stalls in the sink); then chunk B is what the
	// operator is waiting to see in window 1.
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	if _, err := w.Write([]byte("BBBB")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var latency time.Duration
	for {
		if strings.Contains(emu.RowText(0, 80), "BBBB") {
			latency = time.Since(start)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("chunk B never reached window 1's emulator behind the slow writer")
		}
		time.Sleep(5 * time.Millisecond)
	}
	w.Close()
	<-done

	wantAttempts := maxTransientRetries + 1
	// Two chunks, each costing every attempt (the writer never succeeds and
	// is dropped after chunk A, so chunk B costs none).
	if got := slow.count(); got != wantAttempts {
		t.Errorf("slow writer saw %d attempts for chunk A, want %d (maxTransientRetries+1)", got, wantAttempts)
	}
	t.Logf("MEASURED window-1 frame latency for chunk B behind one worst-case slow writer (delay %v): %v; attempts per chunk = %d; predicted stall = %v",
		delay, latency, slow.count(), time.Duration(wantAttempts)*delay)
	if latency > 5*time.Duration(wantAttempts)*delay {
		t.Errorf("latency %v is wildly beyond the predicted %v", latency, time.Duration(wantAttempts)*delay)
	}
}

// qa1's finding: a writer that returns (0, nil) -- zero progress and no
// error, an io.Writer contract violation -- spun writeComplete forever. The
// harness runs it in a goroutine with a timeout so a spin FAILS instead of
// hanging the suite.
type zeroProgressWriter struct{ calls int }

func (z *zeroProgressWriter) Write(p []byte) (int, error) { z.calls++; return 0, nil }

func TestWriteComplete_ZeroProgressWriterIsCapped(t *testing.T) {
	z := &zeroProgressWriter{}
	res := make(chan bool, 1)
	go func() { res <- writeComplete(z, []byte("data")) }()
	select {
	case ok := <-res:
		if ok {
			t.Fatal("a writer that never makes progress reported as complete")
		}
		if z.calls > maxZeroProgressWrites+1 {
			t.Errorf("writer called %d times, want at most %d before being declared finished", z.calls, maxZeroProgressWrites+1)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writeComplete spun for 2s on a (0, nil) writer -- no attempt cap")
	}
}

// A short write WITH progress and no error is the contract edge worth
// tolerating: the remainder is written on the next call, unbounded because
// progress bounds it.
type trickleWriter struct{ got []byte }

func (tw *trickleWriter) Write(p []byte) (int, error) { tw.got = append(tw.got, p[0]); return 1, nil }

func TestWriteComplete_ShortWriteWithProgressCompletes(t *testing.T) {
	tw := &trickleWriter{}
	if !writeComplete(tw, []byte("hello")) || string(tw.got) != "hello" {
		t.Fatalf("trickling writer: complete=%v got=%q", string(tw.got) == "hello", tw.got)
	}
}

// The false invariant must not come back by copy-paste: pane.go asserted
// twice that "network backpressure cannot stall local rendering", which the
// retry budget made false across chunks (shipper nearly took it at face
// value).
func TestPaneSource_DoesNotClaimBackpressureCannotStallRendering(t *testing.T) {
	src, err := os.ReadFile("pane.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "cannot stall") {
		t.Fatal("pane.go still asserts network backpressure cannot stall local rendering; it can, for up to writeComplete's bound -- say what is true")
	}
}
