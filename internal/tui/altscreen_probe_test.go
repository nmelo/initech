//go:build !windows

package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// TestAltScreenProbe_ClaudeCode is a MANUAL Phase-0 confirmation harness for
// ini-44hp. It spawns a REAL `claude` in a small pane (mimicking a multi-pane
// viewport), drives past the per-directory trust prompt, and reports whether
// the installed Claude Code enters the ALTERNATE SCREEN (emu.IsAltScreen())
// at its idle REPL — using the exact x/vt SafeEmulator + readLoop/responseLoop
// wiring production uses, so claude's terminal capability queries are answered
// faithfully.
//
// Gated behind INITECH_ALTPROBE=1 so it never runs in CI or `make test`.
// Run: INITECH_ALTPROBE=1 go test ./internal/tui/ -run TestAltScreenProbe -v -count=1 -timeout 90s
func TestAltScreenProbe_ClaudeCode(t *testing.T) {
	if v := os.Getenv("INITECH_ALTPROBE"); v != "1" && v != "2" {
		t.Skip("set INITECH_ALTPROBE=1 (idle) or 2 (drive tall output) to run the manual claude alt-screen probe")
	}
	// Don't let claude think it's a nested initech session.
	for _, k := range []string{"INITECH_SOCKET", "INITECH_AGENT", "CLAUDE_CODE_ENTRYPOINT", "CLAUDECODE"} {
		os.Unsetenv(k)
	}

	dir := t.TempDir()
	claudeBin := os.Getenv("CLAUDE_BIN")
	if claudeBin == "" {
		claudeBin = "claude"
	}
	t.Logf("probing claude binary: %s", claudeBin)
	const rows, cols = 14, 80 // small, like a multi-pane viewport
	p, err := NewPane(PaneConfig{
		Name:      "altprobe",
		Command:   []string{claudeBin},
		Dir:       dir,
		AgentType: "claude-code",
	}, rows, cols)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	p.Start()
	defer p.Close()

	enter := func() { p.SendKey(tcell.NewEventKey(tcell.KeyEnter, '\r', tcell.ModNone)) }

	trustAccepted := false
	everAlt := false
	driven := false
	maxScrollback := 0
	var lastScreen string
	// drive a deterministic TALL static output once the REPL is up, to test
	// whether output taller than the pane commits to scrollback (recoverable)
	// or is clipped/lost. Only when INITECH_ALTPROBE=2.
	drive := os.Getenv("INITECH_ALTPROBE") == "2"
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(400 * time.Millisecond)
		lastScreen = peekContent(p, 0)
		alt := p.Emulator().IsAltScreen()
		if alt {
			everAlt = true
		}
		if sl := p.Emulator().ScrollbackLen(); sl > maxScrollback {
			maxScrollback = sl
		}
		low := strings.ToLower(lastScreen)
		if !trustAccepted && (strings.Contains(low, "trust this folder") ||
			strings.Contains(low, "trust the files") || strings.Contains(low, "do you trust")) {
			t.Logf("[trust prompt detected; sending Enter to accept] alt=%v", alt)
			enter()
			trustAccepted = true
			continue
		}
		// Once the REPL prompt is visible, drive one tall output.
		if drive && !driven && strings.Contains(lastScreen, "❯") && trustAccepted {
			t.Logf("[driving tall output: integers 1..40] alt=%v scrollback=%d", alt, p.Emulator().ScrollbackLen())
			p.ptmx.Write([]byte("Print the integers 1 to 40, one per line, and nothing else. No preamble."))
			time.Sleep(500 * time.Millisecond)
			p.ptmx.Write([]byte("\r"))
			driven = true
			continue
		}
		t.Logf("tick alt=%v scrollbackLen=%d cursorY=%d", alt, p.Emulator().ScrollbackLen(), p.Emulator().CursorPosition().Y)
	}

	t.Logf("=== RESULT ini-44hp Phase0 ===")
	t.Logf("IsAltScreen(final)=%v everAlt=%v maxScrollbackLen=%d trustAccepted=%v driven=%v",
		p.Emulator().IsAltScreen(), everAlt, maxScrollback, trustAccepted, driven)
	t.Logf("--- final LIVE rendered screen (%dx%d) ---\n%s", rows, cols, lastScreen)

	// Scroll-reveal check: does initech wheel-scroll actually expose the
	// off-screen committed output now sitting in scrollback? Replicates the
	// exact pane_render scrollback mapping (pane_render.go:134-153).
	if drive {
		p.region = Region{X: 0, Y: 0, W: cols, H: rows + 2} // TerminalSize() => (cols, rows)
		renderScrolled := func() string {
			tc, tr := p.region.TerminalSize()
			startRow, _ := p.contentOffset()
			sbLen := p.Emulator().ScrollbackLen()
			emuRows := p.Emulator().Height()
			total := sbLen + emuRows
			var b strings.Builder
			for row := 0; row < tr; row++ {
				vRow := startRow + row
				if vRow >= total {
					break
				}
				for col := 0; col < tc; col++ {
					var content string
					if vRow < sbLen {
						if c := p.Emulator().ScrollbackCellAt(col, vRow); c != nil {
							content = c.Content
						}
					} else if c := p.Emulator().CellAt(col, vRow-sbLen); c != nil {
						content = c.Content
					}
					if content == "" {
						content = " "
					}
					b.WriteString(content)
				}
				b.WriteString("\n")
			}
			return b.String()
		}
		t.Logf("maxScrollOffset=%d", p.maxScrollOffset())
		p.ScrollUp(40)
		startRow, _ := p.contentOffset()
		view := renderScrolled()
		revealed := strings.Contains(view, "1") && strings.Contains(view, "2") && strings.Contains(view, "3")
		t.Logf("after ScrollUp(40): scrollOffset=%d contentOffset.startRow=%d earlyLinesRevealed=%v",
			p.scrollOffset, startRow, revealed)
		t.Logf("--- SCROLLED-UP view (early integers should appear if scroll works) ---\n%s", view)
	}
}
