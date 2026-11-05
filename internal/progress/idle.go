package progress

import (
	"fmt"
	"time"
)

// idleOwnerStates are the owner pane states that count as quiet for the
// idle-owner reading: idle, or idle with text left in the box. A running pane
// is working; a background one waits on its own work; waiting is on a dialog
// (the needs-input list names it); suspended and dead are not quiet panes but
// stopped ones.
var idleOwnerStates = map[string]bool{"idle": true, "pending": true}

// IdleOwner is the one definition of the idle-owner reading: an in_progress
// bead with an owner whose pane (ownerState, by name) is idle or pending and
// has been for rule.IdleAfter or more (b.OwnerIdle, set by the caller from the
// pane). The bead's own moves do not enter it: it is the pane that is quiet.
// It is independent of Stuck, so a bead can read both; a waiting bead never
// reads it. A zero OwnerIdle (not known, or not idle) or a zero IdleAfter
// reads nothing.
func IdleOwner(b Bead, ownerState string, rule Rule) bool {
	if b.Status != "in_progress" || b.Assignee == "" || rule.IdleAfter <= 0 {
		return false
	}
	return idleOwnerStates[ownerState] && b.OwnerIdle >= rule.IdleAfter
}

// IdleRow is one idle-owner bead in the report: the bead, its owner and how
// long the owner's pane has been idle, in whole minutes.
type IdleRow struct {
	ID      string `json:"id"`
	Owner   string `json:"owner"`
	IdleMin int    `json:"idle_min"`
}

// Minutes is a duration as the idle-owner surfaces print it: "23m", whole
// minutes, rounded down.
func Minutes(d time.Duration) string {
	return fmt.Sprintf("%dm", int(d/time.Minute))
}
