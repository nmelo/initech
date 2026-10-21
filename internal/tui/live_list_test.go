package tui

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"gopkg.in/yaml.v3"
)

// ini-3g39: the live list split, Option+Shift+L. The held agent left; on the
// right a fixed 4 x rows grid of the working agents in the Agents panel's
// order, empty boxes drawn "waiting".

func altShiftL(tui *TUI) {
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'L', tcell.ModAlt|tcell.ModShift))
}

var listFleetNames = []string{"super", "pm", "eng1", "eng2", "eng3", "qa1", "qa2"}

// listFleet is a fleet whose pane order (super, pm, eng1...) differs from
// its Agents panel order (bands eng, qa, core: eng1, eng2, eng3, qa1, qa2,
// super, pm), so a test can tell which order filled the boxes. Every pane is
// working; super is held. The layout underneath is grid 4x2, explicit.
func listFleet(t *testing.T, names ...string) *TUI {
	t.Helper()
	if len(names) == 0 {
		names = listFleetNames
	}
	panes := make([]*Pane, len(names))
	for i, n := range names {
		panes[i] = testPane(n)
	}
	tui := newTestTUI(panes...)
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 4, 2
	tui.layoutState.GridExplicit = true
	tui.layoutState.Groups = []string{"eng", "qa", "core"}
	tui.layoutState.GroupOf = map[string]string{
		"eng1": "eng", "eng2": "eng", "eng3": "eng", "qa1": "qa", "qa2": "qa", "super": "core", "pm": "core",
	}
	tui.layoutState.Focused = names[0]
	return tui
}

func listJoined(tui *TUI) string { return strings.Join(tui.layoutState.RightSet, ",") }

func listUnderneathIs4x2(t *testing.T, tui *TUI, what string) {
	t.Helper()
	ls := tui.layoutState
	if tui.liveList != nil || ls.Mode != LayoutGrid || ls.GridCols != 4 || ls.GridRows != 2 || !ls.GridExplicit ||
		ls.RightSetActive || ls.ListRows != 0 {
		t.Fatalf("%s: list %v mode %v grid %dx%d explicit %v rightSet %v rows %d, want the grid 4x2 underneath and no list state",
			what, tui.liveList != nil, ls.Mode, ls.GridCols, ls.GridRows, ls.GridExplicit, ls.RightSetActive, ls.ListRows)
	}
}

// ── criterion 1: entering and leaving ──────────────────────────────────

func TestLiveList_EntersFromGridAndRestores(t *testing.T) {
	tui := listFleet(t)
	altShiftL(tui)
	if tui.liveList == nil || tui.layoutState.Mode != Layout2Col || tui.layoutState.ListRows != liveListDefaultRows {
		t.Fatalf("Option+Shift+L did not enter: list %v mode %v rows %d", tui.liveList != nil, tui.layoutState.Mode, tui.layoutState.ListRows)
	}
	if len(tui.plan.Panes) == 0 || tui.plan.Panes[0].Pane.Name() != "super" {
		t.Fatalf("held pane not on the left: plan %v", liveFocusPlan(tui))
	}
	altShiftL(tui)
	listUnderneathIs4x2(t, tui, "second press")
}

// The kitty keyboard protocol reports the unshifted key with a Shift flag
// (ini-77ys); that shape toggles too.
func TestLiveList_KittyShapeOfTheChordToggles(t *testing.T) {
	tui := listFleet(t)
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModAlt|tcell.ModShift))
	if tui.liveList == nil {
		t.Fatal("'l' with Shift|Alt did not enter the live list")
	}
}

func TestLiveList_FromLiveRestoresLive(t *testing.T) {
	tui := listFleet(t)
	tui.layoutState.Mode, tui.layoutState.LiveAuto = LayoutLive, true
	altShiftL(tui)
	if tui.liveList == nil {
		t.Fatal("did not enter from Live")
	}
	altShiftL(tui)
	if tui.layoutState.Mode != LayoutLive || !tui.layoutState.LiveAuto {
		t.Fatalf("leaving restored mode %v liveAuto %v, want Live auto", tui.layoutState.Mode, tui.layoutState.LiveAuto)
	}
}

