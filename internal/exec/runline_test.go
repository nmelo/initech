//go:build !windows

package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunLine_RunsInTheFolderAndReportsTheExitCode(t *testing.T) {
	dir := t.TempDir()
	out, code, err := RunLine(context.Background(), dir, "pwd; echo err >&2; exit 3")
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	inDir := strings.Contains(out, dir) || strings.Contains(out, real) // macOS: /var is /private/var
	if code != 3 || !inDir || !strings.Contains(out, "err") {
		t.Fatalf("code %d out %q, want 3 and the folder and stderr", code, out)
	}
}

// TestRunLine_TheBoundStopsTheLineAndWhatItStarted: a child the line put in the
// background must not outlive the bound.
func TestRunLine_TheBoundStopsTheLineAndWhatItStarted(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-still-running")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, code, err := RunLine(ctx, dir, "(sleep 2; touch "+marker+") & sleep 30")
	if err == nil || code != -1 {
		t.Fatalf("code %d err %v, want the context's error", code, err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the bound took %s", took)
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the line's background child outlived the bound")
	}
}

// TestRunLine_OutputIsCappedAndTheLineIsNotKilledForIt.
func TestRunLine_OutputIsCappedAndTheLineIsNotKilledForIt(t *testing.T) {
	out, code, err := RunLine(context.Background(), t.TempDir(), "head -c 1000000 /dev/zero | tr '\\0' x; echo; echo done >&2; exit 0")
	if err != nil || code != 0 {
		t.Fatalf("code %d err %v: the line was hurt by the cap", code, err)
	}
	if len(out) > lineOutputCap {
		t.Fatalf("kept %d bytes, cap %d", len(out), lineOutputCap)
	}
}
