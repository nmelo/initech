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
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// nineISXRoot writes a project whose agents are shells, and returns its root.
func nineISXRoot(t *testing.T, assignments string) (root string, nonceAgent string) {
	t.Helper()
	root, err := os.MkdirTemp("", "9isxrig")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	port := rigReserveFreePort(t)

	// DEFENSE 3 -- IDENTITY (ini-tcxe). One agent whose NAME carries this run's
	// nonce, placed in the group window 2 owns so window 2 must render it in
	// its own overlay. If this rig ever dials another run's window 1 -- the
	// contamination that made an entire evening of measurements worthless --
	// it renders THAT run's agents and this name is absent, so the rig aborts
	// instead of reporting a confident verdict about someone else's process.
	//
	// The nonce IS the port: already unique among concurrently running rigs,
	// already threaded to both processes, and it needs no clock or RNG. Two
	// SEQUENTIAL runs can reuse a port, and that is fine -- a finished run
	// cannot contaminate anything.
	//
	// "eng" prefix on purpose: groupFor keys off the eng*/qa* prefix, so this
	// lands in the eng band, which is the group the fixtures assign to
	// window 2.
	nonceAgent = fmt.Sprintf("engnonce%d", port)
	roles := []string{"super", "pm", "qa1", "eng1", "eng2", nonceAgent}
	cfg := "project: ninisx\nroot: " + root + "\nroles:\n"
	for _, r := range roles {
		os.MkdirAll(filepath.Join(root, r), 0o755)
		os.WriteFile(filepath.Join(root, r, "CLAUDE.md"), []byte("# "+r+"\n"), 0o644)
		cfg += "    - " + r + "\n"
	}
	cfg += fmt.Sprintf("window_listen: %q\nrole_overrides:\n", "127.0.0.1:"+strconv.Itoa(port))
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
	return root, nonceAgent
}

// nineISXListenAddr reads back the address this run reserved, so the rig can
// prove its own window 1 is serving before any window 2 dials.
func nineISXListenAddr(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "initech.yaml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "window_listen:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "window_listen:")), "\"")
		}
	}
	t.Fatal("config has no window_listen")
	return ""
}

// rigRequireServing is DEFENSE 2 (ini-tcxe): a rig whose server never came
// up must not produce a verdict.
//
// Its limit is deliberate and is why defense 3 exists: a successful dial proves
// SOMETHING is listening, never that it is OURS. Window 1's bind failure is
// non-fatal in the PRODUCT by design (tui.go:893 -- a secondary window is an
// enhancement and must not kill a session whose agents are already running),
// which is correct and unchanged; the abort belongs here.
func rigRequireServing(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("nothing is listening on %s: window 1 never started its server, so any verdict from "+
		"this run would be about a fleet that does not exist", addr)
}


