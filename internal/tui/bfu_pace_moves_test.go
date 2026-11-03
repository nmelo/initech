package tui

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ini-bfu: the header and the overlay read a comment as a move: the poll
// that sees a bead's comment_count rise is its last move. No new bd call and
// no git call per tick.

// AC2: p03's bead (ini-wra.4) is quiet two hours by updated_at and p03 is
// idle. Two polls with its comment count rising between them: stuck 0 after
// the rise, in the header and the overlay's stuck owners.
func TestPace_ACommentRiseBetweenPollsIsAMove(t *testing.T) {
	tui, _ := ysr3Parked(t)
	now := time.Now()
	ysr2Pane(tui, "p01").SetBead("ini-wra.2", "")
	ysr2Pane(tui, "p03").SetBead("ini-wra.4", "")
	first := paceFixture(now)
	first[4].CommentCount = 3
	tui.notePaceComments(first, now.Add(-time.Minute))
	tui.notePaceAnswer(first, true)
	second := paceFixture(now)
	second[4].CommentCount = 4
	tui.notePaceComments(second, now)
	tui.notePaceAnswer(second, true)
	tui.refreshPaceLines(now)
	if line := ysr2Pane(tui, "p01").PaceLine(); !strings.Contains(line, "0 stuck") {
		t.Errorf("after the comment rise the header reads %q, want 0 stuck", line)
	}
	if tui.paceStuck["p03"] {
		t.Error("p03 is marked stuck in the overlay after its bead's comment count rose")
	}
	// Positive twin: no rise, the same bead reads stuck 1.
	tui2, _ := ysr3Parked(t)
	ysr2Pane(tui2, "p01").SetBead("ini-wra.2", "")
	tui2.notePaceComments(first, now.Add(-time.Minute))
	tui2.notePaceComments(first, now)
	tui2.notePaceAnswer(first, true)
	tui2.refreshPaceLines(now)
	if line := ysr2Pane(tui2, "p01").PaceLine(); !strings.Contains(line, "1 stuck") {
		t.Errorf("with no rise the header reads %q, want 1 stuck", line)
	}
}

// AC4: one poll is exactly one bd call, it carries comment_count, and the
// pace refresh after it runs no bd at all.
func TestPace_OnePollIsOneBdCallAndCarriesCommentCount(t *testing.T) {
	var b bdBead
	if err := json.Unmarshal([]byte(`{"id":"ini-x.1","status":"in_progress","comment_count":7}`), &b); err != nil || b.CommentCount != 7 {
		t.Fatalf("comment_count not read: %+v %v", b, err)
	}
	tui, _ := ysr3Parked(t)
	var calls atomic.Int32
	orig := bdListBeads
	bdListBeads = func(ctx context.Context, dir string) ([]bdBead, error) {
		calls.Add(1)
		return paceFixture(time.Now()), nil
	}
	t.Cleanup(func() { bdListBeads = orig })
	tui.pollBeadsOnce(time.Now())
	tui.refreshPaceLines(time.Now())
	if n := calls.Load(); n != 1 {
		t.Errorf("one poll and a refresh made %d bd calls, want 1", n)
	}
}
