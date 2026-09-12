package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"

	"github.com/nmelo/initech/internal/config"
)

// ini-psjt, second round. qa1 bounced the first fix: refreshWaitingState
// still blocked the main loop on a held emulator through the blocking
// bottom-text read -- the same class fixed for modalMaintenance, one caller
// over. This file holds one cell per MAIN-LOOP reader of a pane's emulator,
// each against the two holder shapes that exist in production:
//
//   - readLoop's shape: renderMu held, then the emulator's write lock held
//     inside Write while the DSR reply blocks on the undrained pipe. This is
//     hover's cycle.
//   - the emulator alone (qa1's reproduction): the write lock held with
//     renderMu free.
//
// Each cell asserts two things: the call returns promptly, and the pane's
// state is KEPT, not cleared -- "cannot read" is never "not on screen".

// holdLikeReadLoop wedges p the way hover's readLoop was wedged. Cleanup
// (undrainedEmu's) drains the pipe so the holder can exit.
func holdLikeReadLoop(t *testing.T, p *Pane) {
	t.Helper()
	go func() {
		p.renderMu.Lock()
		defer p.renderMu.Unlock()
		_, _ = p.emu.Write([]byte("\x1b[6n"))
	}()
	time.Sleep(50 * time.Millisecond)
}

// holdEmulatorOnly wedges only the emulator's write lock (qa1's shape).
func holdEmulatorOnly(t *testing.T, p *Pane) {
	t.Helper()
	go func() { _, _ = p.emu.Write([]byte("\x1b[6n")) }()
	time.Sleep(50 * time.Millisecond)
}

// holders runs fn once per holder shape.
func holders(t *testing.T, fn func(t *testing.T, hold func(*testing.T, *Pane))) {
	t.Run("readLoop_shape", func(t *testing.T) { fn(t, holdLikeReadLoop) })
	t.Run("emulator_only", func(t *testing.T) { fn(t, holdEmulatorOnly) })
}

// releaseHold drains one pending response so a holder's Write returns.
func releaseHold(t *testing.T, emu *vt.SafeEmulator) {
	t.Helper()
	buf := make([]byte, 4096)
	done := make(chan struct{})
	go func() { _, _ = emu.Read(buf); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("fixture: holder did not release after the pipe was drained")
	}
	time.Sleep(20 * time.Millisecond)
}

// renderPane is a local pane sized to a 40x10 region on a simulation screen.
func renderPane(t *testing.T, name string) (*Pane, tcell.SimulationScreen) {
	t.Helper()
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	t.Cleanup(screen.Fini)
	screen.SetSize(40, 10)
	p := &Pane{
		name:    name,
		emu:     undrainedEmu(t),
		alive:   true,
		visible: true,
		region:  Region{X: 0, Y: 0, W: 40, H: 10},
		eventCh: make(chan AgentEvent, 8),
	}
	p.emu.Resize(40, 8)
	return p, screen
}

const renderCallBudget = time.Second

// qa1's cell: the attention refresher must not wait on the pane, and must
// leave the row and the latch exactly as they were.
func TestRefreshWaitingState_DoesNotBlockOnAHeldEmulator(t *testing.T) {
	holders(t, func(t *testing.T, hold func(*testing.T, *Pane)) {
		captureLogs(t)
		p := dialogPane("eng1")
		p.emu = undrainedEmu(t)
		p.emu.Resize(120, 24)
		registerAttentionOSC(p)
		paint(t, p, askUserQuestionDialog...)
		if _, err := p.emu.Write([]byte(measuredOSC777)); err != nil {
			t.Fatalf("write osc: %v", err)
		}
		p.refreshWaitingState()
		if waiting, _, _ := p.WaitingInput(); !waiting {
			t.Fatal("fixture: the dialog did not raise the waiting state")
		}
		if !p.modalSeen() {
			t.Fatal("fixture: the screen did not corroborate the dialog")
		}

		hold(t, p)
		if !completesWithin(renderCallBudget, p.refreshWaitingState) {
			t.Fatal("main loop BLOCKED: refreshWaitingState waited on a pane's emulator lock (qa1's 3/3 reproduction)")
		}
		if waiting, _, _ := p.WaitingInput(); !waiting {
			t.Error("an unreadable screen RETIRED the row: 'cannot read' was taken as 'not on screen'")
		}
		if !p.modalSeen() {
			t.Error("an unreadable screen cleared the corroboration")
		}
	})
}

