package tui

// pace_header.go puts each pane's effort pace on its top edge (ini-y0n.2),
// and one row per effort in flight in the Agents overlay (ini-y0n.3):
// "ini-wra 2/4 · 1/h · 0 stuck", from the bead poll window 1 already runs
// (one bd list every beadPollInterval, ini-wra.1) and the pane states the TUI
// already has. The line is computed once a minute on the main loop; a frame
// only reads it, so drawing does no bd or git work.

import (
	"fmt"
	"sort"
	"time"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/lifecycle"
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
// seen for the first time has no rise; its crossing read finds its true move.
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
// newer of the poll that saw its comments rise and its crossing read. A bead
// whose read has not landed is held at now, so it is not counted before the
// read can say whether it moved.
func (t *TUI) paceMoved(id string, now time.Time) time.Time {
	if t.paceUnread[id] {
		return now
	}
	at := t.paceCommentAt[id]
	if r := t.paceReads[id]; r.at.After(at) {
		return r.at
	}
	return at
}

// paceRule is what this rig counts stuck by: its yaml value, over the working
// chain read from bd once when the poller started. Until that read answers,
// only in_progress can read stuck.
func (t *TUI) paceRule() progress.Rule {
	after := config.DefaultStuckAfter
	if t.project != nil {
		after = t.project.Beads.Value()
	}
	var rule progress.Rule
	if t.paceChain == nil {
		rule = progress.NewRule(after, lifecycle.Parse("").Working())
	} else {
		rule = progress.NewRule(after, t.paceChain)
	}
	rule.IdleAfter = config.DefaultIdleAfter
	if t.project != nil {
		rule.IdleAfter = t.project.Beads.IdleValue()
	}
	return rule
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
	spells := t.paneIdleSpells()
	t.paceIdleFor = make(map[string]time.Duration, len(spells))
	for name, at := range spells {
		t.paceIdleFor[name] = now.Sub(at)
	}
	beads := make([]progress.Bead, len(t.paceBeads))
	for i, b := range t.paceBeads {
		beads[i] = t.paceBead(b, now)
	}
	panes := t.panePaceStates()
	rule := t.paceRule()
	t.paceRows = paceEffortRows(beads, panes, rule, now, t.paceStale)
	t.paceTitles = effortTitles(t.paceBeads)
	t.paceStuck = map[string]bool{}
	t.paceStuckIDs = map[string][]string{}
	t.paceWaitingIDs = map[string][]string{}
	for _, b := range beads {
		if b.Assignee != "" && progress.Stuck(b, panes[b.Assignee], rule, now) {
			t.paceStuck[b.Assignee] = true
			t.paceStuckIDs[b.Assignee] = append(t.paceStuckIDs[b.Assignee], b.ID)
		}
		if b.Assignee != "" && progress.Waiting(b, panes[b.Assignee], rule, now) {
			t.paceWaitingIDs[b.Assignee] = append(t.paceWaitingIDs[b.Assignee], b.ID)
		}
	}
	t.notePaceIdle(beads, panes, rule, spells)
	for _, pv := range t.panes {
		p, ok := pv.(*Pane)
		if !ok {
			continue
		}
		line := ""
		if id := p.BeadID(); id != "" {
			line = progress.PaceOf(beads, id, panes, rule, now, progress.HeaderWindow).Line()
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

// paceWaitingShown is how many waiting ids an effort row names; the rest read
// as a count, so a long wait list cannot widen the overlay across the screen.
// initech progress names them all.
const paceWaitingShown = 3

// paceEffortRows is the Agents overlay's rows: one per effort with a bead in
// flight or a bead the waiting count counts (a bead with no holder has no pane
// row, so its effort's row is where it shows), each the line a pane header on
// that effort shows (the same PaceOf call over the same window), after the
// state icon of the effort's own bead (ini-z9t), then the waiting ids, with
// the stale mark when bd stopped answering; in effort id order. None when
// nothing is in flight or waiting.
func paceEffortRows(beads []progress.Bead, panes map[string]string, rule progress.Rule, now time.Time, stale bool) []string {
	type effort struct{ label, prefix, bead string }
	seen := map[string]bool{}
	var efforts []effort
	for _, b := range beads {
		if !inFlight[b.Status] && !progress.Waiting(b, panes[b.Assignee], rule, now) {
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
	status := map[string]string{}
	byID := map[string]progress.Bead{}
	for _, b := range beads {
		status[b.ID] = b.Status
		byID[b.ID] = b
	}
	var rows []string
	for _, e := range efforts {
		pace := progress.PaceOf(beads, e.bead, panes, rule, now, progress.HeaderWindow)
		line := pace.Line()
		if line == "" {
			continue
		}
		// The state icon of the effort's own bead, the one its label names,
		// left of the label (ini-z9t); a label with no bead of that id, or a
		// status outside the table, gets none.
		if icon, ok := beadStateIcon(status[e.label]); ok {
			line = string(icon) + " " + line
		}
		for i, id := range pace.WaitingIDs {
			if i == paceWaitingShown {
				line += fmt.Sprintf(" +%d", len(pace.WaitingIDs)-i)
				break
			}
			line += " " + id
		}
		// The idle owners, each with how long (ini-9dv), after the waiting ids.
		for i, id := range pace.IdleIDs {
			if i == paceWaitingShown {
				line += fmt.Sprintf(" +%d", len(pace.IdleIDs)-i)
				break
			}
			if i == 0 {
				line += " · idle"
			}
			b := byID[id]
			line += " " + b.Assignee + " " + progress.Minutes(b.OwnerIdle)
		}
		if stale {
			line += paceStaleMark
		}
		rows = append(rows, line)
	}
	return rows
}
