package tui

// maintenance.go is THE per-pane maintenance, one definition for every
// process that hosts panes (ini-dbqy). The TUI's render loop runs it for its
// local panes; initech serve's daemon runs it for the agents it hosts. Before
// this the daemon ran none of it: a remote agent's dialog announcements were
// never read, held mail behind a dialog waited forever on a quiet pane, mail
// and withheld Enters past their deadlines were never reported, and Activity
// never moved off the value NewPane set.

import "time"

// maintenanceFrameInterval is the render loop's frame tick, and the daemon's
// maintenance tick, so a hosted pane is maintained at the same cadence
// wherever it runs.
const maintenanceFrameInterval = 33 * time.Millisecond

// maintainPaneFrame is the per-frame pane maintenance, in the order the
// render loop has always run it: attention detection BEFORE updateActivity,
// so a dialog that opened since the last frame is already reflected in the
// waiting state updateActivity ranks against byte recency (ini-2x8.2).
func maintainPaneFrame(p *Pane) {
	p.refreshWaitingState()
	p.refreshTier2WaitingState()
	p.updateActivity()
}

// maintainModal is the once-a-second, output-independent modal-guard
// housekeeping over a pane list: bound withheld submits, corroborate or heal
// dialog latches, report held mail that cannot be delivered, and drain held
// mail on panes producing no output (ini-9gvn). *last is the caller's
// rate-limit clock. Non-local panes are skipped: they are maintained by the
// process that hosts them.
func maintainModal(panes []PaneView, now time.Time, last *time.Time) {
	if now.Sub(*last) < time.Second {
		return
	}
	*last = now

	// A withheld submit is bounded here for the same reason the deferred queue
	// below is drained here: both resolve on pane OUTPUT, and a quiet pane
	// produces none (ini-vpwg finding 4 is ini-9gvn's third job, one path
	// over). Nothing about it is modal-specific -- it lives here because this
	// is the fleet's once-a-second, output-independent tick.
	sweepWithheldSubmitsIn(panes, now)

	// The process tree, read off this goroutine and only while some pane's
	// footer shows background work, says which jobs run and since when
	// (ini-d2sx). nil when there is no fresh sample.
	wantTree := false
	for _, pv := range panes {
		if p, ok := pv.(*Pane); ok && p.footerBusy() {
			wantTree = true
			break
		}
	}
	tree := procSampler.sample(wantTree)

	for _, pv := range panes {
		p, ok := pv.(*Pane)
		if !ok {
			continue // Remote panes are maintained by the window that owns them.
		}
		// Do-not-disturb ends at its timer, or at turn end (ini-x432).
		p.endDNDIfDue(now)
		// ONE NON-BLOCKING READ PER PANE PER TICK (ini-psjt). Everything below
		// that looks at the screen looks at this snapshot. Through the blocking
		// accessors, a single pane whose emulator lock was held by a stalled
		// writer parked the main loop -- hover's window, 30+ hours, 11,779
		// watchdog dumps -- because the lock was taken here, on the goroutine
		// that renders every other pane. A held lock now costs this pane one
		// tick, and the skip is recorded so a wedged pane is visible rather
		// than silently unmaintained.
		rows, ok := tryScreenRows(p)
		if !ok {
			p.noteScreenSkipped(now, "modal")
			continue
		}
		p.noteScreenRead()
		// Background work from the same snapshot (ini-b4l0); the width is
		// the size withScreen just recorded.
		p.mu.Lock()
		cols := p.lastEmuCols
		p.mu.Unlock()
		starts, treeOK := tree.runningShells(p.pid)
		p.observeBackgroundTree(now, p.IsAlive(), parseFooter(rows, cols), starts, treeOK)
		p.raiseSilentClaudeDialog(rows)
		if screenShowsModal(rows) {
			p.noteDialogSighting()
		}
		// POSITIVE-EVIDENCE CLEAR (ini-gbqc): the pane at its idle prompt for
		// the window, counted from the raise. Runs before the age audit, which
		// remains the backstop for a raise the screen can neither confirm nor
		// refute (no prompt and no dialog visible).
		stable := p.observeIdlePrompt(now, screenShowsIdleComposer(rows))
		if p.auditIdleLatch(now) {
			LogInfo("modal", "dialog latch cleared",
				"pane", p.Name(), "by", "idle prompt (latch cleared: pane returned to its idle prompt)",
				"stable_for", stable, "queued", p.QueuedMessageCount())
			p.reportEvent(AgentEvent{
				Type:   EventMessageSent,
				Pane:   p.Name(),
				Detail: "held mail released: pane returned to prompt",
				Time:   now,
			})
		}
		if p.auditDialogLatch(now) {
			LogWarn("modal", "dialog latch cleared: a declared dialog was never seen on screen (latch downgraded)",
				"pane", p.Name(), "window", latchCorroborationWindow)
			p.reportEvent(AgentEvent{
				Type:   EventMessageSent,
				Pane:   p.Name(),
				Detail: "held mail released: a declared dialog never appeared",
				Time:   now,
			})
		}
		// GIVE UP LOUDLY, NEVER SILENTLY (ini-1898): a dead pane's mail and
		// mail held past the deadline are reported, not retried forever.
		p.resolveHeldMail(now)
		if p.QueuedMessageCount() > 0 && !p.dialogLatched() && !screenShowsModal(rows) {
			// Its own goroutine because the drain paces itself between
			// messages; single-flight so a tick never starts a second drain
			// beside one already running.
			p.startModalDrain()
		}
	}
}
