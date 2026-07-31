//go:build !windows

package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// TestClickBadgeLiveProbe_ClaudeCode is a MANUAL live-verification harness
// for ini-82k. It launches a REAL claude in an isolated temp dir (NewPane's
// cmd.Dir mechanism -- no shell "cd", so cwd-based discovery can never walk
// up into the live fleet from here) and clicks whatever UI affordance it
// draws after a scroll-up. It does NOT write to ~/.claude/settings.json --
// "tui":"fullscreen" is already set there (confirmed by super/operator), so
// a freshly launched claude inherits real alt-screen mode with no config
// change at all. This is the fidelity case: the operator's REAL
// configuration, unmodified, not a target configured into cooperating.
//
// Per the operator's caution: this only launches, scrolls, and clicks --
// it never types a prompt or lets claude do real work.
//
// Gated behind INITECH_ALTPROBE=1, the same env var as the existing
// altscreen_probe_test.go manual harness.
// Run: INITECH_ALTPROBE=1 go test ./internal/tui/ -run TestClickBadgeLiveProbe_ClaudeCode -v -count=1 -timeout 90s
func TestClickBadgeLiveProbe_ClaudeCode(t *testing.T) {
	if v := os.Getenv("INITECH_ALTPROBE"); v != "1" {
		t.Skip("set INITECH_ALTPROBE=1 to run the manual live claude click-badge probe")
	}
	for _, k := range []string{"INITECH_SOCKET", "INITECH_AGENT", "CLAUDE_CODE_ENTRYPOINT", "CLAUDECODE"} {
		os.Unsetenv(k)
	}

	dir := t.TempDir()
	claudeBin := os.Getenv("CLAUDE_BIN")
	if claudeBin == "" {
		claudeBin = "claude"
	}

	const visibleRows, cols = 14, 80
	p, err := NewPane(PaneConfig{
		Name:      "clickprobe",
		Command:   []string{claudeBin},
		Dir:       dir,
		AgentType: "claude-code",
	}, visibleRows, cols)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	p.region = Region{X: 0, Y: 0, W: cols, H: visibleRows + 2}
	p.Start()
	defer p.Close()

	enter := func() { p.SendKey(tcell.NewEventKey(tcell.KeyEnter, '\r', tcell.ModNone)) }

	trustAccepted := false
	deadline := time.Now().Add(45 * time.Second)
	var lastScreen string
	for time.Now().Before(deadline) {
		time.Sleep(400 * time.Millisecond)
		lastScreen = peekContent(p, 0)
		low := strings.ToLower(lastScreen)
		if !trustAccepted && (strings.Contains(low, "trust this folder") ||
			strings.Contains(low, "trust the files") || strings.Contains(low, "do you trust")) {
			t.Logf("[trust prompt detected; accepting]")
			enter()
			trustAccepted = true
			continue
		}
		if strings.Contains(lastScreen, "❯") {
			break // idle REPL ready
		}
	}
	t.Logf("alt-screen after startup: %v", p.Emulator().IsAltScreen())
	if !p.Emulator().IsAltScreen() {
		t.Fatalf("precondition failed: claude did not enter alt-screen mode (re-confirming the deduction directly, per ini-82k's note); content:\n%s", lastScreen)
	}

	tui := newTestTUI(p)
	tui.applyLayout()
	if len(tui.plan.Panes) == 0 {
		t.Fatal("computed layout has no panes")
	}
	r := tui.plan.Panes[0].Region

	// Scroll up (input, not "doing work") to try to surface claude's own
	// scroll-position UI affordance. Not typing anything.
	for i := 0; i < 5; i++ {
		ev := tcell.NewEventMouse(r.X+1, r.Y+3, tcell.WheelUp, tcell.ModNone)
		tui.handleMouse(ev)
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)

	afterScroll := peekContent(p, 0)
	t.Logf("--- content after wheel-up ---\n%s", afterScroll)

	badgeRow, badgeCol := -1, -1
	for row := 0; row < visibleRows; row++ {
		line := p.Emulator().RowText(row, cols)
		if byteIdx := strings.Index(strings.ToLower(line), "jump to bottom"); byteIdx >= 0 {
			badgeRow = row
			// Rune count, not byte offset: the row has multi-byte
			// box-drawing/block glyphs before the badge text, so a byte
			// index would land several columns short of the real column.
			badgeCol = len([]rune(line[:byteIdx]))
			t.Logf("badge found at emulator row %d, col %d: %q", row, badgeCol, line)
			break
		}
	}
	if badgeRow == -1 {
		t.Skipf("'Jump to bottom' badge did not appear after wheel-up -- cannot verify the click fix against claude's real affordance in this run; content:\n%s", afterScroll)
	}

	// emuRow -> screen row: my = r.Y + 1 + emuRow (matches the render path's
	// cr.Y+row mapping and the click path's ly-- adjustment, both verified
	// during investigation). +2 columns into the badge text (not its very
	// first column) to click solidly inside it, not at its edge.
	clickY := r.Y + 1 + badgeRow
	clickX := r.X + badgeCol + 2
	pressEv := tcell.NewEventMouse(clickX, clickY, tcell.Button1, tcell.ModNone)
	tui.handleMouse(pressEv)
	time.Sleep(50 * time.Millisecond)
	releaseEv := tcell.NewEventMouse(clickX, clickY, tcell.ButtonNone, tcell.ModNone)
	tui.handleMouse(releaseEv)
	time.Sleep(500 * time.Millisecond)

	afterClick := peekContent(p, 0)
	t.Logf("--- content after click ---\n%s", afterClick)

	stillThere := false
	for row := 0; row < visibleRows; row++ {
		if strings.Contains(strings.ToLower(p.Emulator().RowText(row, cols)), "jump to bottom") {
			stillThere = true
			break
		}
	}
	if stillThere {
		t.Errorf("'Jump to bottom' badge still present after clicking it -- click did not register")
	} else {
		t.Logf("badge no longer present after click -- click registered")
	}
}

