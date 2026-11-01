package tui

// pace_header.go puts each pane's effort pace on its top edge (ini-y0n.2),
// and one row per effort in flight in the Agents overlay (ini-y0n.3):
// "ini-wra 2/4 · 1/h · 0 stuck", from the bead poll window 1 already runs
// (one bd list every beadPollInterval, ini-wra.1) and the pane states the TUI
// already has. The line is computed once a minute on the main loop; a frame
// only reads it, so drawing does no bd or git work.

import (
	"sort"
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

// notePaceComments records, per bead, the comment count this poll saw and
// the moment it rose past the last poll's: a comment does not move bd's
// updated_at, so that moment is the bead's last move for the pace. A bead
// seen for the first time has no rise; the TUI's start floors it instead.
// Main loop only.
func (t *TUI) notePaceComments(beads []bdBead, now time.Time) {
	if t.paceComments == nil {
		t.paceComments = map[string]int{}
		t.paceCommentAt = map[string]time.Time{}
	}
	for _, b := range beads {
		if prev, seen := t.paceComments[b.ID]; seen && b.CommentCount > prev {
			t.paceCommentAt[b.ID] = now
		}
		t.paceComments[b.ID] = b.CommentCount
	}
}

// paceMoved is a bead's last move beyond updated_at as the TUI knows it: the
// newer of the poll that saw its comments rise and the TUI's start. A commit
// alone is not seen here (the TUI runs no git).
func (t *TUI) paceMoved(id string) time.Time {
	at := t.paceCommentAt[id]
	if t.paceSince.After(at) {
		return t.paceSince
	}
	return at
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
		beads[i] = progress.Bead{ID: b.ID, Title: b.Title, Status: b.Status, Assignee: b.Assignee, UpdatedAt: b.UpdatedAt, ClosedAt: b.ClosedAt, Moved: t.paceMoved(b.ID)}
	}
	panes := make(map[string]string, len(t.panes))
	for _, pv := range t.panes {
		panes[pv.Name()] = pv.Activity().String()
	}
	t.paceRows = paceEffortRows(beads, panes, now, t.paceStale)
	t.paceStuck = map[string]bool{}
	for _, b := range beads {
		if b.Assignee != "" && progress.Stuck(b, panes[b.Assignee], now) {
			t.paceStuck[b.Assignee] = true
		}
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

// inFlight are the bead states that put an effort in the Agents overlay: in
// progress, ready for QA, in QA.
var inFlight = map[string]bool{"in_progress": true, "ready_for_qa": true, "in_qa": true}

// paceEffortRows is the Agents overlay's rows: one per effort with a bead in
// flight, each the line a pane header on that effort shows (the same PaceOf
// call over the same window), with the stale mark when bd stopped answering;
// in effort id order. None when nothing is in flight.
func paceEffortRows(beads []progress.Bead, panes map[string]string, now time.Time, stale bool) []string {
	type effort struct{ label, prefix, bead string }
	seen := map[string]bool{}
	var efforts []effort
	for _, b := range beads {
		if !inFlight[b.Status] {
			continue
		}
		prefix, label := progress.Effort(b.ID)
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		efforts = append(efforts, effort{label, prefix, b.ID})
	}
	sort.Slice(efforts, func(i, j int) bool {
		if efforts[i].label != efforts[j].label {
			return efforts[i].label < efforts[j].label
		}
		return efforts[i].prefix < efforts[j].prefix
	})
	var rows []string
	for _, e := range efforts {
		line := progress.PaceOf(beads, e.bead, panes, now, progress.HeaderWindow).Line()
		if line == "" {
			continue
		}
		if stale {
			line += paceStaleMark
		}
		rows = append(rows, line)
	}
	return rows
}