// rigReserveFreePort reserves a port for THIS rig run by binding :0, reading what
// the kernel assigned, and releasing it.
//
// The first version hard-coded 127.0.0.1:7629, and that was not a style
// problem -- it made the rig NON-REENTRANT, and the consequence is worse than a
// failed bind. Whichever window 1 wins the port serves; the loser's window 2
// then dials the WINNER'S process, which is running a different test root with
// a different fixture. The result is a viewer rendering agents that do not
// match the assertions, with no code defect required. eng1 and I both ran this
// rig on one machine on the same evening, so BOTH our measurements from that
// window are suspect -- including an attribution claim that reached super and a
// screen capture of mine. A rig that silently measures another run's process is
// not a slow test, it is a fabricated finding.
//
// A bind-then-release leaves a small race, and that is a deliberate trade: the
// alternative is threading a resolved address from window 1 to window 2, but
// the viewer dials what the CONFIG says, and the config must exist before
// window 1 starts. An ephemeral port per run collapses the collision window
// from "the whole evening" to "microseconds", which is the part that was
// actually hurting.
func rigReserveFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func nineISXBuild(t *testing.T) string {
	t.Helper()
	// Named after its OWN test (ini-tcxe). Both composed rigs used to build a
	// binary called initech-9isx, so pgrep could not tell one agent's rig
	// process from another's -- which is how a peer reasonably read my live
	// test process as their own leak and killed it. An identity that does not
	// identify is the same defect as a port that is not exclusive.
	bin := filepath.Join(t.TempDir(), "initech-ninisx-rig")
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


// nineISXAwait polls a window's screen until cond holds, and reports how long
// it took. Returns false on timeout.
//
// EVERY GATING CHECK IN THIS RIG GOES THROUGH IT, and that is a correctness
// requirement rather than tidiness. The first version slept a fixed interval
// and sampled once; it passed repeatedly and then failed once, on a run where
// propagation took longer than the sleep. A single sample after a fixed sleep
// measures the sampling instant, not the system -- the same error that produced
// a phantom tier-1 gap in ini-zjhg and nearly had me contradict a peer's
// verification here on one observation. A flaky rig is worse than no rig once
// it is a ship gate: it teaches people to re-run until green, which is exactly
// how a real red gets waved through.
func nineISXAwait(emu *vt.SafeEmulator, cond func(string) bool, limit time.Duration) (time.Duration, bool) {
	start := time.Now()
	for time.Since(start) < limit {
		if cond(nineISXScreen(emu)) {
			return time.Since(start), true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return limit, false
}

// nineISXAssert runs every AC 4/6/7/8/9 check against a live window-2 screen.
// Shared by both startup orders so the two paths are held to identical
// expectations rather than each getting the assertions its own quirks pass.
func nineISXAssert(t *testing.T, label, nonce string, w1emu, w2emu *vt.SafeEmulator, w1pty, w2pty *os.File) {
	t.Helper()

	w2 := nineISXScreen(w2emu)
	t.Logf("%s — WINDOW 2 OVERLAY:\n%s", label, w2)

	// DEFENSE 3, asserted BEFORE anything else (ini-tcxe): every verdict below
	// is about the fleet on this screen, so first prove the screen is ours.
	// Without this the rig will happily describe another run's process in
	// convincing detail -- which is exactly what it did all through ini-x5ob.
	if !strings.Contains(w2, nonce) {
		t.Fatalf("%s: window 2 is not rendering THIS run's fleet -- the nonce agent %q is absent, so "+
			"it dialed another window 1. Every assertion below would be a confident statement about "+
			"someone else's process.\n%s", label, nonce, w2)
	}

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
		root, nonce := nineISXRoot(t, "group_window:\n    eng: window-2\n")
		_, w1pty, w1emu := nineISXStart(t, bin, root)
		time.Sleep(10 * time.Second)
		w1pty.Write([]byte("n")) // decline the consent overlay
		time.Sleep(2 * time.Second)
		rigRequireServing(t, nineISXListenAddr(t, root))

		_, w2pty, w2emu := nineISXStart(t, bin, root, "--window", "2")
		time.Sleep(12 * time.Second)
		w2pty.Write([]byte("n"))
		if _, ok := nineISXAwait(w2emu, func(s string) bool {
			return strings.Contains(s, "eng1") && strings.Contains(s, "eng2")
		}, 30*time.Second); !ok {
			t.Fatalf("window 2 never rendered the agents it owns\n%s", nineISXScreen(w2emu))
		}

		nineISXAssert(t, "assign-then-attach", nonce, w1emu, w2emu, w1pty, w2pty)
	})

	// ── ORDER B: window 2 attaches FIRST, assignment arrives after ──
	// This is the order that has actually broken before: the viewer plans its
	// panes against a store that does not yet mention it, then has to react to
	// a move it did not make. A scoped surface computed once at attach is
	// correct here at attach and wrong forever after.
	t.Run("attach-then-assign", func(t *testing.T) {
		root, nonce := nineISXRoot(t, "")
		_, w1pty, w1emu := nineISXStart(t, bin, root)
		time.Sleep(10 * time.Second)
		w1pty.Write([]byte("n"))
		time.Sleep(2 * time.Second)
		rigRequireServing(t, nineISXListenAddr(t, root))

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
		if _, ok := nineISXAwait(w1emu, func(s string) bool {
			return strings.Contains(s, "initech agents")
		}, 20*time.Second); !ok {
			t.Fatalf("the agents modal never opened on window 1; the move cannot be driven\n%s",
				nineISXScreen(w1emu))
		}
		w1pty.Write([]byte("m"))
		if _, ok := nineISXAwait(w1emu, func(s string) bool {
			return strings.Contains(s, "monitor 2")
		}, 20*time.Second); !ok {
			t.Fatalf("the eng group never moved to a second monitor after 'm'; the rig drove the "+
				"modal wrong rather than the product failing\n%s", nineISXScreen(w1emu))
		}
		w1pty.Write([]byte{0x1b}) // close the modal

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
		took, ok := nineISXAwait(w2emu, func(s string) bool {
			return strings.Contains(s, "eng1")
		}, 30*time.Second)
		if !ok {
			t.Fatalf("attach-then-assign: window 2 never took ownership of the eng group after "+
				"the move, so the scoping assertions below would prove nothing\n%s",
				nineISXScreen(w2emu))
		}
		t.Logf("attach-then-assign: window 2 took ownership after %v", took.Round(time.Millisecond))

		nineISXAssert(t, "attach-then-assign", nonce, w1emu, w2emu, w1pty, w2pty)
	})
}
