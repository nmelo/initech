package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-b0po: the agents box shows who works, then who holds a bead idle, then
// one count. Every instant is fixed; nothing waits.

// boxPanel is a panel TUI that keeps its memory between draws, so a test can
// draw it again at a later instant.
type boxPanel struct {
	tui *TUI
	scr tcell.SimulationScreen
}

func newBoxPanel(t *testing.T, w, boxRows int) *boxPanel {
	t.Helper()
	tui := newTestTUI()
	tui.projectName = "initech"
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(scr.Fini)
	scr.SetSize(w, 40)
	tui.screen = scr
	tui.planTop = 1 + 1 + boxRows + 1
	return &boxPanel{tui: tui, scr: scr}
}

// draw draws the panel at now and returns its rows, the title row first.
func (b *boxPanel) draw(now time.Time, agents []panelAgent) []string {
	b.scr.Clear()
	b.tui.drawRigPanel(now, agents, nil)
	b.scr.Show()
	cells, cw, _ := b.scr.GetContents()
	var rows []string
	for y := 0; y < b.tui.planTop; y++ {
		var sb strings.Builder
		for x := 0; x < cw; x++ {
			c := cells[y*cw+x]
			if len(c.Runes) > 0 {
				sb.WriteRune(c.Runes[0])
			} else {
				sb.WriteRune(' ')
			}
		}
		rows = append(rows, strings.TrimRight(sb.String(), " "))
	}
	return rows
}

// rowOf is the index of the first box row holding name as a whole word.
func rowOf(rows []string, name string) int {
	for i, r := range rows[1:] {
		for _, f := range strings.Fields(r) {
			if f == name {
				return i + 1
			}
		}
	}
	return -1
}

var boxRoster = []panelAgent{
	{Name: "super", Key: "super", Alive: true, State: StateRunning, Since: ago(5 * time.Minute), Bead: "ini-a1", BeadStatus: "in_progress", Title: "fix the send path"},
	{Name: "eng2", Key: "eng2", Alive: true, State: StateBackground, Since: ago(30 * time.Minute)},
	{Name: "eng1", Key: "eng1", Alive: true, State: StateIdle, Since: ago(40 * time.Minute), Bead: "ini-b2", BeadStatus: "in_progress"},
	{Name: "qa1", Key: "qa1", Alive: true, State: StateIdle, Since: ago(10 * time.Minute), Bead: "ini-c3", BeadStatus: "in_progress", Stuck: true},
	{Name: "eng4", Key: "eng4", Alive: true, State: StateWaitingInput, Since: ago(3 * time.Minute), Bead: "ini-d4", BeadStatus: "in_progress"},
	{Name: "pm", Key: "pm", Alive: true, State: StateIdle, Since: ago(2 * time.Minute)},
	{Name: "qa2", Key: "qa2", Alive: true, Suspended: true, State: StateSuspended},
	{Name: "eng9", Key: "eng9", State: StateDead},
}

// TestAgentsBox_WorkingThenHoldingThenOneCount is AC1: section 1 working now,
// longest first; section 2 every bead holder idle, at a prompt or stuck,
// marked, longest first; one count line; the box title counts both sections.
func TestAgentsBox_WorkingThenHoldingThenOneCount(t *testing.T) {
	rows := newBoxPanel(t, 160, 8).draw(panelNow, boxRoster)
	t.Logf("\n%s", strings.Join(rows, "\n"))
	if !strings.Contains(rows[1], "agents · 2 working · 3 holding") {
		t.Fatalf("box title %q", rows[1])
	}
	want := []struct{ name, word, bead string }{
		{"eng2", "background", "—"},
		{"super", "running", "ini-a1"},
		{"eng1", "idle", "ini-b2"},
		{"qa1", "stuck", "ini-c3"},
		{"eng4", "prompt", "ini-d4"},
	}
	for i, w := range want {
		r := rows[2+i]
		if rowOf(rows, w.name) != 2+i || !strings.Contains(r, " "+w.word+" ") || !strings.Contains(r, w.bead) {
			t.Fatalf("box row %d = %q, want %s %s %s", i, r, w.name, w.word, w.bead)
		}
	}
	if !strings.Contains(rows[2+1], "fix the send path") {
		t.Fatalf("super's row %q lacks its bead's title", rows[3])
	}
	if !strings.Contains(rows[7], "1 idle · 1 suspended") {
		t.Fatalf("count line %q", rows[7])
	}
	for _, r := range rows {
		if strings.Contains(r, "needs attention") || strings.Contains(r, "more") ||
			rowOf([]string{"", r}, "pm") > 0 || rowOf([]string{"", r}, "qa2") > 0 || rowOf([]string{"", r}, "eng9") > 0 {
			t.Fatalf("row %q: an idle, suspended or dead agent drawn by name, or an old label", r)
		}
	}
}

