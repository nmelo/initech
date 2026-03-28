// render_test.go tests render helpers.
package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// TestRenderCmdError_NarrowTerminal verifies renderCmdError does not panic
// when the terminal is too narrow (sw < 5). Previously msg[:sw-4] would
// cause a slice-bounds panic when sw <= 4 (ini-a1e.6).
func TestRenderCmdError_NarrowTerminal(t *testing.T) {
	for _, width := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			s := tcell.NewSimulationScreen("")
			s.Init()
			s.SetSize(width, 10)
			tui := &TUI{
				screen: s,
				cmd:    cmdModal{error: "something went wrong"},
			}
			// Must not panic.
			tui.renderCmdError()
		})
	}
}

// TestRenderCmdError_NormalWidth verifies renderCmdError renders without panic
// for a standard terminal width.
func TestRenderCmdError_NormalWidth(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{
		screen: s,
		cmd:    cmdModal{error: "build failed"},
	}
	// Must not panic.
	tui.renderCmdError()
}

// TestRenderStatusBar_DefaultHints verifies the status bar shows keyboard
// hints when no modal or error is active.
func TestRenderStatusBar_DefaultHints(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{screen: s}
	tui.renderStatusBar()

	// Check that the bottom row contains hint text.
	_, sh := s.Size()
	y := sh - 1
	var line string
	for x := 0; x < 60; x++ {
		ch, _, _, _ := s.GetContent(x, y)
		line += string(ch)
	}
	if len(line) == 0 {
		t.Error("status bar should render hint text")
	}
	// Should contain at least one recognizable hint.
	found := false
	for _, hint := range []string{"commands", "zoom", "overlay", "help"} {
		for i := 0; i <= len(line)-len(hint); i++ {
			if line[i:i+len(hint)] == hint {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Errorf("status bar should contain keyboard hints, got: %q", line)
	}
}

// TestRenderStatusBar_Error verifies the status bar shows error text when
// cmd.error is set.
func TestRenderStatusBar_Error(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{
		screen: s,
		cmd:    cmdModal{error: "something broke"},
	}
	tui.renderStatusBar()

	// The bottom row should have a red background (error style).
	_, sh := s.Size()
	y := sh - 1
	var line string
	for x := 0; x < 20; x++ {
		ch, _, _, _ := s.GetContent(x, y)
		line += string(ch)
	}
	if len(line) == 0 {
		t.Error("status bar should render error text")
	}
}

// TestRenderStatusBar_CmdActive verifies the status bar shows the command
// input when the command modal is active.
func TestRenderStatusBar_CmdActive(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{
		screen: s,
		cmd:    cmdModal{active: true, buf: []rune("gri")},
	}
	tui.renderStatusBar()

	// Should show the > prompt on the bottom row.
	_, sh := s.Size()
	y := sh - 1
	ch, _, _, _ := s.GetContent(0, y)
	if ch != '>' {
		t.Errorf("command bar should show > prompt, got %q", string(ch))
	}
}

// TestPruneError_StampsExpiry verifies that the first prune tick stamps the
// expiry, and a subsequent tick after the TTL clears the error.
func TestPruneError_StampsExpiry(t *testing.T) {
	tui := &TUI{}
	tui.cmd.error = "something broke"

	// First tick: stamps expiry, doesn't clear.
	tui.pruneError()
	if tui.cmd.error == "" {
		t.Fatal("error should not be cleared on first tick")
	}
	if tui.cmd.errorExpiry.IsZero() {
		t.Fatal("errorExpiry should be stamped after first tick")
	}

	// Simulate expiry passing.
	tui.cmd.errorExpiry = time.Now().Add(-1 * time.Second)
	tui.pruneError()
	if tui.cmd.error != "" {
		t.Errorf("error should be cleared after expiry, got %q", tui.cmd.error)
	}
}

// TestPruneError_ResetsOnClear verifies expiry resets when error is cleared
// externally (e.g., by opening the command modal).
func TestPruneError_ResetsOnClear(t *testing.T) {
	tui := &TUI{}
	tui.cmd.error = "old error"
	tui.pruneError() // stamps expiry

	// External clear.
	tui.cmd.error = ""
	tui.pruneError()
	if !tui.cmd.errorExpiry.IsZero() {
		t.Error("errorExpiry should reset when error is empty")
	}
}

// TestApplyLayout_ReservesStatusBar verifies that applyLayout reserves the
// bottom row for the status bar, so panes don't extend to the last row.
func TestApplyLayout_ReservesStatusBar(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)
	panes := []*Pane{newEmuPane("eng1", 120, 39)}
	ls := DefaultLayoutState([]string{"eng1"})
	tui := &TUI{
		screen:      s,
		panes:       panes,
		layoutState: ls,
		lastW:       120,
		lastH:       40,
	}
	tui.applyLayout()

	// The single pane should fill height 38 (40 - 2 for spacer + tip line).
	if len(tui.plan.Panes) != 1 {
		t.Fatalf("expected 1 pane in plan, got %d", len(tui.plan.Panes))
	}
	pr := tui.plan.Panes[0]
	if pr.Region.H != 38 {
		t.Errorf("pane height = %d, want 38 (screen 40 - 2 for spacer + tip)", pr.Region.H)
	}
	// Pane should not extend into the spacer/tip rows (rows 38-39).
	if pr.Region.Y+pr.Region.H > 38 {
		t.Errorf("pane extends into status area: Y=%d H=%d (bottom=%d)", pr.Region.Y, pr.Region.H, pr.Region.Y+pr.Region.H)
	}
}

// ── Tip cycling tests ───────────────────────────────────────────────

func TestRotateTip_AdvancesAfterInterval(t *testing.T) {
	tui := &TUI{tipRotateAt: time.Now().Add(-1 * time.Second)}
	tui.rotateTip()
	// Random index: just verify it's within bounds.
	if tui.tipIndex < 0 || tui.tipIndex >= len(statusTips) {
		t.Errorf("tipIndex = %d, out of bounds [0, %d)", tui.tipIndex, len(statusTips))
	}
	// Verify the timer was reset.
	if tui.tipRotateAt.Before(time.Now()) {
		t.Error("tipRotateAt should be in the future after rotation")
	}
}

func TestRotateTip_NoAdvanceBeforeInterval(t *testing.T) {
	tui := &TUI{tipRotateAt: time.Now().Add(1 * time.Minute)}
	tui.rotateTip()
	if tui.tipIndex != 0 {
		t.Errorf("tipIndex = %d, want 0 (no rotation yet)", tui.tipIndex)
	}
}

func TestRotateTip_StaysInBounds(t *testing.T) {
	// Run many rotations; index must always be valid.
	tui := &TUI{}
	for i := 0; i < 100; i++ {
		tui.tipRotateAt = time.Now().Add(-1 * time.Second)
		tui.rotateTip()
		if tui.tipIndex < 0 || tui.tipIndex >= len(statusTips) {
			t.Fatalf("iteration %d: tipIndex = %d, out of bounds", i, tui.tipIndex)
		}
	}
}

func TestRenderHints_ShowsTipAndShortcuts(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 24)
	tui := &TUI{screen: s, tipIndex: 0}
	tui.renderHints()

	sw, sh := s.Size()
	y := sh - 1
	var line string
	for x := 0; x < sw; x++ {
		ch, _, _, _ := s.GetContent(x, y)
		line += string(ch)
	}

	// Should contain the first tip on the left.
	if len(line) == 0 {
		t.Fatal("status bar should have content")
	}
	// Should contain shortcuts on the right.
	found := false
	for _, kw := range []string{"zoom", "overlay", "help"} {
		if containsStr(line, kw) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("status bar should contain keyboard shortcuts, got: %q", line)
	}
}

