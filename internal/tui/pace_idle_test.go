package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/progress"
)

// idleRig: super and eng1 with a screen, bd answering with beads, no crossing
// reads, the line to super recorded. stuck_after is 15m; idle_after is the
// default 20m.
func idleRig(t *testing.T, beads *[]bdBead) (*TUI, tcell.SimulationScreen, *lineRecorder) {
	t.Helper()
	origL, origC, origB, origH := bdListBeads, bdStatusCustom, bdBeadComments, projectHasBeads
	t.Cleanup(func() { bdListBeads, bdStatusCustom, bdBeadComments, projectHasBeads = origL, origC, origB, origH })
	bdListBeads = func(ctx context.Context, dir string) ([]bdBead, error) { return *beads, nil }
	bdStatusCustom = func(ctx context.Context, dir string) (string, error) {
		return "ready_for_qa,in_qa,qa_passed,waiting", nil
	}
	bdBeadComments = func(ctx context.Context, dir, id string) ([]progress.Comment, error) { return nil, nil }
	projectHasBeads = func(string) bool { return false }
	rec := recordInitechLines(t)
	tui, s := newTestTUIWithScreen("super", "eng1")
	tui.project = e4cProject(t, "15m")
	tui.layoutState.Overlay = true
	return tui, s, rec
}

// paneNamed is the local pane called name.
func paneNamed(t *testing.T, tui *TUI, name string) *Pane {
	t.Helper()
	for _, pv := range tui.panes {
		if p, ok := pv.(*Pane); ok && p.name == name {
			return p
		}
	}
	t.Fatalf("no pane %s", name)
	return nil
}

// goIdle puts the pane idle with its spell begun at since.
func goIdle(p *Pane, since time.Time) {
	p.mu.Lock()
	p.activity, p.activitySince = StateIdle, since
	p.mu.Unlock()
}

func idleRefresh(tui *TUI, now time.Time) {
	tui.pollBeadsOnce(now)
	tui.paceAt = time.Time{}
	tui.refreshPaceLines(now)
}

// ini-9dv AC1 and AC2: a bead that moved a minute ago, its owner idle 21m:
// the effort row counts and names it, the pane row says how long, it is not
// stuck, and super hears one line. A minute later, still the same spell:
// nothing more.
func TestIdleOwner_TheBoardShowsItAndSuperHearsOncePerSpell(t *testing.T) {
	now := time.Now()
	beads := []bdBead{{ID: "ini-i.1", Status: "in_progress", Assignee: "eng1", UpdatedAt: now.Add(-time.Minute)}}
	tui, s, rec := idleRig(t, &beads)
	eng1 := paneNamed(t, tui, "eng1")
	goIdle(eng1, now.Add(-21*time.Minute))
	idleRefresh(tui, now)
	if len(tui.paceRows) != 1 || !strings.Contains(tui.paceRows[0], "· 1 idle · idle eng1 21m") {
		t.Fatalf("effort rows %q, want the idle count and eng1 21m", tui.paceRows)
	}
	if tui.paceStuck["eng1"] {
		t.Error("a bead that moved a minute ago reads stuck")
	}
	tui.render()
	if got := simScreenText(s); !strings.Contains(got, "idle 21m · ▶ ini-i.1") {
		t.Errorf("the pane row lacks %q:\n%s", "idle 21m · ▶ ini-i.1", got)
	}
	got := rec.waitFor(1)
	want := "super|initech|[from initech] eng1 idle 21m on ▶ ini-i.1; check on it"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("lines %q, want %q", got, want)
	}
	for i := 1; i <= 5; i++ {
		goIdle(eng1, now.Add(-21*time.Minute)) // the render's detector leaves it idle; restate it
		idleRefresh(tui, now.Add(time.Duration(i)*time.Minute))
	}
	time.Sleep(50 * time.Millisecond)
	if got := rec.all(); len(got) != 1 {
		t.Errorf("five more minutes of the same spell sent %q, want nothing more", got[1:])
	}
}

