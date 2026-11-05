package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/rignumbers"
)

// ini-5d3: the Agents overlay has two forms, one content: floating (the box
// over the panes) and pinned (a section across the top of the page, agents
// left and efforts right, the pane grid below it). Shift+Option+s and the
// command "agents pin|float" flip it; layout.yaml remembers it as
// agents_form. Every effort row reads <icon> <id> · <title>.

var ini5d3Names = []string{"p01", "p02", "p03", "p04"}

// ini5d3Window is effortTUI's four panes in a 2x2 grid with the overlay
// shown, saving to a temp project root.
func ini5d3Window(t *testing.T, w, h int) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	tui, s := effortTUI(t)
	s.SetSize(w, h)
	tui.lastW, tui.lastH = w, h
	tui.projectRoot = t.TempDir()
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 2
	tui.layoutState.GridExplicit = true
	tui.layoutState.Overlay = true
	tui.layoutState.AgentsForm = agentsFormFloating
	tui.applyLayout()
	return tui, s
}

var shiftOptionS = func() *tcell.EventKey { return tcell.NewEventKey(tcell.KeyRune, 'S', tcell.ModAlt|tcell.ModShift) }

// ini5d3Frame renders a frame and returns it as text rows, without the
// bottom status row: it carries the wall clock and the battery.
func ini5d3Frame(t *testing.T, tui *TUI, s tcell.SimulationScreen) []string {
	t.Helper()
	s.Clear()
	tui.render()
	rows := screenRows(t, s)
	return rows[:len(rows)-1]
}

// ini5d3Golden compares a frame with testdata/ini5d3/<name>.txt
// (-update-golden rewrites it).
func ini5d3Golden(t *testing.T, name string, rows []string) {
	t.Helper()
	path := filepath.Join("testdata", "ini5d3", name+".txt")
	got := strings.Join(rows, "\n") + "\n"
	if updateGolden() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s: %v (run with -update-golden)", path, err)
	}
	if got != string(want) {
		t.Errorf("frame %s differs from its golden:\n--- got\n%s--- want\n%s", name, got, want)
	}
}

// topPaneY is the smallest row any drawn pane starts on, and the pane height
// of the first pane.
func topPaneY(tui *TUI) (y, h int) {
	y = 1 << 30
	for _, pr := range tui.plan.Panes {
		if pr.Region.Y < y {
			y, h = pr.Region.Y, pr.Region.H
		}
	}
	return y, h
}

// ini5d3Busy keeps p01 and p02 running through render() (fresh output) and
// gives the overlay its effort rows, so a golden frame carries both columns.
func ini5d3Busy(tui *TUI) {
	now := time.Now()
	for _, n := range []string{"p01", "p02"} {
		p := ysr2Pane(tui, n)
		p.mu.Lock()
		p.lastOutputTime = now.Add(time.Hour)
		p.mu.Unlock()
	}
	ysr2Pane(tui, "p01").SetBead("ini-wra.2", "")
	ysr2Pane(tui, "p02").SetBead("ini-abc", "")
	tui.notePaceAnswer(effortFixture(now), true)
	tui.refreshPaceLines(now)
}

// AC1: the key and the command each flip the form and back; the pane grid
// shrinks below the pinned section and grows back (golden frames, 120x40).
func TestAgentsForm_KeyAndCommandFlipTheFormAndBack(t *testing.T) {
	for _, tc := range []struct {
		name      string
		pin, back func(*TUI)
	}{
		{"key", func(u *TUI) { u.handleKey(shiftOptionS()) }, func(u *TUI) { u.handleKey(shiftOptionS()) }},
		{"command", func(u *TUI) { u.execCmd("agents pin") }, func(u *TUI) { u.execCmd("agents float") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tui, s := ini5d3Window(t, 120, 40)
			ini5d3Busy(tui)
			ini5d3Golden(t, "floating_120x40", ini5d3Frame(t, tui, s))
			y0, h0 := topPaneY(tui)
			if y0 != 0 {
				t.Fatalf("floating: the grid starts on row %d, want 0", y0)
			}

			tc.pin(tui)
			if !tui.agentsPinned() {
				t.Fatal("the form did not change to pinned")
			}
			ini5d3Golden(t, "pinned_120x40", ini5d3Frame(t, tui, s))
			y1, h1 := topPaneY(tui)
			if y1 != 1+agentsPinnedRows || h1 >= h0 {
				t.Errorf("pinned: grid starts on row %d with pane height %d, want row %d and less than %d", y1, h1, 1+agentsPinnedRows, h0)
			}

			tc.back(tui)
			if tui.agentsPinned() {
				t.Fatal("the form did not change back to floating")
			}
			ini5d3Golden(t, "floating_120x40", ini5d3Frame(t, tui, s))
			if y2, h2 := topPaneY(tui); y2 != 0 || h2 != h0 {
				t.Errorf("floating again: grid at row %d height %d, want row 0 height %d", y2, h2, h0)
			}
		})
	}
}

