//go:build linux || darwin

package tui

import (
	"testing"
	"time"
)

// Under the short waits a send still writes exactly the bytes, in the order,
// that TestNineThreeThree_RawModeChildGetsTodaysBytesExactly pins with the
// production waits: the stash key, the bracketed body, one Enter (ini-02h).
// Same real raw-mode child as that test, so it runs in the full suite only.
func TestSubmitDelays_ShortWaitsKeepTodaysBytesInOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("real child process and the send path's startup gate (up to ~17s); runs in the full suite")
	}
	shortSubmitDelays(t)
	p, received := stashRecordingPane(t, "raw -echo -ixon")
	stashAwaitMode(t, p, true)

	const body = "short02h body"
	p.SendText(body, true)
	want := "\x13\x1b[200~" + body + "\x1b[201~\r"
	stashWaitFor(received, "\x1b[201~\r", 10*time.Second)
	time.Sleep(300 * time.Millisecond) // nothing may trail the Enter
	if got := received(); got != want {
		t.Fatalf("a raw-mode child received %q under the short waits, want %q (today's bytes exactly)", got, want)
	}
}
