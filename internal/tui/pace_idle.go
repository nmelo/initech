package tui

import (
	"fmt"
	"time"

	"github.com/nmelo/initech/internal/progress"
)

// paceIdleMark is one idle-owner bead (ini-9dv) as the pane row shows it.
type paceIdleMark struct {
	id   string
	idle time.Duration
}

// paneIdleSpells maps each local pane that is idle to the start of its idle
// spell. A pane on another machine is not in it: this initech cannot see its
// clock, so its beads never read idle-owner here. Main loop only.
func (t *TUI) paneIdleSpells() map[string]time.Time {
	spells := map[string]time.Time{}
	for _, pv := range t.panes {
		p, ok := pv.(*Pane)
		if !ok {
			continue
		}
		if at, ok := p.IdleSpell(); ok {
			spells[p.name] = at
		}
	}
	return spells
}

// notePaceIdle records the idle-owner beads for the pane rows and sends super
// one line per bead per idle spell: the key is the owner and the bead, the
// spell is when the owner's pane went idle. While that spell lasts nothing
// more is sent; after the pane was active and went idle again the spell is
// new, and so is the line. Called from refreshPaceLines. Main loop only.
func (t *TUI) notePaceIdle(beads []progress.Bead, panes map[string]string, rule progress.Rule, spells map[string]time.Time) {
	t.paceIdle = map[string][]paceIdleMark{}
	if t.paceIdleSent == nil {
		t.paceIdleSent = map[string]time.Time{}
	}
	held := map[string]bool{}
	for _, b := range beads {
		if b.Assignee == "" {
			continue
		}
		key := b.Assignee + "|" + b.ID
		if b.Status == "in_progress" {
			held[key] = true
		}
		if !progress.IdleOwner(b, panes[b.Assignee], rule) {
			continue
		}
		t.paceIdle[b.Assignee] = append(t.paceIdle[b.Assignee], paceIdleMark{id: b.ID, idle: b.OwnerIdle})
		spell := spells[b.Assignee]
		if sent, ok := t.paceIdleSent[key]; ok && sent.Equal(spell) {
			continue
		}
		t.paceIdleSent[key] = spell
		t.sendInitechLine(fmt.Sprintf("%s idle %s on ▶ %s; check on it",
			progress.Clean(b.Assignee), progress.Minutes(b.OwnerIdle), progress.Clean(b.ID)))
	}
	// A bead its owner no longer holds in progress forgets its spell, so the
	// map does not grow; one still held keeps it while its pane is busy, so a
	// pane that flickers through background does not send twice.
	for key := range t.paceIdleSent {
		if !held[key] {
			delete(t.paceIdleSent, key)
		}
	}
}

// paceIdleText is the pane row's idle-owner part: "idle 23m · ▶ ini-abc", one
// per bead (normally one); "" when the agent holds none.
func paceIdleText(word string, marks []paceIdleMark) string {
	s := ""
	for i, m := range marks {
		if i > 0 {
			s += " · "
		}
		s += word + " " + progress.Minutes(m.idle) + " · ▶ " + progress.Clean(m.id)
	}
	return s
}