// AC1: every terminal encoding of Shift+Option+s pins the overlay, and plain
// Option+s still only shows and hides it.
func TestAgentsForm_EveryTerminalEncodingOfShiftOptionSFlipsTheForm(t *testing.T) {
	for _, tc := range []struct{ terminal, raw string }{
		{"kitty CSI-u (Ghostty)", "\x1b[115;4u"},
		{"xterm modifyOtherKeys", "\x1b[27;4;83~"},
		{"legacy ESC prefix", "\x1bS"},
	} {
		t.Run(tc.terminal, func(t *testing.T) {
			tui, _ := ini5d3Window(t, 120, 40)
			tui.handleKey(decodeTerminalBytes(t, tc.raw))
			if !tui.agentsPinned() || !tui.layoutState.Overlay {
				t.Errorf("%s: pinned=%v overlay=%v, want pinned and shown", tc.terminal, tui.agentsPinned(), tui.layoutState.Overlay)
			}
		})
	}
	tui, _ := ini5d3Window(t, 120, 40)
	tui.handleKey(decodeTerminalBytes(t, "\x1bs"))
	if tui.agentsPinned() || tui.layoutState.Overlay {
		t.Errorf("Option+s: pinned=%v overlay=%v, want it only hidden", tui.agentsPinned(), tui.layoutState.Overlay)
	}
}

// AC1: pinning a hidden overlay shows it; Option+s then hides the section
// and the grid takes the whole page back.
func TestAgentsForm_OptionSHidesThePinnedSectionAndTheGridGrows(t *testing.T) {
	tui, s := ini5d3Window(t, 120, 40)
	tui.layoutState.Overlay = false
	tui.execCmd("agents pin")
	if !tui.layoutState.Overlay {
		t.Fatal("agents pin left the overlay hidden")
	}
	if rows := ini5d3Frame(t, tui, s); !strings.Contains(rows[0], " Agents ") {
		t.Fatalf("pinned: row 0 reads %q, want the header", rows[0])
	}
	if y, _ := topPaneY(tui); y != 1+agentsPinnedRows {
		t.Fatalf("pinned: grid on row %d", y)
	}
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModAlt))
	rows := ini5d3Frame(t, tui, s)
	if y, _ := topPaneY(tui); y != 0 {
		t.Errorf("hidden: grid on row %d, want 0", y)
	}
	if strings.Contains(rows[0], " Agents ") {
		t.Errorf("hidden: row 0 still reads %q", rows[0])
	}
}

// pinnedFleet is twelve running agents on a tall screen: more than the
// section's eight rows.
func pinnedFleet(t *testing.T, w, h int) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	var names []string
	for i := 1; i <= 12; i++ {
		names = append(names, fmt.Sprintf("a%02d", i))
	}
	tui, s := newTestTUIWithScreen(names...)
	s.SetSize(w, h)
	tui.lastW, tui.lastH = w, h
	for _, n := range names {
		setPaneState(ysr2Pane(tui, n), StateRunning)
	}
	tui.layoutState.Overlay = true
	tui.layoutState.AgentsForm = agentsFormPinned
	tui.applyLayout()
	return tui, s
}

// pinnedFrame draws the pinned section alone over a cleared screen: render()
// would recompute the fixture's states from PTY output and read them idle.
func pinnedFrame(t *testing.T, tui *TUI, s tcell.SimulationScreen) []string {
	t.Helper()
	s.Clear()
	tui.renderPinnedAgents()
	s.Show()
	return screenRows(t, s)
}

