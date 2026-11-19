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
	"fmt"
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
		return strings.Contains(s, agentsModalTitle)
	}, 20*time.Second); !ok {
		t.Fatalf("modal never opened\n%s", nineISXScreen(w1emu))
	}
	rigEnterAgentsList(w1pty) // the panel opens in its search box (ini-hxtg)
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
	// whole-fleet, is the surface that shows their checkboxes. References
	// hiddenBoxMarker (ini-6e97) rather than a copy of the glyph: this check
	// pinned the pre-68qv "[ ]" box, was patched to "[h]" (2160c7d) with
	// another literal, and is fixed here for good -- the third occurrence of
	// this exact defect shape on this one bead, in one afternoon.
	if _, ok := nineISXAwait(w1emu, func(s string) bool {
		return strings.Contains(s, "4 "+hiddenBoxMarker+" eng1") &&
			strings.Contains(s, "5 "+hiddenBoxMarker+" eng2") &&
			strings.Contains(s, "6 "+hiddenBoxMarker+" engnonce")
	}, 10*time.Second); !ok {
		t.Fatalf("window 1's modal never showed all three eng agents unchecked (hidden)\n%s",
			nineISXScreen(w1emu))
	}

	// WINDOW 2 shows every agent it owns hidden: its title row's disclosure,
	// the same line 6M4 reads, says so.
	w2Shows := func(visible, hidden int) func(string) bool {
		want := fmt.Sprintf("%d visible, %d hidden", visible, hidden)
		return func(s string) bool { return strings.Contains(s, want) }
	}
	if _, ok := nineISXAwait(w2emu, w2Shows(0, 3), 15*time.Second); !ok {
		t.Fatalf("window 2 never showed its three agents hidden\n%s", nineISXScreen(w2emu))
	}
	// Its agents box's last line names the one way back that exists there,
	// the main window's Agents panel (ini-vag8, pm R2), and no dots.
	way := "show them from the main window's Agents panel (Option+a there)"
	if _, ok := nineISXAwait(w2emu, func(s string) bool {
		return strings.Contains(s, "3 hidden") && strings.Contains(s, way)
	}, 15*time.Second); !ok || strings.Contains(nineISXScreen(w2emu), "click") {
		t.Fatalf("window 2's agents box does not name the way back (or names dots)\n%s", nineISXScreen(w2emu))
	}

	// UNHIDE eng1 THROUGH THE AGENTS PANE, the route that exists for window
	// 2's agents: window 2 has no Agents pane of its own (Option+A there says
	// it is the main window's, TestFN77Rig_ChildWindowLosesFleetManagementChords),
	// so it is the main window's, already open. A hidden idle agent with no
	// bead has no row and no dot in the agents box (ini-b0po), so the box is
	// not the way back. The cursor is on the nonce cell, two Downs from eng1.
	up := []byte("\x1b[A")
	w1pty.Write(up)
	time.Sleep(200 * time.Millisecond)
	w1pty.Write(up)
	time.Sleep(200 * time.Millisecond)
	w1pty.Write(space) // unhide eng1
	if _, ok := nineISXAwait(w2emu, w2Shows(1, 2), 15*time.Second); !ok {
		t.Fatalf("unhiding eng1 from the Agents pane did not show it in window 2\n%s", nineISXScreen(w2emu))
	}
	t.Log("unhiding eng1 from the Agents pane showed it in window 2")

	// RE-HIDE, then UNHIDE AGAIN: both windows agree each time.
	w1pty.Write(space)
	if _, ok := nineISXAwait(w1emu, func(s string) bool {
		return strings.Contains(s, "4 "+hiddenBoxMarker+" eng1")
	}, 10*time.Second); !ok {
		t.Fatalf("window 1's modal never confirmed eng1 re-hidden\n%s", nineISXScreen(w1emu))
	}
	if _, ok := nineISXAwait(w2emu, w2Shows(0, 3), 15*time.Second); !ok {
		t.Fatalf("re-hiding eng1 did not hide it in window 2\n%s", nineISXScreen(w2emu))
	}
	w1pty.Write(space)
	if _, ok := nineISXAwait(w2emu, w2Shows(1, 2), 15*time.Second); !ok {
		t.Fatalf("unhiding eng1 again did not show it in window 2\n%s", nineISXScreen(w2emu))
	}
	t.Log("hide and unhide from the Agents pane: both windows agreed throughout")
}
