// QA tests for ini-ac4: processTreeRSS.
package tui

import (
	"os"
	"runtime"
	"testing"
)

// TestProcessTreeRSS_Self verifies that processTreeRSS returns a non-zero
// value for the current process.
func TestProcessTreeRSS_Self(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("processTreeRSS uses /proc on Unix")
	}
	pid := os.Getpid()
	rss := processTreeRSS(pid)
	if rss <= 0 {
		t.Errorf("processTreeRSS(self=%d) = %d, want > 0", pid, rss)
	}
}

// TestProcessTreeRSS_InvalidPID returns 0 for invalid PIDs.
func TestProcessTreeRSS_InvalidPID(t *testing.T) {
	if rss := processTreeRSS(0); rss != 0 {
		t.Errorf("processTreeRSS(0) = %d, want 0", rss)
	}
	if rss := processTreeRSS(-1); rss != 0 {
		t.Errorf("processTreeRSS(-1) = %d, want 0", rss)
	}
}

// TestProcessTreeRSS_DeadProcess returns 0 for a nonexistent PID.
func TestProcessTreeRSS_DeadProcess(t *testing.T) {
	rss := processTreeRSS(99999999)
	if rss != 0 {
		t.Errorf("processTreeRSS(dead) = %d, want 0", rss)
	}
}
