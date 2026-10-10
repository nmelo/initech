package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/recording"
)

func replayFixture(t *testing.T) string {
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
	_ = w.Output(0, []byte("hello "))
	_ = w.Output(20*time.Millisecond, []byte("\x1b[1mworld\x1b[0m"))
	_ = w.Trailer(30*time.Millisecond, 0)
	return path
}

// runReplayCmd runs `initech replay` with the given stdin and resets the
// command's package-level flags afterwards.
func runReplayCmd(t *testing.T, stdin io.Reader, args ...string) (string, error) {
	t.Helper()
	t.Cleanup(func() {
		replayOffset, replayScale, replayLoop, replayStopAfter = 0, 1, false, 0
		rootCmd.SetIn(nil)
		rootCmd.SetArgs(nil)
	})
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetIn(stdin)
	rootCmd.SetArgs(append([]string{"replay"}, args...))
	err := rootCmd.Execute()
	return out.String(), err
}

func TestReplayCmd_WritesTheRecordedBytesExactly(t *testing.T) {
	stdinR, stdinW := io.Pipe() // a pane's stdin: open for the whole run
	t.Cleanup(func() { stdinW.Close() })
	got, err := runReplayCmd(t, stdinR, replayFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if want := "hello \x1b[1mworld\x1b[0m"; got != want {
		t.Fatalf("replay wrote %q, want %q byte for byte", got, want)
	}
}

// Behaviour 4: the pane closing (stdin EOF) ends even a looping replay, cleanly.
func TestReplayCmd_StdinCloseEndsALoopingReplayCleanly(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	go func() { time.Sleep(100 * time.Millisecond); stdinW.Close() }()
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := runReplayCmd(t, stdinR, replayFixture(t), "--loop")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stdin close returned %v, want a clean exit", err)
		}
		if el := time.Since(start); el > 2*time.Second {
			t.Fatalf("took %s to notice stdin closed", el)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a looping replay did not stop when its stdin closed")
	}
}

func TestReplayCmd_RefusesAFileThatIsNotARecording(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(bad, []byte("just text"), 0o644)
	_, err := runReplayCmd(t, strings.NewReader(""), bad)
	if err == nil {
		t.Fatal("replay accepted a file that is not a recording")
	}
}
