package tui

import (
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"gopkg.in/yaml.v3"
)

// ini-aa8a: the Focus split and the live focus split come back after a
// restart. layout.yaml keeps the layout UNDERNEATH a split in mode and grid,
// and gains split and held while one is on. A "restart" here is what Run
// does: LoadLayout on the file the old window wrote, into a fresh fleet,
// then adoptLoadedSplit.

func altF(tui *TUI) {
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt))
}

// aa8aFleet is liveFocusTUI's fleet: a, b and c working, d idle with a bead,
// on an explicit 4x1 grid, so the layout underneath a split is one no
// default would reproduce.
func aa8aFleet(t *testing.T) *TUI {
	t.Helper()
	tui := liveFocusTUI(t)
	tui.projectRoot = t.TempDir()
	tui.layoutState.GridCols, tui.layoutState.GridRows = 4, 1
	tui.layoutState.GridExplicit = true
	tui.applyLayout()
	tui.saveLayoutIfConfigured()
	return tui
}

// aa8aRestart starts a fresh window on old's layout.yaml: names is the fleet
// in initech.yaml now, hidden the session-global hidden set (fleet-state.yaml,
// not this file).
func aa8aRestart(t *testing.T, old *TUI, names []string, hidden ...string) *TUI {
	t.Helper()
	panes := make([]*Pane, len(names))
	for i, n := range names {
		panes[i] = testPane(n)
		if n == "d" {
			panes[i].activity = StateIdle
			panes[i].beadIDs = []string{"ini-waiting"}
		}
	}
	tui := newTestTUI(panes...)
	tui.projectRoot = old.projectRoot
	ls, ok := LoadLayout(old.projectRoot, names)
	if !ok {
		t.Fatal("the saved layout did not load")
	}
	ls.Hidden = map[string]bool{}
	for _, h := range hidden {
		ls.Hidden[h] = true
	}
	tui.layoutState = ls
	tui.adoptLoadedSplit(false)
	tui.applyLayout()
	return tui
}

