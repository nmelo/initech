//go:build linux || darwin

package tui

import (
	"testing"
	"time"
)

// ini-47vy AC1, by effect: a real child in raw mode, as Claude Code is,
// records every byte it receives. A message carrying the paste end sequence
// and a carriage return arrives as one paste with the sequence gone and
// nothing typed after it but initech's own Enter. At base the paste ended at
// the sequence and "ls -la" plus a CR arrived as keys.
func TestCleanPTY_APasteEndInsideAMessageTypesNoKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("real child process and the send path's startup gate (up to ~17s); runs in the full suite")
	}
	p, received := stashRecordingPane(t, "raw -echo -ixon")
	stashAwaitMode(t, p, true)

	p.SendText("hello\x1b[201~ls -la\r", true)
	want := "\x13\x1b[200~hellols -la\x1b[201~\r"
	stashWaitFor(received, "\x1b[201~\r", 10*time.Second)
	time.Sleep(300 * time.Millisecond) // nothing may trail the Enter
	if got := received(); got != want {
		t.Fatalf("the child received %q, want %q (one paste, the sequence gone, no keys after it)", got, want)
	}
}

// A bare ESC is removed too: stripping only the end sequence would leave it
// to start a key sequence in the child.
func TestCleanPTY_ABareEscapeInsideAMessageIsRemoved(t *testing.T) {
	if testing.Short() {
		t.Skip("real child process and the send path's startup gate (up to ~17s); runs in the full suite")
	}
	p, received := stashRecordingPane(t, "raw -echo -ixon")
	stashAwaitMode(t, p, true)

	p.SendText("a\x1bb\tc\nd é", true)
	want := "\x13\x1b[200~ab\tc\nd é\x1b[201~\r"
	stashWaitFor(received, "\x1b[201~\r", 10*time.Second)
	time.Sleep(300 * time.Millisecond)
	if got := received(); got != want {
		t.Fatalf("the child received %q, want %q", got, want)
	}
}
