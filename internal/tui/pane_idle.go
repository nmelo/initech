package tui

import "time"

// paneClock is the clock an activity change is stamped with; a test swaps it.
var paneClock = time.Now

// IdleSpell is when the pane's current idle spell began, and true, while its
// activity is idle (pending input and background work are idle underneath).
// Zero and false while it is anything else or before its first change: an
// unknown start is never read as idle since the epoch.
func (p *Pane) IdleSpell() (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activity != StateIdle || p.activitySince.IsZero() {
		return time.Time{}, false
	}
	return p.activitySince, true
}

// ActivitySince is when the pane's activity last changed; zero before its
// first change. Background and pending input are idle underneath, so their
// time counts from when the pane went quiet.
func (p *Pane) ActivitySince() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.activitySince
}

// idleSecOf is the list answer's idle_sec for p: the seconds since its idle
// spell began, for a pane this process runs while it is idle; nil otherwise,
// so a reader never takes an unknown for zero.
func idleSecOf(p PaneView, now time.Time) *int {
	lp, ok := p.(*Pane)
	if !ok {
		return nil
	}
	at, ok := lp.IdleSpell()
	if !ok {
		return nil
	}
	sec := max(int(now.Sub(at)/time.Second), 0)
	return &sec
}
