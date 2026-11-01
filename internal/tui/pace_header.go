package tui

// pace_header.go puts each pane's effort pace on its top edge (ini-y0n.2):
// "ini-wra 2/4 · 1/h · 0 stuck", from the bead poll window 1 already runs
// (one bd list every beadPollInterval, ini-wra.1) and the pane states the TUI
// already has. The line is computed once a minute on the main loop; a frame
// only reads it, so drawing does no bd or git work.

import (
	"time"

	"github.com/nmelo/initech/internal/progress"
)

// paceEvery is how often the header lines are recomputed.
const paceEvery = time.Minute

// paceStaleMark follows a line whose numbers are from before bd stopped
// answering.
const paceStaleMark = " (stale)"

// notePaceAnswer keeps the beads of the poll that just ran, for the next
// refresh. A poll bd did not answer keeps the last answer and marks it stale.
// Main loop only.
func (t *TUI) notePaceAnswer(beads []bdBead, answered bool) {
	if !answered {
		t.paceStale = true
		return
	}
	t.paceBeads, t.paceStale, t.paceAnswered = beads, false, true
}

// refreshPaceLines recomputes every local pane's header line, at most once a
// minute, and at once after the first answer. Main loop only.
func (t *TUI) refreshPaceLines(now time.Time) {
	if !t.paceAnswered {
		return
	}
	if !t.paceAt.IsZero() && now.Sub(t.paceAt) < paceEvery {
		return
	}
	t.paceAt = now
	beads := make([]progress.Bead, len(t.paceBeads))
	for i, b := range t.paceBeads {
		beads[i] = progress.Bead{ID: b.ID, Title: b.Title, Status: b.Status, Assignee: b.Assignee, UpdatedAt: b.UpdatedAt, ClosedAt: b.ClosedAt}
	}
	panes := make(map[string]string, len(t.panes))
	for _, pv := range t.panes {
		panes[pv.Name()] = pv.Activity().String()
	}
	for _, pv := range t.panes {
		p, ok := pv.(*Pane)
		if !ok {
			continue
		}
		line := ""
		if id := p.BeadID(); id != "" {
			line = progress.PaceOf(beads, id, panes, now, progress.HeaderWindow).Line()
			if t.paceStale {
				line += paceStaleMark
			}
		}
		p.setPaceLine(line)
	}
}