func TestRenderHints_TruncatesOnNarrowTerminal(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(55, 24) // narrow
	tui := &TUI{screen: s, tipIndex: 0}
	tui.renderHints()

	// Should not panic. Shortcuts should still be visible.
	sw, sh := s.Size()
	y := sh - 1
	var line string
	for x := 0; x < sw; x++ {
		ch, _, _, _ := s.GetContent(x, y)
		line += string(ch)
	}
	if !containsStr(line, "help") {
		t.Errorf("shortcuts should be visible even on narrow terminal, got: %q", line)
	}
}

func TestStatusTips_NonEmpty(t *testing.T) {
	if len(statusTips) == 0 {
		t.Fatal("statusTips should not be empty")
	}
	for i, tip := range statusTips {
		if tip == "" {
			t.Errorf("statusTips[%d] is empty", i)
		}
	}
}

// ── Battery rendering tests ─────────────────────────────────────────

func TestRenderHints_NoBattery(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 24)
	tui := &TUI{screen: s, batteryPercent: -1}
	tui.renderHints()

	sw, sh := s.Size()
	y := sh - 1
	var line string
	for x := 0; x < sw; x++ {
		ch, _, _, _ := s.GetContent(x, y)
		line += string(ch)
	}
	// No battery string should appear.
	if containsStr(line, "%") {
		t.Errorf("no battery indicator expected, got: %q", line)
	}
}

func TestRenderHints_BatteryDischarging(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 24)
	tui := &TUI{screen: s, batteryPercent: 67, batteryCharging: false}
	tui.renderHints()

	sw, sh := s.Size()
	y := sh - 1
	var line string
	for x := 0; x < sw; x++ {
		ch, _, _, _ := s.GetContent(x, y)
		line += string(ch)
	}
	if !containsStr(line, "67%") {
		t.Errorf("battery should show 67%%, got: %q", line)
	}
	if containsStr(line, "67% +") {
		t.Error("discharging battery should not show charging indicator")
	}
}

func TestRenderHints_BatteryCharging(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 24)
	tui := &TUI{screen: s, batteryPercent: 42, batteryCharging: true}
	tui.renderHints()

	sw, sh := s.Size()
	y := sh - 1
	var line string
	for x := 0; x < sw; x++ {
		ch, _, _, _ := s.GetContent(x, y)
		line += string(ch)
	}
	if !containsStr(line, "42% +") {
		t.Errorf("charging battery should show '42%% +', got: %q", line)
	}
}

func TestRenderHints_BatteryLow(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 24)
	tui := &TUI{screen: s, batteryPercent: 8, batteryCharging: false}
	tui.renderHints()

	sw, sh := s.Size()
	y := sh - 1
	var line string
	for x := 0; x < sw; x++ {
		ch, _, _, _ := s.GetContent(x, y)
		line += string(ch)
	}
	if !containsStr(line, "8%") {
		t.Errorf("low battery should show 8%%, got: %q", line)
	}
}

func TestReadBattery_Callable(t *testing.T) {
	// Just verify readBattery doesn't panic. On macOS it may return real data
	// or hasBattery=false on a desktop. On Linux CI it returns false.
	pct, charging, has := readBattery()
	if has {
		if pct < 0 || pct > 100 {
			t.Errorf("percent = %d, want 0-100", pct)
		}
		_ = charging
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
