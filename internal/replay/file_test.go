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
	if err := Play(context.Background(), FileOpener(path), &out, Options{Loop: true, StopAfter: 35 * time.Millisecond}, clk); err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(out.Bytes(), []byte("ab")); n < 2 {
		t.Fatalf("looped %d pass(es) over the file, want at least 2: %q", n, out.String())
	}
}