func aa8aFile(t *testing.T, tui *TUI) string {
	t.Helper()
	b, err := os.ReadFile(layoutPath(tui.projectRoot))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func aa8aUnderneathIs4x1(t *testing.T, tui *TUI, what string) {
	t.Helper()
	ls := tui.layoutState
	if ls.Mode != LayoutGrid || ls.GridCols != 4 || ls.GridRows != 1 || !ls.GridExplicit {
		t.Fatalf("%s: mode %v grid %dx%d explicit %v, want the layout underneath: grid 4x1 explicit",
			what, ls.Mode, ls.GridCols, ls.GridRows, ls.GridExplicit)
	}
}

// Criterion 2: Option+Shift+F on b, restart. The live split is back with b
// on the left and the working agents on the right, and the next press
// restores the layout that was underneath before the restart.
func TestAa8a_ALiveSplitComesBackAfterARestart(t *testing.T) {
	tui := aa8aFleet(t)
	tui.layoutState.Focused = "b"
	altShiftF(tui)
	tui.saveIfSplitMoved()
	if f := aa8aFile(t, tui); !strings.Contains(f, "mode: grid") || !strings.Contains(f, "split: live\nheld: b\n") {
		t.Fatalf("layout.yaml in the live split:\n%s\nwant mode grid (underneath) and split live, held b", f)
	}

	back := aa8aRestart(t, tui, []string{"a", "b", "c", "d"})
	if back.liveFocus == nil || back.layoutState.Mode != Layout2Col {
		t.Fatalf("after the restart: liveFocus %v mode %v, want the live split", back.liveFocus != nil, back.layoutState.Mode)
	}
	if got := liveFocusJoined(liveFocusPlan(back)); got != "b,a,c" {
		t.Errorf("plan %s, want b held left, then the working a and c (idle d absent)", got)
	}
	altShiftF(back)
	aa8aUnderneathIs4x1(t, back, "Option+Shift+F after the restart")
}

// Criterion 3: Option+f on c, restart. The plain Focus split is back with c
// on the left and every other pane on the right; Option+f restores the
// layout underneath (at v2.25.4 it went to Grid: the file said "main").
func TestAa8a_AFocusSplitComesBackAfterARestart(t *testing.T) {
	tui := aa8aFleet(t)
	tui.layoutState.Focused = "c"
	altF(tui)
	if f := aa8aFile(t, tui); strings.Contains(f, "mode: main") || !strings.Contains(f, "split: focus\nheld: c\n") {
		t.Fatalf("layout.yaml in the Focus split:\n%s\nwant mode grid (underneath), split focus, held c", f)
	}

	back := aa8aRestart(t, tui, []string{"a", "b", "c", "d"})
	if back.layoutState.Mode != Layout2Col || back.focusSplitPrev == nil || back.liveFocus != nil {
		t.Fatalf("after the restart: mode %v, focus snapshot %v, live %v; want the plain Focus split",
			back.layoutState.Mode, back.focusSplitPrev != nil, back.liveFocus != nil)
	}
	if got := liveFocusJoined(liveFocusPlan(back)); got != "c,a,b,d" {
		t.Errorf("plan %s, want c held left, then every other pane", got)
	}
	altF(back)
	aa8aUnderneathIs4x1(t, back, "Option+f after the restart")
}

// Criterion 4: leaving a split writes the file without its keys, and a
// restart opens in the layout underneath.
func TestAa8a_LeavingASplitLeavesNoKeysBehind(t *testing.T) {
	for name, key := range map[string]func(*TUI){"live": altShiftF, "focus": altF} {
		tui := aa8aFleet(t)
		before := aa8aFile(t, tui)
		key(tui)
		tui.saveIfSplitMoved()
		key(tui)
		tui.saveIfSplitMoved()
		if after := aa8aFile(t, tui); after != before {
			t.Errorf("%s: layout.yaml after leaving the split:\n%s\nwant it as before:\n%s", name, after, before)
		}
		back := aa8aRestart(t, tui, []string{"a", "b", "c", "d"})
		if back.liveFocus != nil || back.focusSplitPrev != nil {
			t.Errorf("%s: a split came back after it was left", name)
		}
		aa8aUnderneathIs4x1(t, back, name+", restarted after leaving")
	}
}

// Criterion 7: the held agent removed from initech.yaml, or hidden, before
// the restart. No split, the layout underneath, one WARN naming the agent,
// and the next save drops the keys.
func TestAa8a_AHeldAgentThatIsGoneOpensTheLayoutUnderneath(t *testing.T) {
	for name, c := range map[string]struct {
		names  []string
		hidden []string
		why    string
	}{
		"removed": {[]string{"a", "c", "d"}, nil, "not in this window"},
		"hidden":  {[]string{"a", "b", "c", "d"}, []string{"b"}, "hidden"},
	} {
		logDir := t.TempDir()
		cleanup := InitLogger(logDir, slog.LevelInfo)
		tui := aa8aFleet(t)
		tui.layoutState.Focused = "b"
		altShiftF(tui)
		tui.saveIfSplitMoved()

		back := aa8aRestart(t, tui, c.names, c.hidden...)
		cleanup()
		if back.liveFocus != nil || back.layoutState.Mode == Layout2Col || back.layoutState.RightSetActive {
			t.Errorf("%s: a split (or its empty left column) came back with its held agent gone", name)
		}
		aa8aUnderneathIs4x1(t, back, name)
		log, _ := os.ReadFile(filepath.Join(logDir, ".initech", logFileName))
		var warns []string
		for _, l := range strings.Split(string(log), "\n") {
			if strings.Contains(l, "level=WARN") && strings.Contains(l, "saved split not restored") {
				warns = append(warns, l)
			}
		}
		if len(warns) != 1 || !strings.Contains(warns[0], "held=b") || !strings.Contains(warns[0], c.why) {
			t.Errorf("%s: WARN lines %q, want one naming held=b and %q", name, warns, c.why)
		}
		back.saveIfSplitMoved()
		if f := aa8aFile(t, back); strings.Contains(f, "split:") || strings.Contains(f, "held:") {
			t.Errorf("%s: the next save kept the keys:\n%s", name, f)
		}
	}
}

// Criterion 8: the :main preset is two columns with no split. It is still
// written as mode main with no split key, and comes back as it did.
func TestAa8a_TheMainPresetIsUnchanged(t *testing.T) {
	tui := aa8aFleet(t)
	tui.execCmd("main")
	tui.saveIfSplitMoved()
	f := aa8aFile(t, tui)
	if !strings.Contains(f, "mode: main") || strings.Contains(f, "split:") || strings.Contains(f, "held:") {
		t.Fatalf("layout.yaml after :main:\n%s\nwant mode main and no split key", f)
	}
	back := aa8aRestart(t, tui, []string{"a", "b", "c", "d"})
	if back.layoutState.Mode != Layout2Col || back.focusSplitPrev != nil || back.liveFocus != nil {
		t.Errorf("after the restart: mode %v, focus snapshot %v, live %v; want two columns, no split",
			back.layoutState.Mode, back.focusSplitPrev != nil, back.liveFocus != nil)
	}
	if back.layoutState.Focused != "a" {
		t.Errorf("focused %q, want the first pane, as at v2.25.4", back.layoutState.Focused)
	}
}

// Window 1 only: a viewer never restores a split from the file.
func TestAa8a_AViewerNeverRestoresASplit(t *testing.T) {
	tui := aa8aFleet(t)
	altShiftF(tui)
	tui.saveIfSplitMoved()
	viewer := newTestTUI(testPane("a"), testPane("b"), testPane("c"), testPane("d"))
	ls, _ := LoadLayout(tui.projectRoot, []string{"a", "b", "c", "d"})
	viewer.layoutState = ls
	viewer.adoptLoadedSplit(true)
	if viewer.liveFocus != nil || viewer.layoutState.Mode == Layout2Col {
		t.Fatal("a viewer restored the split from window 1's file")
	}
}

// The trigger: focus moving inside a split, and the drop from the live split
// to the Focus split, both reach the file with no save of their own. The
// restart after the drop is a plain Focus split showing every other pane:
// the frozen right set is not kept (not built).
func TestAa8a_TheFileFollowsTheHeldPaneAndTheSplitKind(t *testing.T) {
	tui := aa8aFleet(t)
	altShiftF(tui)
	tui.saveIfSplitMoved()
	tui.layoutState.Focused = "c" // as a click on c would
	tui.saveIfSplitMoved()
	if f := aa8aFile(t, tui); !strings.Contains(f, "held: c\n") {
		t.Fatalf("focus moved to c inside the split; layout.yaml:\n%s", f)
	}
	altF(tui) // drop to the Focus split, right set frozen
	tui.saveIfSplitMoved()
	if f := aa8aFile(t, tui); !strings.Contains(f, "split: focus\nheld: c\n") || !strings.Contains(f, "mode: grid") {
		t.Fatalf("after the drop to the Focus split; layout.yaml:\n%s", f)
	}
	back := aa8aRestart(t, tui, []string{"a", "b", "c", "d"})
	if got := liveFocusJoined(liveFocusPlan(back)); got != "c,a,b,d" {
		t.Errorf("plan %s, want c held, then every other pane", got)
	}
	// The negative twin: nothing changed, nothing written.
	os.Remove(layoutPath(back.projectRoot))
	back.saveLayoutIfConfigured()
	os.Remove(layoutPath(back.projectRoot))
	back.saveIfSplitMoved()
	if _, err := os.Stat(layoutPath(back.projectRoot)); err == nil {
		t.Error("saveIfSplitMoved wrote the file with nothing changed")
	}
}

// ── the stored format ─────────────────────────────────────────────────

var aa8aKeys = []string{"super", "eng1", "eng2", "qa1"}

// aa8aGoldenInputs are the states the goldens were written from, by
// SaveLayout at v2.25.4 (generated in a v2.25.4 worktree for this bead).
var aa8aGoldenInputs = map[string]LayoutState{
	"grid":  {Mode: LayoutGrid, GridCols: 2, GridRows: 2, GridExplicit: true, Order: []string{"eng1", "eng2", "qa1", "super"}, Groups: []string{"eng", "qa"}, GroupOf: map[string]string{"eng1": "eng", "eng2": "eng", "qa1": "qa"}},
	"live":  {Mode: LayoutLive, GridCols: 3, GridRows: 2, LiveAuto: true, Order: []string{"super", "eng1", "eng2", "qa1"}},
	"split": {Mode: Layout2Col, GridCols: 2, GridRows: 2, Order: []string{"qa1", "super", "eng1", "eng2"}},
}

// Criterion 5: a file written by v2.25.4 loads to exactly the LayoutState
// v2.25.4 loaded from it (recorded there), field for field; and a layout with
// no split is still saved byte for byte as v2.25.4 saved it.
func TestAa8a_AV2254FileLoadsAsItDidThere(t *testing.T) {
	empty := map[string]bool{}
	want := map[string]LayoutState{
		"grid": {Mode: LayoutGrid, GridCols: 2, GridRows: 2, Focused: "super", Hidden: empty, Protected: empty,
			Order: []string{"eng1", "eng2", "qa1", "super"}, Overlay: true, GridExplicit: true,
			Groups: []string{"eng", "qa"}, GroupOf: map[string]string{"eng1": "eng", "eng2": "eng", "qa1": "qa"}},
		"live": {Mode: LayoutLive, GridCols: 3, GridRows: 2, Focused: "super", Hidden: empty, Protected: empty,
			Order: []string{"super", "eng1", "eng2", "qa1"}, Overlay: true, LiveAuto: true},
		"split": {Mode: Layout2Col, GridCols: 2, GridRows: 2, Focused: "super", Hidden: empty, Protected: empty,
			Order: []string{"qa1", "super", "eng1", "eng2"}, Overlay: true},
	}
	for name, w := range want {
		golden, err := os.ReadFile(filepath.Join("testdata", "layout-v2.25.4-"+name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		os.MkdirAll(layoutDir(root), 0o700)
		os.WriteFile(layoutPath(root), golden, 0o600)
		got, ok := LoadLayout(root, aa8aKeys)
		if !ok || !reflect.DeepEqual(got, w) {
			t.Errorf("%s: loaded (ok=%v)\n%#v\nwant, as v2.25.4 loaded it,\n%#v", name, ok, got, w)
		}

		out := t.TempDir()
		if err := SaveLayout(out, aa8aGoldenInputs[name]); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(layoutPath(out)); string(b) != string(golden) {
			t.Errorf("%s: saved\n%s\nwant v2.25.4's bytes\n%s", name, b, golden)
		}
	}
}

// persistentLayoutV2254 is PersistentLayout exactly as v2.25.4 declares it:
// the reader an older initech brings to a newer file.
type persistentLayoutV2254 struct {
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
}

// Criterion 6: an older initech decodes a file with split and held without
// error and gets the layout underneath.
func TestAa8a_AnOlderReaderGetsTheLayoutUnderneath(t *testing.T) {
	tui := aa8aFleet(t)
	tui.layoutState.Focused = "b"
	altShiftF(tui)
	tui.saveIfSplitMoved()
	data := aa8aFile(t, tui)
	if !strings.Contains(data, "split: live") {
		t.Fatalf("fixture: no split key in\n%s", data)
	}
	var old persistentLayoutV2254
	if err := yaml.Unmarshal([]byte(data), &old); err != nil {
		t.Fatalf("an older reader rejects the file: %v", err)
	}
	if old.Mode != "grid" || old.Grid != "4x1" || !old.GridExplicit {
		t.Errorf("an older reader got mode %q grid %q explicit %v, want the layout underneath: grid 4x1 explicit",
			old.Mode, old.Grid, old.GridExplicit)
	}
}

// Feed it nothing: an empty file, and a file with only the new keys, load
// without a panic to defaults.
func TestAa8a_AnEmptyOrKeysOnlyFileLoadsToDefaults(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(layoutDir(root), 0o700)
	os.WriteFile(layoutPath(root), nil, 0o600)
	if _, ok := LoadLayout(root, aa8aKeys); ok {
		t.Error("an empty file loaded as a layout")
	}
	os.WriteFile(layoutPath(root), []byte("split: live\nheld: eng1\n"), 0o600)
	got, ok := LoadLayout(root, aa8aKeys)
	if !ok || got.Mode != LayoutGrid || got.GridCols*got.GridRows < len(aa8aKeys) {
		t.Errorf("keys only: loaded ok=%v mode %v grid %dx%d, want a default grid", ok, got.Mode, got.GridCols, got.GridRows)
	}
	if got.Split != splitLive || got.Held != "eng1" {
		t.Errorf("keys only: split %q held %q, want them reported for Run to restore", got.Split, got.Held)
	}
}
