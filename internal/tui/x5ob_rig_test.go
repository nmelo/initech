//go:build !windows

package tui

// The ini-x5ob composed rig: real binaries, real PTYs, both startup orders,
// and a real disconnect/reattach around the move.
//
// WHY THIS EXISTS SEPARATELY FROM THE 9isx RIG. Not to re-derive it -- that
// rig's fixture and helpers are reused verbatim below. It exists because the
// 9isx rig hard-codes window_listen 127.0.0.1:7629, and a fixed port makes a
// composed rig NON-REENTRANT: two agents on one machine, or one agent running
// -count=2, produce a second window 1 that cannot bind while its window 2
// happily dials into the FIRST run's process -- a different binary over a
// different fixture. That does not fail loudly. It reports "window 2 renders
// nothing" with no code defect required, and it cost this bead a wrong
// attribution and a revert justified by a measurement that had never touched
// the instrumented binary. Every rig here takes a port nobody else holds.
//
// The assertions are SETS, never counts (this bug's own measurement trap: the
// stale membership and the correct one were both four panes).

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// x5obFreePort reserves a port by binding it, reading the assignment, and
// closing. The close-then-reuse window is a race in principle; in practice the
// kernel does not hand the same ephemeral port back within a test's lifetime,
// and the alternative -- a fixed port -- is not a smaller race but a
// guaranteed collision, which is the defect this helper exists to remove.
func x5obFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// x5obRoot writes a project whose agents are shells, on a port of its own.
// Mirrors nineISXRoot's fixture deliberately -- same roles, same order, same
// eng band -- so a difference between the two rigs is never a difference in
// what they set up.
func x5obRoot(t *testing.T, assignments string, port int) string {
	t.Helper()
	root, err := os.MkdirTemp("", "x5obrig")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	// SIX agents, matching the rig that actually reproduces ini-2pce. Five did
	// not, across many runs; the sixth is not decoration. A rig one agent
	// lighter than the phenomenon cannot speak to its absence.
	roles := []string{"super", "pm", "qa1", "eng1", "eng2", "eng3"}
	cfg := "project: x5ob\nroot: " + root + "\nroles:\n"
	for _, r := range roles {
		os.MkdirAll(filepath.Join(root, r), 0o755)
		os.WriteFile(filepath.Join(root, r, "CLAUDE.md"), []byte("# "+r+"\n"), 0o644)
		cfg += "    - " + r + "\n"
	}
	cfg += fmt.Sprintf("window_listen: \"127.0.0.1:%d\"\nrole_overrides:\n", port)
	for _, r := range roles {
		cfg += "    " + r + ":\n        command: [\"sh\"]\n"
	}
	os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o644)

	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	os.WriteFile(filepath.Join(root, ".initech", "layout.yaml"),
		[]byte("grid: 3x2\nmode: grid\norder:\n    - eng1\n    - eng2\n    - eng3\n    - super\n    - pm\n    - qa1\n"), 0o644)
	if assignments != "" {
		os.WriteFile(filepath.Join(root, ".initech", "assignments.yaml"), []byte(assignments), 0o644)
	}
	return root
}

// x5obProvesOwnBinary asserts that the process on this run's port is THIS
// test's binary, by requiring the instrumented hello-path line to appear in
// THIS root's log.
//
// Liveness is not identity, and that distinction is the whole reason this
// bead misdiagnosed itself: a rig that rebuilds its binary, starts it, and
// finds something answering on the port has proved only that SOMETHING is
// answering. When a previous run's process held the port, window 2 dialed it,
// the rig "reproduced" a bug, and zero instrumented lines were written --
// because the reproduction never touched the binary under test. A
// reproduction that never touches your instrumented binary is not a
// reproduction.
func x5obProvesOwnBinary(t *testing.T, root string, port int) {
	t.Helper()
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("nothing is listening on this run's port %d -- window 1 never bound it", port)
	}
	c.Close()

	b, err := os.ReadFile(filepath.Join(root, ".initech", "initech.log"))
	if err != nil {
		t.Fatalf("read this run's log: %v", err)
	}
	// The marker is window 1 announcing the bind, WITH THIS RUN'S PORT in it.
	// Deliberately not an ownership line: that exists only in builds that have
	// the partition code, and this gate has to hold identically on a
	// before-and-after attribution run or it cannot compare them.
	want := fmt.Sprintf(":%d", port)
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "[window-server] listening") && strings.Contains(line, want) {
			return
		}
	}
	t.Fatalf("this run's log has no window-server bind on port %d -- window 1 did not take "+
		"the socket, so whatever window 2 is talking to is a process this test did not "+
		"start, and every assertion below would be measuring it instead", port)
}

