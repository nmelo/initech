package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
	"gopkg.in/yaml.v3"
)

// e4cProject is a project whose yaml says beads: {stuck_after: <value>}.
func e4cProject(t *testing.T, value string) *config.Project {
	t.Helper()
	var p config.Project
	if err := yaml.Unmarshal([]byte("beads:\n  stuck_after: "+value+"\n"), &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

// e4cBeads: p01 on a fresh bead; p03, idle, on a bead 20 minutes quiet.
func e4cBeads(now time.Time, status string) []bdBead {
	return []bdBead{
		{ID: "ini-e.1", Status: "in_progress", Assignee: "p01", UpdatedAt: now.Add(-time.Minute)},
		{ID: "ini-e.2", Status: status, Assignee: "p03", UpdatedAt: now.Add(-20 * time.Minute)},
	}
}

// AC1, AC4: the header and the panel read the rig's value: 20 minutes quiet is
// stuck at 15m, with its id on the owner's row; at the default hour it is not.
func TestPace_TheRigsValueDecidesTheHeaderAndThePanel(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		project *config.Project
		line    string
		ids     string
	}{
		{e4cProject(t, "15m"), "1 stuck", "ini-e.2"},
		{nil, "0 stuck", ""},
	} {
		tui, _ := ysr3Parked(t)
		tui.project = c.project
		ysr2Pane(tui, "p01").SetBead("ini-e.1", "")
		tui.notePaceAnswer(e4cBeads(now, "in_progress"), true)
		tui.refreshPaceLines(now)
		if line := ysr2Pane(tui, "p01").PaceLine(); !strings.Contains(line, c.line) {
			t.Errorf("header %q, want %s", line, c.line)
		}
		if got := strings.Join(tui.paceStuckIDs["p03"], " "); got != c.ids {
			t.Errorf("panel ids %q, want %q", got, c.ids)
		}
	}
}

// AC4: the row itself reads idle · stuck <id>.
func TestOverlay_AStuckRowNamesItsBead(t *testing.T) {
	tui, s := ysr3Parked(t)
	tui.project = e4cProject(t, "15m")
	tui.layoutState.Overlay = true
	now := time.Now()
	tui.notePaceAnswer(e4cBeads(now, "in_progress"), true)
	tui.refreshPaceLines(now)
	tui.render()
	var rows []string
	for _, r := range overlayScreenRows(s) {
		rows = append(rows, string(r))
	}
	if got := strings.Join(rows, "\n"); !strings.Contains(got, "idle · stuck ini-e.2") {
		t.Errorf("no row reads idle · stuck ini-e.2:\n%s", got)
	}
}

// AC3, AC4: the chain comes from the rig's status.custom, read once however
// many polls run: a status in that rig's chain is stuck, one outside is not.
func TestPace_TheChainIsReadOnceFromTheRig(t *testing.T) {
	var calls atomic.Int32
	origC, origL := bdStatusCustom, bdListBeads
	t.Cleanup(func() { bdStatusCustom, bdListBeads = origC, origL })
	bdStatusCustom = func(ctx context.Context, dir string) (string, error) {
		calls.Add(1)
		return "review,qa_passed,added_later", nil
	}
	now := time.Now()
	for status, line := range map[string]string{"review": "1 stuck", "in_qa": "0 stuck", "added_later": "0 stuck"} {
		calls.Store(0)
		bdListBeads = func(ctx context.Context, dir string) ([]bdBead, error) { return e4cBeads(now, status), nil }
		tui, _ := ysr3Parked(t)
		tui.project = e4cProject(t, "15m")
		ysr2Pane(tui, "p01").SetBead("ini-e.1", "")
		tui.pollBeadsOnce(now)
		tui.pollBeadsOnce(now)
		tui.pollBeadsOnce(now)
		tui.refreshPaceLines(now)
		if n := calls.Load(); n != 1 {
			t.Errorf("%s: status.custom read %d times over three polls, want 1", status, n)
		}
		if got := ysr2Pane(tui, "p01").PaceLine(); !strings.Contains(got, line) {
			t.Errorf("%s: header %q, want %s", status, got, line)
		}
	}
}

// Feed it nothing: bd does not answer the read. Only in_progress can read
// stuck, and the next poll asks again until it answers.
func TestPace_AnUnreadChainCountsOnlyInProgressAndIsAskedAgain(t *testing.T) {
	var calls atomic.Int32
	origC, origL := bdStatusCustom, bdListBeads
	t.Cleanup(func() { bdStatusCustom, bdListBeads = origC, origL })
	bdStatusCustom = func(ctx context.Context, dir string) (string, error) {
		calls.Add(1)
		return "", errors.New("bd down")
	}
	now := time.Now()
	bdListBeads = func(ctx context.Context, dir string) ([]bdBead, error) { return e4cBeads(now, "in_qa"), nil }
	tui, _ := ysr3Parked(t)
	tui.project = e4cProject(t, "15m")
	ysr2Pane(tui, "p01").SetBead("ini-e.1", "")
	tui.pollBeadsOnce(now)
	tui.pollBeadsOnce(now)
	tui.refreshPaceLines(now)
	if n := calls.Load(); n != 2 {
		t.Errorf("a failed read was asked %d times over two polls, want 2", n)
	}
	if got := ysr2Pane(tui, "p01").PaceLine(); !strings.Contains(got, "0 stuck") {
		t.Errorf("header %q with the chain unread, want 0 stuck for an in_qa bead", got)
	}
}

// e4cWaitingBeads: p03 idle on a stuck bead and a waiting one; p02's bead is
// fresh; a waiting bead with no holder in another effort, in which nothing else
// is in flight.
func e4cWaitingBeads(now time.Time) []bdBead {
	old := now.Add(-time.Hour)
	return []bdBead{
		{ID: "ini-e.1", Status: "in_progress", Assignee: "p02", UpdatedAt: now},
		{ID: "ini-e.2", Status: "in_progress", Assignee: "p03", UpdatedAt: old},
		{ID: "ini-e.3", Status: "waiting", Assignee: "p03", UpdatedAt: old},
		{ID: "ini-h.1", Status: "waiting", UpdatedAt: old},
		{ID: "ini-h.2", Status: "closed", ClosedAt: old},
	}
}

// AC4, ruling 6: a holderless waiting bead shows on its effort's row even with
// nothing else in flight there; the owner's row names its own waiting beads,
// calmly, and never counts them stuck.
func TestOverlay_WaitingShowsOnTheEffortRowAndTheOwnersRow(t *testing.T) {
	tui, s := ysr3Parked(t)
	tui.project = e4cProject(t, "15m")
	tui.layoutState.Overlay = true
	now := time.Now()
	tui.notePaceAnswer(e4cWaitingBeads(now), true)
	tui.refreshPaceLines(now)
	tui.render()
	var rows []string
	for _, r := range overlayScreenRows(s) {
		rows = append(rows, string(r))
	}
	got := strings.Join(rows, "\n")
	for _, want := range []string{"ini-h 1/2", "1 waiting ini-h.1", "ini-e 0/3", "1 stuck", "1 waiting ini-e.3", "idle · stuck ini-e.2 · waiting ini-e.3"} {
		if !strings.Contains(got, want) {
			t.Errorf("the overlay lacks %q:\n%s", want, got)
		}
	}
	if ids := tui.paceStuckIDs["p03"]; strings.Join(ids, " ") != "ini-e.2" {
		t.Errorf("stuck ids %v: a waiting bead counted stuck", ids)
	}
}

// Ruling 6: an idle agent whose only bead is waiting stays hidden; its bead is
// on the effort row.
func TestOverlay_AnIdleAgentWithOnlyAWaitingBeadStaysHidden(t *testing.T) {
	tui, s := ysr3Parked(t)
	tui.project = e4cProject(t, "15m")
	tui.layoutState.Overlay = true
	now := time.Now()
	tui.notePaceAnswer([]bdBead{{ID: "ini-e.3", Status: "waiting", Assignee: "p03", UpdatedAt: now.Add(-time.Hour)}}, true)
	tui.refreshPaceLines(now)
	tui.render()
	rows := overlayScreenRows(s)
	if _, _, listed := overlayDotOf(rows, "p03"); listed {
		t.Error("an idle agent holding only a waiting bead is listed")
	}
	var text []string
	for _, r := range rows {
		text = append(text, string(r))
	}
	if got := strings.Join(text, "\n"); !strings.Contains(got, "1 waiting ini-e.3") {
		t.Errorf("its bead is not on the effort row:\n%s", got)
	}
}

// A long wait list names three ids and counts the rest, so it cannot widen the
// overlay across the screen.
func TestEffortRow_ALongWaitListIsCut(t *testing.T) {
	tui, _ := ysr3Parked(t)
	tui.project = e4cProject(t, "15m")
	now := time.Now()
	var beads []bdBead
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		beads = append(beads, bdBead{ID: "ini-h." + id, Status: "waiting", UpdatedAt: now.Add(-time.Hour)})
	}
	tui.notePaceAnswer(beads, true)
	tui.refreshPaceLines(now)
	if len(tui.paceRows) != 1 || !strings.HasSuffix(tui.paceRows[0], "5 waiting ini-h.a ini-h.b ini-h.c +2") {
		t.Errorf("rows %q", tui.paceRows)
	}
}
