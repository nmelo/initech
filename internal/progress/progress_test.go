package progress

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 31, 22, 0, 0, 0, time.UTC)

// AC2: AC n/m counts "AC <n> proven" comments against the bullets under the
// heading; no proven comments reads 0/m; no heading reads "-".
func TestACCell_ProvenAgainstTheBulletsUnderTheHeading(t *testing.T) {
	desc := "Story.\n\n### Acceptance Criteria\n- AC1 one\n- AC2 two\n- AC3 three\n\nDocs: the guide\n- not an item"
	cs := []Comment{{Text: "AC 1 proven: abc"}, {Text: "ac2 PROVEN"}, {Text: "AC2 proven twice"}, {Text: "see AC3 proven later"}}
	if got := ACCell(Bead{Description: desc}, cs); got != "2/3" {
		t.Errorf("AC cell %q, want 2/3 (AC1 and AC2; a mention mid-sentence is not a proof)", got)
	}
	if got := ACCell(Bead{Description: desc}, nil); got != "0/3" {
		t.Errorf("no proofs: %q, want 0/3", got)
	}
	if got := ACCell(Bead{Description: "no criteria"}, cs); got != "-" {
		t.Errorf("no heading: %q, want -", got)
	}
}

// This rig writes the heading as a plain "Acceptance Criteria:" line; the
// words quoted inside a sentence are not the heading.
func TestACItems_PlainHeadingAndAQuotedOne(t *testing.T) {
	desc := "What changes.\n\nAcceptance Criteria:\n- AC1 one\n- AC2 counts against the bullets under \"### Acceptance Criteria\"; and so on\n- AC3 three\n\nNot sensitive. Straight to main.\n\nHow to verify: run it.\n- a step, not an item\n"
	n, ok := ACItems(desc)
	if !ok || n != 3 {
		t.Errorf("plain heading with a quoted one inside AC2: %d items ok=%v, want 3", n, ok)
	}
	if _, ok := ACItems(`counts against the bullets under "### Acceptance Criteria" here`); ok {
		t.Error("the heading quoted inside a sentence was taken as the heading")
	}
	if n, ok := ACItems("### Acceptance Criteria\n1. first\n2) second\n* third\n"); !ok || n != 3 {
		t.Errorf("numbered and starred bullets: %d ok=%v, want 3", n, ok)
	}
}

// A commit names a bead only as a whole word.
func TestNamesBead_WholeWordOnly(t *testing.T) {
	for subj, want := range map[string]bool{
		"feat: thing (fx-1)":   true,
		"fix: fx-1 follow-up":  true,
		"fx-1: at the start":   true,
		"see fx-1.":            true,
		"test: fx-10 settles":  false,
		"not fx-1x":            false,
		"xfx-1 glued on":       false,
		"ini-fx-1 a longer id": false,
	} {
		if got := NamesBead(subj, "fx-1"); got != want {
			t.Errorf("NamesBead(%q, fx-1) = %v, want %v", subj, got, want)
		}
	}
}

// AC3: the last move is the newest of the status change, a comment and a
// commit, with its author; a comment newer than updated_at wins; a tie keeps
// the status.
func TestLastMove_NewestWinsWithItsAuthor(t *testing.T) {
	b := Bead{ID: "fx-1", UpdatedAt: now.Add(-3 * time.Hour)}
	cs := []Comment{{Author: "eng1", CreatedAt: now.Add(-2 * time.Hour)}}
	if at, by := LastMove(b, cs, nil); by != "eng1" || !at.Equal(now.Add(-2*time.Hour)) {
		t.Errorf("comment newer than updated_at: %v by %q, want the comment by eng1", at, by)
	}
	commits := []Commit{{When: now.Add(-time.Hour), Subject: "fix (fx-1)"}, {When: now, Subject: "other fx-10"}}
	if _, by := LastMove(b, cs, commits); by != "commit" {
		t.Errorf("a newer commit naming the bead: by %q, want commit", by)
	}
	tie := []Comment{{Author: "eng1", CreatedAt: b.UpdatedAt}}
	if _, by := LastMove(b, tie, nil); by != "status" {
		t.Errorf("a tie: by %q, want status (the earlier kind)", by)
	}
	if _, by := LastMove(b, []Comment{{CreatedAt: now}}, nil); by != "note" {
		t.Errorf("a comment with no author: by %q, want note", by)
	}
}

// AC4 and AC7: a busy owner reads "working, unrecorded"; else the thing the
// last comment names; else stalled. Unread panes never read as busy.
func TestWait_WorkingUnrecordedNamedOrStalled(t *testing.T) {
	b := Bead{ID: "fx-3", Assignee: "eng2"}
	named := []Comment{
		{Text: "waiting on QA", CreatedAt: now.Add(-3 * time.Hour)},
		{Text: "Held for super's word to merge; back soon", CreatedAt: now.Add(-2 * time.Hour)},
	}
	busy := Input{PanesKnown: true, Panes: map[string]string{"eng2": "background"}}
	if got := Wait(b, named, busy); got != WaitWorkingUnrecorded {
		t.Errorf("owner background: %q, want %q", got, WaitWorkingUnrecorded)
	}
	idle := Input{PanesKnown: true, Panes: map[string]string{"eng2": "idle"}}
	if got := Wait(b, named, idle); got != `waiting on "super's word to merge"` {
		t.Errorf("owner idle, last comment names a thing: %q", got)
	}
	if got := Wait(b, []Comment{{Text: "PLAN: steps"}}, idle); got != WaitStalled {
		t.Errorf("nothing named: %q, want stalled", got)
	}
	unread := Input{Panes: map[string]string{"eng2": "running"}}
	if got := Wait(b, nil, unread); got != WaitStalled {
		t.Errorf("panes not read: %q, want stalled (an unread pane is never busy)", got)
	}
}

// The wait section lists only beads quiet an hour or more.
func TestRender_WaitOnlyForBeadsQuietAnHour(t *testing.T) {
	in := Input{Rig: "r", Prefix: "fx-", Hours: 6, Now: now, CommitsKnown: true, Beads: []Bead{
		{ID: "fx-1", Status: "in_progress", Assignee: "eng1", UpdatedAt: now.Add(-59 * time.Minute)},
		{ID: "fx-2", Status: "in_progress", Assignee: "eng2", UpdatedAt: now.Add(-61 * time.Minute)},
	}}
	out := Render(in)
	if !strings.Contains(out, "  fx-2 eng2: stalled") || strings.Contains(out, "  fx-1 eng1:") {
		t.Errorf("want a wait line for fx-2 (61 min) only, not fx-1 (59 min):\n%s", out)
	}
	in.Beads = in.Beads[:1]
	if out := Render(in); !strings.Contains(out, "quiet an hour or more: none") {
		t.Errorf("no quiet bead: want the none line:\n%s", out)
	}
}

// Times print in ET with daylight time: 22:00Z is 18:00 in October and
// 17:00 after daylight time ends.
func TestRender_ETFollowsDaylightTime(t *testing.T) {
	if got := hhmm(now); got != "Sat 18:00" {
		t.Errorf("October: %q, want Sat 18:00", got)
	}
	if got := hhmm(time.Date(2026, 11, 2, 22, 0, 0, 0, time.UTC)); got != "Mon 17:00" {
		t.Errorf("November: %q, want Mon 17:00 (EST)", got)
	}
}