// x5obEvidence prints the partition decisions THIS run's window 1 actually
// made. Attached to every result, pass or fail, because a rig that reports
// only its verdict cannot distinguish "the code decided wrongly" from "the
// code never ran" -- the two outcomes this bead has now confused once each.
func x5obEvidence(t *testing.T, root string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".initech", "initech.log"))
	if err != nil {
		t.Logf("no log to attach: %v", err)
		return
	}
	var keep []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "[ownership]") {
			if i := strings.Index(line, "level="); i >= 0 {
				line = line[i:]
			}
			keep = append(keep, line)
		}
	}
	t.Logf("=== THIS RUN'S PARTITION DECISIONS (%d) ===\n%s", len(keep), strings.Join(keep, "\n"))
	// Registered as cleanup, not checked here: this runs BEFORE the
	// assertions, so t.Failed() is necessarily false at this point and a
	// check here would preserve nothing, every time, silently.
	t.Cleanup(func() {
		dir := os.Getenv("INITECH_X5OB_ARTIFACTS")
		if !t.Failed() || dir == "" {
			return
		}
		full, err := os.ReadFile(filepath.Join(root, ".initech", "initech.log"))
		if err != nil {
			return
		}
		name := filepath.Join(dir, "x5ob-fail-"+strings.ReplaceAll(t.Name(), "/", "_")+".log")
		//nolint:errcheck // best-effort artifact
		os.WriteFile(name, full, 0o644)
		t.Logf("full log preserved: %s", name)
	})
}

// x5obAgentsIn reports which of the fleet's agents appear on a screen, as a
// SET. Counts are the trap this bug was built out of.
func x5obAgentsIn(screen string) map[string]bool {
	out := map[string]bool{}
	for _, a := range []string{"eng1", "eng2", "eng3", "super", "pm", "qa1"} {
		if strings.Contains(screen, a) {
			out[a] = true
		}
	}
	return out
}

func x5obKeys(set map[string]bool) string {
	var ks []string
	for _, a := range []string{"eng1", "eng2", "eng3", "super", "pm", "qa1"} {
		if set[a] {
			ks = append(ks, a)
		}
	}
	if len(ks) == 0 {
		return "(none)"
	}
	return strings.Join(ks, ",")
}

// x5obNeverBareAndSilent is the ini-9fn invariant measured on a real screen:
// a viewer that shows no agents must SAY why, in one of the two decided
// sentences.
//
// This is the assertion the bead's worst state needed and did not have. A
// silently empty window 2 is visually identical to the vanished-pane symptom
// this bug was filed for, and it is the state the operator rejected outright
// when 9fn was written. Absence of panes alone is not a failure -- a viewer
// legitimately owns nothing sometimes -- so what is asserted is the pairing:
// no agents AND no explanation is never allowed.
func x5obNeverBareAndSilent(t *testing.T, label, screen string, agents map[string]bool) {
	t.Helper()
	if len(agents) > 0 {
		return
	}
	if strings.Contains(screen, "waiting for window 1") ||
		strings.Contains(screen, "no groups assigned") {
		return
	}
	t.Errorf("%s: window 2 shows no agents AND no explanation -- the bare unexplained "+
		"window ini-9fn exists to prevent, and the exact shape of the symptom this "+
		"bead was filed for. One of the two decided sentences must be on screen.\n%s",
		label, screen)
}