// TestAgentsBox_NoBeadHolderIsHiddenBehindTheCount is AC2, the must-not: at
// the smallest heights and a narrow width, every idle agent holding a bead has
// its row with its bead, and the count line carries no name and no bead.
func TestAgentsBox_NoBeadHolderIsHiddenBehindTheCount(t *testing.T) {
	var roster []panelAgent
	for i, n := range []string{"eng1", "eng2", "eng3"} {
		roster = append(roster, panelAgent{Name: n, Key: n, Alive: true, State: StateRunning, Since: ago(time.Duration(i+1) * time.Minute), Bead: "ini-w" + n[3:], BeadStatus: "in_progress"})
	}
	holders := []string{"qa1", "qa2", "qa3", "pm"}
	for i, n := range holders {
		roster = append(roster, panelAgent{Name: n, Key: n, Alive: true, State: StateIdle, Since: ago(time.Duration(i+10) * time.Minute), Bead: "ini-h" + n, BeadStatus: "in_progress"})
	}
	roster = append(roster,
		panelAgent{Name: "eng7", Key: "eng7", Alive: true, State: StateIdle, Since: ago(time.Minute)},
		panelAgent{Name: "eng8", Key: "eng8", Alive: true, State: StateIdle, Since: ago(time.Minute)},
		panelAgent{Name: "eng9", Key: "eng9", Alive: true, Suspended: true, State: StateSuspended})
	names := []string{"eng1", "eng2", "eng3", "qa1", "qa2", "qa3", "pm", "eng7", "eng8", "eng9"}
	// The smallest box above the one-row fold is panel_rows' minimum, 3.
	for _, size := range []struct{ w, rows int }{{60, 3}, {80, 3}, {160, 3}, {160, 8}} {
		rows := newBoxPanel(t, size.w, size.rows).draw(panelNow, roster)
		t.Logf("%dx%d\n%s", size.w, size.rows, strings.Join(rows, "\n"))
		for _, h := range holders {
			i := rowOf(rows, h)
			if i < 0 || !strings.Contains(rows[i], "ini-h"+h) {
				t.Fatalf("%dx%d: bead holder %s hidden or without its bead", size.w, size.rows, h)
			}
		}
		tail := ""
		for _, r := range rows {
			if j := strings.Index(r, "2 idle · 1 suspended"); j >= 0 {
				if k := strings.LastIndex(r[:j], "+"); k >= 0 && strings.HasSuffix(strings.TrimSpace(r[k:j]), "working ·") {
					j = k
				}
				tail = r[j:]
			}
		}
		if tail == "" {
			t.Fatalf("%dx%d: no count line", size.w, size.rows)
		}
		for _, n := range names {
			if rowOf([]string{"", tail}, n) > 0 || strings.Contains(tail, "ini-") {
				t.Fatalf("%dx%d: the count line %q carries a name or a bead", size.w, size.rows, tail)
			}
		}
	}
	// The one-row fold (under 30 screen rows) keeps section 2's first row in
	// preference to section 1's.
	rows := newBoxPanel(t, 60, 1).draw(panelNow, roster)
	t.Logf("60x1\n%s", strings.Join(rows, "\n"))
	if i := rowOf(rows, "pm"); i != 2 || !strings.Contains(rows[2], "ini-hpm") || rowOf(rows, "eng1") > 0 {
		t.Fatalf("one row: want section 2's first holder (pm, longest idle), not a working agent:\n%s", strings.Join(rows, "\n"))
	}
}

