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
// It seeds DIVERGENT saved pane orders on BOTH windows, so numbering agreement
// cannot come from either window's local state agreeing by luck.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	cfg += "window_listen: \"127.0.0.1:7617\"\n"
	os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o644)

	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	// The operator's arrangement: eng on monitor 2.
	os.WriteFile(filepath.Join(root, ".initech", "assignments.yaml"),
		[]byte("group_window:\n    eng: window-2\n"), 0o644)
	// Divergent saved orders on BOTH windows.
	os.WriteFile(filepath.Join(root, ".initech", "layout.yaml"),
		[]byte("grid: 4x2\nmode: grid\norder:\n    - eng2\n    - eng1\n    - qa1\n    - pm\n    - growth\n    - shipper\n    - pmm\n    - super\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".initech", "layout-window-2.yaml"),
		[]byte("grid: 4x2\nmode: grid\norder:\n    - qa1\n    - super\n    - eng1\n    - pm\n    - eng2\n    - pmm\n    - growth\n    - shipper\n"), 0o644)

	bin := filepath.Join(t.TempDir(), "initech-6m4")
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
