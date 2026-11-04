package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ini-xl9, step 1 (AC10): an UNKNOWN outcome keeps the screen the send path
// read, beside the log, and the outcome lines name the emulator's size.

// xl9LogDir points the log's folder at a temp dir for the capture and
// returns the folder the captures go to.
func xl9LogDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".initech")
	appLogger.mu.Lock()
	prev := appLogger.path
	appLogger.path = filepath.Join(dir, logFileName)
	appLogger.mu.Unlock()
	t.Cleanup(func() {
		appLogger.mu.Lock()
		appLogger.path = prev
		appLogger.mu.Unlock()
	})
	return filepath.Join(dir, injectCaptureDir)
}

// An UNKNOWN (no composer visible) writes one capture holding the screen,
// 0600 in a 0700 folder, named in the log line with the size.
func TestInjectCapture_AnUnknownKeepsTheScreenItRead(t *testing.T) {
	buf := captureLogs(t)
	capDir := xl9LogDir(t)
	nma5Window(t, 200*time.Millisecond)
	p, submits, _ := proofPane(t)
	nma5Paint(p, "Loading conversation...\r\nsecond row of the screen")
	nma5Enters(t, submits, func(int) {})
	sendSubmitKey(p.emu, p.submitKey)
	confirmAndRetrySubmit(p, newPendingSubmit(nma5Body, "bracketed", ""))

	line := recordContaining(t, buf.String(), "submit outcome UNKNOWN")
	cols, rows := p.emuSize()
	for _, want := range []string{"cols=" + itoa(cols), "rows=" + itoa(rows), "capture=" + capDir} {
		if !strings.Contains(line, want) {
			t.Errorf("the UNKNOWN line %q lacks %q", line, want)
		}
	}
	entries, err := os.ReadDir(capDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("captures %v (%v), want exactly one", entries, err)
	}
	path := filepath.Join(capDir, entries[0].Name())
	b, _ := os.ReadFile(path)
	got := string(b)
	if !strings.HasPrefix(got, "pane="+p.Name()+" cols="+itoa(cols)+" rows="+itoa(rows)+" ") ||
		!strings.Contains(got, "Loading conversation...") || !strings.Contains(got, "second row of the screen") {
		t.Errorf("the capture reads %q, want the header and the screen's rows", got)
	}
	if !strings.Contains(line, path) {
		t.Errorf("the UNKNOWN line names %q, want the file %s", line, path)
	}
	fi, _ := os.Stat(path)
	di, _ := os.Stat(capDir)
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
		t.Errorf("modes file %v folder %v, want 0600 and 0700", fi.Mode().Perm(), di.Mode().Perm())
	}
}

// A taken submit writes no capture and its line names the size too.
func TestInjectCapture_ATakenSubmitWritesNone(t *testing.T) {
	buf := captureLogs(t)
	capDir := xl9LogDir(t)
	nma5Window(t, time.Second)
	f := &pkinFake{body: nma5Body, after: "❯ ", takes: func(int) bool { return true }}
	p, _ := pkinSendRig(t, f)
	if v := pkinSend(p, nma5Body); v.Outcome != DeliveryDelivered {
		t.Fatalf("fixture: the send reads %+v, want delivered", v)
	}
	line := recordContaining(t, buf.String(), "submitted: composer cleared")
	if !strings.Contains(line, "cols=") || !strings.Contains(line, "rows=") {
		t.Errorf("the taken line %q lacks the size", line)
	}
	if _, err := os.Stat(capDir); !os.IsNotExist(err) {
		t.Errorf("a taken submit left a capture folder (%v)", err)
	}
}

// Only the newest twenty are kept; other files there stay; no log file means
// no capture; a pane name cannot leave the folder.
func TestInjectCapture_KeepsTheNewestTwenty(t *testing.T) {
	capDir := xl9LogDir(t)
	t0 := time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC)
	t.Cleanup(func() { injectCaptureNow = time.Now })
	var paths []string
	for i := 0; i < 23; i++ {
		injectCaptureNow = func() time.Time { return t0.Add(time.Duration(i) * time.Second) }
		paths = append(paths, writeInjectCapture("../pm", 59, 40, []string{"row"}))
	}
	if err := os.WriteFile(filepath.Join(capDir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	injectCaptureNow = func() time.Time { return t0.Add(time.Minute) }
	paths = append(paths, writeInjectCapture("pm", 59, 40, []string{"row"}))
	entries, _ := os.ReadDir(capDir)
	if len(entries) != injectCaptureKeep+1 {
		t.Fatalf("%d files, want the newest %d and notes.txt", len(entries), injectCaptureKeep)
	}
	for _, p := range paths[:4] {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s is still there, want it pruned", p)
		}
	}
	for _, p := range paths[4:] {
		if filepath.Dir(p) != capDir {
			t.Errorf("%s is outside the folder", p)
		}
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was pruned, want it kept", p)
		}
	}
	appLogger.mu.Lock()
	appLogger.path = ""
	appLogger.mu.Unlock()
	if got := writeInjectCapture("pm", 59, 40, []string{"row"}); got != "" {
		t.Errorf("with no log file a capture went to %q, want none", got)
	}
}
