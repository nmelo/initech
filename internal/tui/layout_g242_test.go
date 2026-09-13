package tui

// ini-g242: a fleet over 40 agents lost its groups, membership, order and mode
// on every other restart. LoadLayout recomputed the grid counting hidden agents
// as visible (autoGrid(41) = 4x11), that grid was saved, and the next load's
// parseGrid rejected rows > 10 -- and a rejected grid discarded the WHOLE file.

import (
	"fmt"
	"os"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func g242Names(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("agent%02d", i+1)
	}
	return names
}

func g242WriteLayout(t *testing.T, root string, pl PersistentLayout) {
	t.Helper()
	data, err := yaml.Marshal(&pl)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layoutPath(root), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// g242Arrangement is six hand-built groups, a custom order and mode main over
// 41 agents -- the operator's shape on hover.
func g242Arrangement(names []string) PersistentLayout {
	groups := []string{"core", "engops", "backend", "frontend", "qa", "ops"}
	groupOf := map[string]string{}
	for i, n := range names {
		groupOf[n] = groups[i%len(groups)]
	}
	order := make([]string, len(names))
	for i := range names {
		order[i] = names[len(names)-1-i] // reversed: not initech.yaml order
	}
	return PersistentLayout{Grid: "4x11", Mode: "main", Order: order, Groups: groups, GroupOf: groupOf}
}

// THE REGRESSION THAT BIT: a file carrying "4x11" must load with everything
// else in it intact. Only the grid may be recomputed.
func TestLoadLayout_AnUnreadableGridKeepsGroupsMembershipOrderAndMode(t *testing.T) {
	root := inboxRoot(t)
	names := g242Names(41)
	saved := g242Arrangement(names)
	g242WriteLayout(t, root, saved)

	got, ok := LoadLayout(root, names)
	if !ok {
		t.Fatal(`LoadLayout DISCARDED THE WHOLE FILE because its grid read "4x11".

Groups, membership, order and mode were all valid; only the grid was not. The
operator lost six hand-built groups this way on every other restart (ini-g242).`)
	}
	if !reflect.DeepEqual(got.Groups, saved.Groups) {
		t.Errorf("groups = %v, want %v", got.Groups, saved.Groups)
	}
	for _, n := range names {
		if got.GroupOf[n] != saved.GroupOf[n] {
			t.Errorf("%s is in group %q, want %q", n, got.GroupOf[n], saved.GroupOf[n])
			break
		}
	}
	if !reflect.DeepEqual(got.Order, saved.Order) {
		t.Errorf("order was not kept: got %v", got.Order[:5])
	}
	if got.Mode != stringToLayoutMode("main") {
		t.Errorf("mode = %v, want main", got.Mode)
	}
	if _, _, ok := parseGrid(fmt.Sprintf("%dx%d", got.GridCols, got.GridRows), len(names)); !ok {
		t.Errorf("the recomputed grid %dx%d is itself unreadable", got.GridCols, got.GridRows)
	}
}

// Save -> load -> save -> load -> save on a 41-agent fleet: every load keeps the
// arrangement and the file does not change between saves. The every-other-
// restart shape needs TWO cycles to show.
func TestLayoutRoundTrip_A41AgentFleetSurvivesRepeatedRestarts(t *testing.T) {
	root := inboxRoot(t)
	names := g242Names(41)
	g242WriteLayout(t, root, g242Arrangement(names))

	var prev []byte
	for cycle := 1; cycle <= 3; cycle++ {
		state, ok := LoadLayout(root, names)
		if !ok {
			t.Fatalf("restart %d: LoadLayout discarded the layout file", cycle)
		}
		if len(state.Groups) != 6 {
			t.Fatalf("restart %d: %d groups survived, want 6", cycle, len(state.Groups))
		}
		if err := SaveLayout(root, state); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(layoutPath(root))
		if err != nil {
			t.Fatal(err)
		}
		if prev != nil && string(prev) != string(data) {
			t.Fatalf("restart %d changed the saved file:\nbefore:\n%s\nafter:\n%s", cycle, prev, data)
		}
		prev = data
	}
}

// No grid the process computes may be one it cannot read back.
func TestAutoGrid_EveryResultIsReadableByTheLoader(t *testing.T) {
	for n := 1; n <= 200; n++ {
		c, r := autoGrid(n)
		if _, _, ok := parseGrid(fmt.Sprintf("%dx%d", c, r), n); !ok {
			t.Errorf("autoGrid(%d) = %dx%d, which parseGrid rejects: saved, it discards the layout on the next load", n, c, r)
		}
	}
}

// The save path is the last line: whatever state reaches it, the file holds a
// grid the loader accepts.
func TestSaveLayout_NeverWritesAGridTheLoaderRejects(t *testing.T) {
	root := inboxRoot(t)
	state := DefaultLayoutState(g242Names(41))
	state.GridCols, state.GridRows = 4, 11
	if err := SaveLayout(root, state); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(layoutPath(root))
	var pl PersistentLayout
	if err := yaml.Unmarshal(data, &pl); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := parseGrid(pl.Grid, 41); !ok {
		t.Errorf("SaveLayout wrote grid %q, which the next load rejects", pl.Grid)
	}
}

// Startup sizes a loaded grid from the panes that will render, in mode main
// too. recalcGrid alone skipped non-grid modes, which is how 4x11 survived
// startup and was saved.
func TestStartup_ALoadedGridIsSizedFromVisiblePanesInMainMode(t *testing.T) {
	names := g242Names(41)
	var panes []PaneView
	hidden := map[string]bool{}
	for i, n := range names {
		panes = append(panes, &mockPaneView{name: n, alive: true})
		if i < 26 {
			hidden[n] = true
		}
	}
	tui := &TUI{panes: panes, layoutState: LayoutState{
		Mode: stringToLayoutMode("main"), GridCols: 4, GridRows: 11, Hidden: hidden,
	}}

	tui.sizeLoadedGridToVisible()

	wantC, wantR := autoGrid(15)
	if tui.layoutState.GridCols != wantC || tui.layoutState.GridRows != wantR {
		t.Errorf("grid is %dx%d, want %dx%d: 26 of 41 are hidden, so 15 render",
			tui.layoutState.GridCols, tui.layoutState.GridRows, wantC, wantR)
	}
}

func TestStartup_AnExplicitOrLiveGridIsLeftAlone(t *testing.T) {
	panes := []PaneView{&mockPaneView{name: "a", alive: true}, &mockPaneView{name: "b", alive: true}}
	for _, ls := range []LayoutState{
		{Mode: stringToLayoutMode("main"), GridCols: 3, GridRows: 3, GridExplicit: true},
		{Mode: LayoutLive, GridCols: 3, GridRows: 3},
	} {
		tui := &TUI{panes: panes, layoutState: ls}
		tui.sizeLoadedGridToVisible()
		if tui.layoutState.GridCols != 3 || tui.layoutState.GridRows != 3 {
			t.Errorf("mode %v explicit=%v: grid changed to %dx%d; the operator's CxR and live's viewport are not recomputed",
				ls.Mode, ls.GridExplicit, tui.layoutState.GridCols, tui.layoutState.GridRows)
		}
	}
}
