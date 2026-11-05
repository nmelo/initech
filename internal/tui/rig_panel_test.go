package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-fed0: the rig panel's title row and agents box. Every clock here is a
// fixed instant; nothing waits.

var panelNow = time.Date(2026, 11, 5, 10, 10, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return panelNow.Add(-d) }

func TestPanelTitle_CountsEachAlarmWithItsIDs(t *testing.T) {
	beads := []panelBead{
		{ID: "ini-n1", Status: "waiting_on_nelson"},
		{ID: "ini-b1", Status: "blocked"},
		{ID: "ini-s1", Status: "in_progress", LastMoved: ago(25 * time.Minute)},
		{ID: "ini-f1", Status: "in_progress", LastMoved: ago(5 * time.Minute)},
		{ID: "ini-c1", Status: "closed", LastMoved: ago(3 * time.Hour)},
	}
	agents := []panelAgent{
		{Name: "eng4", Alive: true, State: StateWaitingInput},
		{Name: "pm", Alive: true, State: StateRunning},
		{Name: "qa3", Alive: true, Suspended: true, State: StateSuspended},
		{Name: "eng9", Alive: false, State: StateDead},
	}
	title := panelTitleOf(beads, agents, panelNow)
	got := map[string]string{}
	for _, a := range title.Alarms {
		got[a.Label] = strings.Join(a.Names, ",")
	}
	want := map[string]string{"on Nelson": "ini-n1", "blocked": "ini-b1", "stale 20m": "ini-s1", "prompts": "eng4"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if title.Awake != 2 || title.Suspended != 1 {
		t.Fatalf("awake %d suspended %d, want 2 and 1 (the dead one is neither)", title.Awake, title.Suspended)
	}
	line, spans := panelTitleText(title, 0)
	if !strings.HasPrefix(line, "on Nelson 1 ini-n1 · blocked 1 ini-b1 · stale 20m 1 ini-s1 · prompts 1 eng4 · 2 awake · 1 suspended") {
		t.Fatalf("title %q", line)
	}
	if k := spanAt(spans, strings.Index(line, "prompts")); k != "yellow" {
		t.Errorf("prompts is %q, want yellow", k)
	}
	if k := spanAt(spans, 0); k != "red" {
		t.Errorf("a non-zero alarm is %q, want red", k)
	}
}

func spanAt(spans []panelSpan, at int) string {
	for _, s := range spans {
		if at >= s.from && at < s.to {
			return s.kind
		}
	}
	return ""
}

// TestPanelTitle_AZeroIsDimAndTheFoldDropsIDs: nothing to say is said quietly,
// and a narrow screen keeps the counts.
func TestPanelTitle_AZeroIsDimAndTheFoldDropsIDs(t *testing.T) {
	title := panelTitleOf([]panelBead{{ID: "ini-b1", Status: "blocked"}}, nil, panelNow)
	line, spans := panelTitleText(title, 0)
	if k := spanAt(spans, 0); k != "dim" {
		t.Errorf("on Nelson 0 is %q, want dim", k)
	}
	folded, _ := panelTitleText(title, 1)
	if strings.Contains(folded, "ini-b1") || !strings.Contains(folded, "blocked 1") || !strings.Contains(line, "ini-b1") {
		t.Fatalf("full %q folded %q", line, folded)
	}
}

func TestPanelAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second: "0m", 2 * time.Minute: "2m", 26 * time.Minute: "26m",
		64 * time.Minute: "1h04m", 138 * time.Minute: "2h18m",
	} {
		if got := panelAge(ago(d), panelNow); got != want {
			t.Errorf("%s -> %q, want %q", d, got, want)
		}
	}
	if panelAge(time.Time{}, panelNow) != "" {
		t.Error("an unknown start printed a time")
	}
}

// TestPanelColumns_AwakeOnlyInOrderAndTheAttentionRule: suspended and dead
// agents are no rows; a prompt, or a bead held quiet 20 minutes, goes right.
func TestPanelColumns_AwakeOnlyInOrderAndTheAttentionRule(t *testing.T) {
	agents := []panelAgent{
		{Name: "super", Alive: true, State: StateBackground, Since: ago(26 * time.Minute)},              // no bead: working
		{Name: "pm", Alive: true, State: StateRunning, Since: ago(2 * time.Minute), Bead: "ini-p1x"},    // running
		{Name: "eng1", Alive: true, State: StateBackground, Since: ago(72 * time.Minute), Bead: "h.77"}, // quiet with a bead
		{Name: "eng4", Alive: true, State: StateWaitingInput, Since: ago(time.Minute)},                  // prompt
		{Name: "qa2", Alive: true, State: StateIdle, Since: ago(19 * time.Minute), Bead: "h.78"},        // not yet 20m
		{Name: "qa3", Alive: true, Suspended: true, State: StateSuspended},
		{Name: "eng9", State: StateDead},
	}
	working, attention := panelColumns(agents, panelNow)
	names := func(l []panelAgent) string {
		var out []string
		for _, a := range l {
			out = append(out, a.Name)
		}
		return strings.Join(out, ",")
	}
	if names(working) != "super,pm,qa2" || names(attention) != "eng1,eng4" {
		t.Fatalf("working %q attention %q", names(working), names(attention))
	}
}

