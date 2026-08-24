package tui

import (
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// A replay and live fan-out are two different goroutines writing one stream.
// Without serialization they splice, and a spliced CSI sequence is the exact
// corruption ini-dr03 removes -- reintroduced from the sender side.
func TestSyncStream_ConcurrentWritersNeverSplice(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	var got []byte
	var readWG sync.WaitGroup
	readWG.Add(1)
	go func() {
		defer readWG.Done()
		buf := make([]byte, 64*1024)
		for {
			n, err := client.Read(buf)
			if n > 0 {
				got = append(got, buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()

	// A writer that YIELDS MID-WRITE. net.Pipe hands each 512-byte Write to a
	// reader with a bigger buffer in one piece, so it cannot splice however
	// the lock behaves -- an instrument that cannot produce the phenomenon
	// reports its absence as evidence. Measured: with the lock removed, the
	// net.Pipe version stayed green. This one splits every write in half and
	// yields between the halves, which is exactly the interleaving a real
	// concurrent writer can achieve.
	ss := newSyncStream(&halfWriter{w: server})
	live := []byte(strings.Repeat("L", 512))
	replay := []byte(strings.Repeat("R", 512))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); ss.Write(live) }()
		go func() { defer wg.Done(); ss.Write(replay) }()
	}
	wg.Wait()
	server.Close()
	readWG.Wait()

	// Every write must appear as an unbroken run of its own byte. A splice
	// shows up as a run shorter than 512 somewhere in the middle.
	s := string(got)
	for _, run := range splitRuns(s) {
		if len(run)%512 != 0 {
			t.Fatalf("SPLICED: found a run of %d %q bytes, not a multiple of the 512-byte writes; "+
				"a replay was interleaved into a live chunk", len(run), run[:1])
		}
	}
	if len(got) != 20*2*512 {
		t.Errorf("lost bytes: got %d, want %d", len(got), 20*2*512)
	}
}

// splitRuns breaks a string into maximal runs of one repeated byte.
func splitRuns(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		j := i
		for j < len(s) && s[j] == s[i] {
			j++
		}
		out = append(out, s[i:j])
		i = j
	}
	return out
}

// halfWriter writes p in two halves, yielding between them.
type halfWriter struct{ w net.Conn }

func (h *halfWriter) Write(p []byte) (int, error) {
	mid := len(p) / 2
	n1, err := h.w.Write(p[:mid])
	if err != nil {
		return n1, err
	}
	runtime.Gosched()
	n2, err := h.w.Write(p[mid:])
	return n1 + n2, err
}

func (h *halfWriter) Close() error                       { return h.w.Close() }
func (h *halfWriter) Read(p []byte) (int, error)         { return h.w.Read(p) }
func (h *halfWriter) LocalAddr() net.Addr                { return h.w.LocalAddr() }
func (h *halfWriter) RemoteAddr() net.Addr               { return h.w.RemoteAddr() }
func (h *halfWriter) SetDeadline(t time.Time) error      { return h.w.SetDeadline(t) }
func (h *halfWriter) SetReadDeadline(t time.Time) error  { return h.w.SetReadDeadline(t) }
func (h *halfWriter) SetWriteDeadline(t time.Time) error { return h.w.SetWriteDeadline(t) }

var _ io.Writer = (*syncStream)(nil)
