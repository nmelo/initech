package rignumbers

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC)

func ago(m int) time.Time { return now.Add(-time.Duration(m) * time.Minute) }

// fixture is a rig an hour into its day, counted by hand in each test below.
func fixture() Input {
	return Input{
		Now:     now,
		HasRepo: true,
		Commits: []Commit{
			{ago(5), "fix(tui): a thing (ini-aaa)"},
			{ago(30), "feat(cmd): a thing (ini-bbb)"},
			{ago(59), "docs: a sentence"},
			{ago(61), "ci: older than the hour (ini-ccc)"},
			{ago(300), "test: long ago (ini-ddd)"},
		},
		CommitsKnown: true,
		Runs: []Run{
			{"success", ago(10), ago(8)},  // 2m
			{"success", ago(20), ago(16)}, // 4m
			{"failure", ago(40), ago(34)}, // 6m
			{"cancelled", ago(45), ago(44)},
			{"", ago(2), now},             // still running
			{"failure", ago(70), ago(65)}, // outside the hour
		},
		RunsKnown: true,
		Beads: []Bead{
			{ID: "ini-aaa", Status: "qa_passed", CreatedAt: ago(200), LastMoved: ago(5)},  // on main
			{ID: "ini-eee", Status: "qa_passed", CreatedAt: ago(200), LastMoved: ago(25)}, // not on main
			{ID: "ini-ddd", Status: "qa_passed", CreatedAt: ago(400), LastMoved: ago(300)},
			{ID: "ini-fff", Status: "in_progress", CreatedAt: ago(30), LastMoved: ago(21)},
			{ID: "ini-ggg", Status: "in_progress", CreatedAt: ago(90), LastMoved: ago(19)},
			{ID: "ini-hhh", Status: "in_progress", CreatedAt: ago(90), LastMoved: ago(20)},
			{ID: "ini-iii", Status: "open", CreatedAt: ago(10), LastMoved: ago(10)},
			{ID: "ini-jjj", Status: "closed", CreatedAt: ago(500), ClosedAt: ago(15), LastMoved: ago(15)},
			{ID: "ini-kkk", Status: "closed", CreatedAt: ago(500), ClosedAt: ago(61), LastMoved: ago(61)},
		},
		BeadsKnown: true,
		Panes: map[string]string{
			"eng1": "idle", "eng2": "running", "eng3": "idle", "eng10": "idle",
			"qa1": "idle", "super": "idle", "eng4": "dead", "engine": "idle",
		},
	}
}

func intIs(t *testing.T, name string, got *int, want int) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s unknown, want %d", name, want)
	}
	if *got != want {
		t.Errorf("%s = %d, want %d", name, *got, want)
	}
}

// Each number against a hand count of the fixture.
func TestCompute_EveryNumberMatchesAHandCount(t *testing.T) {
	n := Compute(fixture())
	intIs(t, "merges/h", n.MergesPerHour, 3)       // 5, 30 and 59 minutes ago
	intIs(t, "gate green", n.GateGreen, 2)         // two successes in the hour
	intIs(t, "gate red", n.GateRed, 1)             // one failure in the hour; cancelled and running are neither
	intIs(t, "landing", n.LandingDepth, 1)         // ini-eee: qa_passed and named by no commit; ini-ddd is named long ago
	intIs(t, "filed", n.Filed, 2)                  // ini-fff and ini-iii
	intIs(t, "closed", n.Closed, 1)                // ini-jjj; ini-kkk closed 61 minutes ago
	intIs(t, "no move 20m", n.NoMove20m, 2)        // ini-fff (21m) and ini-hhh (20m); ini-ggg moved 19m ago
	intIs(t, "idle engineers", n.IdleEngineers, 3) // eng1, eng3, eng10; not qa1, super, engine or a dead eng4
	if n.GateMedianMinutes == nil || *n.GateMedianMinutes != 4 {
		t.Errorf("gate median = %v, want 4 (of 2, 4 and 6 minutes)", n.GateMedianMinutes)
	}
}

func TestLine_PrintsEveryNumber(t *testing.T) {
	got := Compute(fixture()).Line()
	want := "merges/h 3 · gate 2g/1r med 4m · landing 1 · filed +2/-1 · no move 20m 2 · idle 3"
	if got != want {
		t.Errorf("line\n got %q\nwant %q", got, want)
	}
}

