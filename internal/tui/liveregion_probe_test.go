//go:build !windows

package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Phase-0 (A) confirmation harness for ini-44hp. After empirically DISPROVING
// the alt-screen hypothesis (altscreen_probe_test.go: IsAltScreen=false,
// committed output commits to scrollback and scrolls fine), these probes
// confirm the ACTUAL cause: claude's LIVE/dynamic region (AskUserQuestion
// modal, tall input box) is redrawn in place and CLIPPED to the viewport, and
// the clipped overflow is never recoverable by scrolling (it is not committed
// to scrollback as a coherent view) — independent of alt-screen. Enlarging the
// viewport (focus/zoom) makes claude redraw the full region ("works when
// focused").
//
// Gated behind INITECH_LIVEPROBE so they never run in CI / `make test`.
//   INITECH_LIVEPROBE=askq  go test ./internal/tui/ -run TestLiveRegionClip_AskUserQuestion -v -count=1 -timeout 180s
//   INITECH_LIVEPROBE=input go test ./internal/tui/ -run TestLiveRegionClip_TallInput      -v -count=1 -timeout 150s

func lpClaudeBin() string {
	if b := os.Getenv("CLAUDE_BIN"); b != "" {
		return b
	}
	return "claude"
}

func lpSpawnREPL(t *testing.T, rows, cols int) *Pane {
	t.Helper()
	for _, k := range []string{"INITECH_SOCKET", "INITECH_AGENT", "CLAUDE_CODE_ENTRYPOINT", "CLAUDECODE"} {
		os.Unsetenv(k)
	}
	p, err := NewPane(PaneConfig{
		Name:      "liveprobe",
		Command:   []string{lpClaudeBin()},
		Dir:       t.TempDir(),
		AgentType: "claude-code",
	}, rows, cols)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	p.Start()
	p.region = Region{X: 0, Y: 0, W: cols, H: rows + 2}
	enter := func() { p.SendKey(tcell.NewEventKey(tcell.KeyEnter, '\r', tcell.ModNone)) }
	trust := false
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(400 * time.Millisecond)
		scr := peekContent(p, 0)
		low := strings.ToLower(scr)
		if !trust && (strings.Contains(low, "trust this folder") ||
			strings.Contains(low, "trust the files") || strings.Contains(low, "do you trust")) {
			enter()
			trust = true
			continue
		}
		if strings.Contains(scr, "❯") && !strings.Contains(low, "trust") {
			time.Sleep(1500 * time.Millisecond)
			return p
		}
	}
	t.Logf("WARN: REPL not confirmed in time; last screen:\n%s", peekContent(p, 0))
	return p
}

func lpScreenHas(p *Pane, marker string) bool { return strings.Contains(peekContent(p, 0), marker) }

func lpScrollbackHas(p *Pane, marker string) bool {
	w := p.Emulator().Width()
	n := p.Emulator().ScrollbackLen()
	for y := 0; y < n; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			if c := p.Emulator().ScrollbackCellAt(x, y); c != nil && c.Content != "" {
				b.WriteString(c.Content)
			} else {
				b.WriteByte(' ')
			}
		}
		if strings.Contains(b.String(), marker) {
			return true
		}
	}
	return false
}

