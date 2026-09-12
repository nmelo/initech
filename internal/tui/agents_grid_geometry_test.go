// agents_grid_geometry_test.go pins the transposed one-walk geometry
// (ini-w771): groups are columns, a band is as tall as its tallest group,
// column-rows wrap at gridMaxPerRow with one blank line between, and empty
// groups reserve a landing slot.
package tui

import (
	"fmt"
	"testing"
)

func unitMembers(sizes map[string]int, groups []string) map[string][]int {
	members := make(map[string][]int)
	next := 0
	for _, g := range groups {
		for i := 0; i < sizes[g]; i++ {
			members[g] = append(members[g], next)
			next++
		}
		if sizes[g] == 0 {
			members[g] = nil
		}
	}
	return members
}

// A group is a column: its cells share one x and occupy consecutive rows;
// the next group starts one cell width to the right; headers sit on one
// row above at each column's x.
func TestAgentsGridWalk_GroupIsAColumn(t *testing.T) {
	groups := []string{"core", "eng", "qa"}
	members := unitMembers(map[string]int{"core": 2, "eng": 3, "qa": 1}, groups)
	geo := agentsGridWalk(members, untieredTiers(groups), false, 0, 0, 3)

	// Rhythm: y=1 origin, blank lead, header at 2, gap, cells from 4.
	if len(geo.headers) != 3 {
		t.Fatalf("headers = %d, want 3", len(geo.headers))
	}
	for i, h := range geo.headers {
		if h.x != i*gridCellW || h.y != 2 || h.label != groups[i] {
			t.Errorf("header %d = %+v, want x=%d y=2 label=%s", i, h, i*gridCellW, groups[i])
		}
	}
	byPane := map[int]gridCell{}
	for _, c := range geo.cells {
		byPane[c.paneIdx] = c
	}
	want := []struct{ pane, x, y, line, col int }{
		{0, 0, 4, 0, 0}, {1, 0, 5, 1, 0},
		{2, gridCellW, 4, 0, 1}, {3, gridCellW, 5, 1, 1}, {4, gridCellW, 6, 2, 1},
		{5, 2 * gridCellW, 4, 0, 2},
	}
	for _, w := range want {
		c := byPane[w.pane]
		if c.x != w.x || c.y != w.y || c.line != w.line || c.col != w.col {
			t.Errorf("pane %d cell = x%d y%d line%d col%d, want x%d y%d line%d col%d",
				w.pane, c.x, c.y, c.line, c.col, w.x, w.y, w.line, w.col)
		}
	}
	// Band height = tallest group (3): lead + header + gap + 3 rows.
	if geo.contentLines != 6 {
		t.Errorf("contentLines = %d, want 6", geo.contentLines)
	}
	if len(geo.lines) != 3 {
		t.Errorf("cell lines = %d, want 3 (tallest column)", len(geo.lines))
	}
}

// Band height follows the tallest group at 1..23 agents, never the group
// count: a 1-agent group beside an n-agent group still yields n rows.
func TestAgentsGridWalk_BandHeightIsTallestGroup_1To23(t *testing.T) {
	for n := 1; n <= 23; n++ {
		groups := []string{"big", "one"}
		members := unitMembers(map[string]int{"big": n, "one": 1}, groups)
		geo := agentsGridWalk(members, untieredTiers(groups), false, 0, 0, 2)
		if len(geo.lines) != n {
			t.Errorf("n=%d: cell lines = %d, want %d", n, len(geo.lines), n)
		}
		if geo.contentLines != 3+n {
			t.Errorf("n=%d: contentLines = %d, want %d", n, geo.contentLines, 3+n)
		}
		if len(geo.cells) != n+1 {
			t.Errorf("n=%d: cells = %d, want %d", n, len(geo.cells), n+1)
		}
	}
}

// Columns per row = the group count, capped at gridMaxPerRow; beyond the
// cap the remaining groups start a second column-row inside the band with
// exactly one blank line between (spec rule 5, operator-kept 2026-09-12).
func TestAgentsGridColumnsPerRow_CapsAt6AndWraps_1To8Groups(t *testing.T) {
	for n := 1; n <= 8; n++ {
		n := n
		t.Run(fmt.Sprintf("groups=%d", n), func(t *testing.T) {
			groups := make([]string, n)
			sizes := map[string]int{}
			for i := range groups {
				groups[i] = fmt.Sprintf("g%d", i)
				sizes[groups[i]] = 1
			}
			members := unitMembers(sizes, groups)
			tiers := untieredTiers(groups)

			wantPerRow := n
			if wantPerRow > gridMaxPerRow {
				wantPerRow = gridMaxPerRow
			}
			perRow := agentsGridColumnsPerRow(tiers, 1000)
			if perRow != wantPerRow {
				t.Fatalf("perRow = %d, want %d", perRow, wantPerRow)
			}
			geo := agentsGridWalk(members, tiers, false, 0, 0, perRow)
			if len(geo.headers) != n {
				t.Fatalf("headers = %d, want %d", len(geo.headers), n)
			}
			wantColRows := (n + perRow - 1) / perRow
			if len(geo.lines) != wantColRows {
				t.Errorf("cell lines = %d, want %d (one row per column-row of 1-agent groups)", len(geo.lines), wantColRows)
			}
			for i, h := range geo.headers {
				if h.x != (i%perRow)*gridCellW {
					t.Errorf("header %d x = %d, want %d", i, h.x, (i%perRow)*gridCellW)
				}
			}
			for _, c := range geo.cells {
				if c.col < 0 || c.col >= perRow {
					t.Errorf("cell col %d outside [0,%d)", c.col, perRow)
				}
			}
			if n > gridMaxPerRow {
				// First column-row: header y=2, cells y=4. Second: blank at
				// 5, header at 6, cells at 8.
				second := geo.headers[gridMaxPerRow]
				if second.y != 6 {
					t.Errorf("second column-row header y = %d, want 6 (one blank line after the first column-row's cells)", second.y)
				}
				last := agentsCellForPane(geo.cells, n-1)
				if last == nil || last.y != 8 || last.line != 1 || last.colRow != 1 {
					t.Errorf("wrapped cell = %+v, want y=8 line=1 colRow=1", last)
				}
			}
		})
	}
}

