package tui

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// symlinkRig makes a rig folder reached through a link, as every rig under
// /tmp is on macOS, with Claude's config folder in a test folder of its own.
// It returns the pane folder as initech spells it and as Claude sees it.
func symlinkRig(t *testing.T) (spelled, real string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(base, "claude"))
	real = filepath.Join(base, "real", "rig", "eng1")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(base, "link", "rig", "eng1"), real
}

// writeTranscript puts one session file in Claude's folder for dir, as Claude does.
func writeTranscript(t *testing.T, dir, name, body string, at time.Time) string {
	t.Helper()
	folder := sessionDirFor(dir)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(folder, name)
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, at, at); err != nil {
		t.Fatal(err)
	}
	return file
}

// ini-646: Claude names its transcript folder after the working directory it
// sees, the real path; initech looked only under the path as it spells it, so
// a rig under a symlink (every rig under /tmp on macOS) never found them.
func TestNewestTranscript_ARigUnderASymlink_FindsWhatClaudeWrote(t *testing.T) {
	spelled, real := symlinkRig(t)
	want := writeTranscript(t, real, "s1.jsonl", "{}\n", time.Now())
	if got := NewestTranscript(spelled); got != want {
		t.Errorf("NewestTranscript(%q) = %q, want %q", spelled, got, want)
	}
}

// AC1 by effect: through the symlink the journal reads the transcript, so the
// pane has its session id, which is what writes the session-bead record line.
func TestJournal_ARigUnderASymlink_ReadsItsSessionID(t *testing.T) {
	spelled, real := symlinkRig(t)
	line := `{"type":"user","isSidechain":false,"sessionId":"s646","timestamp":"2026-11-01T05:00:00Z","message":{"content":"hello"}}` + "\n"
	want := writeTranscript(t, real, "s646.jsonl", line, time.Now())
	realDir, err := realSessionDir(spelled)
	if err != nil || realDir == "" {
		t.Fatalf("realSessionDir(%q) = %q, %v", spelled, realDir, err)
	}
	p := &Pane{name: "eng1", jsonlDir: sessionDirFor(spelled), jsonlRealDir: realDir}
	p.journalTick(&journalTail{})
	if got := p.sessionWork().sessionID; got != "s646" {
		t.Errorf("session id = %q, want s646: no record line can be written", got)
	}
	info := PaneInfo{Name: "eng1"}
	fillPaneDetail(&info, p)
	if info.SessionFile != want {
		t.Errorf("session_file = %q, want %q", info.SessionFile, want)
	}
}

// AC2: on a real path there is one folder, and the pane carries no second one.
func TestRealSessionDir_ARealPath_NamesNoSecondFolder(t *testing.T) {
	_, real := symlinkRig(t)
	if got, err := realSessionDir(real); got != "" || err != nil {
		t.Errorf("realSessionDir(real path) = %q, %v, want none", got, err)
	}
	want := writeTranscript(t, real, "s1.jsonl", "{}\n", time.Now())
	if got := NewestTranscript(real); got != want {
		t.Errorf("NewestTranscript = %q, want %q", got, want)
	}
}

// AC3, feed it nothing: a folder that does not exist has no real path; what
// was written under the spelling as given is still found.
func TestRealSessionDir_ARootThatDoesNotExist_KeepsTheSpelledFolder(t *testing.T) {
	spelled, _ := symlinkRig(t)
	gone := filepath.Join(filepath.Dir(spelled), "gone")
	if got, err := realSessionDir(gone); got != "" || err == nil {
		t.Errorf("realSessionDir(missing) = %q, %v, want none and an error", got, err)
	}
	if got := NewestTranscript(gone); got != "" {
		t.Errorf("NewestTranscript(missing) = %q, want none", got)
	}
	want := writeTranscript(t, gone, "s1.jsonl", "{}\n", time.Now())
	if got := NewestTranscript(gone); got != want {
		t.Errorf("NewestTranscript(missing, spelled folder holds one) = %q, want %q", got, want)
	}
}

// AC3: a symlink loop ends by itself with an error, and the pane still reads
// the spelled folder.
func TestRealSessionDir_ASymlinkLoop_IsBoundedAndKeepsTheSpelledFolder(t *testing.T) {
	spelled, _ := symlinkRig(t)
	base := filepath.Dir(spelled)
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := realSessionDir(a); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a symlink loop resolved without an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resolving a symlink loop did not end")
	}
	want := writeTranscript(t, a, "s1.jsonl", "{}\n", time.Now())
	if got := NewestTranscript(a); got != want {
		t.Errorf("NewestTranscript(loop) = %q, want %q", got, want)
	}
}

// AC3: a folder under both spellings: the newest file wins, whichever side it
// is on (a rig moved, or Claude reached it both ways).
func TestNewestTranscript_BothSpellings_TheNewestWins(t *testing.T) {
	now := time.Now()
	for name, realNewer := range map[string]bool{"real newer": true, "spelled newer": false} {
		t.Run(name, func(t *testing.T) {
			spelled, real := symlinkRig(t)
			older, newer := now.Add(-time.Hour), now
			if !realNewer {
				older, newer = newer, older
			}
			inSpelled := writeTranscript(t, spelled, "a.jsonl", "{}\n", older)
			inReal := writeTranscript(t, real, "b.jsonl", "{}\n", newer)
			want := inReal
			if !realNewer {
				want = inSpelled
			}
			if got := NewestTranscript(spelled); got != want {
				t.Errorf("NewestTranscript = %q, want %q", got, want)
			}
		})
	}
}

// AC3: a pane whose folder cannot be resolved says so once, as it starts.
func TestPaneRealSessionDir_AFolderThatCannotBeResolved_LogsOneLine(t *testing.T) {
	spelled, _ := symlinkRig(t)
	root := t.TempDir()
	defer InitLogger(root, slog.LevelWarn)()
	if got := paneRealSessionDir("eng1", filepath.Join(filepath.Dir(spelled), "gone")); got != "" {
		t.Errorf("paneRealSessionDir = %q, want none", got)
	}
	data, err := os.ReadFile(filepath.Join(root, ".initech", logFileName))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "has no real path"); n != 1 {
		t.Errorf("log carries %d lines about it, want 1:\n%s", n, data)
	}
}
