//go:build !windows

package tui

// ninisx_rig_test.go — the COMPOSED two-window rig for ini-9isx (AC10).
//
// Real initech binaries, real PTYs, two real windows, the real attach
// handshake, and window 2's actual rendered screen as the assertion surface.
// The unit tests one file over prove the predicates; this proves the product,
// because ini-9isx's whole subject is what a second window DRAWS — and the
// multi-window cascade retrospective is explicit that part-level rigor does not
// sum to a system claim.
//
// BOTH STARTUP ORDERS (the standing mandate): assign-then-attach and
// attach-then-assign reach the same scoped state by different paths, and the
// bugs this fleet has actually shipped in this area (ini-xq4r, ini-6m4) were
// all order-dependent — a viewer that planned zero panes because it attached
// before the store was written, a modal that was correct until the other window
// changed something.
//
// STAND-IN, STATED (real-workload fixture rule): the AGENT process is `sh`, not
// Claude. Everything under test here — assignment, scoping, the overlay and
// modal draw paths, the disclosure line — is driven by fleet state and never
// reads a single byte of agent output. Using real Claude would add three
// minutes and eight API sessions to measure a code path that cannot tell the
// difference. Initech itself is NOT stood in for: both windows are the real
// built binary.
//
// Run: INITECH_9ISX=1 go test ./internal/tui/ -run NineISXRig -v -timeout 600s

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// nineISXRoot writes a project whose agents are shells, and returns its root.
func nineISXRoot(t *testing.T, assignments string) string {
	t.Helper()
	root, err := os.MkdirTemp("", "9isxrig")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	roles := []string{"super", "pm", "qa1", "eng1", "eng2"}
	cfg := "project: ninisx\nroot: " + root + "\nroles:\n"
	for _, r := range roles {
		os.MkdirAll(filepath.Join(root, r), 0o755)
		os.WriteFile(filepath.Join(root, r, "CLAUDE.md"), []byte("# "+r+"\n"), 0o644)
		cfg += "    - " + r + "\n"
	}
	cfg += "window_listen: \"127.0.0.1:7629\"\nrole_overrides:\n"
	for _, r := range roles {
		cfg += "    " + r + ":\n        command: [\"sh\"]\n"
	}
	os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o644)

	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	// Order eng first so the modal's DEFAULT selection is an eng agent: 'm'
	// then moves the eng group with no navigation. ini-6m4's rig recorded why
	// navigation is the wrong tool here -- '/' search mode intercepts every
	// subsequent rune, so the 'm' lands in the search buffer and nothing moves,
	// and the rig then truthfully reports a move that never happened.
	os.WriteFile(filepath.Join(root, ".initech", "layout.yaml"),
		[]byte("grid: 3x2\nmode: grid\norder:\n    - eng1\n    - eng2\n    - super\n    - pm\n    - qa1\n"), 0o644)
	// eng1/eng2 seed into the "eng" band; assigning eng to window 2 splits the
	// fleet 3/2 across the monitors.
	if assignments != "" {
		os.WriteFile(filepath.Join(root, ".initech", "assignments.yaml"), []byte(assignments), 0o644)
	}
	return root
}

