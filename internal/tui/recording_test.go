package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/nmelo/initech/internal/recording"
)

// Tests for ini-pqdy.2: per-pane PTY recording.

// withRecordingSession turns recording on for the test and off after it.
func withRecordingSession(t *testing.T) string {
	t.Helper()
	dir, err := startRecordingSession(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stopRecordingSession)
	return dir
}

// screenPrint is every cell's content and style plus the cursor, so two
// screens compare on what a terminal would show, not just the text.
func screenPrint(e *vt.SafeEmulator, cols, rows int) string {
	var b strings.Builder
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			if c := e.CellAt(x, y); c != nil {
				fmt.Fprintf(&b, "%s|%v;", c.Content, c.Style)
			} else {
				b.WriteString("_;")
			}
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "cursor=%v", e.CursorPosition())
	return b.String()
}

func recWaitForScreen(t *testing.T, p *Pane, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		cols, rows := p.emuSize()
		for y := 0; y < rows; y++ {
			if strings.Contains(p.emu.RowText(y, cols), want) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%q never appeared on the pane", want)
}

// replay feeds a recording into a fresh emulator at the recorded size,
// applying resize events, and returns it with its final size.
func replay(t *testing.T, path string) (*vt.SafeEmulator, int, int, []recording.Record) {
	t.Helper()
	rd, err := recording.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	h := rd.Header()
	cols, rows := h.Cols, h.Rows
	emu := vt.NewSafeEmulator(cols, rows)
	// The emulator answers queries on its input pipe; drain it or a reply
	// blocks Write (the same reason Pane runs responseLoop).
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { emu.Close() })
	var recs []recording.Record
	for {
		r, err := rd.Next()
		if errors.Is(err, io.EOF) {
			return emu, cols, rows, recs
		}
		if err != nil {
			t.Fatal(err)
		}
		recs = append(recs, r)
		switch r.Kind {
		case recording.KindOutput:
			emu.Write(r.Data)
		case recording.KindResize:
			cols, rows = r.Cols, r.Rows
			emu.Resize(cols, rows)
		}
	}
}

// AC1: a real pane's recording, replayed into a fresh emulator at the
// recorded size, ends on the same screen the pane ended on -- cell for cell,
// style for style, with the cursor -- across a resize in the middle.
func TestRecording_ReplayReproducesThePanesFinalScreen(t *testing.T) {
	dir := withRecordingSession(t)
	script := `printf '\033[2J\033[H\033[1;31mred bold\033[0m plain\r\n'
printf 'a long line that will wrap at forty columns and keep on going past it\r\n'
printf '\033[5;10Hplaced\033[K\033[7;1H\033[44mblue bg\033[0m\r\n'
printf 'PART1\r\n'
sleep 0.4
printf 'after resize: another long line wrapping at a different width now\r\n'
printf '\033[3;3H\033[2Kcleared row three\033[9;1HDONE-MARK'
sleep 5`
	p, err := NewPane(PaneConfig{Name: "rec-roundtrip", Command: []string{"sh", "-c", script}}, 12, 40)
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	recWaitForScreen(t, p, "PART1")
	p.Resize(10, 30)
	recWaitForScreen(t, p, "DONE-MARK")
	cols, rows := p.emuSize()
	want := screenPrint(p.emu, cols, rows)
	p.Close()

	path := filepath.Join(dir, "rec-roundtrip"+recording.FileExt)
	emu, rcols, rrows, recs := replay(t, path)
	if rcols != cols || rrows != rows {
		t.Fatalf("recording ends at %dx%d, the pane at %dx%d", rcols, rrows, cols, rows)
	}
	var resizes, outputs int
	for _, r := range recs {
		switch r.Kind {
		case recording.KindResize:
			resizes++
		case recording.KindOutput:
			outputs++
		}
	}
	if resizes == 0 || outputs == 0 {
		t.Fatalf("recording has %d output and %d resize records; the fixture did not exercise both", outputs, resizes)
	}
	if last := recs[len(recs)-1]; last.Kind != recording.KindTrailer || last.Dropped != 0 {
		t.Errorf("last record = %+v, want a clean trailer with nothing dropped", last)
	}
	if got := screenPrint(emu, cols, rows); got != want {
		t.Errorf("replayed screen differs from the pane's final screen\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

// AC3: recording is off by default -- a pane made with no session open has
// no recorder and writes no file.
func TestRecording_OffByDefault(t *testing.T) {
	stopRecordingSession()
	p, err := NewPane(PaneConfig{Name: "rec-off", Command: []string{"sh", "-c", "printf hi; sleep 5"}}, 10, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.rec != nil {
		t.Error("a pane created with recording off has a recorder")
	}
}

// The session directory is <dir>/<YYYYMMDD-HHMMSS>-<pid>, and a pane name
// recorded twice in one session (restart, resume) gets a numbered second
// file instead of overwriting the first.
func TestRecording_SessionDirAndRespawnedPaneFiles(t *testing.T) {
	base := t.TempDir()
	now := time.Date(2026, 10, 10, 9, 8, 7, 0, time.Local)
	dir, err := startRecordingSession(base, now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stopRecordingSession)
	if want := filepath.Join(base, fmt.Sprintf("20261010-090807-%d", os.Getpid())); dir != want {
		t.Errorf("session dir = %s, want %s", dir, want)
	}
	a := openPaneRecorder("eng1", 80, 24)
	b := openPaneRecorder("eng1", 80, 24)
	if a == nil || b == nil {
		t.Fatal("recorder not opened")
	}
	closePaneRecorder("eng1", a)
	closePaneRecorder("eng1", b)
	for _, f := range []string{"eng1.irec", "eng1-2.irec"} {
		rd, err := recording.Open(filepath.Join(dir, f))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if h := rd.Header(); h.Pane != "eng1" || h.Cols != 80 || h.Rows != 24 {
			t.Errorf("%s header = %+v", f, h)
		}
		rd.Close()
	}
}