// TestPanelHold_TheAttentionColumnStaysAMinuteAfterEmptying: no thrash.
func TestPanelHold_TheAttentionColumnStaysAMinuteAfterEmptying(t *testing.T) {
	var h panelHold
	if h.show(false, panelNow) {
		t.Fatal("an attention column that never held anyone is drawn")
	}
	if !h.show(true, panelNow) {
		t.Fatal("a non-empty column is not drawn")
	}
	if !h.show(false, panelNow.Add(59*time.Second)) {
		t.Fatal("the column went within the hold")
	}
	if h.show(false, panelNow.Add(61*time.Second)) {
		t.Fatal("the column stayed past the hold")
	}
}

func TestPanelBeadCell(t *testing.T) {
	if got := panelBeadCell(panelAgent{Bead: "ini-p1x", BeadStatus: "in_progress"}); got != "▶ ini-p1x" {
		t.Fatalf("got %q", got)
	}
	if got := panelBeadCell(panelAgent{}); got != "—" {
		t.Fatalf("no bead got %q", got)
	}
}

// renderPanelCapture draws the panel for agents and beads on a w x 40 screen
// and returns its rows, so a test reads what the operator would.
func renderPanelCapture(t *testing.T, w int, agents []panelAgent, beads []panelBead) []string {
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
	tui.planTop = 1 + 1 + panelDefaultRows + 1
	tui.drawRigPanel(panelNow, agents, beads)
	tui.renderProjectBadge() // Render draws it over row 0 after the panel.
	scr.Show()
	cells, cw, _ := scr.GetContents()
	var rows []string
	for y := 0; y < tui.planTop; y++ {
		var b strings.Builder
		for x := 0; x < cw; x++ {
			c := cells[y*cw+x]
			if len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			} else {
				b.WriteRune(' ')
			}
		}
		rows = append(rows, strings.TrimRight(b.String(), " "))
	}
	return rows
}

var panelQuiet = []panelAgent{
	{Name: "super", Alive: true, State: StateBackground, Since: ago(26 * time.Minute)},
	{Name: "pm", Alive: true, State: StateRunning, Since: ago(2 * time.Minute), Bead: "ini-p1x", BeadStatus: "in_progress"},
	{Name: "eng1", Alive: true, State: StateIdle, Since: ago(64 * time.Minute)},
	{Name: "eng4", Alive: true, State: StateBackground, Since: ago(11 * time.Minute), Bead: "ini-nxyf", BeadStatus: "in_progress"},
	{Name: "qa1", Alive: true, Suspended: true, State: StateSuspended},
}

// TestRigPanel_TheQuietCaptureAt120 is m1-quiet's title row and agents box.
func TestRigPanel_TheQuietCaptureAt120(t *testing.T) {
	rows := renderPanelCapture(t, 120, panelQuiet, nil)
	t.Logf("\n%s", strings.Join(rows, "\n"))
	if !strings.HasPrefix(rows[0], " initech") || !strings.HasSuffix(rows[0], "on Nelson 0 · blocked 0 · stale 20m 0 · prompts 0 · 4 awake · 1 suspended") {
		t.Fatalf("title row %q", rows[0])
	}
	if !strings.Contains(rows[1], "agents · working") || !strings.Contains(rows[1], "moved 30m") {
		t.Fatalf("box labels %q", rows[1])
	}
	want := []string{"○ super  background    26m  —", "● pm     running        2m  ▶ ini-p1x", "○ eng1   idle        1h04m  —", "○ eng4   background    11m  ▶ ini-nxyf"}
	for i, w := range want {
		if !strings.Contains(rows[2+i], w) {
			t.Errorf("row %d = %q, want %q in it", i, rows[2+i], w)
		}
	}
	for _, r := range rows {
		if strings.Contains(r, "qa1") || strings.Contains(r, "needs attention") {
			t.Fatalf("a suspended agent or an empty attention column is drawn: %q", r)
		}
	}
}

