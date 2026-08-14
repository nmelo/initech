package tui

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

// Tests for ini-9ka.5: monitor-tier rendering + the three tier interactions.

// tierTUI builds a TUI with a real project root (so the assignment store
// persists), tiers gated on or off, and the standard three seeded bands.
func tierTUI(t *testing.T, tiersActive bool, names ...string) (*TUI, string) {
	t.Helper()
	tui, _ := newTestTUIWithScreen(names...)
	root := t.TempDir()
	tui.projectRoot = root
	tui.project = &config.Project{Name: "test", Root: root}
	if tiersActive {
		tui.project.WindowListen = "127.0.0.1:7400"
	}
	tui.ensureGroups(false)
	tui.agents.selected = 0
	return tui, root
}

// selectAgent points the modal selection at the named agent.
func selectAgent(t *testing.T, tui *TUI, name string) {
	t.Helper()
	for i, p := range tui.panes {
		if p.Name() == name {
			tui.agents.selected = i
			return
		}
	}
	t.Fatalf("agent %q not in fleet", name)
}

// ── gating ──────────────────────────────────────────────────────────

// TestAgentsTiers_GatedOnWindowListenNotAttachCount pins the gating signal:
// tiers render on CONFIGURATION (project.WindowListen), not on how many
// windows happen to be attached. Attach-count gating would make tiers vanish
// during fold-back, exactly when the operator needs to see where groups belong.
func TestAgentsTiers_GatedOnWindowListenNotAttachCount(t *testing.T) {
	single, _ := tierTUI(t, false, "super", "eng1", "qa1")
	if single.agentsTiersActive() {
		t.Error("tiers active with an empty WindowListen -- a single-window fleet must see no tiers")
	}

	multi, _ := tierTUI(t, true, "super", "eng1", "qa1")
	if !multi.agentsTiersActive() {
		t.Error("tiers inactive with WindowListen configured")
	}
	// No window is attached in either case; the signal is configuration only.
}

// TestAgentsTiers_SingleWindowEmitsNoTierGeometry is the structural half of
// the byte-for-byte AC: with tiers off, the walk produces no tier rows at all,
// so the untiered layout is not merely visually equal but geometrically
// identical.
func TestAgentsTiers_SingleWindowEmitsNoTierGeometry(t *testing.T) {
	tui, _ := tierTUI(t, false, "super", "eng1", "qa1")
	sw, sh := tui.screen.Size()
	_, geo := tui.agentsFrameGeometry(sw, sh, false)
	if len(geo.tiers) != 0 {
		t.Errorf("tiers = %v, want none when single-window", geo.tiers)
	}
}

// TestAgentsTiers_RenderShowsMonitorHeaders confirms tier headers appear, with
// the spec's double rule, when more than one window is configured.
func TestAgentsTiers_RenderShowsMonitorHeaders(t *testing.T) {
	tui, _ := tierTUI(t, true, "super", "eng1", "qa1")
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("eng", "window-2"); err != nil {
		t.Fatal(err)
	}
	tui.renderAgentsGrid()

	out := screenText(t, tui)
	if !strings.Contains(out, "══ monitor 1") {
		t.Errorf("missing monitor 1 tier header:\n%s", out)
	}
	if !strings.Contains(out, "══ monitor 2") {
		t.Errorf("missing monitor 2 tier header:\n%s", out)
	}
}

