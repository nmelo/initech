//go:build !windows

package tui

// sixm4_rig_test.go is the ini-6m4 composed acceptance rig: two REAL windows,
// REAL Claude agents, asserting the full parity matrix live.
//
// REAL Claude is load-bearing, not flourish: Claude's TUI emits scroll regions
// sized to its pane, and those bytes in the RingBuf replay are what crashed
// window 2 (ini-w6z -- 53-row replay into 24-row emulators). An sh-echo rig
// carries no scroll regions and missed that class three runs in a row. This is
// also why the rig asserts window 2's SURVIVAL through replay ingestion before
// it asserts anything cosmetic.
//
// Gated behind INITECH_6M4=1 because it spawns real Claude sessions and takes
// about a minute. Run it when touching the viewer's modal, layout, or the
// remote-pane replay path, and at release:
//
//	INITECH_6M4=1 go test ./internal/tui/ -run TestSixM4Rig -v -count=1 -timeout 500s
//
// It seeds a saved pane order for the session, so numbering agreement
// SUBJECT MOVED FOR WINDOW 2 (ini-fn77). The agents modal is main-window only
// now, so this rig no longer compares two modals: window 1 keeps its modal
// assertions (tier headers, whole-fleet membership, no false scope
// disclosure, expanded-is-inert), and WINDOW 2'S HALF IS THE OVERLAY -- the
// surface a viewer still has. What was dropped outright is cross-window
// NUMBERING PARITY, because grab-by-number is a modal affordance and there is
// no second numbering to agree with; it is not replaced by a weaker check.
// What was KEPT, re-pointed rather than deleted, is the propagation claim:
// window 1 hides an agent, window 2 must learn about it. The function name
// still says ModalParity and is left alone deliberately -- shipper's
// every-release gate invokes it by name.

// cannot come from either window's local state agreeing by luck.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