// gh unreachable: the gate and merge numbers are unknown, never zero.
func TestCompute_UnknownRepoReadsPrintAsUnknown(t *testing.T) {
	in := fixture()
	in.CommitsKnown, in.RunsKnown = false, false
	n := Compute(in)
	if n.MergesPerHour != nil || n.GateRed != nil || n.GateMedianMinutes != nil || n.LandingDepth != nil {
		t.Fatalf("unknown reads gave numbers: %+v", n)
	}
	got := n.Line()
	if !strings.HasPrefix(got, "merges/h ? · gate ? · landing ? · filed +2/-1") {
		t.Errorf("line %q, want the repo numbers as ?", got)
	}
}

// A rig with no repo prints its repo numbers as -, and the bead and pane
// numbers still count.
func TestCompute_ARigWithNoRepo(t *testing.T) {
	in := fixture()
	in.HasRepo, in.Commits, in.Runs = false, nil, nil
	n := Compute(in)
	if got := n.Line(); !strings.HasPrefix(got, "merges/h - · gate - · landing - · filed +2/-1") {
		t.Errorf("line %q, want the repo numbers as -", got)
	}
	intIs(t, "idle engineers", n.IdleEngineers, 3)
}

// Feed it nothing: no reads at all is all unknown, and the line still prints.
func TestCompute_NothingRead(t *testing.T) {
	n := Compute(Input{Now: now, HasRepo: true})
	if got := n.Line(); got != "merges/h ? · gate ? · landing ? · filed ? · no move 20m ? · idle ?" {
		t.Errorf("line %q", got)
	}
}

func TestAlarm_OneLinePerCrossingNothingWhileCrossedAndReArmed(t *testing.T) {
	th := Thresholds{"gate_red_max": 1, "idle_engineers_min": 2}
	var a Alarm
	in := fixture()
	step := func(red, idle int) []string {
		in.Runs = nil
		for i := 0; i < red; i++ {
			in.Runs = append(in.Runs, Run{"failure", ago(5), ago(4)})
		}
		in.Panes = map[string]string{}
		for i := 0; i < idle; i++ {
			in.Panes["eng"+string(rune('1'+i))] = "idle"
		}
		return a.Check(Compute(in), th)
	}
	if got := step(1, 2); len(got) != 0 {
		t.Fatalf("inside both thresholds sent %q", got)
	}
	got := step(2, 2)
	if len(got) != 1 || got[0] != "rig numbers: gate red is 2, over its max of 1" {
		t.Fatalf("first crossing sent %q, want one line naming gate red", got)
	}
	if got := step(3, 2); len(got) != 0 {
		t.Fatalf("still crossed sent %q, want nothing", got)
	}
	if got := step(1, 2); len(got) != 0 {
		t.Fatalf("back inside sent %q", got)
	}
	if got := step(2, 1); len(got) != 2 {
		t.Fatalf("re-armed red plus a new idle crossing sent %q, want two lines", got)
	} else if got[1] != "rig numbers: idle engineers is 1, under its min of 2" {
		t.Errorf("idle line %q", got[1])
	}
}

// An unknown number never crosses, never re-arms and never sends.
func TestAlarm_AnUnknownNumberSendsNothing(t *testing.T) {
	th := Thresholds{"gate_red_max": 0}
	var a Alarm
	in := fixture()
	in.RunsKnown = false
	if got := a.Check(Compute(in), th); len(got) != 0 {
		t.Fatalf("unknown gate sent %q", got)
	}
	in.RunsKnown = true
	if got := a.Check(Compute(in), th); len(got) != 1 {
		t.Fatalf("known gate over 0 sent %q, want one line", got)
	}
}

// No thresholds: nothing ever sends.
func TestAlarm_NoThresholdsSendNothing(t *testing.T) {
	var a Alarm
	if got := a.Check(Compute(fixture()), nil); len(got) != 0 {
		t.Fatalf("no thresholds sent %q", got)
	}
}