func nineISXBuild(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "initech-9isx")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func nineISXStart(t *testing.T, bin, root string, args ...string) (*exec.Cmd, *os.File, *vt.SafeEmulator) {
	t.Helper()
	c := newCmd(bin, root, args...)
	p, err := pty.StartWithSize(c, &pty.Winsize{Rows: 44, Cols: 130})
	if err != nil {
		t.Fatalf("start %v: %v", args, err)
	}
	emu := vt.NewSafeEmulator(130, 44)
	go func() {
		b := make([]byte, 4096)
		for {
			if _, err := emu.Read(b); err != nil {
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				emu.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { p.Close(); c.Process.Kill(); c.Process.Wait() })
	return c, p, emu
}

// nineISXScreen snapshots at THIS rig's real dimensions.
//
// It does not reuse snapRows, and that is the point: snapRows reads a
// hardcoded 120x40 because the rig it was written for ran at 120x40. This rig
// runs at 130x44, and reusing it silently dropped the last ten columns of every
// row -- which is exactly where the scope disclosure's tail sits. The rig then
// reported that window 2 "hides agents without naming the affordance", a
// product defect it had invented by measuring 120 columns of a 130-column
// screen. A capture narrower than the thing captured does not fail loudly; it
// reports a smaller truth.
func nineISXScreen(emu *vt.SafeEmulator) string {
	const cols, rows = 130, 44
	var out []string
	for y := 0; y < rows; y++ {
		if r := strings.TrimRight(emu.RowText(y, cols), " "); r != "" {
			out = append(out, fmt.Sprintf("%2d| %s", y, r))
		}
	}
	return strings.Join(out, "\n")
}

// nineISXAssert runs every AC 4/6/7/8/9 check against a live window-2 screen.
// Shared by both startup orders so the two paths are held to identical
// expectations rather than each getting the assertions its own quirks pass.
func nineISXAssert(t *testing.T, label string, w1emu, w2emu *vt.SafeEmulator, w1pty, w2pty *os.File) {
	t.Helper()

	w2 := nineISXScreen(w2emu)
	t.Logf("%s — WINDOW 2 OVERLAY:\n%s", label, w2)

	// AC1/AC2: no window prefix anywhere on the viewer's screen.
	if strings.Contains(w2, WindowOnePeerName+":") {
		t.Errorf("%s: window 2 still renders the %q prefix -- the clutter this bead removes\n%s",
			label, WindowOnePeerName+":", w2)
	}

	// AC4: the overlay lists this window's agents only.
	for _, own := range []string{"eng1", "eng2"} {
		if !strings.Contains(w2, own) {
			t.Errorf("%s: window 2's own agent %q is MISSING from its overlay -- over-scoping hides "+
				"an agent from the window that owns it\n%s", label, own, w2)
		}
	}
	for _, other := range []string{"super", "pm", "qa1"} {
		if strings.Contains(w2, other) {
			t.Errorf("%s: window 1's agent %q leaked into window 2's overlay -- the unscoped list is "+
				"what the operator asked to be rid of\n%s", label, other, w2)
		}
	}

	// AC6: the scope is DISCLOSED -- count and affordance, not silence.
	if !strings.Contains(w2, "+3") {
		t.Errorf("%s: window 2's overlay does not disclose how many agents it hides. Silent scoping "+
			"is indistinguishable from the accidental divergence the parity invariant kills\n%s",
			label, w2)
	}
	if !strings.Contains(w2, "shows all") {
		t.Errorf("%s: window 2's overlay hides agents without naming the affordance that reveals "+
			"them\n%s", label, w2)
	}

	// WINDOW 1's own surfaces are asserted to AGREE WITH ITS OWN PANE PLAN,
	// not to a hardcoded expectation -- because that agreement is the property
	// this bead actually owns. The overlay and the plan read the same
	// visiblePanesForWindow, so a window whose plan still holds an agent must
	// still list it, and a window whose plan has released one must disclose the
	// gap. Either state is legal here; disagreeing with itself is not.
	//
	// This matters because the two startup orders reach DIFFERENT window-1
	// plans, and one of them looks wrong: with the assignment seeded before
	// window 2 attaches, window 1 keeps eng in its plan and renders it while
	// window 2 renders it too. That is the pane-plan partition (ini-9ka.7 /
	// ini-xq4r), which this bead does not touch -- the overlay is a faithful
	// mirror of whatever the plan says. Reported separately rather than
	// asserted here, because a rig that fails on someone else's defect stops
	// reporting on its own.
	w1 := nineISXScreen(w1emu)
	t.Logf("%s — WINDOW 1:\n%s", label, w1)
	w1Planned := 0
	for _, own := range []string{"eng1", "eng2"} {
		if strings.Contains(w1, own) {
			w1Planned++
		}
	}
	if w1Planned == 0 && !strings.Contains(w1, "shows all") {
		t.Errorf("%s: window 1 released the eng agents from its plan and did not disclose the "+
			"gap -- a scoped surface with no disclosure fails the amended invariant\n%s", label, w1)
	}
	if w1Planned == 2 && strings.Contains(w1, "shows all") {
		t.Errorf("%s: window 1 still lists both eng agents yet claims to be hiding some -- its "+
			"overlay and its plan disagree\n%s", label, w1)
	}
	if w1Planned == 2 {
		t.Logf("%s: NOTE — window 1 still renders eng1/eng2 while window 2 also renders them. "+
			"The overlay agrees with the plan, so this is the pane-plan partition, not this "+
			"bead's scoping. Reported to super separately.", label)
	}
}

// TestNineISXRig_ScopedOverlayBothStartupOrders is AC10.
func TestNineISXRig_ScopedOverlayBothStartupOrders(t *testing.T) {
	if os.Getenv("INITECH_9ISX") != "1" {
		t.Skip("set INITECH_9ISX=1 to run the composed two-window rig for ini-9isx")
	}
	bin := nineISXBuild(t)

	// ── ORDER A: assigned BEFORE window 2 attaches ──────────────────
	// The viewer meets a store that already describes its slice.
	t.Run("assign-then-attach", func(t *testing.T) {
		root := nineISXRoot(t, "group_window:\n    eng: window-2\n")
		_, w1pty, w1emu := nineISXStart(t, bin, root)
		time.Sleep(10 * time.Second)
		w1pty.Write([]byte("n")) // decline the consent overlay
		time.Sleep(2 * time.Second)

		_, w2pty, w2emu := nineISXStart(t, bin, root, "--window", "2")
		time.Sleep(12 * time.Second)
		w2pty.Write([]byte("n"))
		time.Sleep(12 * time.Second)

		nineISXAssert(t, "assign-then-attach", w1emu, w2emu, w1pty, w2pty)
	})

	// ── ORDER B: window 2 attaches FIRST, assignment arrives after ──
	// This is the order that has actually broken before: the viewer plans its
	// panes against a store that does not yet mention it, then has to react to
	// a move it did not make. A scoped surface computed once at attach is
	// correct here at attach and wrong forever after.
	t.Run("attach-then-assign", func(t *testing.T) {
		root := nineISXRoot(t, "")
		_, w1pty, w1emu := nineISXStart(t, bin, root)
		time.Sleep(10 * time.Second)
		w1pty.Write([]byte("n"))
		time.Sleep(2 * time.Second)

		_, w2pty, w2emu := nineISXStart(t, bin, root, "--window", "2")
		time.Sleep(12 * time.Second)
		w2pty.Write([]byte("n"))
		time.Sleep(2 * time.Second)

		// Assign eng to window 2 through the REAL modal on window 1 -- the
		// move routes through window-1 authority (la97), which is the only
		// write path this feature is allowed to use.
		w1pty.Write([]byte("`"))
		time.Sleep(600 * time.Millisecond)
		w1pty.Write([]byte("agents\r"))
		time.Sleep(2500 * time.Millisecond)
		t.Logf("W1 MODAL before m:\n%s", nineISXScreen(w1emu))
		w1pty.Write([]byte("m"))
		time.Sleep(3 * time.Second)
		t.Logf("W1 MODAL after m:\n%s", nineISXScreen(w1emu))
		w1pty.Write([]byte{0x1b}) // close the modal
		time.Sleep(4 * time.Second)

		// Assert the PRODUCT'S state, not the store file. An earlier draft
		// read assignments.yaml and failed on its contents while window 1's own
		// overlay plainly showed the move had happened -- the rig was asserting
		// a persistence detail that belongs to ini-la97, and calling a working
		// feature broken. What this bead owns is what the windows DISPLAY.
		if b, err := os.ReadFile(filepath.Join(root, ".initech", "assignments.yaml")); err == nil {
			t.Logf("assignments.yaml after the move: %q", b)
		}
		t.Logf("attach-then-assign — W1 at assertion point:\n%s", nineISXScreen(w1emu))

		// The precondition is checked on WINDOW 2, not window 1, and the
		// distinction was learned the hard way. Window 1's view of the split
		// depends on whether it currently believes window 2 is connected, and
		// this rig's window 2 does reattach mid-run -- its own notice line says
		// so on screen. Gating on a flapping precondition makes the rig report
		// on connection stability while claiming to report on scoping.
		// Window 2's own scope needs no such belief: a viewer scopes to itself.
		if w2 := nineISXScreen(w2emu); !strings.Contains(w2, "eng1") {
			t.Fatalf("attach-then-assign: window 2 never took ownership of the eng group after "+
				"the move, so the scoping assertions below would prove nothing\n%s", w2)
		}

		nineISXAssert(t, "attach-then-assign", w1emu, w2emu, w1pty, w2pty)
	})
}