// AC2: active then idle again is a new spell, and a new line; idle again for
// under 20m sends nothing yet.
func TestIdleOwner_ActiveThenIdleAgainSendsAgain(t *testing.T) {
	now := time.Now()
	beads := []bdBead{{ID: "ini-i.2", Status: "in_progress", Assignee: "eng1", UpdatedAt: now}}
	tui, _, rec := idleRig(t, &beads)
	eng1 := paneNamed(t, tui, "eng1")
	goIdle(eng1, now.Add(-20*time.Minute))
	idleRefresh(tui, now)
	rec.waitFor(1)
	setActivity(tui, "eng1", StateRunning)
	idleRefresh(tui, now.Add(time.Minute))
	if len(tui.paceIdle["eng1"]) != 0 {
		t.Fatalf("a running owner still reads idle: %+v", tui.paceIdle)
	}
	goIdle(eng1, now.Add(2*time.Minute))
	idleRefresh(tui, now.Add(10*time.Minute))
	time.Sleep(50 * time.Millisecond)
	if got := rec.all(); len(got) != 1 {
		t.Fatalf("idle 8m sent %q", got)
	}
	idleRefresh(tui, now.Add(23*time.Minute))
	got := rec.waitFor(2)
	if len(got) != 2 || got[1] != "super|initech|[from initech] eng1 idle 21m on ▶ ini-i.2; check on it" {
		t.Errorf("lines %q, want a second line for the second spell", got)
	}
}

// AC1: a bead quiet past stuck_after with its owner idle past idle_after is
// listed under both, on the pane row and the effort row.
func TestIdleOwner_StuckAndIdleReadsUnderBoth(t *testing.T) {
	now := time.Now()
	beads := []bdBead{{ID: "ini-i.3", Status: "in_progress", Assignee: "eng1", UpdatedAt: now.Add(-3 * time.Hour)}}
	tui, s, rec := idleRig(t, &beads)
	goIdle(paneNamed(t, tui, "eng1"), now.Add(-90*time.Minute))
	idleRefresh(tui, now)
	if !tui.paceStuck["eng1"] || len(tui.paceIdle["eng1"]) != 1 {
		t.Fatalf("stuck %v idle %+v, want both", tui.paceStuck, tui.paceIdle)
	}
	// A one-bead effort prints no stuck count (Line); the pane row says it.
	if !strings.Contains(tui.paceRows[0], "· 1 idle · idle eng1 90m") {
		t.Errorf("effort row %q", tui.paceRows[0])
	}
	tui.render()
	if got := simScreenText(s); !strings.Contains(got, "idle 90m · ▶ ini-i.3 · stuck ini-i.3") {
		t.Errorf("the pane row does not read both:\n%s", got)
	}
	rec.waitFor(1)
}

// AC1: a waiting bead never reads idle-owner, however long its owner idles.
func TestIdleOwner_AWaitingBeadNever(t *testing.T) {
	now := time.Now()
	beads := []bdBead{{ID: "ini-i.4", Status: "waiting", Assignee: "eng1", UpdatedAt: now.Add(-3 * time.Hour)}}
	tui, _, rec := idleRig(t, &beads)
	goIdle(paneNamed(t, tui, "eng1"), now.Add(-3*time.Hour))
	idleRefresh(tui, now)
	time.Sleep(50 * time.Millisecond)
	if len(tui.paceIdle) != 0 || len(rec.all()) != 0 {
		t.Errorf("a waiting bead read idle-owner: %+v, lines %q", tui.paceIdle, rec.all())
	}
	if len(tui.paceRows) == 1 && strings.Contains(tui.paceRows[0], "idle") {
		t.Errorf("effort row %q", tui.paceRows[0])
	}
}

