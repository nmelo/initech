package tui

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-y0n.2: a pane's top edge shows its effort's pace, computed once a
// minute from the bead poll; a frame does no bd or git work.

// paceFixture is the ini-wra effort: two of four steps closed, one of them
// inside the six-hour window; p03's bead is quiet two hours.
func paceFixture(now time.Time) []bdBead {
	return []bdBead{
		{ID: "ini-wra", Status: "open"},
		{ID: "ini-wra.1", Status: "closed", ClosedAt: now.Add(-2 * time.Hour)},
		{ID: "ini-wra.2", Status: "in_progress", Assignee: "p01", UpdatedAt: now.Add(-10 * time.Minute)},
		{ID: "ini-wra.3", Status: "closed", ClosedAt: now.Add(-8 * time.Hour)},
		{ID: "ini-wra.4", Status: "in_progress", Assignee: "p03", UpdatedAt: now.Add(-2 * time.Hour)},
	}
}

func paceTopEdge(s tcell.SimulationScreen, p *Pane) string {
	return ysr3Row(s, p.region, p.region.Y)
}

// AC1: a pane on ini-wra.2 shows the effort's line on its top edge; p03, idle
// and quiet two hours on ini-wra.4, is the one stuck bead.
func TestPaceHeader_APaneShowsItsEffort(t *testing.T) {
	tui, s := ysr3Parked(t)
	now := time.Now()
	p := ysr2Pane(tui, "p01")
	p.SetBead("ini-wra.2", "")
	ysr2Pane(tui, "p03").SetBead("ini-wra.4", "")
	tui.notePaceAnswer(paceFixture(now), true)
	tui.refreshPaceLines(now)
	tui.render()
	if got := paceTopEdge(s, p); !strings.HasPrefix(got, "─ ini-wra 2/4 · 0.2/h · 1 stuck ─") {
		t.Errorf("p01's top edge reads %q, want the effort line", got)
	}
	ysr2SameAsFull(t, tui, s, "an effort line")
}

// AC2: a pane with no bead shows no line: its edge stays the plain line.
func TestPaceHeader_NoBeadNoLine(t *testing.T) {
	tui, s := ysr3Parked(t)
	now := time.Now()
	ysr2Pane(tui, "p01").SetBead("ini-wra.2", "")
	tui.notePaceAnswer(paceFixture(now), true)
	tui.refreshPaceLines(now)
	tui.render()
	if bad := amxTopEdgeFlat(s, ysr2Pane(tui, "p02").region); bad != "" {
		t.Errorf("a pane with no bead has a line on its edge: %s", bad)
	}
	if l := ysr2Pane(tui, "p02").PaceLine(); l != "" {
		t.Errorf("a pane with no bead holds line %q", l)
	}
	// A bead cleared: the line goes at the next minute.
	p := ysr2Pane(tui, "p01")
	if p.PaceLine() == "" {
		t.Fatal("fixture: p01 has no line")
	}
	p.SetBead("", "")
	tui.refreshPaceLines(now.Add(time.Minute))
	if l := p.PaceLine(); l != "" {
		t.Errorf("a pane whose bead was cleared still holds %q", l)
	}
}

