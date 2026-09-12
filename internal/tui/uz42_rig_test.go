//go:build !windows

package tui

// QA composed rig for ini-uz42 AC6 -- the live two-window check eng1's own
// DONE comment flagged as "not measured by me": window 2 assigned a group,
// hide every member from window 1's Agents panel, confirm window 2 shows the
// all-hidden state-3 copy with the right count; unhide ONE by dot-click IN
// WINDOW 2, confirm that pane renders and the hint disappears; re-hide from
// window 1, confirm the hint returns; unhide from window 1's panel (Space),
// confirm the pane renders. Both windows must agree on hidden marks
// throughout.
//
// Real initech binaries, real PTYs, two real windows -- reuses ini-fn77's rig
// plumbing (nineISXRoot/Build/Start/Screen/Await, fn77FindOverlayDot/Click),
// same package.
//
// Run: INITECH_UZ42=1 go test ./internal/tui/ -run UZ42Rig -v -timeout 180s

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestUZ42Rig_AllHiddenExplanationAndRecoveryRoundTrip(t *testing.T) {
	if os.Getenv("INITECH_UZ42") != "1" {
		t.Skip("set INITECH_UZ42=1 to run the composed two-window rig for ini-uz42")
	}
	bin := nineISXBuild(t)
	root, _ := nineISXRoot(t, "group_window:\n    eng: window-2\n")

	_, w1pty, w1emu, _ := nineISXStart(t, bin, root)
	time.Sleep(10 * time.Second)
	w1pty.Write([]byte("n"))
	time.Sleep(2 * time.Second)
	rigRequireServing(t, nineISXListenAddr(t, root))

	_, w2pty, w2emu, _ := nineISXStart(t, bin, root, "--window", "2")
	time.Sleep(12 * time.Second)
	w2pty.Write([]byte("n"))
	if _, ok := nineISXAwait(w2emu, func(s string) bool {
		return strings.Contains(s, "eng1") && strings.Contains(s, "eng2")
	}, 30*time.Second); !ok {
		t.Fatalf("window 2 never rendered the agents it owns\n%s", nineISXScreen(w2emu))
	}

	// Open window 1's real Agents modal and hide all three eng-group members
	// (eng1, eng2, the nonce agent) via the real Space-toggle path -- not a
	// direct setHidden call, because the claim under test is the whole
	// operator-visible round trip.
	w1pty.Write([]byte("`"))
	time.Sleep(600 * time.Millisecond)
	w1pty.Write([]byte("agents\r"))
	if _, ok := nineISXAwait(w1emu, func(s string) bool {
		return strings.Contains(s, "initech agents")
	}, 20*time.Second); !ok {
		t.Fatalf("modal never opened\n%s", nineISXScreen(w1emu))
	}
	// Transposed grid (ini-w771): groups are COLUMNS. From super, Down walks
	// core (super -> pm); at core's end the next Down continues into monitor
	// 2's nearest column, eng, landing on eng1; further Downs walk eng.
	down, space := []byte("\x1b[B"), []byte(" ")
	w1pty.Write(down)
	time.Sleep(200 * time.Millisecond)
	w1pty.Write(down)
	time.Sleep(200 * time.Millisecond)
	w1pty.Write(space) // hide eng1
	time.Sleep(300 * time.Millisecond)
	w1pty.Write(down)
	time.Sleep(200 * time.Millisecond)
	w1pty.Write(space) // hide eng2
	time.Sleep(300 * time.Millisecond)
	w1pty.Write(down)
	time.Sleep(200 * time.Millisecond)
	w1pty.Write(space) // hide the nonce agent
	time.Sleep(300 * time.Millisecond)
	// Window 1's OWN overlay is scoped to what window 1 owns (super/pm/qa1),
	// so it never lists eng agents at all -- the modal itself, which is
	// whole-fleet, is the surface that shows their checkboxes.
	if _, ok := nineISXAwait(w1emu, func(s string) bool {
		return strings.Contains(s, "4 [ ] eng1") && strings.Contains(s, "5 [ ] eng2") &&
			strings.Contains(s, "6 [ ] engnonce")
	}, 10*time.Second); !ok {
		t.Fatalf("window 1's modal never showed all three eng agents unchecked (hidden)\n%s",
			nineISXScreen(w1emu))
	}

	// WINDOW 2 must show the state-3 all-hidden explanation, count 3, not a
	// blank screen and not the pre-fn77 "press Alt+a to assign" dead end.
	if _, ok := nineISXAwait(w2emu, func(s string) bool {
		return strings.Contains(s, "all 3 agents assigned here are hidden")
	}, 15*time.Second); !ok {
		t.Fatalf("window 2 never showed the all-hidden state-3 explanation\n%s", nineISXScreen(w2emu))
	}
	if s := nineISXScreen(w2emu); strings.Contains(s, "press Alt+a to assign") {
		t.Fatalf("window 2 still shows the pre-fn77 dead-end hint\n%s", s)
	}
	t.Log("window 2 shows the all-hidden state-3 explanation with the correct count")

	// UNHIDE ONE BY DOT-CLICK IN WINDOW 2 ITSELF -- AC6's local-recovery claim.
	col, row, ok := fn77FindOverlayDot(w2emu, "eng1")
	if !ok {
		t.Fatalf("could not find eng1's overlay dot in window 2\n%s", nineISXScreen(w2emu))
	}
	fn77Click(w2pty, col, row)
	if _, ok := nineISXAwait(w2emu, func(s string) bool {
		return strings.Contains(s, "eng1") && !strings.Contains(s, "all 3 agents assigned here are hidden")
	}, 15*time.Second); !ok {
		t.Fatalf("unhiding eng1 by dot-click in window 2 did not render it and clear the hint\n%s",
			nineISXScreen(w2emu))
	}
	t.Log("window 2's own dot-click unhid eng1: pane renders, all-hidden hint cleared")

	// RE-HIDE FROM WINDOW 1: cursor is on the nonce cell (two Downs from
	// eng1 in the eng column); move back up and toggle.
	up := []byte("\x1b[A")
	w1pty.Write(up)
	time.Sleep(200 * time.Millisecond)
	w1pty.Write(up)
	time.Sleep(200 * time.Millisecond)
	w1pty.Write(space) // re-hide eng1
	time.Sleep(300 * time.Millisecond)
	if _, ok := nineISXAwait(w1emu, func(s string) bool {
		return strings.Contains(s, "4 [ ] eng1")
	}, 10*time.Second); !ok {
		t.Fatalf("window 1's modal never confirmed eng1 re-hidden\n%s", nineISXScreen(w1emu))
	}
	if _, ok := nineISXAwait(w2emu, func(s string) bool {
		return strings.Contains(s, "all 3 agents assigned here are hidden")
	}, 15*time.Second); !ok {
		t.Fatalf("re-hiding eng1 from window 1 did not bring the all-hidden hint back in window 2\n%s",
			nineISXScreen(w2emu))
	}
	t.Log("re-hiding eng1 from window 1 brought the all-hidden hint back in window 2")

	// UNHIDE FROM WINDOW 1'S PANEL (Space), the second named route.
	w1pty.Write(space) // unhide eng1 again
	time.Sleep(300 * time.Millisecond)
	if _, ok := nineISXAwait(w2emu, func(s string) bool {
		return strings.Contains(s, "eng1") && !strings.Contains(s, "all 3 agents assigned here are hidden")
	}, 15*time.Second); !ok {
		t.Fatalf("unhiding eng1 from window 1's panel did not render it in window 2\n%s",
			nineISXScreen(w2emu))
	}
	t.Log("unhiding eng1 from window 1's panel rendered it in window 2; both windows agreed throughout")
}