// AC4, feed it nothing: an owner with no pane here, a bead with no owner, and
// an owner on another machine (a pane this initech does not run) read
// nothing and send nothing. Positive twin: the local eng1 does.
func TestIdleOwner_NoPaneNoOwnerOrARemoteOwnerReadNothing(t *testing.T) {
	now := time.Now()
	beads := []bdBead{
		{ID: "ini-i.5", Status: "in_progress", Assignee: "eng9", UpdatedAt: now},
		{ID: "ini-i.6", Status: "in_progress", UpdatedAt: now},
		{ID: "ini-i.7", Status: "in_progress", Assignee: "far1", UpdatedAt: now},
	}
	tui, _, rec := idleRig(t, &beads)
	tui.panes = append(tui.panes, &mockPaneView{name: "far1", host: "box2", alive: true, activity: StateIdle})
	idleRefresh(tui, now)
	time.Sleep(50 * time.Millisecond)
	if len(tui.paceIdle) != 0 || len(rec.all()) != 0 {
		t.Fatalf("read idle-owner from nothing: %+v, lines %q", tui.paceIdle, rec.all())
	}
	beads = append(beads, bdBead{ID: "ini-i.8", Status: "in_progress", Assignee: "eng1", UpdatedAt: now})
	goIdle(paneNamed(t, tui, "eng1"), now.Add(-30*time.Minute))
	idleRefresh(tui, now.Add(time.Minute))
	if got := rec.waitFor(1); len(got) != 1 || !strings.Contains(got[0], "eng1 idle 31m on ▶ ini-i.8") {
		t.Errorf("the twin: lines %q", got)
	}
}

// The pane clock: an activity change stamps the spell; staying idle keeps it;
// running clears it; a pane before its first change has none.
func TestIdleSpell_AChangeStampsItStayingIdleKeepsIt(t *testing.T) {
	p := testPane("eng1")
	if _, ok := p.IdleSpell(); ok {
		t.Fatal("a pane before its first change has an idle spell")
	}
	at := time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC)
	orig := paneClock
	t.Cleanup(func() { paneClock = orig })
	paneClock = func() time.Time { return at }
	p.lastOutputTime = time.Now().Add(-time.Hour)
	p.updateActivity()
	if got, ok := p.IdleSpell(); !ok || !got.Equal(at) {
		t.Fatalf("idle spell %v %v, want %v", got, ok, at)
	}
	paneClock = func() time.Time { return at.Add(time.Hour) }
	p.updateActivity()
	if got, _ := p.IdleSpell(); !got.Equal(at) {
		t.Errorf("staying idle moved the spell to %v", got)
	}
	p.lastOutputTime = time.Now()
	p.updateActivity()
	if _, ok := p.IdleSpell(); ok {
		t.Error("a running pane has an idle spell")
	}
}

// ini-9dv, pm's IPC cells: the list answer carries idle_sec for a local pane
// while it is idle, and omits it (never 0) for a busy pane and for a pane this
// process does not run.
func TestIdleSec_TheListAnswerSendsItOnlyForALocalIdlePane(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"))
	tui.panes = append(tui.panes, &mockPaneView{name: "far1", host: "box2", alive: true, activity: StateIdle})
	now := time.Now()
	goIdle(paneNamed(t, tui, "eng1"), now.Add(-90*time.Second))
	setActivity(tui, "eng2", StateRunning)
	panes, ok := tui.AllPanes()
	if !ok {
		t.Fatal("no list")
	}
	got := map[string]*int{}
	for _, pi := range panes {
		got[pi.Name] = pi.IdleSec
	}
	if s := got["eng1"]; s == nil || *s < 90 || *s > 95 {
		t.Errorf("eng1 idle_sec %v, want about 90", s)
	}
	if got["eng2"] != nil || got["far1"] != nil {
		t.Errorf("idle_sec sent for a busy pane (%v) or a remote one (%v)", got["eng2"], got["far1"])
	}
	b, _ := json.Marshal(PaneInfo{Name: "x"})
	if strings.Contains(string(b), "idle_sec") {
		t.Errorf("an unknown idle time went on the wire: %s", b)
	}
}