// AC2: two columns, agents left, at most eight rows plus the header, the
// longer column scrolling within them under the wheel.
func TestAgentsForm_PinnedIsEightRowsPlusHeaderAndScrolls(t *testing.T) {
	tui, s := pinnedFleet(t, 120, 40)
	rows := pinnedFrame(t, tui, s)
	if !strings.Contains(rows[0], " Agents ") {
		t.Fatalf("row 0 is not the header: %q", rows[0])
	}
	for i := 1; i <= agentsPinnedRows; i++ {
		if want := fmt.Sprintf("a%02d", i); !strings.Contains(rows[i], want) {
			t.Errorf("row %d reads %q, want %s", i, rows[i], want)
		}
		if !strings.Contains(rows[i], "│") {
			t.Errorf("row %d has no column divider: %q", i, rows[i])
		}
	}
	// Row 9 is the first row below the section: a ninth agent would land there.
	if strings.Contains(strings.Join(rows[:2+agentsPinnedRows], "\n"), "a09") {
		t.Error("the ninth agent is drawn: the column is not cut to eight rows")
	}
	if y, _ := topPaneY(tui); y != 1+agentsPinnedRows {
		t.Errorf("grid on row %d, want %d", y, 1+agentsPinnedRows)
	}
	// The wheel over the agents column scrolls it; it stops at the last
	// full window.
	for i := 0; i < 10; i++ {
		tui.handleMouse(tcell.NewEventMouse(3, 4, tcell.WheelDown, 0))
	}
	rows = pinnedFrame(t, tui, s)
	if !strings.Contains(rows[1], "a05") || !strings.Contains(rows[agentsPinnedRows], "a12") {
		t.Errorf("scrolled to the end: rows 1 and 8 read %q and %q, want a05 and a12", rows[1], rows[agentsPinnedRows])
	}
	tui.handleMouse(tcell.NewEventMouse(3, 4, tcell.WheelUp, 0))
	rows = pinnedFrame(t, tui, s)
	if !strings.Contains(rows[1], "a04") {
		t.Errorf("one up: row 1 reads %q, want a04", rows[1])
	}
}

// AC2: under 30 rows each column is one summary line ending with +N; back
// at 30 or more the columns return.
func TestAgentsForm_ShortTerminalFoldsEachColumnToOneLine(t *testing.T) {
	tui, s := pinnedFleet(t, 120, 24)
	rows := pinnedFrame(t, tui, s)
	ini5d3Golden(t, "pinned_short_120x24", rows)
	left := strings.SplitN(rows[1], "│", 2)[0]
	if !strings.HasPrefix(strings.TrimSpace(left), "12 agents: a01 running") || !strings.Contains(left, " +") {
		t.Errorf("short: the agents line reads %q, want the count, the first that fit and +N", left)
	}
	if y, _ := topPaneY(tui); y != 2 {
		t.Errorf("short: grid on row %d, want 2 (header and one line)", y)
	}
	s.SetSize(120, 40)
	tui.applyLayout()
	rows = pinnedFrame(t, tui, s)
	if y, _ := topPaneY(tui); y != 1+agentsPinnedRows || !strings.Contains(rows[2], "a02") {
		t.Errorf("tall again: grid on row %d, row 2 %q; want the columns back", y, rows[2])
	}
}

// AC3: the form survives a restart through layout.yaml, and the key writes
// it at once.
func TestAgentsForm_SurvivesARestartThroughLayoutYAML(t *testing.T) {
	tui, _ := ini5d3Window(t, 120, 40)
	tui.handleKey(shiftOptionS())
	b, err := os.ReadFile(layoutPath(tui.projectRoot))
	if err != nil || !strings.Contains(string(b), "agents_form: pinned") {
		t.Fatalf("after the key the file reads %q (%v), want agents_form: pinned", b, err)
	}
	ls, ok := LoadLayout(tui.projectRoot, ini5d3Names)
	if !ok || ls.AgentsForm != agentsFormPinned {
		t.Fatalf("reload: ok=%v form=%q, want pinned", ok, ls.AgentsForm)
	}
	w1 := listFleet(t, ini5d3Names...)
	w1.layoutState = ls
	w1.adoptLoadedAgentsForm(false)
	if !w1.agentsPinned() {
		t.Error("window 1 lost the saved pinned form on adopt")
	}
	tui.handleKey(shiftOptionS())
	if ls, _ := LoadLayout(tui.projectRoot, ini5d3Names); ls.AgentsForm != agentsFormFloating {
		t.Errorf("floating again: the file reads %q", ls.AgentsForm)
	}
}

