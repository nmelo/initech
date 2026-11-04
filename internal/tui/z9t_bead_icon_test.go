package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/progress"
)

// ini-z9t: one character before the bead id in the pane ribbon says the
// bead's bd status, from the poll the TUI already runs; the same icon goes
// left of each effort row's id in the Agents overlay.

// z9tTable is Nelson's table, written out here so a changed glyph in the code
// is a red test, not a silent change.
var z9tTable = map[string]rune{
	"open": '○', "in_progress": '▶', "ready_for_qa": '◎', "in_qa": '◎',
	"qa_passed": '◆', "ready_to_ship": '◆', "waiting": '☾', "blocked": '■',
	"waiting_on_nelson": '?', "deferred": '❄', "closed": '✓',
}

// z9tRibbon renders the parked fleet with p01 on ini-z9t; when status is not
// "", one poll answers with the bead at that status. It returns p01's ribbon.
func z9tRibbon(t *testing.T, status string) string {
	t.Helper()
	tui, s := ysr3Parked(t)
	p := ysr2Pane(tui, "p01")
	p.SetBead("ini-z9t", "")
	if status != "" {
		p.applyDerivedBeads([]bdBead{{ID: "ini-z9t", Status: status, Assignee: "p01"}, {ID: "ini-zzz", Status: "closed"}}, true, time.Now())
	}
	tui.render()
	return sf52Ribbon(s, p)
}

// AC1, AC2: each status in the table draws its icon in the bar's cell before
// the id; the rest of the row is byte for byte the row with the bar.
func TestBeadIcon_EachStatusDrawsItsIconWhereTheBarWas(t *testing.T) {
	bar := z9tRibbon(t, "")
	at := strings.Index(bar, "| ini-z9t")
	if at < 0 {
		t.Fatalf("fixture: an unread status reads %q, want the bar before the id", bar)
	}
	for status, icon := range z9tTable {
		got := z9tRibbon(t, status)
		want := strings.Replace(bar, "| ini-z9t", string(icon)+" ini-z9t", 1)
		if got != want {
			t.Errorf("%s: the ribbon reads %q, want %q", status, got, want)
		}
	}
}

// AC1: a status outside the table and an unread one keep the bar; a pane
// with no bead draws no bar and no icon.
func TestBeadIcon_AnUnknownOrUnreadStatusKeepsTheBar(t *testing.T) {
	bar := z9tRibbon(t, "")
	if got := z9tRibbon(t, "pinned"); got != bar {
		t.Errorf("a status outside the table reads %q, want the bar row %q", got, bar)
	}
	tui, s := ysr3Parked(t)
	p := ysr2Pane(tui, "p01")
	p.applyDerivedBeads([]bdBead{{ID: "ini-z9t", Status: "open"}}, true, time.Now())
	tui.render()
	row := sf52Ribbon(s, p)
	if strings.ContainsAny(row, "|○▶◎◆☾■?❄✓") {
		t.Errorf("a pane with no bead reads %q, want no bar and no icon", row)
	}
}

// AC2, AC4: every icon and every fallback is one cell by the TUI's own width
// function, the table is the one the bead settled, and a row drawn with a
// fallback in the icon's place keeps every other cell where it was.
func TestBeadIcon_EveryIconAndFallbackIsOneCell(t *testing.T) {
	if len(beadStateIcons) != len(z9tTable) {
		t.Errorf("the table has %d statuses, want %d", len(beadStateIcons), len(z9tTable))
	}
	for status, want := range z9tTable {
		if got, ok := beadStateIcon(status); !ok || got != want {
			t.Errorf("%s: icon %q (%v), want %q", status, got, ok, want)
		}
	}
	for _, r := range append([]rune{beadBar}, beadStateFallbacks...) {
		if w := displayWidth(string(r)); w != 1 {
			t.Errorf("%q is %d cells, want 1", r, w)
		}
	}
	for _, r := range z9tTable {
		if w := displayWidth(string(r)); w != 1 {
			t.Errorf("%q is %d cells, want 1", r, w)
		}
	}
	row := func(icon rune) string {
		sim := tcell.NewSimulationScreen("")
		sim.Init()
		sim.SetSize(60, 3)
		r := Region{X: 0, Y: 0, W: 60, H: 3}
		renderRibbon(&clampedScreen{Screen: sim, r: r}, r, ribbonLook{title: " 1 eng5 ", bead: "ini-z9t", beadTitle: "icons", beadIcon: icon})
		return ysr3Row(sim, r, 2)
	}
	bar := row(0)
	for _, f := range beadStateFallbacks {
		if got, want := row(f), strings.Replace(bar, "|", string(f), 1); got != want {
			t.Errorf("with fallback %q the row reads %q, want %q", f, got, want)
		}
	}
}

// AC3: the status comes from the poll the TUI already runs, one bd list per
// poll and nothing more; a bead that moves shows its new icon at the next poll.
func TestBeadIcon_TheNextPollShowsTheNewStateWithNoNewBdCall(t *testing.T) {
	var lists, custom atomic.Int32
	origC, origL := bdStatusCustom, bdListBeads
	t.Cleanup(func() { bdStatusCustom, bdListBeads = origC, origL })
	bdStatusCustom = func(context.Context, string) (string, error) { custom.Add(1); return "", nil }
	status := "in_progress"
	bdListBeads = func(context.Context, string) ([]bdBead, error) {
		lists.Add(1)
		return []bdBead{{ID: "ini-z9t", Status: status, Assignee: "p01"}}, nil
	}
	tui, s := ysr3Parked(t)
	p := ysr2Pane(tui, "p01")
	now := time.Now()
	tui.pollBeadsOnce(now)
	tui.render()
	if row := sf52Ribbon(s, p); !strings.Contains(row, "▶ ini-z9t") {
		t.Fatalf("in progress: %q, want ▶ before the id", row)
	}
	p.SetBead("ini-z9t", "") // it stays the pane's bead once it leaves in_progress
	status = "qa_passed"
	tui.pollBeadsOnce(now.Add(10 * time.Second))
	tui.render()
	if row := sf52Ribbon(s, p); !strings.Contains(row, "◆ ini-z9t") {
		t.Errorf("after a move to qa_passed: %q, want ◆ before the id", row)
	}
	if n := lists.Load(); n != 2 {
		t.Errorf("two polls ran %d bd lists, want 2: the icon asks bd nothing", n)
	}
	if n := custom.Load(); n != 1 {
		t.Errorf("status.custom read %d times, want once", n)
	}
}

// Super's scope addition: an effort row carries its own bead's icon left of
// its id; an effort with no bead of its own id gets none.
func TestBeadIcon_AnEffortRowCarriesItsOwnBeadsIcon(t *testing.T) {
	now := time.Now()
	beads := []progress.Bead{
		{ID: "ini-abc", Status: "in_progress", Assignee: "p01", UpdatedAt: now},
		{ID: "ini-z9t", Status: "in_qa", Assignee: "qa2", UpdatedAt: now},
		{ID: "ini-noepic.1", Status: "in_progress", Assignee: "p02", UpdatedAt: now},
		{ID: "ini-noepic.2", Status: "closed", ClosedAt: now},
	}
	panes := map[string]string{"p01": "running", "p02": "running", "qa2": "running"}
	rows := paceEffortRows(beads, panes, progress.NewRule(time.Hour, nil), now, false)
	want := []string{"▶ ini-abc 0/1 · 0/h", "ini-noepic 1/2 · 0.2/h · 0 stuck", "◎ ini-z9t 0/1 · 0/h"}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Errorf("rows %q, want %q", rows, want)
	}
}