func TestLiveList_FromTheFocusSplitKeepsTheHeldAgentAndItsSnapshot(t *testing.T) {
	tui := listFleet(t)
	tui.layoutState.Focused = "qa1"
	altF(tui)
	altShiftL(tui)
	if tui.liveList == nil || tui.focusSplitPrev != nil || tui.layoutState.Focused != "qa1" {
		t.Fatalf("from the Focus split: list %v, focus snapshot left %v, held %q; want the list on qa1, snapshot adopted",
			tui.liveList != nil, tui.focusSplitPrev != nil, tui.layoutState.Focused)
	}
	altShiftL(tui)
	listUnderneathIs4x2(t, tui, "leaving after the Focus split")
}

func TestLiveList_FromLiveFocusKeepsTheHeldAgentAndItsSnapshot(t *testing.T) {
	tui := listFleet(t)
	tui.layoutState.Focused = "eng2"
	altShiftF(tui)
	altShiftL(tui)
	if tui.liveList == nil || tui.liveFocus != nil || tui.layoutState.Focused != "eng2" {
		t.Fatalf("from Live Focus: list %v, live focus left %v, held %q", tui.liveList != nil, tui.liveFocus != nil, tui.layoutState.Focused)
	}
	altShiftL(tui)
	listUnderneathIs4x2(t, tui, "leaving after Live Focus")
}

// Option+Shift+F inside the list hands over to Live Focus on the same held
// agent, and leaving that restores the layout from before the list.
// Option+f inside the list does nothing (freezing it is not built), and
// Option+digit leaves it as it leaves the other splits.
func TestLiveList_OtherSplitKeysInsideIt(t *testing.T) {
	tui := listFleet(t)
	altShiftL(tui)
	altF(tui)
	if tui.liveList == nil || tui.focusSplitPrev != nil {
		t.Fatal("Option+f inside the list changed the mode")
	}
	altShiftF(tui)
	if tui.liveList != nil || tui.liveFocus == nil || tui.layoutState.Focused != "super" || tui.layoutState.ListRows != 0 {
		t.Fatalf("Option+Shift+F inside the list: list %v live focus %v held %q rows %d",
			tui.liveList != nil, tui.liveFocus != nil, tui.layoutState.Focused, tui.layoutState.ListRows)
	}
	altShiftF(tui)
	listUnderneathIs4x2(t, tui, "leaving Live Focus entered from the list")

	altShiftL(tui)
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModAlt))
	if tui.liveList != nil || tui.layoutState.RightSetActive || tui.layoutState.ListRows != 0 {
		t.Fatal("Option+2 left list state behind")
	}
}

func TestLiveList_OnePaneFleetIsANoOp(t *testing.T) {
	tui := listFleet(t, "solo")
	altShiftL(tui)
	if tui.liveList != nil || tui.layoutState.Mode != LayoutGrid {
		t.Fatal("a one-pane fleet entered the live list")
	}
}

func TestLiveList_HelpLineSitsAfterLiveFocus(t *testing.T) {
	lines := getHelpLines()
	for i, l := range lines {
		if strings.Contains(l, "Focus split with a live right grid") {
			if i+1 >= len(lines) || !strings.Contains(lines[i+1], "Live list split") || !strings.HasPrefix(strings.TrimSpace(lines[i+1]), "Shift+") {
				t.Fatalf("line after the Live Focus chord = %q, want the Live list chord", lines[i+1])
			}
			return
		}
	}
	t.Fatal("no Live Focus line in the help overlay")
}

func TestLiveList_ListCommandToggles(t *testing.T) {
	tui := listFleet(t)
	tui.execCmd("list")
	if tui.liveList == nil {
		t.Fatal("`list` did not enter")
	}
	tui.execCmd("list")
	listUnderneathIs4x2(t, tui, "`list` again")
}

// ── criterion 2: fixed geometry ────────────────────────────────────────