// TestX5obRig_AssignThenAttach is the order the reverted fix broke, measured on
// a port nobody else holds.
//
// The fleet is started with eng ALREADY assigned to window 2, so window 1
// computes its partition once with no viewer connected and correctly folds
// everything back to itself. Window 2 then attaches. What it renders is the
// whole question: its own agents (correct), nothing (the hole the first fix
// opened), or nothing-without-explanation (the state ini-9fn exists to
// prevent).
func TestX5obRig_AssignThenAttach(t *testing.T) {
	if os.Getenv("INITECH_X5OB") == "" {
		t.Skip("composed rig: set INITECH_X5OB=1 (real binaries, PTYs, ~40s)")
	}
	port := x5obFreePort(t)
	bin := nineISXBuild(t)
	root := x5obRoot(t, "group_window:\n    eng: window-2\n", port)

	_, w1pty, w1emu := nineISXStart(t, bin, root)
	time.Sleep(10 * time.Second)
	w1pty.Write([]byte("n")) // decline the consent overlay
	time.Sleep(2 * time.Second)
	x5obProvesOwnBinary(t, root, port)

	_, _, w2emu := nineISXStart(t, bin, root, "--window", "2")
	time.Sleep(12 * time.Second)

	x5obEvidence(t, root)
	w2screen := nineISXScreen(w2emu)
	w1 := x5obAgentsIn(nineISXScreen(w1emu))
	w2 := x5obAgentsIn(w2screen)
	t.Logf("window 1 shows: %s", x5obKeys(w1))
	t.Logf("window 2 shows: %s", x5obKeys(w2))
	x5obNeverBareAndSilent(t, "assign-then-attach", w2screen, w2)

	for _, a := range []string{"eng1", "eng2"} {
		if !w2[a] {
			t.Errorf("window 2 does not show %q, which is assigned to it -- the operator's "+
				"monitor is missing an agent it owns (window 2 set: %s)", a, x5obKeys(w2))
		}
		if w1[a] {
			t.Errorf("window 1 still shows %q after it moved to window 2 -- the duplicate "+
				"render, which is the mode the partition authority exists to kill "+
				"(window 1 set: %s)", a, x5obKeys(w1))
		}
	}
	for _, a := range []string{"super", "pm", "qa1"} {
		if !w1[a] {
			t.Errorf("window 1 lost %q, which was never assigned away (window 1 set: %s)",
				a, x5obKeys(w1))
		}
	}
}

// TestX5obRig_AttachThenAssign is the order that already worked, kept as a
// regression anchor rather than dropped once it passed.
//
// It is the order in which the first fix looked correct, and that is exactly
// why it stays: the move here goes THROUGH the agents modal, which seeds the
// group map on the way past. That accident is what made the broken order look
// like a startup-order flake instead of an empty-input bug, and a rig that
// only runs the failing case cannot see the difference again.
func TestX5obRig_AttachThenAssign(t *testing.T) {
	if os.Getenv("INITECH_X5OB") == "" {
		t.Skip("composed rig: set INITECH_X5OB=1 (real binaries, PTYs, ~45s)")
	}
	port := x5obFreePort(t)
	bin := nineISXBuild(t)
	root := x5obRoot(t, "", port)

	_, w1pty, w1emu := nineISXStart(t, bin, root)
	time.Sleep(10 * time.Second)
	w1pty.Write([]byte("n"))
	time.Sleep(2 * time.Second)
	x5obProvesOwnBinary(t, root, port)

	_, w2pty, w2emu := nineISXStart(t, bin, root, "--window", "2")
	time.Sleep(12 * time.Second)
	w2pty.Write([]byte("n"))
	time.Sleep(2 * time.Second)

	// The move routes through window-1 authority (ini-la97), which is the only
	// write path this feature is allowed to use.
	w1pty.Write([]byte("`"))
	time.Sleep(600 * time.Millisecond)
	w1pty.Write([]byte("agents\r"))
	time.Sleep(2500 * time.Millisecond)
	w1pty.Write([]byte("m"))
	time.Sleep(3 * time.Second)
	w1pty.Write([]byte{0x1b}) // close the modal
	time.Sleep(5 * time.Second)

	x5obEvidence(t, root)
	w2screen := nineISXScreen(w2emu)
	w1 := x5obAgentsIn(nineISXScreen(w1emu))
	w2 := x5obAgentsIn(w2screen)
	t.Logf("window 1 shows: %s", x5obKeys(w1))
	t.Logf("window 2 shows: %s", x5obKeys(w2))
	x5obNeverBareAndSilent(t, "attach-then-assign", w2screen, w2)

	for _, a := range []string{"eng1", "eng2"} {
		if !w2[a] {
			t.Errorf("window 2 does not show %q after the move (window 2 set: %s)", a, x5obKeys(w2))
		}
		if w1[a] {
			t.Errorf("window 1 still shows %q after moving it away -- the duplicate render "+
				"(window 1 set: %s)", a, x5obKeys(w1))
		}
	}
}

