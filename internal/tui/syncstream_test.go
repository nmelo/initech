package tui

import (
	"io"
	"net"
	"strings"
	"sync"
	"testing"
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

	ss := newSyncStream(server)
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

var _ io.Writer = (*syncStream)(nil)