func TestRefreshTier2WaitingState_DoesNotBlockOnAHeldEmulator(t *testing.T) {
	holders(t, func(t *testing.T, hold func(*testing.T, *Pane)) {
		captureLogs(t)
		p := &Pane{name: "eng3", emu: undrainedEmu(t), alive: true, agentType: config.AgentTypeCodex, eventCh: make(chan AgentEvent, 8)}
		if !tier2Eligible(p) {
			t.Fatal("fixture: pane must be tier-2 eligible (no tier-1 signal)")
		}
		p.SetWaitingInputTier(tier2PreviewText, WaitingTierListOnly)

		hold(t, p)
		if !completesWithin(renderCallBudget, p.refreshTier2WaitingState) {
			t.Fatal("main loop BLOCKED: refreshTier2WaitingState waited on a pane's emulator lock")
		}
		if waiting, _, _ := p.WaitingInput(); !waiting {
			t.Error("an unreadable screen RETIRED the tier-2 row: 'cannot read' was taken as 'no dialog'")
		}
	})
}

func TestPaneRender_DoesNotBlockOnAHeldEmulator(t *testing.T) {
	holders(t, func(t *testing.T, hold func(*testing.T, *Pane)) {
		captureLogs(t)
		p, screen := renderPane(t, "eng1")
		hold(t, p)
		if !completesWithin(renderCallBudget, func() { p.Render(screen, true, false, 1, Selection{}) }) {
			t.Fatal("main loop BLOCKED: Pane.Render waited on a pane's emulator lock; " +
				"this is the next read in frame order after the attention refreshers")
		}
	})
}

// A held pane keeps showing its last frame -- a blank region would read as
// the pane having died -- and after screenFrozenAfter says why.
func TestPaneRender_HeldPaneKeepsItsLastFrameThenSaysWhy(t *testing.T) {
	captureLogs(t)
	p, screen := renderPane(t, "eng1")
	if _, err := p.emu.Write([]byte("hello frozen pane")); err != nil {
		t.Fatalf("paint: %v", err)
	}
	p.Render(screen, true, false, 1, Selection{})
	screen.Show()
	if !strings.Contains(strings.Join(screenRows(t, screen), "\n"), "hello frozen pane") {
		t.Fatal("fixture: the unheld frame did not draw the content")
	}

	holdLikeReadLoop(t, p)
	screen.Clear()
	p.Render(screen, true, false, 1, Selection{})
	screen.Show()
	rows := strings.Join(screenRows(t, screen), "\n")
	if !strings.Contains(rows, "hello frozen pane") {
		t.Errorf("held pane lost its last frame; screen:\n%s", rows)
	}
	if strings.Contains(rows, strings.TrimSpace(screenFrozenMarker)) {
		t.Errorf("marker drawn on the FIRST skipped frame; a busy healthy pane would flash it:\n%s", rows)
	}

	p.mu.Lock()
	p.screenSkipSince = time.Now().Add(-screenFrozenAfter - time.Second)
	p.mu.Unlock()
	screen.Clear()
	p.Render(screen, true, false, 1, Selection{})
	screen.Show()
	rows = strings.Join(screenRows(t, screen), "\n")
	if !strings.Contains(rows, strings.TrimSpace(screenFrozenMarker)) {
		t.Errorf("pane unreadable for longer than screenFrozenAfter does not say so:\n%s", rows)
	}
	if !strings.Contains(rows, "hello frozen") {
		t.Errorf("the marker replaced the whole frame instead of one row:\n%s", rows)
	}
}