// TestAgentsGridColumnsPerRow_WrapThresholdIsSix pins the operator's decided
// wrap threshold (ini-w771, "KEEP the column-wrap default") to the literal
// value 6, independent of gridMaxPerRow itself.
//
// TestAgentsGridColumnsPerRow_CapsAt6AndWraps_1To8Groups derives its own
// expected value FROM gridMaxPerRow ("wantPerRow = gridMaxPerRow" when
// n > gridMaxPerRow), so it cannot discriminate a change to the constant's
// value: any gridMaxPerRow >= 8 makes that test's own cap unreachable for
// n in [1,8] and it passes vacuously. TestAgentsGridColumnsPerRow_ShrinksForNarrowTerminal
// hardcodes 6 too, but only ever exercises exactly 6 groups, so a widened cap
// is never the binding constraint there either. Neither test can tell 6 from
// 60. This one hardcodes 7 groups and the literal 6 on both sides.
func TestAgentsGridColumnsPerRow_WrapThresholdIsSix(t *testing.T) {
	groups := []string{"a", "b", "c", "d", "e", "f", "g"}
	tiers := untieredTiers(groups)
	if got := agentsGridColumnsPerRow(tiers, 1000); got != 6 {
		t.Fatalf("7 groups on a wide terminal: perRow = %d, want 6 (the operator's decided threshold)", got)
	}
}

// A genuinely narrow terminal shrinks columns below the content cap, never
// below one.
func TestAgentsGridColumnsPerRow_ShrinksForNarrowTerminal(t *testing.T) {
	groups := []string{"a", "b", "c", "d", "e", "f"}
	tiers := untieredTiers(groups)
	if wide := agentsGridColumnsPerRow(tiers, 1000); wide != 6 {
		t.Fatalf("wide: perRow = %d, want 6", wide)
	}
	narrow := agentsGridColumnsPerRow(tiers, 40)
	if narrow >= 6 || narrow < 1 {
		t.Errorf("narrow: perRow = %d, want in [1,6)", narrow)
	}
}

// An empty group is a header over one reserved row: the walk emits no cell
// for it but records a slot so a grabbed agent can land there.
func TestAgentsGridWalk_EmptyGroupReservesOneSlot(t *testing.T) {
	groups := []string{"core", "mkt"}
	members := unitMembers(map[string]int{"core": 1, "mkt": 0}, groups)
	geo := agentsGridWalk(members, untieredTiers(groups), false, 0, 0, 2)
	if len(geo.cells) != 1 {
		t.Fatalf("cells = %d, want 1", len(geo.cells))
	}
	if len(geo.lines) != 1 || len(geo.lines[0].slots) != 2 {
		t.Fatalf("lines = %+v, want one line with two slots", geo.lines)
	}
	slot := geo.lines[0].slots[1]
	if slot.label != "mkt" || !slot.isEmpty || slot.x != gridCellW {
		t.Errorf("empty slot = %+v, want label=mkt isEmpty x=%d", slot, gridCellW)
	}
	if geo.lines[0].slots[0].isEmpty {
		t.Error("core's slot must not read as empty")
	}
}

// Cells are emitted in reading order: column-row, then column, then row.
func TestAgentsGridWalk_CellsInReadingOrder(t *testing.T) {
	groups := []string{"core", "eng"}
	members := map[string][]int{"core": {0, 2}, "eng": {1, 3}}
	geo := agentsGridWalk(members, untieredTiers(groups), false, 0, 0, 2)
	var got []int
	for _, c := range geo.cells {
		got = append(got, c.paneIdx)
	}
	if fmt.Sprint(got) != fmt.Sprint([]int{0, 2, 1, 3}) {
		t.Errorf("cell order = %v, want [0 2 1 3] (core column top-to-bottom, then eng)", got)
	}
}

// A long group label widens every column, so a "─ name ─" header never runs
// into its neighbour's; agent names keep widening it exactly as shipped.
func TestAgentsGridCellW_FitsLongGroupLabel(t *testing.T) {
	panes := []PaneView{testPane("eng1"), testPane("eng2")}
	if got := agentsGridCellWFor(panes, []string{"eng", "core"}); got != gridCellWMin {
		t.Fatalf("short names and labels: cell width = %d, want %d", got, gridCellWMin)
	}
	long := "documentation-review" // 20 runes: "─ " + label + " " + gutter = 24
	if got := agentsGridCellWFor(panes, []string{"eng", long}); got != 24 {
		t.Errorf("long label: cell width = %d, want 24", got)
	}
}
