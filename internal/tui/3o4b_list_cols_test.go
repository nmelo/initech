package tui

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// ini-3o4b: inside the live list split, grid CxR sets the columns as well as
// the rows (each 1 to 6), and the shape comes back after a restart:
// layout.yaml carries list_cols beside list_rows, with the same lifecycle.

// listShape is the list's box grid as the plan lays it out: the distinct
// column and row positions of every box, filled or empty.
func listShape(tui *TUI) (cols, rows int) {
	xs, ys := map[int]bool{}, map[int]bool{}
	boxes := append([]Region{}, tui.plan.Waiting...)
	for i, pr := range tui.plan.Panes {
		if i == 0 && pr.Pane.Name() == tui.layoutState.Focused {
			continue // the held pane on the left
		}
		boxes = append(boxes, pr.Region)
	}
	for _, r := range boxes {
		xs[r.X], ys[r.Y] = true, true
	}
	return len(xs), len(ys)
}

// listHas reports whether name is in list.
func listHas(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

const listGridErr = `invalid grid %q for the live list: use CxR with C and R from 1 to 6`

// Criteria 1 and 2: every CxR in 1..6 sets that shape; anything else leaves
// the grid as it is and says exactly why.
func TestListCols_GridSetsColumnsAndRows(t *testing.T) {
	tui := listFleet(t)
	altShiftL(tui)
	for c := 1; c <= 6; c++ {
		for r := 1; r <= 6; r++ {
			arg := string(rune('0'+c)) + "x" + string(rune('0'+r))
			tui.execCmd("grid " + arg)
			if tui.cmd.error != "" {
				t.Fatalf("grid %s refused: %q", arg, tui.cmd.error)
			}
			if gc, gr := listShape(tui); gc != c || gr != r {
				t.Fatalf("grid %s laid out %dx%d", arg, gc, gr)
			}
		}
	}
	tui.execCmd("grid 3x2")
	for _, bad := range []string{"0x3", "7x2", "3x7", "3", "axb", "3x"} {
		tui.cmd.error = ""
		tui.execCmd("grid " + bad)
		if want := strings.Replace(listGridErr, "%q", `"`+bad+`"`, 1); tui.cmd.error != want {
			t.Errorf("grid %s: error %q, want %q", bad, tui.cmd.error, want)
		}
		if gc, gr := listShape(tui); gc != 3 || gr != 2 {
			t.Errorf("grid %s changed the grid to %dx%d", bad, gc, gr)
		}
	}
}

// Criterion 3: the boxes are C*R whatever the count; an agent in box nine of
// a 3x3 keeps it when a higher-ordered agent starts.
func TestListCols_PlacementRunsOverAllTheBoxes(t *testing.T) {
	names := []string{"held"}
	for i := 1; i <= 10; i++ {
		names = append(names, "r"+string(rune('a'+i-1)))
	}
	tui := listFleet(t, names...)
	tui.layoutState.Groups, tui.layoutState.GroupOf = nil, nil
	setActivity(tui, "ra", StateIdle)
	altShiftL(tui)
	tui.execCmd("grid 3x3")
	if got := len(tui.layoutState.RightSet); got != 9 {
		t.Fatalf("%d agents in a 3x3, want 9: %s", got, listJoined(tui))
	}
	ninth := tui.layoutState.RightSet[8]
	setActivity(tui, "ra", StateRunning)
	now := time.Now()
	tui.tickLiveList(now.Add(11 * time.Second))
	tui.tickLiveList(now.Add(12 * time.Second))
	if got := tui.layoutState.RightSet; len(got) != 9 || !listHas(got, ninth) {
		t.Errorf("box nine's agent %s lost its box when ra started: %v", ninth, got)
	}
}

// Criterion 4: list_cols is written only while the split is on.
func TestListCols_SavedOnlyWhileOn(t *testing.T) {
	tui := listFleet(t)
	tui.projectRoot = t.TempDir()
	altShiftL(tui)
	tui.execCmd("grid 6x2")
	tui.saveIfSplitMoved()
	f := listFile(t, tui)
	if !strings.Contains(f, "list_cols: 6\n") || !strings.Contains(f, "list_rows: 2\n") {
		t.Errorf("saved while on:\n%s\nwant list_cols: 6 and list_rows: 2", f)
	}
	altShiftL(tui)
	tui.saveIfSplitMoved()
	if f := listFile(t, tui); strings.Contains(f, "list_cols") || strings.Contains(f, "list_rows") {
		t.Errorf("saved after leaving:\n%s\nwant neither list key", f)
	}
}

// Criterion 4: the shape comes back after a restart, 6x2 here.
func TestListCols_TheShapeComesBackAfterARestart(t *testing.T) {
	tui := listFleet(t)
	tui.projectRoot = t.TempDir()
	altShiftL(tui)
	tui.execCmd("grid 6x2")
	tui.saveIfSplitMoved()
	back := listRestart(t, tui.projectRoot, listFleetNames, false)
	if back.liveList == nil {
		t.Fatal("the list did not come back")
	}
	if c, r := listShape(back); c != 6 || r != 2 {
		t.Errorf("came back %dx%d, want 6x2", c, r)
	}
}

// listFromFile restarts a fleet from the given layout.yaml bytes.
func listFromFile(t *testing.T, data []byte) *TUI {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(layoutDir(root), 0o700)
	if err := os.WriteFile(layoutPath(root), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return listRestart(t, root, []string{"super", "eng1", "eng2", "qa1"}, false)
}

// Criterion 4: a file as b6ef2ad (v2.31.x) writes it, with list_rows and no
// list_cols, loads as 4x2.
func TestListCols_AFileWithoutTheKeyLoadsFourColumns(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "layout_list_rows_only.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "list_cols") || !strings.Contains(string(raw), "list_rows: 2") {
		t.Fatalf("the fixture is not today's format:\n%s", raw)
	}
	back := listFromFile(t, raw)
	if back.liveList == nil {
		t.Fatal("the list did not come back")
	}
	if c, r := listShape(back); c != 4 || r != 2 {
		t.Errorf("today's file came back %dx%d, want 4x2", c, r)
	}
}

// Criterion 4: a column count out of range loads as four, with a warning.
func TestListCols_OutOfRangeLoadsFourWithAWarning(t *testing.T) {
	logDir := t.TempDir()
	cleanup := InitLogger(logDir, slog.LevelInfo)
	back := listFromFile(t, []byte("grid: 2x2\nmode: grid\nsplit: list\nheld: super\nlist_rows: 2\nlist_cols: 9\n"))
	cleanup()
	if back.liveList == nil {
		t.Fatal("the list did not come back")
	}
	if c, r := listShape(back); c != 4 || r != 2 {
		t.Errorf("list_cols 9 came back %dx%d, want 4x2", c, r)
	}
	log, _ := os.ReadFile(filepath.Join(logDir, ".initech", logFileName))
	if !strings.Contains(string(log), "list_cols=9") {
		t.Errorf("no warning naming list_cols=9:\n%s", log)
	}
}

// Feed it nothing: an empty file, and a file with only split: list and held.
func TestListCols_EmptyAndMinimalFiles(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(layoutDir(root), 0o700)
	os.WriteFile(layoutPath(root), nil, 0o600)
	if _, ok := LoadLayout(root, listFleetNames); ok {
		t.Error("an empty file loaded as a layout")
	}
	back := listFromFile(t, []byte("split: list\nheld: super\n"))
	if back.liveList == nil {
		t.Fatal("split: list with a held agent did not come back")
	}
	if c, r := listShape(back); c != 4 || r != 3 {
		t.Errorf("a minimal file came back %dx%d, want the default 4x3", c, r)
	}
}

// An older initech reads a file with list_cols without error.
func TestListCols_OlderReadersIgnoreTheKey(t *testing.T) {
	data := []byte("grid: 2x2\nmode: grid\nsplit: list\nheld: super\nlist_rows: 2\nlist_cols: 6\noverlay: true\n")
	var v260 persistentLayoutV260
	if err := yaml.Unmarshal(data, &v260); err != nil || v260.Split != "list" {
		t.Errorf("v2.26.0 reader: err %v split %q", err, v260.Split)
	}
	var v2314 persistentLayoutV2314
	if err := yaml.Unmarshal(data, &v2314); err != nil || v2314.ListRows != 2 {
		t.Errorf("v2.31.4 reader: err %v list_rows %d", err, v2314.ListRows)
	}
}

// Criterion 5: the header label names the chosen shape.
func TestListCols_TheLabelNamesTheShape(t *testing.T) {
	tui, s := newTestTUIWithScreen("held", "a", "b", "c")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.Focused = "held"
	altShiftL(tui)
	tui.execCmd("grid 6x2")
	tui.applyLayout()
	tui.render()
	w, h := s.Size()
	var screen strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ch, _, _, _ := s.GetContent(x, y)
			screen.WriteRune(ch)
		}
	}
	if !strings.Contains(screen.String(), "Live list 6x2 (") {
		t.Errorf("no \"Live list 6x2 (\" on screen")
	}
}

// The geometry: 1+C*R regions whatever the count, and the boxes of one grid
// are the same size, to the one cell a screen that does not divide evenly
// leaves over (spread as every grid spreads it).
func TestListCols_TheGeometryIsCByR(t *testing.T) {
	for _, shape := range [][2]int{{3, 3}, {6, 1}, {1, 6}, {6, 6}, {1, 1}} {
		c, r := shape[0], shape[1]
		all := calcLiveListSplit(c, r, 160, 45)
		if len(all) != 1+c*r {
			t.Fatalf("%dx%d: %d regions, want %d", c, r, len(all), 1+c*r)
		}
		minW, maxW, minH, maxH := 1<<30, 0, 1<<30, 0
		for _, b := range all[1:] {
			minW, maxW = min(minW, b.W), max(maxW, b.W)
			minH, maxH = min(minH, b.H), max(maxH, b.H)
		}
		if maxW-minW > 1 || maxH-minH > 1 {
			t.Errorf("%dx%d: box widths %d..%d, heights %d..%d, want equal to one cell", c, r, minW, maxW, minH, maxH)
		}
	}
}