// AC4: the line is recomputed once a minute, not on every frame, and the
// frames of that minute make no bd call; the poll makes its own and nothing
// more.
func TestPaceHeader_OnceAMinuteAndNoBdOnAFrame(t *testing.T) {
	var calls atomic.Int32
	old := bdListBeads
	bdListBeads = func(ctx context.Context, dir string) ([]bdBead, error) {
		calls.Add(1)
		return nil, nil
	}
	t.Cleanup(func() { bdListBeads = old })

	tui, s := ysr3Parked(t)
	t0 := time.Now()
	p := ysr2Pane(tui, "p01")
	p.SetBead("ini-wra.2", "")
	tui.notePaceAnswer(paceFixture(t0), true)
	tui.refreshPaceLines(t0)
	first := p.PaceLine()

	beads := paceFixture(t0)
	beads[2].Status, beads[2].ClosedAt = "closed", t0 // ini-wra.2 closes
	tui.notePaceAnswer(beads, true)
	for f := 0; f < 60*30; f += 30 { // a minute of frames, a second at a time
		now := t0.Add(time.Duration(f/30) * time.Second)
		tui.refreshPaceLines(now)
		tui.render()
		if now.Sub(t0) < time.Minute && p.PaceLine() != first {
			t.Fatalf("the line changed %v into the minute: %q", now.Sub(t0), p.PaceLine())
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("a minute of frames and refreshes called bd %d times, want 0", n)
	}
	tui.refreshPaceLines(t0.Add(time.Minute))
	tui.render()
	if got := paceTopEdge(s, p); !strings.Contains(got, "ini-wra 3/4") {
		t.Errorf("a minute on, the edge reads %q, want the closed bead counted", got)
	}
	if !slices.Equal(bdListArgs, []string{"list", "--all", "--limit", "0", "--json"}) {
		t.Errorf("the poll asks bd for %v; the pace needs every bead, closed ones included", bdListArgs)
	}
}

// AC5: bd unreachable keeps the last numbers with the stale mark; a lone
// bead has the short form; a narrow pane is cut at its edge, the effort's
// name first.
func TestPaceHeader_NothingToShow(t *testing.T) {
	tui, _ := ysr3Parked(t)
	now := time.Now()
	p := ysr2Pane(tui, "p01")
	p.SetBead("ini-wra.2", "")
	tui.notePaceAnswer(paceFixture(now), true)
	tui.refreshPaceLines(now)
	tui.notePaceAnswer(nil, false)
	tui.refreshPaceLines(now.Add(time.Minute))
	if got := p.PaceLine(); got != "ini-wra 2/4 · 0.2/h · 1 stuck (stale)" {
		t.Errorf("bd unreachable: %q, want the last numbers marked stale", got)
	}
	tui.notePaceAnswer(append(paceFixture(now), bdBead{ID: "ini-abc", Status: "in_progress", Assignee: "p02"}), true)
	ysr2Pane(tui, "p02").SetBead("ini-abc", "")
	tui.refreshPaceLines(now.Add(2 * time.Minute))
	if got := ysr2Pane(tui, "p02").PaceLine(); got != "ini-abc 0/1 · 0/h" {
		t.Errorf("a lone bead: %q", got)
	}

	sim := tcell.NewSimulationScreen("")
	sim.Init()
	sim.SetSize(40, 5)
	r := Region{X: 0, Y: 0, W: 12, H: 5}
	drawTopEdge(&clampedScreen{Screen: sim, r: r}, r, "ini-wra 2/4 · 0.2/h · 0 stuck", 0)
	if got := ysr3Row(sim, Region{X: 0, Y: 0, W: 14}, 0); got != "─ ini-wra 2/  " {
		t.Errorf("a 12-wide edge reads %q, want the effort's name first and nothing past the edge", got)
	}

	// No poll answered yet: no line at all.
	tui2, _ := ysr3Parked(t)
	ysr2Pane(tui2, "p01").SetBead("ini-wra.2", "")
	tui2.refreshPaceLines(now)
	if got := ysr2Pane(tui2, "p01").PaceLine(); got != "" {
		t.Errorf("before any bd answer: %q, want none", got)
	}
}

// qa2's FAIL on 6e7fa80: on the leftmost pane the project badge (row 0, from
// column 0) covered the start of the line. The line now starts one cell after
// the badge, so the effort's name shows on pane 1 too; a pane off the top row
// or right of the badge is not moved.
func TestPaceHeader_TheProjectBadgeDoesNotCoverTheLine(t *testing.T) {
	tui, s := ysr3Parked(t)
	tui.projectName = "iqy2"
	now := time.Now()
	p1, p2, p5 := ysr2Pane(tui, "p01"), ysr2Pane(tui, "p02"), ysr2Pane(tui, "p05")
	for _, p := range []*Pane{p1, p2, p5} {
		p.SetBead("ini-wra.2", "")
	}
	tui.notePaceAnswer(paceFixture(now), true)
	tui.refreshPaceLines(now)
	tui.render()
	if got := paceTopEdge(s, p1); !strings.HasPrefix(got, " iqy2 ─ ini-wra 2/4 · 0.2/h · 1 stuck ─") {
		t.Errorf("pane 1's top row reads %q, want the badge then the whole line", got)
	}
	for _, p := range []*Pane{p2, p5} {
		if got := paceTopEdge(s, p); !strings.HasPrefix(got, "─ ini-wra 2/4") {
			t.Errorf("%s's edge reads %q; only the pane under the badge moves", p.name, got)
		}
	}
	ysr2SameAsFull(t, tui, s, "the line beside the badge")

	// A longer badge (a renamed project) moves the line with it.
	tui.projectName = "a-much-longer-project"
	tui.render()
	if got := paceTopEdge(s, p1); !strings.Contains(got, " a-much-longer-project ─ ini-wra 2/4") {
		t.Errorf("after a rename pane 1 reads %q", got)
	}
	ysr2SameAsFull(t, tui, s, "the line beside a longer badge")
}

// The badge's end counts the unread mark beside it, measured by the layout
// that draws them.
func TestPaceHeader_TheBadgeEndCountsTheUnreadMark(t *testing.T) {
	tui, _ := inboxTUI(t, inboxItemAt("p1", "eng1", "unread", time.Minute))
	tui.projectName = "initech"
	if got, want := tui.projectBadgeEnd(), len(" initech ")+len(" ✉ 1")-2; got != want {
		t.Errorf("badge end %d, want %d (the label and the unread mark)", got, want)
	}
	if got := headerInsetFor(Region{X: 0, Y: 0, W: 40}, 13); got != 13 {
		t.Errorf("a pane at the corner: inset %d, want 13", got)
	}
	if got := headerInsetFor(Region{X: 40, Y: 0, W: 40}, 13); got != 0 {
		t.Errorf("a pane right of the badge: inset %d, want 0", got)
	}
	if got := headerInsetFor(Region{X: 0, Y: 16, W: 40}, 13); got != 0 {
		t.Errorf("a pane below the top row: inset %d, want 0", got)
	}
}
