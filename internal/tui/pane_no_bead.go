package tui

// pane_no_bead.go is the "no bead" marker for unattributed work (ini-wra.3):
// a pane that has been working for noBeadAfter with no bead, derived or set,
// reads "no bead" in initech status, patrol and its ribbon. Display only:
// nothing is ever typed into the agent's pane for it.

import "time"

// noBeadAfter is how much working time with no bead turns the marker on.
// Only working time counts (running, or quiet with a background job); idle
// and suspended time pause the count, and a bead resets it.
const noBeadAfter = 15 * time.Minute

// noBeadMaxStep caps what one tick adds, so a stalled tick loop (a machine
// asleep, a long GC pause) is not counted as fifteen minutes of work.
const noBeadMaxStep = 5 * time.Second

// noteNoBeadWork advances the pane's unattributed-work count by the time since
// the last tick. Called once a second from maintainModal, for the panes this
// process runs.
func (p *Pane) noteNoBeadWork(now time.Time) {
	working := paneIsWorking(p) // takes p.mu itself
	p.mu.Lock()
	defer p.mu.Unlock()
	last := p.noBeadTick
	p.noBeadTick = now
	if p.primaryBeadLocked() != "" || p.noBeadWork != "" {
		// A bead (or the dispatcher's no-bead words) attributes the work.
		p.noBeadFor = 0
		return
	}
	if !working || last.IsZero() || !now.After(last) {
		return
	}
	step := now.Sub(last)
	if step > noBeadMaxStep {
		step = noBeadMaxStep
	}
	p.noBeadFor += step
}

// Unattributed reports whether the pane has worked noBeadAfter with no bead
// and still has none.
func (p *Pane) Unattributed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.noBeadFor >= noBeadAfter && p.primaryBeadLocked() == "" && p.noBeadWork == ""
}

// unattributedOf reports pv's marker; false for a pane without one.
func unattributedOf(pv PaneView) bool {
	if p, ok := pv.(*Pane); ok {
		return p.Unattributed()
	}
	return false
}