func TestParseThresholdKey_NamesEveryNumberAndOnlyThose(t *testing.T) {
	for _, k := range []string{"merges_per_hour_min", "gate_red_max", "gate_median_minutes_max", "landing_depth_max", "idle_engineers_min", "no_move_20m_max", "filed_max", "closed_min", "gate_green_min"} {
		if err := ValidKey(k); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
	for _, k := range []string{"gate_red", "gate_red_above", "foo_max", ""} {
		if err := ValidKey(k); err == nil {
			t.Errorf("%q passed", k)
		}
	}
}

// TestCompute_TodaysMainAndClosedCountFromLocalMidnight (ini-9z0b): a run or
// a close before midnight is yesterday's; a run still going is no verdict;
// the latest finished run decides red.
func TestCompute_TodaysMainAndClosedCountFromLocalMidnight(t *testing.T) {
	loc := time.FixedZone("EST", -5*3600)
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, loc)
	at := func(h, m int) time.Time { return time.Date(2026, 11, 5, h, m, 0, 0, loc) }
	yesterday := time.Date(2026, 11, 4, 23, 50, 0, 0, loc)
	in := Input{Now: now, HasRepo: true, RunsKnown: true, BeadsKnown: true,
		Runs: []Run{
			{Conclusion: "success", CreatedAt: at(8, 0), UpdatedAt: at(8, 2)},
			{Conclusion: "success", CreatedAt: at(8, 30), UpdatedAt: at(8, 34)},
			{Conclusion: "failure", CreatedAt: at(9, 0), UpdatedAt: at(9, 9)},
			{Conclusion: "", CreatedAt: at(9, 55)},                                               // running
			{Conclusion: "failure", CreatedAt: yesterday, UpdatedAt: yesterday.Add(time.Minute)}, // yesterday
		},
		Beads: []Bead{
			{ID: "a", ClosedAt: at(7, 0)},
			{ID: "b", ClosedAt: yesterday},
			{ID: "c"},
		},
	}
	n := Compute(in)
	if *n.MainPass != 2 || *n.MainFail != 1 || !n.MainLatestRed {
		t.Fatalf("main today pass %d fail %d latest red %v, want 2, 1, true", *n.MainPass, *n.MainFail, n.MainLatestRed)
	}
	if *n.MainMedianMinutes != 4 {
		t.Fatalf("median %v, want 4 (2, 4, 9)", *n.MainMedianMinutes)
	}
	if *n.ClosedToday != 1 {
		t.Fatalf("closed today %d, want 1", *n.ClosedToday)
	}
	// A pass after the failure turns the main item back.
	in.Runs = append(in.Runs, Run{Conclusion: "success", CreatedAt: at(9, 30), UpdatedAt: at(9, 33)})
	if Compute(in).MainLatestRed {
		t.Fatal("the latest finished run passed and main still reads red")
	}
	// Feed it nothing: runs unread are unknown, not zero.
	in.RunsKnown = false
	if n := Compute(in); n.MainPass != nil || n.MainMedianMinutes != nil {
		t.Fatal("unread runs read as numbers")
	}
}

// TestBarLeft_WordsNumbersAndTheRedParts (ini-9z0b).
func TestBarLeft_WordsNumbersAndTheRedParts(t *testing.T) {
	i := func(v int) *int { return &v }
	f := func(v float64) *float64 { return &v }
	red := func(line string, spans []Span) string {
		r := []rune(line)
		var out []string
		for _, s := range spans {
			out = append(out, string(r[s.From:s.To]))
		}
		return strings.Join(out, "|")
	}
	n := Numbers{Repo: true, MergesPerHour: i(11), MainPass: i(10), MainFail: i(1), MainMedianMinutes: f(2), ClosedToday: i(9)}
	line, spans := n.BarLeft()
	if line != "merges/h 11 · main ✓10 ✗1 · median 2m · closed today 9" || red(line, spans) != "✗1" {
		t.Fatalf("%q red %q", line, red(line, spans))
	}
	n.MainLatestRed = true
	if line, spans = n.BarLeft(); red(line, spans) != "main ✓10 ✗1" {
		t.Fatalf("latest red: red %q", red(line, spans))
	}
	quiet := Numbers{Repo: true, MergesPerHour: i(0), MainPass: i(0), MainFail: i(0), ClosedToday: i(7)}
	if line, spans := quiet.BarLeft(); line != "merges/h 0 · main ✓0 ✗0 · median — · closed today 7" || len(spans) != 0 {
		t.Fatalf("quiet %q spans %v", line, spans)
	}
	if line, _ := (Numbers{Repo: true}).BarLeft(); line != "merges/h ? · main ? · median ? · closed today ?" {
		t.Fatalf("unread %q", line)
	}
	if line, _ := (Numbers{}).BarLeft(); line != "merges/h - · main - · median - · closed today ?" {
		t.Fatalf("no repo %q", line)
	}
}
