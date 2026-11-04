package tui

// inject_capture.go keeps the screen of every send whose outcome is UNKNOWN
// (ini-xl9): the rows the send path read, as one file beside the log, so a
// miss is read from what initech saw, never guessed.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// injectCaptureDir is the folder beside the log that holds the captures, and
// injectCaptureKeep how many it keeps: the newest, the rest removed.
const (
	injectCaptureDir  = "inject-captures"
	injectCapturePref = "unknown-"
	injectCaptureKeep = 20
)

// injectCaptureNow is the capture's clock; a variable so tests fix it.
var injectCaptureNow = time.Now

// injectCaptureFolder is the folder for captures: inject-captures beside the
// log file, or "" when there is no log file (nothing is written then).
func injectCaptureFolder() string {
	appLogger.mu.Lock()
	path := appLogger.path
	appLogger.mu.Unlock()
	if path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(path), injectCaptureDir)
}

// writeInjectCapture writes screen, the rows read at an UNKNOWN outcome, with
// a header naming the pane and the emulator's size, and returns the file's
// path, or "" when nothing was written. The folder is 0700 and each file
// 0600, like the log: a screen can hold anything an agent printed. Only the
// newest injectCaptureKeep captures are kept. Best effort: a failure costs
// the capture, never the send.
func writeInjectCapture(pane string, cols, rows int, screen []string) string {
	dir := injectCaptureFolder()
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	now := injectCaptureNow()
	name := injectCapturePref + safeCaptureName(pane) + "-" + now.UTC().Format("20060102T150405.000000000Z") + ".txt"
	path := filepath.Join(dir, name)
	body := fmt.Sprintf("pane=%s cols=%d rows=%d time=%s\n%s\n", pane, cols, rows, now.UTC().Format(time.RFC3339Nano), strings.Join(screen, "\n"))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return ""
	}
	pruneInjectCaptures(dir)
	return path
}

// safeCaptureName keeps a pane name to letters, digits, dash and underscore,
// so a name can never reach outside the folder.
func safeCaptureName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// pruneInjectCaptures removes all but the newest injectCaptureKeep captures
// in dir, by the time in their names; any other file there is left alone.
func pruneInjectCaptures(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type capture struct{ name, stamp string }
	var caps []capture
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, injectCapturePref) || !strings.HasSuffix(n, ".txt") {
			continue
		}
		i := strings.LastIndexByte(n, '-')
		caps = append(caps, capture{n, n[i+1:]})
	}
	if len(caps) <= injectCaptureKeep {
		return
	}
	sort.Slice(caps, func(i, j int) bool { return caps[i].stamp < caps[j].stamp })
	for _, c := range caps[:len(caps)-injectCaptureKeep] {
		os.Remove(filepath.Join(dir, c.name)) //nolint:errcheck // best effort, like the capture
	}
}