// screenText dumps the simulation screen as text.
func screenText(t *testing.T, tui *TUI) string {
	t.Helper()
	s := tui.screen
	w, h := s.Size()
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ch, _, _, _ := s.GetContent(x, y)
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// ── one-walk geometry ───────────────────────────────────────────────

// TestAgentsTiers_RenderedPositionsMatchComputedGeometry is the one-walk AC:
// what is DRAWN must sit exactly where the single geometry computation says.
// This reads the rendered screen back, rather than re-calling the walk -- a
// test that re-called the walk would only prove the walk agrees with itself,
// which is the tautology the AC is written to avoid.
func TestAgentsTiers_RenderedPositionsMatchComputedGeometry(t *testing.T) {
	tui, _ := tierTUI(t, true, "super", "pm", "eng1", "eng2", "qa1", "qa2")
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("qa", "window-2"); err != nil {
		t.Fatal(err)
	}
	tui.renderAgentsGrid()

	sw, sh := tui.screen.Size()
	box, geo := tui.agentsFrameGeometry(sw, sh, false)

	if len(geo.tiers) < 2 {
		t.Fatalf("expected two tiers, got %d", len(geo.tiers))
	}

	// Every computed tier header row must actually carry a tier header.
	for _, tl := range geo.tiers {
		if got := tierRowText(t, tui, tl.y, box.innerX, box.boxW); !strings.HasPrefix(got, "══ monitor") {
			t.Errorf("tier row y=%d reads %q, want a monitor header at the computed position", tl.y, got)
		}
	}
	// Every computed band label row must carry that band's rule.
	for _, bl := range geo.bands {
		want := "─ " + bl.label + " "
		if got := tierRowText(t, tui, bl.y, box.innerX, box.boxW); !strings.HasPrefix(got, want) {
			t.Errorf("band row y=%d reads %q, want prefix %q", bl.y, got, want)
		}
	}
	// Every computed cell must carry its agent's number at its computed x/y.
	for _, c := range geo.cells {
		got := tierRowText(t, tui, c.y, c.x, box.boxW)
		if strings.TrimSpace(got) == "" {
			t.Errorf("cell for pane %d computed at (%d,%d) but that position is blank", c.paneIdx, c.x, c.y)
		}
	}
}

// tierRowText reads count columns of a rendered row starting at x.
func tierRowText(t *testing.T, tui *TUI, y, x, count int) string {
	t.Helper()
	s := tui.screen
	w, _ := s.Size()
	var b strings.Builder
	for i := 0; i < count && x+i < w; i++ {
		ch, _, _, _ := s.GetContent(x+i, y)
		if ch == 0 {
			ch = ' '
		}
		b.WriteRune(ch)
	}
	return b.String()
}

// TestAgentsGridWalk_HeightMatchesPositions guards the specific divergence
// that motivated the consolidation: box height and label positions used to be
// computed by two independent walks. The last drawn row must fit inside the
// content height the same walk reports.
func TestAgentsGridWalk_HeightMatchesPositions(t *testing.T) {
	tui, _ := tierTUI(t, true, "super", "pm", "eng1", "eng2", "eng3", "qa1")
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("eng", "window-2"); err != nil {
		t.Fatal(err)
	}
	sw, sh := tui.screen.Size()
	box, geo := tui.agentsFrameGeometry(sw, sh, false)

	firstY := box.startY + box.searchRows
	lowest := firstY
	for _, c := range geo.cells {
		if c.y > lowest {
			lowest = c.y
		}
	}
	for _, bl := range geo.bands {
		if bl.y > lowest {
			lowest = bl.y
		}
	}
	for _, tl := range geo.tiers {
		if tl.y > lowest {
			lowest = tl.y
		}
	}
	if used := lowest - firstY; used > geo.contentLines {
		t.Errorf("lowest drawn row is %d lines past the origin but contentLines = %d -- height and positions diverged", used, geo.contentLines)
	}
}

// ── interactions ────────────────────────────────────────────────────

// TestAgentsTiers_MoveGroupCyclesWindows covers `m`: the selected agent's
// whole group moves, and cycles when N>2 rather than toggling between two.
func TestAgentsTiers_MoveGroupCyclesWindows(t *testing.T) {
	tui, root := tierTUI(t, true, "super", "eng1", "qa1")
	selectAgent(t, tui, "eng1")

	// First move: eng leaves window 1 for a newly-created window.
	tui.agentsMoveGroupToNextWindow()
	first := tui.agentsAssignment().WindowOfGroup("eng")
	if first == WindowOne {
		t.Fatal("m did not move eng off window 1")
	}

	// Persisted immediately (ini-9ka.4's MoveGroup writes on every call).
	onDisk, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.WindowOfGroup("eng"); got != first {
		t.Errorf("on-disk window for eng = %q, want %q immediately after m", got, first)
	}

	// Keep pressing m: it must eventually return to window 1, i.e. cycle
	// rather than get stuck on the last window.
	seen := map[string]bool{first: true}
	returned := false
	for i := 0; i < 6; i++ {
		tui.agentsMoveGroupToNextWindow()
		w := tui.agentsAssignment().WindowOfGroup("eng")
		seen[w] = true
		if w == WindowOne {
			returned = true
			break
		}
	}
	if !returned {
		t.Errorf("m never cycled eng back to window 1; windows seen: %v", seen)
	}
}

// TestAgentsTiers_MoveGroupInertWhenSingleWindow confirms `m` does nothing on
// a single-window fleet -- the zero-change guarantee covers interactions too,
// not just rendering.
func TestAgentsTiers_MoveGroupInertWhenSingleWindow(t *testing.T) {
	tui, root := tierTUI(t, false, "super", "eng1", "qa1")
	selectAgent(t, tui, "eng1")
	tui.agentsMoveGroupToNextWindow()

	if got := tui.agentsAssignment().WindowOfGroup("eng"); got != WindowOne {
		t.Errorf("m moved eng to %q on a single-window fleet, want no change", got)
	}
	if _, err := LoadAssignment(root, WindowOne); err != nil {
		t.Fatalf("assignment store unreadable: %v", err)
	}
}

// TestAgentsTiers_GrabAcrossTierMovesWindowImplicitly covers the grab model:
// an agent's window follows from its GROUP, so moving it into a group that
// lives on another window changes its window with no per-agent window state.
// That is what keeps "every agent in exactly one window" true by construction.
func TestAgentsTiers_GrabAcrossTierMovesWindowImplicitly(t *testing.T) {
	tui, _ := tierTUI(t, true, "super", "eng1", "qa1")
	assign := tui.agentsAssignment()
	if err := mustAssignWriter(t, assign).MoveGroup("qa", "window-2"); err != nil {
		t.Fatal(err)
	}

	eng1 := agentKey(tui.panes[1])
	if got := assign.WindowOfAgent(eng1, tui.layoutState.GroupOf); got != WindowOne {
		t.Fatalf("eng1 starts on %q, want window 1", got)
	}

	// The grab: eng1's band changes to qa, which lives on window2.
	tui.layoutState.GroupOf[eng1] = "qa"

	if got := assign.WindowOfAgent(eng1, tui.layoutState.GroupOf); got != "window-2" {
		t.Errorf("after grabbing eng1 into qa, its window = %q, want window-2 (window follows group)", got)
	}
}

// ── g-create: the ini-2rc lesson, both halves ───────────────────────

// TestAgentsTiers_CreateGroupLandsOnSelectionWindow is the specified
// g-create rule: the new group is created on the window of the group the
// selection was in at creation time. Exercised with the selection on a
// WINDOW-2 group, which is the case that would silently default to window 1.
func TestAgentsTiers_CreateGroupLandsOnSelectionWindow(t *testing.T) {
	tui, root := tierTUI(t, true, "super", "eng1", "qa1")
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("qa", "window-2"); err != nil {
		t.Fatal(err)
	}
	selectAgent(t, tui, "qa1") // selection sits on a window-2 group

	tui.agentsCreateGroup("triage")

	if got := tui.agentsAssignment().WindowOfGroup("triage"); got != "window-2" {
		t.Errorf("new group landed on %q, want window-2 (the window the selection was in)", got)
	}
	onDisk, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.WindowOfGroup("triage"); got != "window-2" {
		t.Errorf("on-disk window for the new group = %q, want window-2", got)
	}
}

// TestAgentsTiers_CreateGroupOnWindowOneStoresNothing is the other half: a
// group created while the selection is on window 1 must not write a row,
// since window 1 is represented by absence.
func TestAgentsTiers_CreateGroupOnWindowOneStoresNothing(t *testing.T) {
	tui, _ := tierTUI(t, true, "super", "eng1", "qa1")
	selectAgent(t, tui, "super") // core is on window 1

	tui.agentsCreateGroup("triage")

	if got := tui.agentsAssignment().WindowOfGroup("triage"); got != WindowOne {
		t.Errorf("new group landed on %q, want window 1", got)
	}
}

// TestAgentsTiers_EmptyGroupCreatedOnWindowTwoVanishesOnClose is the compound
// sequence flagged as the QA-breaker: g-create while the selection sits on a
// window-2 group, then close with the new group still empty. It must vanish
// exactly as it would on window 1, AND its window assignment must be cleared
// -- a lingering group->window row would resurface the group on window 2 if
// that label were ever recreated.
func TestAgentsTiers_EmptyGroupCreatedOnWindowTwoVanishesOnClose(t *testing.T) {
	tui, root := tierTUI(t, true, "super", "eng1", "qa1")
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("qa", "window-2"); err != nil {
		t.Fatal(err)
	}
	selectAgent(t, tui, "qa1")

	tui.agentsCreateGroup("triage")
	if got := tui.agentsAssignment().WindowOfGroup("triage"); got != "window-2" {
		t.Fatalf("precondition: new group should be on window2, got %q", got)
	}

	// Close with the group still empty.
	tui.agentsPruneEmptyGroups()

	for _, g := range tui.layoutState.Groups {
		if g == "triage" {
			t.Error("empty group created on window 2 survived modal close")
		}
	}
	if got := tui.agentsAssignment().WindowOfGroup("triage"); got != WindowOne {
		t.Errorf("pruned group still assigned to %q -- a stale row would resurface it on recreate", got)
	}
	onDisk, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.WindowOfGroup("triage"); got != WindowOne {
		t.Errorf("on-disk assignment for the pruned group = %q, want cleared", got)
	}
}

// ── search across tiers ─────────────────────────────────────────────

// TestAgentsTiers_SearchDimsInPlaceAcrossTiers confirms v2.5.0 search
// behavior is extended across tiers rather than replaced: non-matching agents
// dim in place, and tier headers stay rendered so the hierarchy remains
// readable while results dim.
func TestAgentsTiers_SearchDimsInPlaceAcrossTiers(t *testing.T) {
	tui, _ := tierTUI(t, true, "super", "eng1", "eng2", "qa1")
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("qa", "window-2"); err != nil {
		t.Fatal(err)
	}
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("qa")
	tui.renderAgentsGrid()

	out := screenText(t, tui)
	// Every agent still occupies its cell (dimmed, not removed).
	for _, name := range []string{"super", "eng1", "eng2", "qa1"} {
		if !strings.Contains(out, name) {
			t.Errorf("agent %q disappeared during search; search must dim in place:\n%s", name, out)
		}
	}
	// Tier headers survive the search.
	if !strings.Contains(out, "══ monitor 1") || !strings.Contains(out, "══ monitor 2") {
		t.Errorf("tier headers lost during search:\n%s", out)
	}
}
