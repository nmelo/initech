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

	openModal(w1pty)
	openModal(w2pty)

	w1rows := nonEmpty(snapRows(w1emu))
	w2rows := nonEmpty(snapRows(w2emu))
	w1screen := strings.Join(w1rows, "\n")
	w2screen := strings.Join(w2rows, "\n")

	// ── 2. Tiers on BOTH windows ────────────────────────────────────
	for _, tier := range []string{"monitor 1", "monitor 2"} {
		if !strings.Contains(w1screen, tier) {
			t.Errorf("window 1's modal is missing the %q tier header", tier)
		}
		if !strings.Contains(w2screen, tier) {
			t.Errorf("window 2's modal is missing the %q tier header -- the viewer's tier "+
				"gate is off again (WindowListen is empty on a viewer BY CONSTRUCTION)", tier)
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
	n1, n2 := numbersOf(w1screen), numbersOf(w2screen)
	if len(n1) != len(roles) || len(n2) != len(roles) {
		t.Fatalf("modal parse found %d/%d numbered agents (want %d each)\nW1:\n%s\nW2:\n%s",
			len(n1), len(n2), len(roles), w1screen, w2screen)
	}
	for agent, num := range n1 {
		if n2[agent] != num {
			t.Errorf("agent %s is number %s in window 1 but %s in window 2 -- grab-by-number "+
				"acts on different agents per window", agent, num, n2[agent])
		}
	}

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

	w2pty.Write([]byte("\x1b")) // close
	time.Sleep(1500 * time.Millisecond)
	openModal(w2pty)
	if !strings.Contains(strings.Join(nonEmpty(snapRows(w2emu)), "\n"), "[ ] "+hiddenAgent) {
		t.Errorf("window 1 hid %s, and window 2's REOPENED modal still shows it visible -- "+
			"the follower is reading its startup fleet-state snapshot, and a toggle from "+
			"window 2 would un-hide what window 1 hid", hiddenAgent)
	}
}