// The frame cache and the skip must clear on the next successful read: a
// pane that recovers shows live content again, not its frozen frame.
func TestPaneRender_RecoveredPaneShowsLiveContentAgain(t *testing.T) {
	captureLogs(t)
	p, screen := renderPane(t, "eng1")
	if _, err := p.emu.Write([]byte("before")); err != nil {
		t.Fatalf("paint: %v", err)
	}
	p.Render(screen, true, false, 1, Selection{})
	holdLikeReadLoop(t, p)
	p.Render(screen, true, false, 1, Selection{})
	releaseHold(t, p.emu)

	if _, err := p.emu.Write([]byte(" after")); err != nil {
		t.Fatalf("paint: %v", err)
	}
	screen.Clear()
	p.Render(screen, true, false, 1, Selection{})
	screen.Show()
	rows := strings.Join(screenRows(t, screen), "\n")
	if !strings.Contains(rows, "before after") {
		t.Errorf("recovered pane still shows its frozen frame:\n%s", rows)
	}
	p.mu.Lock()
	since := p.screenSkipSince
	p.mu.Unlock()
	if !since.IsZero() {
		t.Error("skip record not cleared by a successful render")
	}
}

// Scroll geometry on the main loop: the mouse handlers and ScrollUp read the
// emulator through contentOffset/maxScrollOffset without renderMu. Held, they
// answer from the last read instead of waiting.
func TestScrollGeometry_DoesNotBlockOnAHeldEmulator(t *testing.T) {
	holders(t, func(t *testing.T, hold func(*testing.T, *Pane)) {
		captureLogs(t)
		p, screen := renderPane(t, "eng1")
		p.Render(screen, true, false, 1, Selection{}) // one successful read seeds the fallbacks
		wantCols, wantRows := p.emuSize()
		if wantCols != 40 || wantRows != 8 {
			t.Fatalf("fixture: emuSize = %dx%d, want 40x8", wantCols, wantRows)
		}

		hold(t, p)
		if !completesWithin(renderCallBudget, func() { p.contentOffset() }) {
			t.Fatal("main loop BLOCKED: contentOffset waited on a pane's emulator lock (mouse path)")
		}
		if !completesWithin(renderCallBudget, func() { p.maxScrollOffset() }) {
			t.Fatal("main loop BLOCKED: maxScrollOffset waited on a pane's emulator lock")
		}
		if !completesWithin(renderCallBudget, func() { p.ScrollUp(3) }) {
			t.Fatal("main loop BLOCKED: ScrollUp waited on a pane's emulator lock (key handler)")
		}
		var cols, rows int
		if !completesWithin(renderCallBudget, func() { cols, rows = p.emuSize() }) {
			t.Fatal("main loop BLOCKED: emuSize waited on a pane's emulator lock (restart/resume path)")
		}
		if cols != wantCols || rows != wantRows {
			t.Errorf("held emuSize = %dx%d, want the last size seen under the lock %dx%d", cols, rows, wantCols, wantRows)
		}
	})
}

// :peek and patrol run on the main loop (patrol under runOnMain). A held pane
// reports that it is unreadable instead of parking the window.
func TestPeekContent_DoesNotBlockOnAHeldEmulator(t *testing.T) {
	holders(t, func(t *testing.T, hold func(*testing.T, *Pane)) {
		p, _ := renderPane(t, "eng1")
		hold(t, p)
		var got string
		if !completesWithin(renderCallBudget, func() { got = peekContent(p, 5) }) {
			t.Fatal("main loop BLOCKED: peekContent waited on a pane's emulator lock (:peek, patrol)")
		}
		if !strings.Contains(got, peekUnreadable) {
			t.Errorf("held peek = %q, want the unreadable marker", got)
		}
	})
}