// TestAgentsBox_TheOrderHoldsUntilASettledChange is AC3: a draw with no state
// change keeps the order; a change that settles re-sorts.
func TestAgentsBox_TheOrderHoldsUntilASettledChange(t *testing.T) {
	b := newBoxPanel(t, 160, 8)
	a := panelAgent{Name: "alpha", Key: "alpha", Alive: true, State: StateRunning, Since: ago(10 * time.Minute)}
	z := panelAgent{Name: "zulu", Key: "zulu", Alive: true, State: StateRunning, Since: ago(5 * time.Minute)}
	order := func(rows []string) bool { return rowOf(rows, "alpha") < rowOf(rows, "zulu") }
	if rows := b.draw(panelNow, []panelAgent{z, a}); !order(rows) {
		t.Fatalf("longest running first:\n%s", strings.Join(rows, "\n"))
	}
	if rows := b.draw(panelNow.Add(10*time.Second), []panelAgent{z, a}); !order(rows) {
		t.Fatal("a tick with no change re-sorted")
	}
	// alpha goes to the background; not settled at 20s, so nothing moves.
	a.State, a.Since = StateBackground, panelNow.Add(15*time.Second)
	if rows := b.draw(panelNow.Add(20*time.Second), []panelAgent{z, a}); !order(rows) || !strings.Contains(rows[rowOf(rows, "alpha")], "running") {
		t.Fatalf("an unsettled change moved alpha:\n%s", strings.Join(rows, "\n"))
	}
	// Settled after 60s: alpha's background began at +15s, so zulu is now
	// the longest in its state.
	rows := b.draw(panelNow.Add(80*time.Second), []panelAgent{z, a})
	if order(rows) || !strings.Contains(rows[rowOf(rows, "alpha")], "background") {
		t.Fatalf("a settled change did not re-sort:\n%s", strings.Join(rows, "\n"))
	}
}

// TestAgentsBox_TimeInStateSurvivesRedrawsAndFlicker is AC4: the time grows
// across redraws, a flicker under a minute does not reset it, a change that
// holds a minute does and counts from when it began, and a prompt settles at
// once.
func TestAgentsBox_TimeInStateSurvivesRedrawsAndFlicker(t *testing.T) {
	b := newBoxPanel(t, 160, 8)
	a := panelAgent{Name: "eng1", Key: "eng1", Alive: true, State: StateIdle, Since: ago(40 * time.Minute), Bead: "ini-b2", BeadStatus: "in_progress"}
	cell := func(rows []string, word, age string) bool {
		i := rowOf(rows, "eng1")
		return i > 0 && strings.Contains(rows[i], " "+word+" ") && strings.Contains(rows[i], " "+age+" ")
	}
	if rows := b.draw(panelNow, []panelAgent{a}); !cell(rows, "idle", "40m") {
		t.Fatalf("first draw:\n%s", strings.Join(rows, "\n"))
	}
	// A 5-second run between two idles: the raw state flickers.
	a.State, a.Since = StateRunning, panelNow.Add(115*time.Second)
	if rows := b.draw(panelNow.Add(2*time.Minute), []panelAgent{a}); !cell(rows, "idle", "42m") {
		t.Fatalf("a flicker reset the time:\n%s", strings.Join(rows, "\n"))
	}
	a.State, a.Since = StateIdle, panelNow.Add(2*time.Minute)
	if rows := b.draw(panelNow.Add(3*time.Minute), []panelAgent{a}); !cell(rows, "idle", "43m") {
		t.Fatalf("back to idle, the time was not kept:\n%s", strings.Join(rows, "\n"))
	}
	// Running from +3m, held past a minute: settled, counted from +3m.
	a.State, a.Since = StateRunning, panelNow.Add(3*time.Minute)
	b.draw(panelNow.Add(3*time.Minute+30*time.Second), []panelAgent{a})
	if rows := b.draw(panelNow.Add(5*time.Minute), []panelAgent{a}); !cell(rows, "running", "2m") {
		t.Fatalf("a settled change:\n%s", strings.Join(rows, "\n"))
	}
	// A prompt settles at once.
	a.State, a.Since = StateWaitingInput, panelNow.Add(5*time.Minute)
	if rows := b.draw(panelNow.Add(5*time.Minute), []panelAgent{a}); !cell(rows, "prompt", "0m") {
		t.Fatalf("a prompt waited to settle:\n%s", strings.Join(rows, "\n"))
	}
}

// TestAgentsBox_TheRealSnapshotMarksAStuckHolder is reachability: through
// render, an idle agent whose bead the pace rule reads stuck is drawn in
// section 2 as stuck, with its bead.
func TestAgentsBox_TheRealSnapshotMarksAStuckHolder(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.layoutState.Overlay = true
	tui.panes[0].(*Pane).SetBead("ini-st1", "")
	tui.paceStuck = map[string]bool{"eng1": true}
	tui.render()
	var rows []string
	for _, r := range overlayScreenRows(s) {
		rows = append(rows, string(r))
	}
	i := rowOf(append([]string{""}, rows...), "eng1") - 1
	if i < 0 || !strings.Contains(rows[i], " stuck ") || !strings.Contains(rows[i], "ini-st1") {
		t.Fatalf("eng1 not drawn stuck with its bead:\n%s", strings.Join(rows, "\n"))
	}
}
