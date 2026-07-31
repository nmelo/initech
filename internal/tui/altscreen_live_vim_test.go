//go:build !windows

package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAltScreenLiveProbe_Vim is a MANUAL live-verification harness for
// ini-y97. It spawns a REAL vim (a real alt-screen program, standing in for
// Claude Code's "tui":"fullscreen" mode per the bead's AC, without touching
// the operator's live global ~/.claude/settings.json) side by side with a
// REAL normal (non-alt-screen) shell pane, mirroring the two-pane layout
// from Nelson's original live report (panes 15/16, side by side). It checks
// the actual reported bug end to end: does the alt-screen child's bottom
// row render inside the pane's true visible geometry, or does it land off
// -screen because the emulator was inflated to 3x height underneath it? And
// does fixing that leave the NEIGHBORING normal pane's ini-44hp inflated
// height untouched -- each pane's resize is pane-local state, but this
// proves it live rather than by code inspection alone.
//
// vim always draws a status line at its own last row (laststatus=2 forces
// it). Pre-fix, resizeLocked always inflates the emulator to
// effectiveEmuRows(visibleRows), so vim believes it has that many rows and
// draws its status line down at the inflated row -- far below the visible
// pane, which is exactly the "bottom row never renders" symptom. Post-fix,
// checkAltScreenTransition/resizeLocked detect IsAltScreen() and resize vim
// to the TRUE visible rows, so the status line lands on the real last row.
//
// Gated behind INITECH_ALTPROBE_VIM=1 so it never runs in CI or `make test`.
// Run: INITECH_ALTPROBE_VIM=1 go test ./internal/tui/ -run TestAltScreenLiveProbe_Vim -v -count=1 -timeout 30s
func TestAltScreenLiveProbe_Vim(t *testing.T) {
	if os.Getenv("INITECH_ALTPROBE_VIM") != "1" {
		t.Skip("set INITECH_ALTPROBE_VIM=1 to run the manual live vim alt-screen probe")
	}
	if _, err := os.Stat("/usr/bin/vim"); err != nil {
		if _, err := os.Stat("/opt/homebrew/bin/vim"); err != nil {
			t.Skip("vim not found")
		}
	}

	dir := t.TempDir()
	testFile := filepath.Join(dir, "testfile.txt")
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, "LINE_"+strconv.Itoa(i))
	}
	if err := os.WriteFile(testFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write testfile: %v", err)
	}

	const visibleRows, cols = 14, 80

	// Neighboring pane: a plain, non-alt-screen shell -- the other half of
	// the two-pane layout, standing in for a normal scrolling agent pane
	// next to the alt-screen one.
	neighbor, err := NewPane(PaneConfig{
		Name:      "neighbor",
		Command:   []string{"/bin/sh", "-c", "while true; do date; sleep 1; done"},
		Dir:       dir,
		AgentType: "generic",
	}, visibleRows, cols)
	if err != nil {
		t.Fatalf("NewPane(neighbor): %v", err)
	}
	neighbor.region = Region{X: 0, Y: 0, W: cols, H: visibleRows + 2}
	neighbor.Start()
	defer neighbor.Close()
	neighbor.Resize(visibleRows, cols) // establish the ini-44hp inflated baseline

	wantNeighborInflated := effectiveEmuRows(visibleRows)
	if got := neighbor.Emulator().Height(); got != wantNeighborInflated {
		t.Fatalf("precondition: neighbor emulator height = %d, want %d (inflated baseline)", got, wantNeighborInflated)
	}

	p, err := NewPane(PaneConfig{
		Name:      "vimprobe",
		Command:   []string{"vim", "-u", "NONE", "-N", "-c", "set laststatus=2 nocompatible", testFile},
		Dir:       dir,
		AgentType: "generic",
	}, visibleRows, cols)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	// NewPane never sets p.region -- production wires it via applyLayout(),
	// which checkAltScreenTransition depends on to compute its resize
	// target. Set it here to the same rows/cols NewPane was given, mirroring
	// the invariant applyLayout maintains (region and Resize agree).
	p.region = Region{X: 0, Y: 0, W: cols, H: visibleRows + 2}
	p.Start()
	defer p.Close()

	deadline := time.Now().Add(10 * time.Second)
	entered := false
	for time.Now().Before(deadline) {
		if p.Emulator().IsAltScreen() {
			entered = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !entered {
		t.Fatalf("vim never entered alt-screen mode within timeout; content:\n%s", peekContent(p, 0))
	}

	// Give resizeLocked/checkAltScreenTransition a moment to run and vim a
	// moment to redraw its status line at the (now-correct) last row.
	time.Sleep(300 * time.Millisecond)

	if got := p.Emulator().Height(); got != visibleRows {
		t.Errorf("emulator height after real vim entered alt-screen = %d, want %d (true visible rows, not inflated)", got, visibleRows)
	}

	statusRow := strings.TrimSpace(p.Emulator().RowText(visibleRows-1, cols))
	t.Logf("bottom visible row (%d) content: %q", visibleRows-1, statusRow)
	if statusRow == "" {
		t.Errorf("bottom visible row of pane is blank -- this is the ini-y97 symptom: vim's status line rendered off-screen instead of at the true bottom row")
	}
	if !strings.Contains(statusRow, "testfile.txt") {
		t.Errorf("bottom visible row = %q, want it to contain vim's status line (filename %q)", statusRow, "testfile.txt")
	}

	// Multi-pane independence: the neighboring normal pane must still be at
	// its ini-44hp inflated height -- vim's alt-screen resize is pane-local
	// and must not have disturbed it.
	if got := neighbor.Emulator().Height(); got != wantNeighborInflated {
		t.Errorf("neighbor pane emulator height after vim's alt-screen resize = %d, want %d (unaffected, still inflated)", got, wantNeighborInflated)
	}

	// Round-trip: quit vim, confirm the pane reverts to the inflated
	// scrollable-live-region height once alt-screen mode exits.
	p.ptmx.Write([]byte(":q!\r"))
	exitDeadline := time.Now().Add(10 * time.Second)
	exited := false
	for time.Now().Before(exitDeadline) {
		if !p.Emulator().IsAltScreen() {
			exited = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !exited {
		t.Fatalf("vim never exited alt-screen mode within timeout")
	}
	time.Sleep(300 * time.Millisecond)
	if got, want := p.Emulator().Height(), effectiveEmuRows(visibleRows); got != want {
		t.Errorf("emulator height after vim exited alt-screen = %d, want %d (restored inflated height)", got, want)
	}
}