// The boxes are the same size and in the same place with 1, 5, 12 and 13
// working agents; the thirteenth waits.
func TestLiveList_BoxesAreFixedWhateverTheCount(t *testing.T) {
	names := []string{"held"}
	for i := 1; i <= 13; i++ {
		names = append(names, "r"+string(rune('a'+i-1)))
	}
	panes := testPanes(names...)
	want := calcLiveListSplit(liveListDefaultCols, 3, 160, 45)
	if len(want) != 13 {
		t.Fatalf("calcLiveListSplit(3) gave %d regions, want 1+12", len(want))
	}
	for _, k := range []int{1, 5, 12, 13} {
		state := LayoutState{Mode: Layout2Col, Focused: "held", RightSetActive: true, ListRows: 3, RightSet: names[1 : 1+k]}
		plan := computeLayout(state, panes, 160, 45)
		var got []Region
		for _, pr := range plan.Panes {
			got = append(got, pr.Region)
		}
		got = append(got, plan.Waiting...)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%d working: regions\n%v\nwant the fixed grid\n%v", k, got, want)
		}
		if shown := len(plan.Panes) - 1; shown != min(k, 12) {
			t.Errorf("%d working: %d in boxes, want %d", k, shown, min(k, 12))
		}
	}
}

// ── criterion 3: order ─────────────────────────────────────────────────

// Boxes read in the Agents panel's order, not pane order and not the
// engine's: pm is second in the panes and last in the panel.
func TestLiveList_BoxesReadInPanelOrder(t *testing.T) {
	tui := listFleet(t)
	altShiftL(tui)
	if got := listJoined(tui); got != "eng1,eng2,eng3,qa1,qa2,pm" {
		t.Fatalf("boxes %s, want panel order eng1,eng2,eng3,qa1,qa2,pm", got)
	}
}

// A leaver's followers close the gap on the next tick; a joiner slots back
// into its order position; a pinned idle agent appears in its position and
// stays, leaves while suspended, and returns when it wakes.
func TestLiveList_LeaversJoinersAndPinsKeepTheOrder(t *testing.T) {
	tui := listFleet(t)
	altShiftL(tui)
	now := time.Now()

	setActivity(tui, "eng2", StateIdle)
	tui.tickLiveList(now.Add(11 * time.Second))
	if got := listJoined(tui); got != "eng1,eng3,qa1,qa2,pm" {
		t.Fatalf("eng2 stopped: boxes %s, want the gap closed", got)
	}
	setActivity(tui, "eng2", StateRunning)
	tui.tickLiveList(now.Add(12 * time.Second))
	if got := listJoined(tui); got != "eng1,eng2,eng3,qa1,qa2,pm" {
		t.Fatalf("eng2 started again: boxes %s, want it back in its place", got)
	}

	setActivity(tui, "qa1", StateIdle)
	tui.layoutState.LivePinned = map[string]int{"qa1": 0}
	tui.tickLiveList(now.Add(30 * time.Second))
	if got := listJoined(tui); got != "eng1,eng2,eng3,qa1,qa2,pm" {
		t.Fatalf("qa1 idle but pinned: boxes %s, want it kept in its place", got)
	}
	setSuspendedForTest(tui, "qa1", true)
	tui.tickLiveList(now.Add(31 * time.Second))
	if strings.Contains(listJoined(tui), "qa1") {
		t.Fatalf("qa1 pinned and suspended: boxes %s, want it out", listJoined(tui))
	}
	setSuspendedForTest(tui, "qa1", false)
	tui.tickLiveList(now.Add(32 * time.Second))
	if got := listJoined(tui); got != "eng1,eng2,eng3,qa1,qa2,pm" {
		t.Fatalf("qa1 woke: boxes %s, want it back in its place", got)
	}
}

