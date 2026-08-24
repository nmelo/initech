//go:build !windows

package tui

// fn77_rig_test.go — the COMPOSED two-window rig for ini-fn77 part 1 (a0498b7).
//
// Real initech binaries, real PTYs, two real windows, the real attach
// handshake, and each window's actual rendered screen as the assertion
// surface. Unit tests in fleet_chords_test.go prove the predicates directly
// against handleKey/openAgentsModal; this proves the product end to end
// through the real input pipeline, per the bead's own instruction ("the
// two-window check is live") and the multi-window cascade retrospective
// (part-level rigor does not sum to a system claim).
//
// Reuses ninisx_rig_test.go's rig plumbing (same package) rather than
// re-deriving it: nineISXRoot/nineISXBuild/nineISXStart/nineISXScreen/
// nineISXAwait/rigReserveFreePort/rigRequireServing/nineISXListenAddr are
// all generic to "two real initech windows", not specific to ini-9isx.
//
// STAND-IN, STATED: the agent process is `sh`, not Claude -- this bead's
// whole subject is the fleet-management CHORD GATE (backtick/Option+A),
// which never reads agent output. Both WINDOWS are the real built binary.
//
// Run: INITECH_FN77=1 go test ./internal/tui/ -run FN77Rig -v -timeout 300s

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestFN77Rig_ChildWindowLosesFleetManagementChords(t *testing.T) {
	if os.Getenv("INITECH_FN77") != "1" {
		t.Skip("set INITECH_FN77=1 to run the composed two-window rig for ini-fn77")
	}
	bin := nineISXBuild(t)
	root, _ := nineISXRoot(t, "")

	_, w1pty, w1emu, _ := nineISXStart(t, bin, root)
	time.Sleep(10 * time.Second)
	w1pty.Write([]byte("n")) // decline the consent overlay
	time.Sleep(2 * time.Second)
	rigRequireServing(t, nineISXListenAddr(t, root))

	_, w2pty, w2emu, _ := nineISXStart(t, bin, root, "--window", "2")
	time.Sleep(12 * time.Second)
	w2pty.Write([]byte("n"))
	time.Sleep(2 * time.Second)

	// ── CONTROL: window 1 keeps the feature ─────────────────────────────
	// Option+A (ESC 'a', the meta encoding a raw PTY sends for Alt/Option)
	// driven through the REAL input pipeline, not the backtick+":agents"
	// side door the ini-9isx rig uses. The gate must not cost window 1
	// anything.
	w1pty.Write([]byte{0x1b, 'a'})
	if _, ok := nineISXAwait(w1emu, func(s string) bool {
		return strings.Contains(s, "initech agents")
	}, 20*time.Second); !ok {
		t.Fatalf("Option+A did not open the agents modal in window 1 -- the gate must not "+
			"cost the main window the feature\n%s", nineISXScreen(w1emu))
	}
	w1pty.Write([]byte{0x1b}) // close the modal

	// ── WINDOW 2, BACKTICK: swallowed, not forwarded, no notice ─────────
	// Asserted as a screen-content diff (not a raw-byte diff, which would
	// false-positive on the housekeeping ticker's redraws of unchanged
	// state) -- before and after must render identically.
	before := nineISXScreen(w2emu)
	w2pty.Write([]byte("`"))
	time.Sleep(2 * time.Second)
	after := nineISXScreen(w2emu)
	if before != after {
		t.Fatalf("window 2's screen changed after a backtick keypress; the AC requires it be "+
			"swallowed with no modal, no notice, and nothing forwarded to the pane\nBEFORE:\n%s\n"+
			"AFTER:\n%s", before, after)
	}

	// ── WINDOW 2, OPTION+A: notice, not the modal ────────────────────────
	// The toast is width-capped at 50 columns (renderNotifications) and this
	// message truncates to "...main windo…" -- "window only" does not
	// survive whole, so the match string is the prefix that does.
	w2pty.Write([]byte{0x1b, 'a'})
	if _, ok := nineISXAwait(w2emu, func(s string) bool {
		return strings.Contains(strings.ToLower(s), "available in the main")
	}, 20*time.Second); !ok {
		t.Fatalf("window 2 never showed the main-window-only notice after Option+A -- a chord "+
			"that does nothing and says nothing reads as a broken build (ini-162m)\n%s",
			nineISXScreen(w2emu))
	}
	if s := nineISXScreen(w2emu); strings.Contains(s, "initech agents") {
		t.Fatalf("the agents modal opened in window 2 despite the notice; fleet management is "+
			"the main window's\n%s", s)
	}
}