// TestClickBadgeLiveProbe_VimControl is the control for
// TestClickBadgeLiveProbe_ClaudeCode: a REAL vim with mouse=a (explicitly
// configured, unlike claude's already-on fullscreen mode -- this is a
// DIFFERENT kind of target on purpose, per the operator's "use both" note)
// gets a real click on a specific line and must move its cursor there. If
// this fails too, the click-delivery bug is general, not claude-specific.
//
// Gated behind INITECH_ALTPROBE=1, same as the claude probe above.
// Run: INITECH_ALTPROBE=1 go test ./internal/tui/ -run TestClickBadgeLiveProbe_VimControl -v -count=1 -timeout 30s
func TestClickBadgeLiveProbe_VimControl(t *testing.T) {
	if os.Getenv("INITECH_ALTPROBE") != "1" {
		t.Skip("set INITECH_ALTPROBE=1 to run the manual live vim click control")
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
	p, err := NewPane(PaneConfig{
		Name:      "vimclickcontrol",
		Command:   []string{"vim", "-u", "NONE", "-N", "-c", "set mouse=a ruler laststatus=2 nocompatible", testFile},
		Dir:       dir,
		AgentType: "generic",
	}, visibleRows, cols)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
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
		t.Fatalf("vim never entered alt-screen mode; content:\n%s", peekContent(p, 0))
	}
	time.Sleep(300 * time.Millisecond)

	tui := newTestTUI(p)
	tui.applyLayout()
	r := tui.plan.Panes[0].Region

	// Click on content row 5 (0-indexed): vim shows LINE_1..LINE_N from the
	// top, so content row 5 displays "LINE_6". A working click should move
	// vim's cursor there, visible in the ruler as "6,1" (line,col).
	const targetContentRow = 5
	clickY := r.Y + 1 + targetContentRow
	pressEv := tcell.NewEventMouse(r.X+1, clickY, tcell.Button1, tcell.ModNone)
	tui.handleMouse(pressEv)
	time.Sleep(50 * time.Millisecond)
	releaseEv := tcell.NewEventMouse(r.X+1, clickY, tcell.ButtonNone, tcell.ModNone)
	tui.handleMouse(releaseEv)
	time.Sleep(300 * time.Millisecond)

	// Row visibleRows-2, not visibleRows-1: with laststatus=2 and no
	// separate command-line activity, vim's ruler renders on the status
	// line itself (row 12 of 14, confirmed by a direct row-by-row dump).
	// Row 13 holds a stale startup-message artifact unrelated to the ruler.
	statusRow := strings.TrimSpace(p.Emulator().RowText(visibleRows-2, cols))
	t.Logf("status/ruler row after click on content row %d (should show LINE_%d): %q", targetContentRow, targetContentRow+1, statusRow)
	wantLine := strconv.Itoa(targetContentRow + 1)
	if !strings.Contains(statusRow, wantLine+",") {
		t.Errorf("ruler = %q, want it to show line %s (vim's cursor should have moved to the clicked line)", statusRow, wantLine)
	}
}
