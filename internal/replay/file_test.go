package replay

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/recording"
)

// writeFixture writes a real recording with eng4's own Writer (ini-pqdy.2):
// output "a" at 0, a resize at 5ms, output "b" at 10ms, a trailer at 20ms.
func writeFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "eng1"+recording.FileExt)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w, err := recording.NewWriter(f, recording.Header{Version: recording.Version, Pane: "eng1", Start: time.Now(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return w.Output(0, []byte("a")) },
		func() error { return w.Resize(5*time.Millisecond, 120, 40) },
		func() error { return w.Output(10*time.Millisecond, []byte("b")) },
		func() error { return w.Trailer(20*time.Millisecond, 0) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestFileOpener_PlaysOnlyOutputAtItsRecordedTimes(t *testing.T) {
	path := writeFixture(t)
	clk := &fakeClock{now: time.Unix(1000, 0)}
	w := &stampWriter{clk: clk, start: clk.now}
	if err := Play(context.Background(), FileOpener(path), w, Options{}, clk); err != nil {
		t.Fatal(err)
	}
	if got := w.buf.String(); got != "ab" {
		t.Fatalf("played %q, want only the output bytes \"ab\" (the resize writes nothing)", got)
	}
	if w.at[0] != 0 || w.at[1] != 10*time.Millisecond {
		t.Fatalf("written at %v, want [0s 10ms]", w.at)
	}
}

func TestFileOpener_ALoopReopensTheFileEachPass(t *testing.T) {
	path := writeFixture(t)
	clk := &fakeClock{now: time.Unix(1000, 0)}
	var out bytes.Buffer
	if err := Play(context.Background(), FileOpener(path), &out, Options{Loop: true, StopAfter: 250 * time.Millisecond}, clk); err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(out.Bytes(), []byte("ab")); n < 2 {
		t.Fatalf("looped %d pass(es) over the file, want at least 2: %q", n, out.String())
	}
}

// A looping pass lasts until the recording's END (the trailer), not just its
// last output, so the silent tail is kept. The trailer is put at 300ms, past
// the 100ms floor, so pass 2 starting at 300ms can only come from reading it.
func TestFileOpener_ALoopingPassLastsUntilTheRecordingsEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tail"+recording.FileExt)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := recording.NewWriter(f, recording.Header{Version: recording.Version, Pane: "eng1", Start: time.Now(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Output(0, []byte("a"))
	_ = w.Output(10*time.Millisecond, []byte("b"))
	_ = w.Trailer(300*time.Millisecond, 0)
	f.Close()
	clk := &fakeClock{now: time.Unix(1000, 0)}
	sw := &stampWriter{clk: clk, start: clk.now}
	if err := Play(context.Background(), FileOpener(path), sw, Options{Loop: true, StopAfter: 400 * time.Millisecond}, clk); err != nil {
		t.Fatal(err)
	}
	if len(sw.at) < 3 || sw.at[2] != 300*time.Millisecond {
		t.Fatalf("writes at %v; pass 2 must start at the trailer (300ms), not at the last output or the floor", sw.at)
	}
}