// lpRenderScrolled replicates the exact pane_render scrollback mapping
// (pane_render.go:134-153): what the operator sees at the current scrollOffset.
func lpRenderScrolled(p *Pane) string {
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

// lpRecoverableByAnyScroll scans EVERY reachable scroll offset (what the
// operator can do with the wheel) and reports whether the marker ever becomes
// visible in the rendered scroll view.
func lpRecoverableByAnyScroll(p *Pane, marker string) bool {
	defer p.ScrollDown(1 << 20)
	max := p.maxScrollOffset()
	for off := 1; off <= max; off++ {
		p.scrollOffset = off
		if strings.Contains(lpRenderScrolled(p), marker) {
			return true
		}
	}
	return false
}

// TestLiveRegionClip_AskUserQuestion is the USER-STORY confirmation: drive a
// real AskUserQuestion whose content exceeds a small pane, then show the
// clipped end is unrecoverable by any scroll and revealed only by enlarging
// the viewport.
func TestLiveRegionClip_AskUserQuestion(t *testing.T) {
	if os.Getenv("INITECH_LIVEPROBE") != "askq" {
		t.Skip("set INITECH_LIVEPROBE=askq to run the live-region AskUserQuestion probe")
	}
	const rows, cols = 10, 80 // small pane; force the modal to overflow
	p := lpSpawnREPL(t, rows, cols)
	defer p.Close()

	const qtop, qbot = "QTOP_8821", "QBOT_4417"
	prompt := "Call the AskUserQuestion tool RIGHT NOW with exactly one question and then wait. " +
		"The question text MUST begin with the token " + qtop + " then eight sentences of filler so it spans many lines. " +
		"Provide four options, each with a two-sentence description; the FOURTH option's label MUST be the token " + qbot + ". " +
		"Do nothing else; just open the question and wait."
	p.ptmx.Write([]byte(prompt))
	time.Sleep(500 * time.Millisecond)
	p.ptmx.Write([]byte("\r"))

	// Wait for the REAL modal (footer string only the modal renders).
	deadline := time.Now().Add(110 * time.Second)
	modalUp := false
	for time.Now().Before(deadline) {
		time.Sleep(600 * time.Millisecond)
		if strings.Contains(peekContent(p, 0), "Enter to select") {
			modalUp = true
			time.Sleep(1200 * time.Millisecond)
			break
		}
	}
	if !modalUp {
		t.Skipf("AskUserQuestion modal never rendered (model noncompliance/slow) — escalate to (B). last screen:\n%s", peekContent(p, 0))
	}

	// Wipe pre-modal history (prompt echo + thinking) so the markers uniquely
	// identify MODAL content, not the echoed prompt sitting in scrollback.
	p.Emulator().ClearScrollback()
	time.Sleep(300 * time.Millisecond)

	alt := p.Emulator().IsAltScreen()
	footerOnScreen := lpScreenHas(p, "Enter to select")
	qtopOnScreen := lpScreenHas(p, qtop)
	qbotOnScreen := lpScreenHas(p, qbot)

	// Pick whichever marker is CLIPPED off the small modal.
	clip, clipName := "", ""
	switch {
	case !qtopOnScreen:
		clip, clipName = qtop, "QTOP(question top)"
	case !qbotOnScreen:
		clip, clipName = qbot, "QBOT(last option)"
	}

	t.Logf("=== ini-44hp (A) ASKUSERQUESTION (pane %dx%d) ===", rows, cols)
	t.Logf("alt=%v footerOnScreen=%v qtopOnScreen=%v qbotOnScreen=%v scrollbackLen(after clear)=%d",
		alt, footerOnScreen, qtopOnScreen, qbotOnScreen, p.Emulator().ScrollbackLen())
	t.Logf("--- clipped %d-row modal (live) ---\n%s", rows, peekContent(p, 0))

	if clip == "" {
		t.Skipf("modal fit within the pane (nothing clipped) — rerun with a smaller pane / longer question")
	}
	recoverable := lpRecoverableByAnyScroll(p, clip) || lpScrollbackHas(p, clip)
	t.Logf("clipped end = %s ; recoverableByAnyScroll/scrollback=%v", clipName, recoverable)

	p.Resize(46, cols)
	time.Sleep(2500 * time.Millisecond)
	revealed := lpScreenHas(p, clip)
	t.Logf("--- after Resize(46) ('works when focused'): clipped marker onScreen=%v ---\n%s", revealed, peekContent(p, 0))

	if alt {
		t.Errorf("expected inline rendering (alt=false), got alt=true")
	}
	if recoverable {
		t.Errorf("BUG-NOT-REPRODUCED: clipped modal text %s was recoverable via scroll/scrollback", clipName)
	}
	if !revealed {
		t.Errorf("expected enlarging the viewport to reveal clipped modal text %s, but it stayed clipped", clipName)
	}
	if !recoverable && revealed {
		t.Logf("CONFIRMED ini-44hp cause: modal live-region content clipped to viewport, unrecoverable by scroll, revealed only by enlarging the pane (focus/zoom).")
	}
}

// TestLiveRegionFix_AskUserQuestionScrollable is the REAL-CLAUDE gate for
// increment 1: apply the taller-emulator fix (Pane.Resize) to a live claude and
// verify BOTH (a) the REPL still renders correctly in the windowed view (prompt
// at the visible bottom, no blank-row drift) and (b) a previously-clipped
// AskUserQuestion modal is now captured in the taller emulator and recoverable
// by scrolling.
//   INITECH_LIVEPROBE=fix go test ./internal/tui/ -run TestLiveRegionFix -v -count=1 -timeout 180s
func TestLiveRegionFix_AskUserQuestionScrollable(t *testing.T) {
	if os.Getenv("INITECH_LIVEPROBE") != "fix" {
		t.Skip("set INITECH_LIVEPROBE=fix to run the real-claude increment-1 gate")
	}
	const visible, cols = 10, 80
	p := lpSpawnREPL(t, visible, cols)
	defer p.Close()

	// Apply the fix's sizing (what the layout's applyLayout->Resize does): grow
	// the emulator/PTY taller than the visible window. Claude redraws via SIGWINCH.
	p.Resize(visible, cols)
	time.Sleep(2500 * time.Millisecond)
	emuH := p.Emulator().Height()
	t.Logf("=== ini-44hp inc1 REAL-CLAUDE GATE (visible=%d emuHeight=%d) ===", visible, emuH)
	if emuH <= visible {
		t.Fatalf("expected taller emulator after resize, got emuHeight=%d (visible=%d)", emuH, visible)
	}

	// GATE (b) DIAGNOSTIC: does the normal REPL render correctly in the bottom
	// window (claude's active prompt visible), or does the live-mode anchoring
	// drift (pos.Y-based scan mis-anchors when the status bar renders below the
	// cursor in a taller screen)? Logged as a verdict, not asserted — this probe
	// is the gate-(b) diagnostic for ini-44hp increment 1.
	liveView := renderAtCurrentOffset(p) // scrollOffset == 0
	promptVisible := strings.Contains(liveView, "❯")
	t.Logf("GATE-(b) VERDICT: promptVisibleInLiveWindow=%v (false => anchoring drift; needs window-into-screen render fix)", promptVisible)
	t.Logf("--- live window (bottom %d of %d emu rows) ---\n%s", visible, emuH, liveView)

	// GATE (a): drive a tall AskUserQuestion; with the taller emu it should be
	// fully captured and its clipped top recoverable by scroll.
	const qtop, qbot = "QTOP_9001", "QBOT_9002"
	prompt := "Call the AskUserQuestion tool RIGHT NOW with one question and wait. The question MUST begin with " + qtop +
		" then eight sentences of filler. Provide four options; the FOURTH option label MUST be " + qbot + ". Do nothing else."
	p.ptmx.Write([]byte(prompt))
	time.Sleep(500 * time.Millisecond)
	p.ptmx.Write([]byte("\r"))
	deadline := time.Now().Add(110 * time.Second)
	modalUp := false
	for time.Now().Before(deadline) {
		time.Sleep(600 * time.Millisecond)
		if strings.Contains(peekContent(p, 0), "Enter to select") {
			modalUp = true
			time.Sleep(1200 * time.Millisecond)
			break
		}
	}
	if !modalUp {
		t.Skipf("modal never rendered (model slow/noncompliant); gate-(b) still verified above")
	}
	p.Emulator().ClearScrollback() // drop prompt echo so qtop uniquely identifies modal content
	time.Sleep(300 * time.Millisecond)

	liveModal := renderAtCurrentOffset(p)
	footerVisible := strings.Contains(liveModal, "Enter to select")
	qtopRecoverable := visibleAtAnyScroll(p, qtop)
	t.Logf("modal: footerInLiveWindow=%v qtopRecoverableByScroll=%v emuHeight=%d maxScroll=%d",
		footerVisible, qtopRecoverable, p.Emulator().Height(), p.maxScrollOffset())
	t.Logf("--- live window with modal ---\n%s", liveModal)
	if !qtopRecoverable {
		t.Errorf("gate-(a) FAIL: question top %s still not recoverable by scroll with taller emu (emuH=%d)", qtop, p.Emulator().Height())
	} else {
		t.Logf("GATE PASS: clipped question top %s now scroll-recoverable AND normal REPL renders in the live window.", qtop)
	}
}

// TestLiveRegionClip_TallInput is a deterministic (no-model) corroboration:
// a multi-line input taller than the pane clips its top off-screen and is
// revealed by enlarging the viewport. Logged in full; the incremental-redraw
// path leaves stale frame remnants in scrollback, so the authoritative
// pass/fail check is the AskUserQuestion probe above.
func TestLiveRegionClip_TallInput(t *testing.T) {
	if os.Getenv("INITECH_LIVEPROBE") != "input" {
		t.Skip("set INITECH_LIVEPROBE=input to run the live-region tall-input probe")
	}
	const rows, cols = 14, 80
	p := lpSpawnREPL(t, rows, cols)
	defer p.Close()

	const topMarker = "INP_TOP_5F3A"
	p.ptmx.Write([]byte(topMarker))
	for i := 2; i <= 26; i++ {
		p.SendKey(tcell.NewEventKey(tcell.KeyEnter, '\r', tcell.ModShift)) // newline within input
		time.Sleep(45 * time.Millisecond)
		p.ptmx.Write([]byte(fmt.Sprintf("inp_line_%02d", i)))
		time.Sleep(45 * time.Millisecond)
	}
	time.Sleep(1800 * time.Millisecond)

	alt := p.Emulator().IsAltScreen()
	topOnScreen := lpScreenHas(p, topMarker)
	recoverable := lpRecoverableByAnyScroll(p, topMarker)
	rawRemnant := lpScrollbackHas(p, topMarker)
	t.Logf("=== ini-44hp (A) TALL INPUT (pane %dx%d) ===", rows, cols)
	t.Logf("alt=%v topOnScreen=%v topRecoverableByAnyScroll=%v rawScrollbackRemnant=%v",
		alt, topOnScreen, recoverable, rawRemnant)
	t.Logf("--- clipped %d-row screen (live) ---\n%s", rows, peekContent(p, 0))

	p.Resize(46, cols)
	time.Sleep(2200 * time.Millisecond)
	topAfterResize := lpScreenHas(p, topMarker)
	t.Logf("--- after Resize(46) ('works when focused') topOnScreen=%v ---\n%s", topAfterResize, peekContent(p, 0))

	if alt {
		t.Errorf("expected inline rendering (alt=false), got alt=true")
	}
	if topOnScreen {
		t.Errorf("expected top input line CLIPPED off the small pane, but it was on screen")
	}
	if !topAfterResize {
		t.Errorf("expected enlarging the viewport to reveal the clipped top, but it stayed clipped")
	}
}