// TestX5obRig_ReattachIsReserved covers the hole that serve-once-at-handshake
// creates: a viewer that disconnects and comes back.
//
// Ownership is pushed by window 1, so a viewer's copy is only ever as fresh as
// the last thing it was told. A window that drops and returns has forgotten
// everything, and if window 1 treats it as already-informed the operator gets
// a permanently blank monitor that only a full restart fixes. The operator's
// real session did exactly this -- a disconnect/reattach cycle mid-day -- which
// is why it is a rig leg and not a unit test.
func TestX5obRig_ReattachIsReserved(t *testing.T) {
	if os.Getenv("INITECH_X5OB") == "" {
		t.Skip("composed rig: set INITECH_X5OB=1 (real binaries, PTYs, ~55s)")
	}
	port := x5obFreePort(t)
	bin := nineISXBuild(t)
	root := x5obRoot(t, "group_window:\n    eng: window-2\n", port)

	_, w1pty, w1emu := nineISXStart(t, bin, root)
	time.Sleep(10 * time.Second)
	w1pty.Write([]byte("n"))
	time.Sleep(2 * time.Second)
	x5obProvesOwnBinary(t, root, port)

	w2cmd, w2pty, w2emu := nineISXStart(t, bin, root, "--window", "2")
	time.Sleep(12 * time.Second)
	if before := x5obAgentsIn(nineISXScreen(w2emu)); !before["eng1"] {
		t.Fatalf("window 2 never took its agents on the FIRST attach (set: %s); the reattach "+
			"assertion below would prove nothing", x5obKeys(before))
	}

	// Drop it the way a closed terminal does, then bring it back.
	w2pty.Close()
	w2cmd.Process.Kill()
	w2cmd.Process.Wait()
	time.Sleep(6 * time.Second)

	_, _, w2bemu := nineISXStart(t, bin, root, "--window", "2")
	time.Sleep(14 * time.Second)

	x5obEvidence(t, root)
	w2bscreen := nineISXScreen(w2bemu)
	w1 := x5obAgentsIn(nineISXScreen(w1emu))
	w2b := x5obAgentsIn(w2bscreen)
	x5obNeverBareAndSilent(t, "after reattach", w2bscreen, w2b)
	t.Logf("after reattach -- window 1 shows: %s", x5obKeys(w1))
	t.Logf("after reattach -- window 2 shows: %s", x5obKeys(w2b))

	for _, a := range []string{"eng1", "eng2"} {
		if !w2b[a] {
			t.Errorf("window 2 came back without %q -- it was served once at the first "+
				"handshake and never re-served, so the operator's monitor stays blank "+
				"until a full restart (window 2 set: %s)", a, x5obKeys(w2b))
		}
		if w1[a] {
			t.Errorf("window 1 kept rendering %q after window 2 returned (window 1 set: %s)",
				a, x5obKeys(w1))
		}
	}
}
