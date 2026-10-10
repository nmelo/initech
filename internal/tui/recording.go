package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/nmelo/initech/internal/recording"
)

// PTY recording (ini-pqdy.2). When a session starts with recording on, every
// local pane writes the exact bytes its child prints, with timing and size
// changes, to <dir>/<YYYYMMDD-HHMMSS>-<pid>/<pane>.irec. Off by default; the
// lab replays these files (ini-pqdy.3). Input is never recorded, and panes
// hosted by a remote daemon are not recorded here.
//
// The session is process-wide because NewPane is called from a dozen spawn
// paths (launch, restart, resume, hot-add) that do not share a TUI handle;
// one session per process is also the only shape a recording session has.

var (
	recSessionMu  sync.Mutex
	recSessionDir string         // Empty: recording is off.
	recPaneCount  map[string]int // Files opened per pane name this session.
)

// startRecordingSession creates the session directory under dir and turns
// recording on for every pane created after it. Returns the directory.
func startRecordingSession(dir string, now time.Time) (string, error) {
	session := filepath.Join(dir, now.Format("20060102-150405")+"-"+strconv.Itoa(os.Getpid()))
	if err := os.MkdirAll(session, 0o700); err != nil {
		return "", fmt.Errorf("recording directory: %w", err)
	}
	recSessionMu.Lock()
	recSessionDir = session
	recPaneCount = map[string]int{}
	recSessionMu.Unlock()
	return session, nil
}

// stopRecordingSession turns recording off for panes created from now on.
// Panes already recording keep their files until they close.
func stopRecordingSession() {
	recSessionMu.Lock()
	recSessionDir = ""
	recPaneCount = nil
	recSessionMu.Unlock()
}

// openPaneRecorder returns a recorder for a new pane, or nil when recording
// is off or the file cannot be created (logged; the pane runs unrecorded).
// A pane name seen before in this session (a restart or resume) gets a
// numbered file, so a respawn never overwrites the earlier recording.
func openPaneRecorder(name string, cols, rows int) *recording.Recorder {
	recSessionMu.Lock()
	dir := recSessionDir
	if dir == "" {
		recSessionMu.Unlock()
		return nil
	}
	recPaneCount[name]++
	n := recPaneCount[name]
	recSessionMu.Unlock()

	base := name
	if n > 1 {
		base = name + "-" + strconv.Itoa(n)
	}
	path := filepath.Join(dir, base+recording.FileExt)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		LogWarn("recording", "cannot create pane recording; pane runs unrecorded", "pane", name, "err", err)
		return nil
	}
	rec, err := recording.NewRecorder(f, recording.Header{Pane: name, Cols: cols, Rows: rows})
	if err != nil {
		f.Close()
		LogWarn("recording", "cannot start pane recording; pane runs unrecorded", "pane", name, "err", err)
		return nil
	}
	return rec
}

// closePaneRecorder ends a pane's recording and logs any chunks dropped.
func closePaneRecorder(name string, rec *recording.Recorder) {
	if rec == nil {
		return
	}
	err := rec.Close()
	if d := rec.Dropped(); d > 0 || err != nil {
		LogWarn("recording", "pane recording closed with losses", "pane", name, "dropped", d, "err", err)
	}
}

// DefaultRecordingDir is where recordings go when the flag names no
// directory: under the user's home, outside any repository.
func DefaultRecordingDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "initech-recordings")
	}
	return filepath.Join(home, ".initech", "recordings")
}