// AC3, feed it nothing: a file without the field, an empty value and a bad
// value all read floating; only the bad value logs, once.
func TestAgentsForm_AbsentOrBadValueReadsFloating(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		logs       bool
	}{
		{"absent", "", false},
		{"empty", "agents_form: \"\"\n", false},
		{"bad", "agents_form: sideways\n", true},
		{"cased", "agents_form: Pinned\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLogs(t)
			root := t.TempDir()
			os.MkdirAll(layoutDir(root), 0o700)
			os.WriteFile(layoutPath(root), []byte("grid: 2x2\nmode: grid\n"+tc.line), 0o600)
			ls, ok := LoadLayout(root, ini5d3Names)
			if !ok || ls.AgentsForm != agentsFormFloating {
				t.Fatalf("ok=%v form=%q, want floating", ok, ls.AgentsForm)
			}
			n := strings.Count(buf.String(), "agents_form is neither floating nor pinned")
			if tc.logs && n != 1 || !tc.logs && n != 0 {
				t.Errorf("%d log lines, want %v:\n%s", n, tc.logs, buf.String())
			}
		})
	}
}

// AC3, the migration: a file written before the field loads floating, and
// the next save writes the field.
func TestAgentsForm_AFileWithoutTheFieldGetsItOnTheNextSave(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(layoutDir(root), 0o700)
	os.WriteFile(layoutPath(root), []byte("grid: 2x2\nmode: grid\noverlay: true\n"), 0o600)
	ls, ok := LoadLayout(root, ini5d3Names)
	if !ok {
		t.Fatal("the old file did not load")
	}
	if err := SaveLayout(root, ls); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(layoutPath(root)); !strings.Contains(string(b), "agents_form: floating") {
		t.Errorf("after the save the file reads:\n%s", b)
	}
}

// AC3: every layout writer keeps the field. SaveLayout writes it for both
// forms; LoadLayout's heal write-back (a window-alias group_of key) keeps a
// pinned value as it found it.
func TestAgentsForm_EveryLayoutWriterKeepsTheField(t *testing.T) {
	for _, form := range []string{agentsFormFloating, agentsFormPinned} {
		root := t.TempDir()
		st := DefaultLayoutState(ini5d3Names)
		st.AgentsForm = form
		if err := SaveLayout(root, st); err != nil {
			t.Fatal(err)
		}
		if ls, _ := LoadLayout(root, ini5d3Names); ls.AgentsForm != form {
			t.Errorf("SaveLayout round trip: %q came back %q", form, ls.AgentsForm)
		}
	}
	root := t.TempDir()
	os.MkdirAll(layoutDir(root), 0o700)
	os.WriteFile(layoutPath(root), []byte("grid: 2x2\nmode: grid\ngroups: [eng]\ngroup_of:\n  window1:p01: eng\nagents_form: pinned\n"), 0o600)
	if ls, _ := LoadLayout(root, ini5d3Names); ls.AgentsForm != agentsFormPinned {
		t.Fatalf("heal load: form %q", ls.AgentsForm)
	}
	b, _ := os.ReadFile(layoutPath(root))
	if strings.Contains(string(b), "window1:") {
		t.Fatalf("the heal did not write back, so this test proves nothing:\n%s", b)
	}
	if !strings.Contains(string(b), "agents_form: pinned") {
		t.Errorf("the heal write-back dropped the field:\n%s", b)
	}
}

