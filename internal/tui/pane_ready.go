package tui

import "time"

// Pane readiness (ini-kbpo): a watch line due while its owner's pane is still
// coming up waits in STARTING and goes in once the pane is ready, as at a
// stop. It is never refused and holds no retry loop of its own: the pane runs
// what waits on it the moment its startup gate opens. Built for watch lines;
// an ordinary send to a starting pane is unchanged (refused).

// whenReady runs fn once the pane's startup gate is open: now (reporting
// true) when it is already open, else when the gate opens. key keeps one
// waiter per caller, so a pass that runs again does not queue a second.
func (p *Pane) whenReady(key string, now time.Time, fn func()) bool {
	if !p.startingNow(now) {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.readyWaiters == nil {
		p.readyWaiters = map[string]func(){}
	}
	p.readyWaiters[key] = fn
	return false
}

// openStartupGate is the maintenance tick's read of the startup gate,
// without awaitStartupSettled's wait: a pane that has been quiet for
// startupSettleStable, or started more than startupGateWindow ago, is
// settled. It runs what waits on readiness and reports whether it opened.
func (p *Pane) openStartupGate(now time.Time) bool {
	if p.agentReader() != noAgentReader || !p.hasPTY() {
		p.runReadyWaiters() // no startup gate for this pane
		return false
	}
	p.mu.Lock()
	settled, started := p.startupSettled, p.startedAt
	p.mu.Unlock()
	if settled || started.IsZero() {
		p.runReadyWaiters()
		return false
	}
	last := p.LastOutputTime()
	quiet := !last.IsZero() && last.After(started) && now.Sub(last) >= startupSettleStable
	if !quiet && now.Sub(started) < startupGateWindow {
		return false
	}
	p.mu.Lock()
	p.startupSettled = true
	p.mu.Unlock()
	p.runReadyWaiters()
	return true
}

// runReadyWaiters runs and clears what waits on readiness, each off the
// caller's goroutine (the main loop never waits on a send).
func (p *Pane) runReadyWaiters() {
	p.mu.Lock()
	ws := p.readyWaiters
	p.readyWaiters = nil
	p.mu.Unlock()
	for _, fn := range ws {
		if p.safeGo != nil {
			p.safeGo(fn)
		} else {
			go fn()
		}
	}
}
