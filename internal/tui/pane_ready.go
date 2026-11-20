package tui

import (
	"fmt"
	"time"
)

// Pane readiness (ini-kbpo): a watch line due while its owner's pane is still
// coming up waits in STARTING and goes in once the pane is ready, as at a
// stop. It is never refused and holds no retry loop of its own: the pane runs
// what waits on it the moment its startup gate opens. A send to a starting
// pane is held the same way (ini-krly): one mechanism.

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
	quiet := !last.IsZero() && !last.Before(started) && now.Sub(last) >= startupSettleStable
	if !quiet && now.Sub(started) < startupGateWindow {
		return false
	}
	p.mu.Lock()
	p.startupSettled = true
	p.mu.Unlock()
	p.runReadyWaiters()
	return true
}

// sendReadyKey is the readiness waiter held sends to a starting pane share.
const sendReadyKey = "send-start"

// holdWhileStarting holds a send to a pane that is still starting (ini-krly)
// instead of refusing it: ordinary held mail with its receipt, the same
// limits and held file, typed once the pane is ready (its startup gate opens:
// quiet, or startupGateWindow at the latest), in order behind earlier held
// mail. Every other hold applies from then on.
func (p *Pane) holdWhileStarting(text string, enter bool, o sendOrigin) SendVerdict {
	refusal, receipt := p.enqueueReceipt(QueuedMessage{Text: text, Enter: enter, From: o.From, Time: time.Now(), Receipt: o.Receipt}, sendAgainWhenStarted)
	if refusal != "" {
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: refusal}
	}
	ahead := p.QueuedMessageCount() - 1
	LogInfo("inject", "held: the agent is still starting", "pane", p.name, "queued", ahead+1)
	p.startModalDrain() // it waits for the pane to be ready, off this goroutine
	return SendVerdict{Outcome: DeliveryHeld, Reason: fmt.Sprintf("held for %s: still starting, %d ahead (receipt %s)", p.name, ahead, receipt), Receipt: receipt}
}

// waitUntilReady waits, at most max, for the pane to stop reading starting:
// the child quiet for startupSettleStable (this opens the gate), or the gate
// opened elsewhere (the maintenance tick, a send's wait). It reports false at
// max, or when the pane closes, still starting.
func (p *Pane) waitUntilReady(max time.Duration) bool {
	deadline := time.Now().Add(max)
	for {
		now := time.Now()
		if !p.startingNow(now) {
			return true
		}
		p.mu.Lock()
		started := p.startedAt
		p.mu.Unlock()
		if last := p.LastOutputTime(); !last.IsZero() && !last.Before(started) && now.Sub(last) >= startupSettleStable {
			p.mu.Lock()
			p.startupSettled = true
			p.mu.Unlock()
			p.runReadyWaiters()
			return true
		}
		if !now.Before(deadline) {
			return false
		}
		select {
		case <-p.closingC():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
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