// AC3: a second window starts floating whatever window 1 saved, toggles in
// its own session, and writes nothing.
func TestAgentsForm_ASecondWindowKeepsItsOwnAndWritesNothing(t *testing.T) {
	root := t.TempDir()
	st := DefaultLayoutState(ini5d3Names)
	st.AgentsForm = agentsFormPinned
	SaveLayout(root, st)
	before, _ := os.ReadFile(layoutPath(root))
	ls, _ := LoadLayout(root, ini5d3Names)
	viewer := listFleet(t, ini5d3Names...)
	viewer.projectRoot = root
	viewer.windowID = "window-2"
	viewer.layoutState = ls
	viewer.adoptLoadedAgentsForm(true)
	if viewer.agentsPinned() {
		t.Error("a second window started pinned from window 1's file")
	}
	viewer.handleKey(shiftOptionS())
	if !viewer.agentsPinned() {
		t.Error("the key did not pin the second window's overlay")
	}
	viewer.execCmd("agents float")
	if after, _ := os.ReadFile(layoutPath(root)); string(after) != string(before) {
		t.Errorf("a second window rewrote layout.yaml:\n%s", after)
	}
}

// AC6: one write per change. Frames and a repeated choice write nothing.
func TestAgentsForm_OneWritePerChangeAndNoneFromFrames(t *testing.T) {
	tui, s := ini5d3Window(t, 120, 40)
	path := layoutPath(tui.projectRoot)
	tui.execCmd("agents pin")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the change wrote nothing: %v", err)
	}
	os.Remove(path)
	for i := 0; i < 5; i++ {
		ini5d3Frame(t, tui, s)
	}
	tui.execCmd("agents pin")
	if _, err := os.Stat(path); err == nil {
		t.Error("frames or a repeated agents pin wrote layout.yaml")
	}
	tui.execCmd("agents float")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the change back wrote nothing: %v", err)
	}
}

// The command refuses a word it does not know, and changes nothing.
func TestAgentsForm_CommandRefusesAnUnknownWord(t *testing.T) {
	tui, _ := ini5d3Window(t, 120, 40)
	tui.execCmd("agents sideways")
	if tui.agentsPinned() || !strings.Contains(tui.cmd.error, "agents [pin|float]") {
		t.Errorf("pinned=%v error=%q", tui.agentsPinned(), tui.cmd.error)
	}
}

// ini5d3Titled is effortTUI with ini-abc carrying a long bd title.
func ini5d3Titled(t *testing.T, w, h int, form string) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	tui, s := ini5d3Window(t, w, h)
	now := time.Now()
	ysr2Pane(tui, "p01").SetBead("ini-wra.2", "")
	ysr2Pane(tui, "p02").SetBead("ini-abc", "")
	beads := effortFixture(now)
	for i := range beads {
		switch beads[i].ID {
		case "ini-abc":
			beads[i].Title = "Agents modal: a pinned form across the top of the page, toggled by one key, remembered"
		case "ini-wra":
			beads[i].Title = "short"
		}
	}
	tui.notePaceAnswer(beads, true)
	tui.refreshPaceLines(now)
	tui.layoutState.AgentsForm = form
	tui.applyLayout()
	return tui, s
}

// AC4: each effort row reads icon, id, a middle dot and the title cut to the
// column with an ellipsis, never wrapped; a golden row per form. In the
// floating box the title never widens it.
func TestAgentsForm_EffortRowsCarryTheTitleCutToTheColumn(t *testing.T) {
	plain, ps := ini5d3Window(t, 120, 40)
	ysr2Pane(plain, "p02").SetBead("ini-abc", "")
	now := time.Now()
	plain.notePaceAnswer(effortFixture(now), true)
	plain.refreshPaceLines(now)
	ini5d3Frame(t, plain, ps)
	plainW := plain.overlayBounds.xEnd - plain.overlayBounds.x

	for _, form := range []string{agentsFormFloating, agentsFormPinned} {
		t.Run(form, func(t *testing.T) {
			tui, s := ini5d3Titled(t, 120, 40, form)
			rows := ini5d3Frame(t, tui, s)
			var abc string
			for _, r := range rows {
				if i := strings.Index(r, "▶ ini-abc · "); i >= 0 {
					abc = r[i:]
					break
				}
			}
			if abc == "" {
				t.Fatalf("no ini-abc row with a title:\n%s", strings.Join(rows, "\n"))
			}
			ini5d3Golden(t, "effort_row_"+form, []string{strings.TrimRight(strings.Split(abc, "│")[0], " ")})
			if !strings.Contains(abc, "…") || !strings.Contains(abc, "0/1 · 0/h") {
				t.Errorf("row %q: want the title cut with an ellipsis and the counts kept", abc)
			}
			if !strings.Contains(strings.Join(rows, "\n"), "○ ini-wra · short 2/4") {
				t.Error("a short title is not drawn whole")
			}
			if form == agentsFormFloating {
				if w := tui.overlayBounds.xEnd - tui.overlayBounds.x; w != plainW {
					t.Errorf("titles widened the box from %d to %d", plainW, w)
				}
			}
		})
	}
}