// TestRigPanel_NeedsAttentionIsItsOwnColumn is m3-trouble's agents box.
func TestRigPanel_NeedsAttentionIsItsOwnColumn(t *testing.T) {
	agents := []panelAgent{
		{Name: "super", Alive: true, State: StateRunning, Since: ago(time.Minute)},
		{Name: "eng4", Alive: true, State: StateWaitingInput, Since: ago(41 * time.Minute), Bead: "h.76.2", BeadStatus: "in_progress"},
	}
	rows := renderPanelCapture(t, 120, agents, nil)
	t.Logf("\n%s", strings.Join(rows, "\n"))
	if !strings.Contains(rows[1], "needs attention") {
		t.Fatalf("no attention column: %q", rows[1])
	}
	r := rows[2]
	if !(strings.Index(r, "super") < strings.Index(r, "eng4")) || !strings.Contains(r, "prompt") {
		t.Fatalf("row %q: super working on the left, eng4 at its prompt on the right", r)
	}
	if !strings.Contains(rows[0], "prompts 1 eng4") {
		t.Fatalf("title %q", rows[0])
	}
}

// TestRigPanel_NarrowDropsIDsThenTheBeadColumn is the fold order at 60.
func TestRigPanel_NarrowDropsIDsThenTheBeadColumn(t *testing.T) {
	agents := append([]panelAgent(nil), panelQuiet...)
	rows := renderPanelCapture(t, 60, agents, []panelBead{{ID: "ini-b1", Status: "blocked"}})
	t.Logf("\n%s", strings.Join(rows, "\n"))
	if strings.Contains(rows[0], "ini-b1") || !strings.Contains(rows[0], "blocked 1") {
		t.Fatalf("narrow title %q: ids folded, counts kept", rows[0])
	}
	// At 60 even bare counts do not fit: the alarms, the title's job, are
	// kept and the tail gives way.
	if !strings.Contains(rows[0], "on Nelson 0") || !strings.Contains(rows[0], "prompts 0") {
		t.Fatalf("narrow title %q: the alarms were cut", rows[0])
	}
	for _, r := range rows[2:6] {
		if strings.Contains(r, "ini-p1x") || strings.Contains(r, "ini-nxyf") {
			t.Fatalf("narrow agents box keeps the bead column: %q", r)
		}
	}
	if !strings.Contains(rows[3], "pm") || !strings.Contains(rows[3], "running") || !strings.Contains(rows[3], "2m") {
		t.Fatalf("narrow row %q: name, state and time stay", rows[3])
	}
}

// TestRigPanel_At80TheIDsFoldAndEveryCountAndTheBeadColumnStay is AC4 at 80
// columns: the first fold step only.
func TestRigPanel_At80TheIDsFoldAndEveryCountAndTheBeadColumnStay(t *testing.T) {
	rows := renderPanelCapture(t, 80, panelQuiet, []panelBead{{ID: "ini-b1", Status: "blocked"}})
	t.Logf("\n%s", strings.Join(rows, "\n"))
	// The badge and the bare line need 84 columns: the suspended count goes
	// whole, never cut mid-word.
	if strings.Contains(rows[0], "ini-b1") || !strings.HasSuffix(rows[0], "prompts 0 · 4 awake") || !strings.Contains(rows[0], "initech") {
		t.Fatalf("title %q: ids folded, the suspended count left out whole, the rest kept", rows[0])
	}
	if !strings.Contains(rows[3], "▶ ini-p1x") {
		t.Fatalf("row %q: the bead column stays at 80", rows[3])
	}
}

// TestRigPanel_Window2SaysTheMovedBoxIsInWindow1 is AC5: a second window does
// not run the bead poll the moved box is read from, so it says where it is.
func TestRigPanel_Window2SaysTheMovedBoxIsInWindow1(t *testing.T) {
	for _, win := range []string{"", "window-2"} {
		tui := newTestTUI()
		tui.windowID = win
		scr := tcell.NewSimulationScreen("UTF-8")
		if err := scr.Init(); err != nil {
			t.Fatal(err)
		}
		scr.SetSize(120, 40)
		tui.screen = scr
		tui.planTop = 1 + 1 + panelDefaultRows + 1
		tui.drawRigPanel(panelNow, panelQuiet, nil)
		scr.Show()
		cells, cw, _ := scr.GetContents()
		var b strings.Builder
		for y := 0; y < tui.planTop; y++ {
			for x := 0; x < cw; x++ {
				if c := cells[y*cw+x]; len(c.Runes) > 0 {
					b.WriteRune(c.Runes[0])
				}
			}
			b.WriteRune('\n')
		}
		says := strings.Contains(b.String(), "moved 30m is in window 1")
		if win == "" && says {
			t.Error("window 1 says its own moved box is elsewhere")
		}
		if win == "window-2" && !says {
			t.Errorf("window 2 does not say the moved box is in window 1:\n%s", b.String())
		}
		scr.Fini()
	}
}
