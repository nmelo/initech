package tui

import (
	"fmt"
	"strings"
)

// Modal suspend/wake (operator request, 2026-08-15): `s` toggles the selected
// agent, `S` its whole band. Thin dispatchers over the same one-mechanism
// pair every other entry point uses — suspendAgent (truthful refusals: a
// running or waiting agent is never parked silently) and resumePane (all
// wakes, including startup stagger, walk this path). The modal's error line
// doubles as the notice surface, so every outcome — park, wake, refusal —
// is reported where the operator is looking.

// agentsToggleSuspend parks or wakes the selected agent.
func (t *TUI) agentsToggleSuspend() {
	if t.agents.selected < 0 || t.agents.selected >= len(t.panes) {
		return
	}
	pv := t.panes[t.agents.selected]
	lp, ok := pv.(*Pane)
	if !ok {
		t.agents.error = paneDisplayName(pv) + " runs on another machine; suspend it from that machine's session"
		return
	}
	if lp.IsSuspended() {
		t.wakePanesInBackground([]*Pane{lp}, "modal wake")
		t.agents.error = "waking " + lp.Name()
		return
	}
	if err := t.suspendAgent(lp.Name()); err != nil {
		t.agents.error = err.Error()
		return
	}
	t.agents.error = "suspended " + lp.Name() + " — wakes on a message, a keystroke, or s again"
}

// agentsToggleSuspendGroup parks every parkable member of the selected
// agent's band, or wakes every member when none is awake. Direction is
// derived from the band, not toggled blindly: any awake member means this is
// a suspend pass (busy members are skipped BY NAME), and only an all-parked
// band wakes.
func (t *TUI) agentsToggleSuspendGroup() {
	if t.agents.selected < 0 || t.agents.selected >= len(t.panes) {
		return
	}
	sel := t.panes[t.agents.selected]
	if _, isLocal := sel.(*Pane); !isLocal {
		t.agents.error = paneDisplayName(sel) + " runs on another machine; suspend it from that machine's session"
		return
	}
	label := t.agentsBandLabelOf(sel)

	var members []*Pane
	anyAwake := false
	for _, pv := range t.panes {
		lp, ok := pv.(*Pane)
		if !ok || t.agentsBandLabelOf(pv) != label {
			continue
		}
		members = append(members, lp)
		if lp.IsAlive() && !lp.IsSuspended() {
			anyAwake = true
		}
	}
	if len(members) == 0 {
		return
	}

	if !anyAwake {
		var parked []*Pane
		for _, m := range members {
			if m.IsSuspended() {
				parked = append(parked, m)
			}
		}
		t.wakePanesInBackground(parked, "modal group wake")
		t.agents.error = fmt.Sprintf("waking %d in %s", len(parked), label)
		return
	}

	suspended := 0
	var skipped []string
	for _, m := range members {
		if m.IsSuspended() || !m.IsAlive() {
			continue
		}
		if err := t.suspendAgent(m.Name()); err != nil {
			skipped = append(skipped, m.Name())
			continue
		}
		suspended++
	}
	msg := fmt.Sprintf("suspended %d in %s", suspended, label)
	if len(skipped) > 0 {
		msg += " — skipped (busy): " + strings.Join(skipped, ", ")
	}
	t.agents.error = msg
}

// wakePanesInBackground resumes panes off-main, sequentially: resumePane
// blocks until the agent's first output, which self-paces a group wake the
// same way the startup stagger self-paces a launch. Off-main because a wake
// forks and waits — blocking the render loop on it would freeze the UI for
// exactly the operator who just asked for something.
func (t *TUI) wakePanesInBackground(panes []*Pane, trigger string) {
	run := t.safeGo
	if run == nil {
		run = func(fn func()) { go fn() }
	}
	run(func() {
		for _, p := range panes {
			if err := t.resumePane(p, trigger); err != nil {
				LogWarn("resource", "modal wake failed", "agent", p.Name(), "err", err)
			}
		}
	})
}
