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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
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

// TestFN77Rig_OverlayDotClickFromAChildWindowStillHidesAnAgent closes the gap
// eng1's own DONE comment named as unmeasured. The deletion pass in this
// series (3cf2320/71be81b) nearly took the fleet-state follower channel with
// it: setHidden/sendFleetStateCmd/applyFleetStateCmd were KEPT specifically
// because mouse.go:36 routes the overlay dot click through them from ANY
// window -- a live feature that has nothing to do with the agents modal this
// bead removes. eng1's own evidence that channel survived was a passing
// pre-existing unit test plus source reading, not a two-window run. This is
// that run.
func TestFN77Rig_OverlayDotClickFromAChildWindowStillHidesAnAgent(t *testing.T) {
	if os.Getenv("INITECH_FN77") != "1" {
		t.Skip("set INITECH_FN77=1 to run the composed two-window rig for ini-fn77")
	}
	bin := nineISXBuild(t)
	// eng owns window 2, so window 2's own overlay has a dot to click. With
	// the default fixture (no groups assigned) window 2's overlay is empty
	// and mouse.go's own guard (agentCount > 0) would make the click a
	// no-op for a reason that has nothing to do with this bead.
	root, _ := nineISXRoot(t, "group_window:\n    eng: window-2\n")

	_, w1pty, w1emu, _ := nineISXStart(t, bin, root)
	time.Sleep(10 * time.Second)
	w1pty.Write([]byte("n")) // decline the consent overlay
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

	// Find the OVERLAY's dot for eng1: the black-circle glyph only the
	// overlay draws (render.go:929), on the same row as the agent name, so a
	// pane's own ribbon (which can also say "eng1") is never mistaken for
	// it.
	col, row, ok := fn77FindOverlayDot(w2emu, "eng1")
	if !ok {
		t.Fatalf("could not find the overlay dot for eng1 in window 2's own screen\n%s",
			nineISXScreen(w2emu))
	}

	// A real SGR mouse click (the encoding tcell's EnableMouse negotiates),
	// written raw over the PTY exactly as a real terminal forwards a click --
	// not SendKey, not a direct toggleHidden call, because the claim under
	// test is the WHOLE path: mouse.go's hit-test, toggleHidden, setHidden's
	// non-authority branch, sendFleetStateCmd over the network, window 1's
	// applyFleetStateCmd, and the broadcast back to window 2's own render.
	fn77Click(w2pty, col, row)

	if _, ok := nineISXAwait(w2emu, func(s string) bool {
		return strings.Contains(s, "eng1 [h]")
	}, 20*time.Second); !ok {
		t.Fatalf("window 2's own overlay never marked eng1 hidden after its own dot click -- "+
			"the fleet-state follower channel eng1's DONE comment flagged as unmeasured "+
			"(mouse.go's overlay click -> setHidden -> sendFleetStateCmd -> window 1 -> "+
			"broadcast back) is broken\n%s", nineISXScreen(w2emu))
	}
	t.Log("window 2's overlay dot click hid eng1 and the broadcast came back")

	// The write actually reached window 1's fleet store, not just window 2's
	// local rendering: window 1 is the authority and persists it.
	if b, err := os.ReadFile(filepath.Join(root, ".initech", "fleet-state.yaml")); err == nil {
		if !strings.Contains(string(b), "eng1") {
			t.Fatalf("fleet-state.yaml does not record eng1 as hidden after the click -- window "+
				"2's overlay updated but the write never reached the authority's store:\n%s", b)
		}
		t.Logf("fleet-state.yaml after the click: %s", b)
	} else {
		t.Fatalf("read fleet-state.yaml: %v", err)
	}
	t.Logf("window 1's own screen after window 2's hide:\n%s", nineISXScreen(w1emu))
}

// fn77FindOverlayDot returns the screen column/row of the overlay's status
// dot on the row also naming the given agent. The dot glyph is state-
// dependent -- '○' idle/suspended, '●' running, per render.go:929 -- so an
// idle sh stand-in draws the hollow circle, not the filled one; either
// glyph, two columns left of the name (px+2 to the name's px+4, render.go:
// 946/956), is accepted. Requiring one of them immediately left of the name
// is what keeps this from matching a pane's own ribbon, which can also say
// the agent's name but never precedes it with a status dot.
func fn77FindOverlayDot(emu *vt.SafeEmulator, agent string) (col, row int, ok bool) {
	const cols, rows = 130, 44
	target := []rune(agent)
	for y := 0; y < rows; y++ {
		line := []rune(emu.RowText(y, cols))
		for x := 2; x+len(target) <= len(line); x++ {
			if !runesEqual(line[x:x+len(target)], target) {
				continue
			}
			dot := line[x-2]
			if dot == '●' || dot == '○' {
				return x - 2, y, true
			}
		}
	}
	return 0, 0, false
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fn77Click writes a raw SGR mouse click (press + release) at the given
// screen column/row -- the same encoding tcell's EnableMouse negotiates and
// a real terminal would send for a real click. Coordinates are 1-based in
// the SGR protocol.
func fn77Click(pty *os.File, col, row int) {
	pty.Write([]byte(fmt.Sprintf("\x1b[<0;%d;%dM", col+1, row+1)))
	time.Sleep(100 * time.Millisecond)
	pty.Write([]byte(fmt.Sprintf("\x1b[<0;%d;%dm", col+1, row+1)))
}