func TestSixM4Rig_ViewerModalParityAndReplaySurvival(t *testing.T) {
	if os.Getenv("INITECH_6M4") != "1" {
		t.Skip("set INITECH_6M4=1 to run the two-window composed acceptance rig (spawns real Claude sessions)")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}

	root, err := os.MkdirTemp("", "6m4rig")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer os.RemoveAll(root)

	// The operator's role order: declaration order differs from group order,
	// which is what makes numbering divergence expressible at all.
	roles := []string{"super", "pmm", "shipper", "growth", "pm", "qa1", "eng1", "eng2"}
	for _, r := range roles {
		os.MkdirAll(filepath.Join(root, r), 0o755)
		os.WriteFile(filepath.Join(root, r, "CLAUDE.md"), []byte("# "+r+"\n"), 0o644)
	}
	cfg := "project: sixm4\nroot: " + root + "\nroles:\n"
	for _, r := range roles {
		cfg += "    - " + r + "\n"
	}
	// Ephemeral per run (ini-tcxe). This rig carried the identical hard-coded
	// port defect as the ninisx rig; it has not bitten only because two people
	// have not run it at once yet, which is luck rather than a property.
	sixm4Addr := "127.0.0.1:" + strconv.Itoa(rigReserveFreePort(t))
	cfg += fmt.Sprintf("window_listen: %q\n", sixm4Addr)
	os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o644)

	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	// The assignment is DELIBERATELY NOT SEEDED here. Hand-writing the store
	// with "window-2" is exactly how ini-xq4r hid: the rig wrote the correct
	// identity form the modal's generator never produced, so six beads of
	// fixes shipped over a store the real UI could not create. The placement
	// leg below assigns eng via the modal's real m key instead -- the full
	// loop modal-writes -> store -> viewer-filters -> panes move.
	// A saved order for the session. The rig used to ALSO seed a
	// layout-window-2.yaml to give the two windows "divergent saved orders" --
	// but nothing ever read that file, in this rig or in any release, so the
	// divergence it described never existed and every assertion below keys off
	// window 1's order. ini-qodm retired the per-window surface and the inert
	// fixture with it: a viewer's arrangement is session-scoped by design and
	// starts fresh on attach.
	os.WriteFile(filepath.Join(root, ".initech", "layout.yaml"),
		[]byte("grid: 4x2\nmode: grid\norder:\n    - eng2\n    - eng1\n    - qa1\n    - pm\n    - growth\n    - shipper\n    - pmm\n    - super\n"), 0o644)

	// Named after its own test (ini-tcxe): rig binaries that share a name are
	// indistinguishable to pgrep, which is how one agent's live test process
	// got read as another's leak and killed.
	bin := filepath.Join(t.TempDir(), "initech-sixm4-rig")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	start := func(args ...string) (*exec.Cmd, *os.File, *vt.SafeEmulator) {
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
		return c, p, emu
	}

	w1, w1pty, w1emu := start()
	defer func() { w1pty.Close(); w1.Process.Kill(); w1.Process.Wait() }()
	time.Sleep(12 * time.Second)
	w1pty.Write([]byte("n")) // decline the notification-hook consent overlay
	time.Sleep(2 * time.Second)

	// DEFENSE 2 (ini-tcxe): prove window 1 is actually serving before a viewer
	// dials. Without it, a rig whose bind lost a race still produces a verdict
	// -- about whichever process did win the port.
	rigRequireServing(t, sixm4Addr)

	w2, w2pty, w2emu := start("--window", "2")
	defer func() { w2pty.Close(); w2.Process.Kill(); w2.Process.Wait() }()

	// ── 1. SURVIVAL through real replay ingestion ───────────────────
	// The operator's window 2 died ~2s after connect, in a relaunch loop.
	for i := 0; i < 6; i++ {
		time.Sleep(5 * time.Second)
		if w2.ProcessState != nil || w2.Process.Signal(syscall.Signal(0)) != nil {
			t.Fatalf("window 2 died within %ds of attach -- the ini-w6z crash loop is back "+
				"(check .initech/crash.log in the rig root before it is removed)", (i+1)*5)
		}
	}
	w2pty.Write([]byte("n"))
	time.Sleep(2 * time.Second)

	openModal := func(p *os.File) {
		p.Write([]byte("`"))
		time.Sleep(600 * time.Millisecond)
		p.Write([]byte("agents\r"))
		time.Sleep(2500 * time.Millisecond)
	}
	// ── EMPTY-VIEWER HINT (ini-9fn): before anything is assigned, window 2
	// owns no groups and must show the operator-decided hint line -- not the
	// bare black screen he explicitly rejected after living through a crash
	// loop that looked exactly like it.
	w2empty := strings.Join(nonEmpty(snapRows(w2emu)), "\n")
	if !strings.Contains(w2empty, "no groups assigned to this window") {
		t.Errorf("window 2 with nothing assigned shows no hint line -- the bare empty window "+
			"the operator rejected\nW2:\n%s", w2empty)
	}

	// ── PLACEMENT (ini-xq4r): assign eng via the REAL modal ─────────
	// Select eng1 (the '/' search reaches it by name) and press m: the group
	// moves to a NEW second window identity produced by the modal's own
	// generator -- the form under test.
	// The seeded window-1 layout order puts eng2 first, so the modal's
	// DEFAULT selection is already an eng agent -- 'm' with no navigation
	// moves the eng group. (A first draft navigated by '/' search; search
	// mode intercepts every subsequent rune, so the 'm' was swallowed into
	// the search buffer and no move ever happened -- the rig then correctly
	// reported 'assignment moved nothing', which was true of the RIG.)
	openModal(w1pty)
	w1pty.Write([]byte("m"))
	time.Sleep(2 * time.Second)
	if b, err := os.ReadFile(filepath.Join(root, ".initech", "assignments.yaml")); err == nil {
		t.Logf("assignments.yaml after m: %q", string(b))
	} else {
		t.Logf("assignments.yaml after m: UNREADABLE (%v) -- the move never persisted", err)
	}
	t.Logf("W1 MODAL after m:\n%s", strings.Join(nonEmpty(snapRows(w1emu)), "\n"))
	w1pty.Write([]byte("\x1b")) // close the modal so the pane area is readable
	time.Sleep(2 * time.Second)

	w2pane := strings.Join(nonEmpty(snapRows(w2emu)), "\n")
	// RENDERED-pane evidence only: pane titles and the window bar both print
	// " N name " (index, space, name) -- the overlay prints "○ host:name" and
	// the modal "[x] name", so this regexp matches exactly the surfaces that
	// mean "this window RENDERS this agent". The first draft of this assertion
	// used bare Contains and matched window 2's OVERLAY (a fleet surface that
	// legitimately lists everything), passing while the pane area was empty.
	engRendered := regexp.MustCompile(` \d+ eng[12] `)
	// Window 2's rendered evidence is pane CONTENT, not chrome: remote panes
	// do not draw the " N name " title local panes do (measured from the
	// dump), but each agent's Claude session prints its workspace path, which
	// ends in the agent's directory. A window rendering /eng1 and /eng2
	// content is rendering the eng panes.
	for _, marker := range []string{"/eng1", "/eng2"} {
		if !strings.Contains(w2pane, marker) {
			t.Errorf("window 2's pane area shows no %s content after the modal move -- "+
				"assignment moved the modal, not the panes (ini-xq4r)\nW2:\n%s", marker, w2pane)
		}
	}
	// The hint VANISHED the moment the group arrived (same frame class as the
	// move notice; no state anyone has to clear).
	if strings.Contains(w2pane, "no groups assigned to this window") {
		t.Errorf("window 2 still shows the empty-viewer hint while rendering its assigned "+
			"group -- the hint is covering live panes\nW2:\n%s", w2pane)
	}
	w1pane := strings.Join(nonEmpty(snapRows(w1emu)), "\n")
	if hits := engRendered.FindAllString(w1pane, -1); len(hits) > 0 {
		t.Errorf("window 1 still renders eng pane titles %v while window 2 is connected and "+
			"owns the group -- 'exactly one window' violated", hits)
	}

	// WINDOW 1 ONLY (ini-fn77): the agents modal is main-window only, so there
	// is no second modal to compare against. Window 2's half of this rig moved
	// to the OVERLAY, which is what a viewer still has.
	openModal(w1pty)

	w1rows := nonEmpty(snapRows(w1emu))
	w2rows := nonEmpty(snapRows(w2emu))
	w1screen := strings.Join(w1rows, "\n")
	_ = w2rows // window 2 has no modal to compare; its half of this rig is the overlay below

	// ── 2. The modal is TIERED and WHOLE-FLEET from every window ────
	//
	// THIRD DECISION ON THIS SURFACE, each recorded in the assertion it
	// replaced: StayWholeFleet -> ScopedByDefault (ini-9isx) ->
	// AlwaysWholeFleet (operator, 2026-08-15: "why do I need to press a to
	// show all, I never asked for that"). This block previously asserted the
	// scoped 6/2 default that ini-l5sy reconciled it to; the operator reversed
	// that, so the modal is unscoped from every window and the expanded flag is
	// inert on membership (ini-ynrp, mirroring 6b796ed's unit reconciles).
	//
	// The TIER HEADERS survive every one of those turns, because they were
	// never the scoping: they are a fleet fact about which monitor an agent
	// lives on, and both windows show both.
	for _, tier := range []string{"monitor 1", "monitor 2"} {
		if !strings.Contains(w1screen, tier) {
			t.Errorf("window 1's modal is missing the %q tier header", tier)
		}
	}

	// AND IT CLAIMS TO HIDE NOTHING. An unscoped surface that still prints
	// "+N on window M" is worse than a wrong count: it teaches the operator
	// that something is missing and offers a keypress that no longer does
	// anything. The overlay keeps its disclosure -- the overlay still scopes --
	// which is why this assertion names the MODAL specifically.
	// Matched INSIDE THE MODAL'S OWN BOTTOM BORDER, never anywhere on screen.
	// The full-pane capture also carries the agents OVERLAY, which still scopes
	// and still discloses -- by design, and it is a different surface. A
	// whole-screen search for "+N on window M" reports the modal for the
	// overlay's line, which is what the first version of this assertion did.
	scopeNote := regexp.MustCompile("\u2514\u2500 \\+\\d+ on window \\d")
	for _, w := range []struct {
		name, screen string
	}{{"window 1", w1screen}} {
		if hit := scopeNote.FindString(w.screen); hit != "" {
			t.Errorf("%s's modal footer still discloses a scope it no longer has (%q); an "+
				"unscoped surface must not claim to hide anything\n%s", w.name, hit, w.screen)
		}
	}

	// ── 3. Fleet numbering agrees, despite divergent local orders ───
	numRe := regexp.MustCompile(`(\d+) \[[x ]\] (\w+)`)
	numbersOf := func(screen string) map[string]string {
		m := map[string]string{}
		for _, hit := range numRe.FindAllStringSubmatch(screen, -1) {
			m[hit[2]] = hit[1]
		}
		return m
	}

	// CROSS-WINDOW NUMBERING PARITY IS GONE (ini-fn77) and is not replaced:
	// grab-by-number is a modal affordance, and window 2 has no modal, so
	// there is no second numbering for the two to agree or disagree about.
	// What remains is that window 1's modal is whole-fleet.
	dn1 := numbersOf(w1screen)
	if len(dn1) != len(roles) {
		t.Fatalf("window 1's modal lists %d agents (want %d -- the modal is whole-fleet)"+
			"\nW1:\n%s", len(dn1), len(roles), w1screen)
	}
	// EXPANDED IS INERT ON MEMBERSHIP, and this leg proves it rather than
	// assuming it: the same whole fleet before and after 'a'. Window 1 only
	// since ini-fn77 -- the expanded toggle lives inside the modal, so a
	// viewer has no expanded state to compare.
	w1pty.Write([]byte("a"))
	time.Sleep(2500 * time.Millisecond)
	w1exp := strings.Join(nonEmpty(snapRows(w1emu)), "\n")

	n1 := numbersOf(w1exp)
	t.Logf("NUMBERING default  w1=%v", dn1)
	t.Logf("NUMBERING expanded w1=%v", n1)
	if len(n1) != len(roles) {
		t.Fatalf("EXPANDED modal lists %d agents (want %d). Expanded is inert on membership "+
			"now, so differing from the default view means the flag still moves something "+
			"it should not.\nW1:\n%s", len(n1), len(roles), w1exp)
	}
	w1pty.Write([]byte("a")) // back to the default view
	time.Sleep(1500 * time.Millisecond)

	// ── 4. A hide in window 1 reaches window 2's reopened modal ─────
	w1pty.Write([]byte("\x1b[C")) // select the second cell
	time.Sleep(400 * time.Millisecond)
	w1pty.Write([]byte(" ")) // hide it
	time.Sleep(3 * time.Second)

	hiddenRe := regexp.MustCompile(`\[ \] (\w+)`)
	hit := hiddenRe.FindStringSubmatch(strings.Join(nonEmpty(snapRows(w1emu)), "\n"))
	if hit == nil {
		t.Fatal("window 1's hide did not take effect in its own modal; the rig cannot test propagation")
	}
	hiddenAgent := hit[1]

	// WINDOW 2 LEARNS ABOUT IT ON ITS OVERLAY (ini-fn77 re-scope).
	//
	// This leg used to reopen window 2's agents MODAL and look for the hidden
	// agent there. The modal is main-window only now, so that surface is gone
	// -- but the PROPAGATION it was testing is not, and it is the half worth
	// gating: a viewer must not go on rendering a fleet state window 1 has
	// changed. The overlay is where a viewer observes it, and it is a surface
	// window 2 still has. Measured, not assumed: the overlay marks the hidden
	// agent "○ eng1 [h]" and footers "1 visible, 1 hidden".
	w1pty.Write([]byte("\x1b")) // close window 1's modal so nothing overlaps
	time.Sleep(2500 * time.Millisecond)
	w2after := strings.Join(nonEmpty(snapRows(w2emu)), "\n")

	marked := regexp.MustCompile(`\x{25cb} ` + hiddenAgent + ` \[h\]`)
	if !marked.MatchString(w2after) {
		t.Errorf("window 1 hid %s and window 2's overlay does not mark it hidden -- the "+
			"viewer is rendering its startup fleet-state snapshot, so the two windows "+
			"disagree about what the fleet looks like\nW2:\n%s", hiddenAgent, w2after)
	}
	// SPECIFICITY: the marker must belong to the hidden agent, not decorate
	// every row. Without this, an overlay that marked everything would pass.
	other := "eng2"
	if hiddenAgent == other {
		other = "eng1"
	}
	if regexp.MustCompile(`\x{25cb} ` + other + ` \[h\]`).MatchString(w2after) {
		t.Errorf("window 2's overlay marks %s hidden too, but only %s was hidden -- the "+
			"marker is not reporting per-agent state\nW2:\n%s", other, hiddenAgent, w2after)
	}
}