// AC4, unit: the title fits the width or is left out; an untitled row, a
// row with no id and the rig row are unchanged.
func TestTitledEffortRow_FitsOrLeavesTheTitleOut(t *testing.T) {
	titles := map[string]string{"ini-abc": "a long title here", "ini-x": "t"}
	for _, tc := range []struct {
		line  string
		width int
		want  string
	}{
		{"▶ ini-abc 0/1 · 0/h", 80, "▶ ini-abc · a long title here 0/1 · 0/h"},
		{"▶ ini-abc 0/1 · 0/h", 30, "▶ ini-abc · a long… 0/1 · 0/h"},
		{"ini-abc 0/1", 15, "ini-abc 0/1"}, // room for one rune only: left out
		{"ini-abc 0/1", 16, "ini-abc · a… 0/1"},
		{"ini-zzz 0/1", 80, "ini-zzz 0/1"},
		{"rig: 3 closed", 80, "rig: 3 closed"},
		{"", 80, ""},
	} {
		if got := titledEffortRow(tc.line, titles, tc.width); got != tc.want {
			t.Errorf("titledEffortRow(%q, %d) = %q, want %q", tc.line, tc.width, got, tc.want)
		}
		if got := titledEffortRow(tc.line, titles, tc.width); len([]rune(got)) > max(tc.width, len([]rune(tc.line))) {
			t.Errorf("%q wider than %d", got, tc.width)
		}
	}
}

// AC5: the rig numbers row is the last line of the pinned section.
func TestAgentsForm_RigRowIsThePinnedSectionsLastLine(t *testing.T) {
	for _, h := range []int{40, 24} {
		tui, s := ini5d3Window(t, 120, h)
		tui.rig.numbers = ini5d3Rig()
		tui.layoutState.AgentsForm = agentsFormPinned
		rows := ini5d3Frame(t, tui, s)
		last := tui.planTop - 1
		if !strings.Contains(rows[last], rigRowPrefix) {
			t.Errorf("h=%d: the section's last line %d reads %q, want the rig row", h, last, rows[last])
		}
	}
}

// ini5d3Rig is a rig numbers result with one count, enough for its row.
func ini5d3Rig() *rignumbers.Numbers {
	merges := 3
	return &rignumbers.Numbers{At: time.Now(), Repo: true, MergesPerHour: &merges}
}

// The needs-input list draws below the pinned section, not under it.
func TestAgentsForm_NeedsInputListDrawsBelowThePinnedSection(t *testing.T) {
	for _, form := range []string{agentsFormFloating, agentsFormPinned} {
		tui, s := ini5d3Window(t, 120, 40)
		tui.layoutState.AgentsForm = form
		tui.applyLayout()
		ysr2Pane(tui, "p04").SetWaitingInput("Do you want to proceed?")
		// The list, then the overlay over it, in render's order.
		s.Clear()
		tui.renderAttention()
		if tui.agentsPinned() {
			tui.renderPinnedAgents()
		} else {
			tui.renderOverlay()
		}
		s.Show()
		rows := screenRows(t, s)
		at := -1
		for y, r := range rows {
			if strings.Contains(r, "p04") && strings.Contains(r, "proceed") {
				at = y
				break
			}
		}
		if at < 0 {
			t.Fatalf("%s: the needs-input list is not on screen:\n%s", form, strings.Join(rows, "\n"))
		}
		if form == agentsFormPinned && at <= tui.planTop {
			t.Errorf("pinned: the list's row is %d, inside the %d-row section", at, tui.planTop)
		}
	}
}