// The placement rules, cell by cell, with no engine.
func TestLiveList_PlacementRules(t *testing.T) {
	panel := []string{"a", "b", "c", "d", "e"}
	for name, c := range map[string]struct {
		in, prev []string
		pinned   map[string]int
		boxes    int
		want     string
	}{
		"fills in panel order, not engine order":   {[]string{"e", "c", "a"}, nil, nil, 4, "a,c,e"},
		"the first boxes go first in the order":    {[]string{"e", "d", "c", "b", "a"}, nil, nil, 3, "a,b,c"},
		"a newcomer waits while the grid is full":  {[]string{"a", "c", "d", "e"}, []string{"c", "d", "e"}, nil, 3, "c,d,e"},
		"a freed box goes to the first waiting":    {[]string{"a", "b", "e"}, []string{"c", "e"}, nil, 2, "a,e"},
		"a pinned agent is counted first":          {[]string{"a", "b", "e"}, []string{"a", "b"}, map[string]int{"e": 0}, 2, "a,e"},
		"pinned is drawn in its panel place":       {[]string{"a", "c"}, nil, map[string]int{"c": 0}, 4, "a,c"},
		"an agent no longer in leaves":             {[]string{"a"}, []string{"a", "b"}, nil, 4, "a"},
		"nobody working":                           {nil, []string{"a"}, nil, 4, ""},
		"an agent the panel does not list is last": {[]string{"zz", "b"}, nil, nil, 4, "b,zz"},
	} {
		got := strings.Join(liveListPlace(c.in, panel, c.pinned, c.prev, c.boxes), ",")
		if got != c.want {
			t.Errorf("%s: placed %q, want %q", name, got, c.want)
		}
	}
}

// ── criterion 4: membership is Live Focus's ────────────────────────────

func TestLiveList_MembershipIsLiveFocusMembership(t *testing.T) {
	tui := listFleet(t)
	tui.layoutState.Hidden = map[string]bool{"eng1": true}
	altShiftL(tui)
	if reflect.ValueOf(tui.liveList.engine.Eligible).Pointer() != reflect.ValueOf(paneIsWorking).Pointer() {
		t.Fatal("the list's engine does not use paneIsWorking, the live focus split's predicate")
	}
	if strings.Contains(listJoined(tui), "eng1") {
		t.Fatalf("hidden eng1 is in a box: %s", listJoined(tui))
	}
	if strings.Contains(listJoined(tui), "super") {
		t.Fatalf("the held agent is also in a box: %s", listJoined(tui))
	}
}

// ── criterion 5: waiting boxes, golden ─────────────────────────────────

