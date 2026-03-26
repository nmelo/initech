package tui

import (
	"runtime"
	"strings"
	"testing"
)

// TestCopyToClipboard_MessagePrefix verifies that copyToClipboard always
// returns a message prefixed with "patrol: " regardless of outcome (ini-a1e.33).
func TestCopyToClipboard_MessagePrefix(t *testing.T) {
	msg := copyToClipboard("hello test")
	if !strings.HasPrefix(msg, "patrol: ") {
		t.Errorf("message should start with 'patrol: ', got %q", msg)
	}
}

// TestCopyToClipboard_EmptyText verifies the function handles empty input
// without panicking and still returns a "patrol: " message.
func TestCopyToClipboard_EmptyText(t *testing.T) {
	msg := copyToClipboard("")
	if !strings.HasPrefix(msg, "patrol: ") {
		t.Errorf("message should start with 'patrol: ', got %q", msg)
	}
}

// TestCopyToClipboard_DarwinSuccess verifies the success message on macOS
// where pbcopy is available. On non-Darwin platforms the test is skipped.
func TestCopyToClipboard_DarwinSuccess(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("pbcopy not available; skipping Darwin clipboard test")
	}
	msg := copyToClipboard("initech test clipboard payload")
	if msg != "patrol: copied to clipboard" {
		t.Errorf("expected 'patrol: copied to clipboard', got %q", msg)
	}
}

// TestCopyToClipboard_UnavailableMessage verifies the unavailable message is
// returned on a platform where no known clipboard tool is expected. We can
// only assert the format since we cannot portably guarantee absence of tools.
func TestCopyToClipboard_UnavailableMessage(t *testing.T) {
	// On unsupported platforms (not darwin, not linux) copyToClipboard must
	// return the "unavailable" message.
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		t.Skip("clipboard tools may be present; skipping unavailable-path test")
	}
	msg := copyToClipboard("test")
	want := "patrol: clipboard unavailable"
	if !strings.Contains(msg, want) {
		t.Errorf("expected message containing %q, got %q", want, msg)
	}
}