// The selection copy on mouse-up reads every selected cell on the main loop.
func TestExtractSelectionText_DoesNotBlockOnAHeldEmulator(t *testing.T) {
	holders(t, func(t *testing.T, hold func(*testing.T, *Pane)) {
		captureLogs(t)
		p, _ := renderPane(t, "eng1")
		if _, err := p.emu.Write([]byte("copy me")); err != nil {
			t.Fatalf("paint: %v", err)
		}
		tui := newTestTUI(p)
		tui.sel = mouseSelection{active: true, pane: 0, startX: 0, startY: 0, endX: 6, endY: 0}
		if got := tui.extractSelectionText(); got != "copy me" {
			t.Fatalf("fixture: unheld copy = %q, want %q", got, "copy me")
		}

		hold(t, p)
		var got string
		if !completesWithin(renderCallBudget, func() { got = tui.extractSelectionText() }) {
			t.Fatal("main loop BLOCKED: extractSelectionText waited on a pane's emulator lock (mouse-up)")
		}
		if got != "" {
			t.Errorf("held copy = %q, want nothing copied rather than a torn or stale read", got)
		}
	})
}

// A layout resize on the main loop must not wait on the pane either. It is
// remembered and applied by the next frame after the lock frees.
func TestResizeFromMainLoop_DefersWhileHeldAndAppliesOnceFreed(t *testing.T) {
	captureLogs(t)
	p, screen := renderPane(t, "eng1")
	p.Render(screen, true, false, 1, Selection{}) // one successful read seeds emuSize's fallback
	holdLikeReadLoop(t, p)

	if !completesWithin(renderCallBudget, func() { p.resizeFromMainLoop(12, 60) }) {
		t.Fatal("main loop BLOCKED: resize waited on a pane's emulator lock (applyLayout)")
	}
	if p.pendingResize == nil {
		t.Fatal("a resize the pane could not take was dropped instead of remembered")
	}
	if cols, rows := p.emuSize(); cols != 40 || rows != 8 {
		t.Fatalf("held pane changed size to %dx%d without the lock", cols, rows)
	}

	releaseHold(t, p.emu)
	p.Render(screen, true, false, 1, Selection{}) // the frame retries pending resizes
	if p.pendingResize != nil {
		t.Fatal("pending resize not applied once the lock was free")
	}
	if cols, rows := p.emuSize(); cols != 60 || rows < 12 {
		t.Errorf("after release emuSize = %dx%d, want 60 cols and at least 12 rows", cols, rows)
	}
}

// The skip is logged once per rate-limit window, whoever the reader is.
func TestScreenSkip_IsLoggedOnceAcrossReaders(t *testing.T) {
	logs := captureLogs(t)
	p, screen := renderPane(t, "eng1")
	holdLikeReadLoop(t, p)
	for i := 0; i < 3; i++ {
		p.Render(screen, true, false, 1, Selection{})
		p.refreshTier2WaitingState()
	}
	out := logs.String()
	if n := strings.Count(out, "screen read SKIPPED"); n != 1 {
		t.Errorf("skip logged %d times inside one rate-limit window, want 1:\n%s", n, out)
	}
	line := recordContaining(t, out, "screen read SKIPPED")
	if !strings.Contains(line, "level=INFO") || !strings.Contains(line, "reader=") {
		t.Errorf("skip record must be INFO and name its reader: %q", line)
	}
}

// The unheld path is the common one and must still read through the region:
// a mutant that skips every pane would leave the row never corroborated and
// the frame never drawn.
func TestScreenReaders_UnheldPaneIsReadLive(t *testing.T) {
	captureLogs(t)
	p, screen := renderPane(t, "eng1")
	if _, err := p.emu.Write([]byte("live")); err != nil {
		t.Fatalf("paint: %v", err)
	}
	p.Render(screen, true, false, 1, Selection{})
	screen.Show()
	if !strings.Contains(strings.Join(screenRows(t, screen), "\n"), "live") {
		t.Error("unheld pane not rendered")
	}
	rows, ok := tryScreenRows(p)
	if !ok || !strings.Contains(strings.Join(rows, "\n"), "live") {
		t.Errorf("unheld pane not read: ok=%v rows=%q", ok, rows)
	}
	if got := peekContent(p, 5); !strings.Contains(got, "live") {
		t.Errorf("unheld peek = %q", got)
	}
}