// listRenderToString renders a frame and returns the pane area as text (the
// status bar's clock is below it).
func listRenderToString(t *testing.T, tui *TUI, s tcell.SimulationScreen) string {
	t.Helper()
	tui.applyLayout()
	tui.render()
	w, h := s.Size()
	var b strings.Builder
	for y := 0; y < h-2; y++ {
		var line strings.Builder
		for x := 0; x < w; x++ {
			ch, _, _, _ := s.GetContent(x, y)
			if ch == 0 {
				ch = ' '
			}
			line.WriteRune(ch)
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// Two agents working at 4x3: two boxes filled and ten empty boxes, which
// draw nothing (ini-lpnu). Regenerate only deliberately:
//
//	go test ./internal/tui/ -run TestLiveList_WaitingBoxesGolden -update-golden
func TestLiveList_WaitingBoxesGolden(t *testing.T) {
	tui, s := newTestTUIWithScreen("held", "a", "b", "c", "d")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.Focused = "held"
	setActivity(tui, "c", StateIdle)
	setActivity(tui, "d", StateIdle)
	// The agents overlay (Option+s) draws over the top-right corner in every
	// layout, the last box included; off, so the golden pins the boxes.
	tui.layoutState.Overlay = false
	altShiftL(tui)
	if len(tui.plan.Waiting) != 10 {
		t.Fatalf("%d empty boxes, want 10 (12 boxes, 2 agents)", len(tui.plan.Waiting))
	}
	got := listRenderToString(t, tui, s)
	assertEmptyBoxesBlank(t, tui, s, "golden frame")
	if strings.Contains(got, "waiting") {
		t.Errorf("an empty box draws a label:\n%s", got)
	}
	path := filepath.Join("testdata", "live_list_4x3_two_agents.golden")
	if updateGolden() {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden updated: %s", path)
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (regenerate with -update-golden, deliberately): %v", err)
	}
	if want := strings.ReplaceAll(string(raw), "\r\n", "\n"); got != want {
		t.Errorf("live list render changed.\n--- want ---\n%s\n--- got ---\n%s\n%s", want, got, firstDiffLine(want, got))
	}
}

// ── criterion 6: grid 4xR ──────────────────────────────────────────────

func TestLiveList_GridSetsTheRows(t *testing.T) {
	tui := listFleet(t)
	altShiftL(tui)
	for r := 1; r <= liveListMaxRows; r++ {
		tui.execCmd("grid 4x" + string(rune('0'+r)))
		if tui.cmd.error != "" || tui.listRows != r || tui.layoutState.ListRows != r {
			t.Fatalf("grid 4x%d: rows %d/%d error %q", r, tui.listRows, tui.layoutState.ListRows, tui.cmd.error)
		}
	}
	tui.execCmd("grid 4x2")
	// Columns other than four are valid since ini-3o4b (TestListCols_*).
	for _, bad := range []string{"4x0", "4x7", "4", "4x2x1", "abc"} {
		tui.cmd.error = ""
		tui.execCmd("grid " + bad)
		if tui.cmd.error == "" || tui.listRows != 2 || tui.layoutState.ListRows != 2 || tui.liveList == nil {
			t.Errorf("grid %s: error %q rows %d list %v, want an error and 4x2 unchanged", bad, tui.cmd.error, tui.listRows, tui.liveList != nil)
		}
	}
	tui.applyLayout()
	if got := len(tui.plan.Panes) - 1 + len(tui.plan.Waiting); got != 8 {
		t.Errorf("4x2 draws %d boxes, want 8", got)
	}
}

// ── criterion 7: persistence ───────────────────────────────────────────

func listFile(t *testing.T, tui *TUI) string {
	t.Helper()
	b, err := os.ReadFile(layoutPath(tui.projectRoot))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// listRestart is Run's sequence on old's layout.yaml, for a fleet named names.
func listRestart(t *testing.T, root string, names []string, viewer bool) *TUI {
	t.Helper()
	tui := listFleet(t, names...)
	tui.projectRoot = root
	ls, ok := LoadLayout(root, names)
	if !ok {
		t.Fatal("the saved layout did not load")
	}
	ls.Hidden = map[string]bool{}
	tui.layoutState = ls
	tui.adoptLoadedSplit(viewer)
	tui.applyLayout()
	return tui
}

func TestLiveList_ComesBackAfterARestartWithItsRows(t *testing.T) {
	tui := listFleet(t)
	tui.projectRoot = t.TempDir()
	tui.layoutState.Focused = "qa2"
	altShiftL(tui)
	tui.execCmd("grid 4x2")
	tui.saveIfSplitMoved()
	f := listFile(t, tui)
	if !strings.Contains(f, "mode: grid") || !strings.Contains(f, "split: list\nheld: qa2\nlist_rows: 2\n") {
		t.Fatalf("layout.yaml in the list:\n%s\nwant mode grid (underneath), split list, held qa2, list_rows 2", f)
	}
	back := listRestart(t, tui.projectRoot, listFleetNames, false)
	if back.liveList == nil || back.layoutState.Focused != "qa2" || back.listRows != 2 || back.layoutState.ListRows != 2 {
		t.Fatalf("after the restart: list %v held %q rows %d/%d, want the list on qa2 at 4x2",
			back.liveList != nil, back.layoutState.Focused, back.listRows, back.layoutState.ListRows)
	}
	altShiftL(back)
	listUnderneathIs4x2(t, back, "leaving after the restart")
	back.saveIfSplitMoved()
	if f := listFile(t, back); strings.Contains(f, "split:") || strings.Contains(f, "list_rows:") {
		t.Errorf("after leaving, layout.yaml kept the list keys:\n%s", f)
	}
}

func TestLiveList_SavedRowsAbsentOrOutOfRangeMeanThree(t *testing.T) {
	for name, extra := range map[string]string{"absent": "", "out of range": "list_rows: 9\n"} {
		root := t.TempDir()
		os.MkdirAll(layoutDir(root), 0o700)
		os.WriteFile(layoutPath(root), []byte("grid: 4x2\ngrid_explicit: true\nmode: grid\ngroups: [eng, qa, core]\nsplit: list\nheld: super\n"+extra), 0o600)
		back := listRestart(t, root, listFleetNames, false)
		if back.liveList == nil || back.listRows != liveListDefaultRows {
			t.Errorf("%s: list %v rows %d, want the list at 3 rows", name, back.liveList != nil, back.listRows)
		}
	}
}

func TestLiveList_AHeldAgentGoneOrAViewerOpensTheLayoutUnderneath(t *testing.T) {
	tui := listFleet(t)
	tui.projectRoot = t.TempDir()
	tui.layoutState.Focused = "qa2"
	altShiftL(tui)
	tui.saveIfSplitMoved()

	logDir := t.TempDir()
	cleanup := InitLogger(logDir, slog.LevelInfo)
	gone := listRestart(t, tui.projectRoot, []string{"super", "pm", "eng1", "eng2", "eng3", "qa1"}, false)
	cleanup()
	if gone.liveList != nil || gone.layoutState.Mode == Layout2Col {
		t.Error("the list came back with its held agent gone")
	}
	log, _ := os.ReadFile(filepath.Join(logDir, ".initech", logFileName))
	if !strings.Contains(string(log), "saved split not restored") || !strings.Contains(string(log), "held=qa2") {
		t.Errorf("no WARN naming qa2:\n%s", log)
	}
	if viewer := listRestart(t, tui.projectRoot, listFleetNames, true); viewer.liveList != nil {
		t.Error("a viewer restored the list")
	}
}

// listV260Inputs are the states the v2.26.0 goldens were written from, by
// SaveLayout at v2.26.0 (generated in a v2.26.0 worktree for this bead).
var listV260Inputs = map[string]LayoutState{
	"grid":  {Mode: LayoutGrid, GridCols: 2, GridRows: 2, GridExplicit: true, Order: []string{"eng1", "eng2", "qa1", "super"}, Groups: []string{"eng", "qa"}, GroupOf: map[string]string{"eng1": "eng", "eng2": "eng", "qa1": "qa"}},
	"live":  {Mode: LayoutGrid, GridCols: 4, GridRows: 1, GridExplicit: true, Order: []string{"super", "eng1", "eng2", "qa1"}, Split: "live", Held: "eng2"},
	"focus": {Mode: LayoutLive, GridCols: 3, GridRows: 2, LiveAuto: true, Order: []string{"qa1", "super", "eng1", "eng2"}, Split: "focus", Held: "qa1"},
}

// A file written by v2.26.0 loads to exactly what v2.26.0 loaded (recorded
// there), and the same layouts still save byte for byte as v2.26.0 did.
func TestLiveList_AV260FileLoadsAsItDidThere(t *testing.T) {
	empty := map[string]bool{}
	want := map[string]LayoutState{
		"grid": {Mode: LayoutGrid, GridCols: 2, GridRows: 2, Focused: "super", Hidden: empty, Protected: empty,
			Order: []string{"eng1", "eng2", "qa1", "super"}, Overlay: true, GridExplicit: true,
			Groups: []string{"eng", "qa"}, GroupOf: map[string]string{"eng1": "eng", "eng2": "eng", "qa1": "qa"}},
		"live": {Mode: LayoutGrid, GridCols: 4, GridRows: 1, Focused: "super", Hidden: empty, Protected: empty,
			Order: []string{"super", "eng1", "eng2", "qa1"}, Overlay: true, GridExplicit: true, Split: "live", Held: "eng2"},
		"focus": {Mode: LayoutLive, GridCols: 3, GridRows: 2, Focused: "super", Hidden: empty, Protected: empty,
			Order: []string{"qa1", "super", "eng1", "eng2"}, Overlay: true, LiveAuto: true, Split: "focus", Held: "qa1"},
	}
	for name, w := range want {
		golden, err := os.ReadFile(filepath.Join("testdata", "layout-v2.26.0-"+name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		os.MkdirAll(layoutDir(root), 0o700)
		os.WriteFile(layoutPath(root), golden, 0o600)
		got, ok := LoadLayout(root, aa8aKeys)
		if !ok || !reflect.DeepEqual(got, w) {
			t.Errorf("%s: loaded (ok=%v)\n%#v\nwant, as v2.26.0 loaded it,\n%#v", name, ok, got, w)
		}
		out := t.TempDir()
		if err := SaveLayout(out, listV260Inputs[name]); err != nil {
			t.Fatal(err)
		}
		// v2.26.0's bytes plus the overlay key, which every save now writes
		// last (ini-ye1x).
		wantSaved := string(golden) + fmt.Sprintf("overlay: %v\n", listV260Inputs[name].Overlay)
		if b, _ := os.ReadFile(layoutPath(out)); string(b) != wantSaved {
			t.Errorf("%s: saved\n%s\nwant v2.26.0's bytes and the overlay key\n%s", name, b, wantSaved)
		}
	}
}

// persistentLayoutV260 is PersistentLayout exactly as v2.26.0 declares it.
type persistentLayoutV260 struct {
	Grid         string            `yaml:"grid"`
	GridExplicit bool              `yaml:"grid_explicit,omitempty"`
	Hidden       []string          `yaml:"hidden,omitempty"`
	Protected    []string          `yaml:"protected,omitempty"`
	DepPinned    []string          `yaml:"pinned,omitempty"`
	Order        []string          `yaml:"order,omitempty"`
	Mode         string            `yaml:"mode"`
	LivePinned   map[string]int    `yaml:"live_pinned,omitempty"`
	LiveAuto     bool              `yaml:"live_auto,omitempty"`
	Groups       []string          `yaml:"groups,omitempty"`
	GroupOf      map[string]string `yaml:"group_of,omitempty"`
	Split        string            `yaml:"split,omitempty"`
	Held         string            `yaml:"held,omitempty"`
}

// Older readers decode a list file without error and get the layout
// underneath. v2.26.0 sees split "list", which its restore does not know: it
// warns and opens the layout underneath.
func TestLiveList_OlderReadersGetTheLayoutUnderneath(t *testing.T) {
	tui := listFleet(t)
	tui.projectRoot = t.TempDir()
	altShiftL(tui)
	tui.execCmd("grid 4x2")
	tui.saveIfSplitMoved()
	data := []byte(listFile(t, tui))
	var v260 persistentLayoutV260
	if err := yaml.Unmarshal(data, &v260); err != nil || v260.Mode != "grid" || v260.Grid != "4x2" || v260.Split != "list" {
		t.Errorf("v2.26.0 reader: err %v mode %q grid %q split %q", err, v260.Mode, v260.Grid, v260.Split)
	}
	var v2254 persistentLayoutV2254
	if err := yaml.Unmarshal(data, &v2254); err != nil || v2254.Mode != "grid" || v2254.Grid != "4x2" {
		t.Errorf("v2.25.4 reader: err %v mode %q grid %q", err, v2254.Mode, v2254.Grid)
	}
}

// ── feed it nothing ────────────────────────────────────────────────────

func TestLiveList_NothingToShow(t *testing.T) {
	tui := listFleet(t)
	for _, n := range listFleetNames {
		setActivity(tui, n, StateIdle)
	}
	altShiftL(tui)
	if len(tui.layoutState.RightSet) != 0 || len(tui.plan.Waiting) != 12 || len(tui.plan.Panes) != 1 {
		t.Errorf("nobody working: boxes %v, %d waiting, %d panes; want 12 waiting tiles and the held pane",
			tui.layoutState.RightSet, len(tui.plan.Waiting), len(tui.plan.Panes))
	}
	empty := newTestTUI()
	altShiftL(empty)
	if empty.liveList != nil {
		t.Error("an empty fleet entered the list")
	}
	if plan := computeLayout(LayoutState{Mode: Layout2Col, RightSetActive: true, ListRows: 3}, nil, 160, 45); len(plan.Panes) != 0 {
		t.Error("no panes drew something")
	}
	root := t.TempDir()
	os.MkdirAll(layoutDir(root), 0o700)
	os.WriteFile(layoutPath(root), nil, 0o600)
	if _, ok := LoadLayout(root, listFleetNames); ok {
		t.Error("an empty file loaded as a layout")
	}
}
