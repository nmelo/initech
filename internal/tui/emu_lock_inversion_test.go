package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// ini-psjt: hover's main loop sat 30+ hours on an emulator RLock. The dump's
// cycle, all in one pane: an IPC send's sendSubmitKey held the emulator WRITE
// lock inside SafeEmulator.SendKey while blocked writing the key into the
// emulator's input pipe; responseLoop, which drains that pipe, was stuck in
// ptmx.Write because the child had stopped reading; and the child had stopped
// because readLoop, which drains the PTY, was queued on the same emulator lock.
// The main loop then joined the queue at modalMaintenance's screen read.
//
// Two layers, two tests. Layer (a) is the inversion itself: a key send must not
// hold the emulator lock across a blocking pipe write. Layer (b) is the blast
// radius: the main loop must never block on any pane's lock, whoever holds it.

// undrainedEmu is an emulator nobody reads responses from -- responseLoop stuck
// in ptmx.Write, in the dump. Cleanup drains it so blocked writers can exit.
func undrainedEmu(t *testing.T) *vt.SafeEmulator {
	t.Helper()
	emu := vt.NewSafeEmulator(80, 24)
	t.Cleanup(func() {
		buf := make([]byte, 4096)
		for i := 0; i < 8; i++ {
			done := make(chan struct{})
			go func() { emu.Read(buf); close(done) }() //nolint:errcheck
			select {
			case <-done:
			case <-time.After(50 * time.Millisecond):
				return
			}
		}
	})
	return emu
}

// completesWithin runs fn on its own goroutine and reports whether it returned
// before d. A false is a hang, exactly as the watchdog saw it.
func completesWithin(d time.Duration, fn func()) bool {
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// LAYER (a): a submit key blocked on an undrained pipe must not hold the
// emulator lock while it waits. Today it does, so a plain reader is stuck
// behind it for as long as the pipe stays full -- in hover, 30 hours.
func TestEmulator_SendKeyDoesNotHoldTheLockAcrossTheBlockingPipeWrite(t *testing.T) {
	emu := undrainedEmu(t)
	go sendSubmitKey(emu, "") // blocks in the pipe write; the question is what it holds meanwhile
	time.Sleep(50 * time.Millisecond)

	if !completesWithin(500*time.Millisecond, func() { emu.Width() }) {
		t.Fatal("INVERSION: SendKey holds the emulator lock while blocked on the input pipe; " +
			"a reader (readLoop, the main loop) is stuck behind it for as long as the pipe is full")
	}
}

// LAYER (b): whoever holds the emulator write lock, modalMaintenance's screen
// reads must not block the main loop on it. The holder here is a DIFFERENT
// production path from layer (a) -- a DSR query inside Write, which the
// emulator answers by writing to the same undrained pipe while still holding
// the lock (the fork's own Read comment describes it) -- so this stays a real
// holder after layer (a) is fixed, and any child that emits a cursor-position
// query while responseLoop is wedged reproduces the class with no SendKey at
// all.
func TestModalMaintenance_DoesNotBlockTheMainLoopOnAHeldEmulator(t *testing.T) {
	emu := undrainedEmu(t)
	p := &Pane{name: "eng1", emu: emu, eventCh: make(chan AgentEvent, 8)}
	tui := &TUI{}
	tui.panes = toPaneViews([]*Pane{p})

	go emu.Write([]byte("\x1b[6n")) // DSR: reply written to the pipe under the write lock; nobody drains
	time.Sleep(50 * time.Millisecond)

	if !completesWithin(500*time.Millisecond, func() {
		tui.lastModalMaint = time.Time{}
		tui.modalMaintenance(time.Now())
	}) {
		t.Fatal("main loop BLOCKED: modalMaintenance waited on a pane's emulator lock; " +
			"one wedged pane freezes the whole window (hover, 11,779 watchdog dumps)")
	}
}

// A skipped pane must be VISIBLE. Silently unmaintaining a wedged pane is the
// ini-9gvn invisibility class: mail held, nothing in the log.
func TestModalMaintenance_SkippedPaneIsLoggedAtInfo(t *testing.T) {
	logs := captureLogs(t)
	emu := undrainedEmu(t)
	p := &Pane{name: "eng1", emu: emu, eventCh: make(chan AgentEvent, 8)}
	tui := &TUI{}
	tui.panes = toPaneViews([]*Pane{p})
	go emu.Write([]byte("\x1b[6n")) // holds the write lock; nobody drains the reply
	time.Sleep(50 * time.Millisecond)

	base := time.Now()
	for i := 0; i < 3; i++ { // three ticks inside one rate-limit window
		tui.lastModalMaint = time.Time{}
		tui.modalMaintenance(base.Add(time.Duration(i) * time.Second))
	}
	line := recordContaining(t, logs.String(), "screen read SKIPPED")
	if !strings.Contains(line, "level=INFO") {
		t.Errorf("the skip is below the default log level: %q", line)
	}
}

// The skip must be the EXCEPTION: a pane whose lock is free is read on every
// tick, through the snapshot, and sight still corroborates a latch. A mutant
// that skips every pane leaves this uncorroborated.
func TestModalMaintenance_UnheldPaneIsStillReadThroughTheSnapshot(t *testing.T) {
	captureLogs(t)
	p := &Pane{name: "eng1", emu: vt.NewSafeEmulator(80, 24), eventCh: make(chan AgentEvent, 8)}
	p.dialogOpen = true // declared, not yet seen
	p.dialogOpenAt = time.Now()
	_, _ = p.emu.Write([]byte("\x1b[2J\x1b[20;1HDo you want to proceed?\r\n❯ 1. Yes\r\n  2. No"))
	tui := &TUI{}
	tui.panes = toPaneViews([]*Pane{p})

	tui.lastModalMaint = time.Time{}
	tui.modalMaintenance(time.Now())

	p.mu.Lock()
	seen := p.dialogCorroborated
	p.mu.Unlock()
	if !seen {
		t.Fatal("a rendered dialog on an unheld pane was not corroborated: the tick skipped a pane it could read")
	}
}
