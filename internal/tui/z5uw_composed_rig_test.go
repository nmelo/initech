//go:build !windows

package tui

// z5uw_composed_rig_test.go -- the COMPOSED two-main/viewer rig for ini-z5uw.
//
// QA-authored (qa3), independent of eng3's unit fixtures. Real initech
// binaries, real PTYs, a real bind conflict between two full main processes,
// and a real viewer's rendered footer as the assertion surface -- the unit
// tests one file over (window_bind_notice_test.go, window_status_test.go)
// prove the notice/authority PREDICATES against a SimulationScreen; this
// proves the product claim eng3's own DONE flagged as still belonging to
// independent QA: which PROCESS a viewer actually lands on when two mains
// contend for the same window port.
//
// Reuses ninisx_rig_test.go's build/start/screen/await plumbing (same
// package). Run: INITECH_Z5UW=1 go test ./internal/tui/ -run TestZ5uwRig -v -timeout 180s

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// z5uwFlatScreen concatenates every row with NO separator, unlike
// nineISXScreen (which joins with "\n" and is meant for reading distinct
// rows). The bind-failure notice is a long wrapped line, and the OS-reported
// holder path can push words across a row boundary mid-word (eng3's own unit
// test hit this: "restart this main" split into "...then rest" / "art this
// main."). A newline-joined screen would never satisfy a Contains check
// across that break; concatenating raw, exactly as the terminal wrapped it,
// reconstructs the original unwrapped string.
func z5uwFlatScreen(emu *vt.SafeEmulator) string {
	const cols, rows = 130, 44
	var b strings.Builder
	for y := 0; y < rows; y++ {
		b.WriteString(emu.RowText(y, cols))
	}
	return b.String()
}

// z5uwRoot writes a minimal project (two shell agents, one window_listen
// port) so two of them can be pointed at the SAME port to force the bind
// conflict this bead is about.
func z5uwRoot(t *testing.T, port int) string {
	t.Helper()
	root, err := os.MkdirTemp("", "z5uwrig")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	roles := []string{"super", "eng1"}
	cfg := "project: z5uwrig\nroot: " + root + "\nroles:\n"
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
	os.WriteFile(filepath.Join(root, ".initech", "layout.yaml"),
		[]byte("grid: 2x1\nmode: grid\norder:\n    - super\n    - eng1\n"), 0o644)
	// A secondary-window assignment so a failed bind is the HIGH-emphasis
	// (red) case per window_status.go's renderWindowConnectionStatus -- the
	// scenario this bead exists for is a second window actually in play.
	os.WriteFile(filepath.Join(root, ".initech", "assignments.yaml"),
		[]byte("group_window:\n    eng: window-2\n"), 0o644)
	return root
}

func TestZ5uwRig_HeldPortNoticeAndViewerIdentity(t *testing.T) {
	if os.Getenv("INITECH_Z5UW") != "1" {
		t.Skip("set INITECH_Z5UW=1 to run the composed window-port bind-conflict rig for ini-z5uw")
	}
	bin := nineISXBuild(t)
	port := rigReserveFreePort(t)
	rootA := z5uwRoot(t, port)
	rootB := z5uwRoot(t, port)

	// Main A binds first and actually serves the window port.
	cmdA, _, _, _ := nineISXStart(t, bin, rootA)
	addr := nineISXListenAddr(t, rootA)
	rigRequireServing(t, addr)
	pidA := cmdA.Process.Pid
	t.Logf("main A pid=%d listening on %s", pidA, addr)

	// Main B starts against the SAME port and must lose the bind.
	cmdB, _, emuB, _ := nineISXStart(t, bin, rootB)
	pidB := cmdB.Process.Pid
	if pidB == pidA {
		t.Fatalf("rig bug: main A and main B share a pid")
	}

	elapsed, ok := nineISXAwait(emuB, func(string) bool {
		flat := z5uwFlatScreen(emuB)
		return strings.Contains(flat, "WINDOW PORT UNAVAILABLE") &&
			strings.Contains(flat, "FAILED") &&
			strings.Contains(flat, fmt.Sprintf("PID %d", pidA)) &&
			strings.Contains(flat, "restart this main")
	}, 30*time.Second)
	if !ok {
		t.Fatalf("main B (pid %d) never showed the held-port notice naming main A (pid %d) within 30s; last screen:\n%s",
			pidB, pidA, nineISXScreen(emuB))
	}
	t.Logf("main B notice named main A's pid/age/remedy after %s", elapsed)

	// A viewer attached over window B's config dials window_listen, which
	// main A -- not B -- actually answers. The footer must name A.
	cmdV, ptyV, emuV, _ := nineISXStart(t, bin, rootB, "--window", "2")
	// Since ini-evdn the identity line is hidden by default and Option+w
	// (ESC w on a raw PTY) shows it. Wait for the viewer to render its
	// agent and confirm the line is absent. A viewer currently opens the
	// attention-consent prompt unseen and its first key answers it (the fn77
	// rig sends one for the same reason), so defer it with a lone Esc --
	// which records no answer -- before toggling the line on.
	if _, ok := nineISXAwait(emuV, func(s string) bool { return strings.Contains(s, "eng1") }, 30*time.Second); !ok {
		t.Fatalf("viewer never rendered its agent within 30s; last screen:\n%s", nineISXScreen(emuV))
	}
	if strings.Contains(z5uwFlatScreen(emuV), "main PID") {
		t.Fatalf("viewer shows the identity line before Option+w; it is hidden by default (ini-evdn)\n%s", nineISXScreen(emuV))
	}
	ptyV.Write([]byte{0x1b})
	time.Sleep(time.Second)
	ptyV.Write([]byte{0x1b, 'w'})
	elapsedV, ok := nineISXAwait(emuV, func(string) bool {
		return strings.Contains(z5uwFlatScreen(emuV), fmt.Sprintf("main PID %d", pidA))
	}, 30*time.Second)
	if !ok {
		t.Fatalf("viewer never identified main A (pid %d) as its authority within 30s; last screen:\n%s",
			pidA, nineISXScreen(emuV))
	}
	if strings.Contains(z5uwFlatScreen(emuV), fmt.Sprintf("main PID %d", pidB)) {
		t.Fatalf("viewer footer names main B (the process that never bound) instead of main A")
	}
	t.Logf("viewer identified main A (not B) as authority after %s", elapsedV)

	// Close only the rig-owned A, start its replacement, and confirm the
	// still-running viewer's footer refreshes to the new authority rather
	// than caching A's identity or B's shared pid-file contents.
	cmdA.Process.Kill()
	cmdA.Wait()

	cmdA2, _, _, _ := nineISXStart(t, bin, rootA)
	pidA2 := cmdA2.Process.Pid
	if pidA2 == pidA {
		t.Fatalf("rig bug: replacement main reused pid %d", pidA)
	}
	rigRequireServing(t, addr)

	elapsedR, ok := nineISXAwait(emuV, func(s string) bool {
		return strings.Contains(s, fmt.Sprintf("main PID %d", pidA2))
	}, 30*time.Second)
	if !ok {
		t.Fatalf("viewer did not refresh identity to the replacement main (pid %d) after reconnect within 30s; last screen:\n%s",
			pidA2, nineISXScreen(emuV))
	}
	t.Logf("viewer refreshed to the replacement main (pid %d) after %s", pidA2, elapsedR)
	cmdV.Process.Kill()
}
