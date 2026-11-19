package tui

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

// ini-vag8: the agents box's last line, whenever any agent is hidden, counts
// the hidden ones and the idle ones among them, names the idle ones, and
// names the one way back that exists in that window (pm R2, panel 3 of
// docs/pictures/initech-window2-agents-box-mock.png).

// vag8Panel3 is the mock's rig: eng1 and eng2 shown; eng3 hidden and
// working, qa1 hidden holding a bead, eng4 and eng5 hidden and idle with no
// bead.
func vag8Panel3() []panelAgent {
	ag := func(name string, st ActivityState, hidden bool, bead string) panelAgent {
		return panelAgent{Name: name, Key: name, State: st, Alive: true, Hidden: hidden, Bead: bead, BeadStatus: "in_progress"}
	}
	return []panelAgent{
		ag("eng1", StateRunning, false, "ini-v9ww"),
		ag("eng2", StateIdle, false, ""),
		ag("eng3", StateRunning, true, "ini-z4l3"),
		ag("qa1", StateIdle, true, "ini-slqv"),
		ag("eng4", StateIdle, true, ""),
		ag("eng5", StateIdle, true, ""),
	}
}

// vag8Box draws the panel for agents in a window of the given width and
// returns its rows and the box's last inner row.
func vag8Box(t *testing.T, window string, width int, agents []panelAgent) (rows []string, last string) {
	t.Helper()
	tui, s := panelWindow(t, width, 40)
	tui.windowID = window
	s.Clear()
	tui.drawRigPanel(panelNow, agents, nil)
	s.Show()
	for _, r := range overlayScreenRows(s)[:tui.planTop] {
		rows = append(rows, string(r))
	}
	return rows, rows[tui.planTop-2]
}

// boxCell is the agents box's part of a row: between its two side borders.
func boxCell(row string) string {
	r := []rune(row)
	end := len(r)
	for i := 1; i < len(r); i++ {
		if r[i] == '│' {
			end = i
			break
		}
	}
	return strings.TrimSpace(string(r[1:end]))
}

// AC3: four hidden in window 2, two of them idle with no bead.
func TestVag8Box_FourHiddenTwoIdle_LastLineCountsNamesAndTheWay(t *testing.T) {
	rows, last := vag8Box(t, "window-2", 200, vag8Panel3())
	want := "4 hidden, 2 of them idle (eng4, eng5): show them from the main window's Agents panel (Option+a there)"
	if got := boxCell(last); got != want {
		t.Errorf("last line = %q, want %q\n%s", got, want, strings.Join(rows, "\n"))
	}
	all := strings.Join(rows, "\n")
	for _, gone := range []string{"dot", "click", "command bar"} {
		if strings.Contains(all, gone) {
			t.Errorf("the panel points at %q:\n%s", gone, all)
		}
	}
	// The working and holding hidden agents keep their rows, the idle ones
	// with no bead have none (R3).
	for _, r := range []string{"● eng3 " + hiddenBoxMarker, "○ qa1 " + hiddenBoxMarker} {
		if !strings.Contains(all, r) {
			t.Errorf("no row %q:\n%s", r, all)
		}
	}
	if strings.Contains(all, "● eng4") || strings.Contains(all, "○ eng4") {
		t.Errorf("eng4, idle with no bead, got a row:\n%s", all)
	}
}

// Names past what fits become +N, and the way back is never cut.
func TestVag8Box_NamesPastTheWidthBecomePlusN(t *testing.T) {
	agents := []panelAgent{{Name: "eng1", Key: "eng1", State: StateRunning, Alive: true, Bead: "ini-a"}}
	for i := 0; i < 12; i++ {
		n := fmt.Sprintf("engineer-%02d", i)
		agents = append(agents, panelAgent{Name: n, Key: n, State: StateIdle, Alive: true, Hidden: true})
	}
	rows, last := vag8Box(t, "window-2", 160, agents)
	got := boxCell(last)
	if !strings.HasPrefix(got, "12 hidden, 12 of them idle (engineer-00, ") || !strings.Contains(got, ", +") ||
		!strings.HasSuffix(got, "(Option+a there)") {
		t.Errorf("last line = %q\n%s", got, strings.Join(rows, "\n"))
	}
	if limit := 160 - 1 - panelMovedMinWidth - 4; len([]rune(got)) > limit {
		t.Errorf("the line is %d wide, past %d", len([]rune(got)), limit)
	}
	if !strings.Contains(strings.Join(rows, "\n"), "moved 30m") {
		t.Errorf("the line pushed the moved box out:\n%s", strings.Join(rows, "\n"))
	}
}

// AC4: one hidden working agent: its row and dot agree with the line, which
// counts it hidden and not idle.
func TestVag8Box_OneHiddenWorkingAgent_RowDotAndLineAgree(t *testing.T) {
	agents := []panelAgent{
		{Name: "eng1", Key: "eng1", State: StateRunning, Alive: true, Hidden: true, Bead: "ini-a", BeadStatus: "in_progress"},
		{Name: "eng2", Key: "eng2", State: StateIdle, Alive: true},
	}
	rows, last := vag8Box(t, "window-2", 200, agents)
	all := strings.Join(rows, "\n")
	if !strings.Contains(all, "● eng1 "+hiddenBoxMarker) || !strings.Contains(all, "running") {
		t.Errorf("eng1 has no running row with its dot:\n%s", all)
	}
	if got, want := boxCell(last), "1 hidden: show them from the main window's Agents panel (Option+a there)"; got != want {
		t.Errorf("last line = %q, want %q", got, want)
	}
}

// Window 1 has its own Agents panel: the line names it without "main window".
func TestVag8Box_WindowOneNamesItsOwnAgentsPanel(t *testing.T) {
	_, last := vag8Box(t, WindowOne, 200, vag8Panel3())
	if got, want := boxCell(last), "4 hidden, 2 of them idle (eng4, eng5): show them from the Agents panel (Option+a)"; got != want {
		t.Errorf("last line = %q, want %q", got, want)
	}
}

// Nothing hidden, no line: the last row stays an agent row.
func TestVag8Box_NothingHidden_NoLine(t *testing.T) {
	agents := vag8Panel3()
	for i := range agents {
		agents[i].Hidden = false
	}
	rows, _ := vag8Box(t, "window-2", 200, agents)
	if all := strings.Join(rows, "\n"); strings.Contains(all, "hidden") || strings.Contains(all, "Agents panel") {
		t.Errorf("a line with nothing hidden:\n%s", all)
	}
}

// AC5: the idle hidden names are exactly the hidden agents status --json
// reports idle, holding no bead.
func TestVag8Box_IdleNamesEqualStatusJSON(t *testing.T) {
	w2, owned := vag8AllHidden(t)
	vag8Working(w2, owned[0])
	agents, _ := w2.panelSnapshot(panelNow)
	working, holding, _ := w2.panelSections(agents, panelNow)
	hidden, idle := panelHiddenIdle(agents, working, holding)
	var want []string
	for _, p := range w2.panes {
		if w2.layoutState.Hidden[agentKey(p)] && p.Activity().String() == "idle" && p.BeadID() == "" &&
			ownedSet(owned)[agentKey(p)] {
			want = append(want, paneDisplayName(p))
		}
	}
	if len(want) == 0 || len(want) == len(owned) {
		t.Fatalf("fixture: %d of %d idle; the check needs some idle and some not", len(want), len(owned))
	}
	if hidden != len(owned) || strings.Join(idle, ",") != strings.Join(want, ",") {
		t.Errorf("hidden %d idle %v, want %d and %v", hidden, idle, len(owned), want)
	}
}

// AC2's pin (not reproduced, kept as a guard): a hidden agent in window 2
// that prints reads working, with its row and dot. Two writes through a real
// stream: net.Pipe's second Write returns only once the read loop has come
// back for more, so the first chunk's state is set, with no wait.
func TestVag8Box_HiddenPrintingAgentInWindowTwo_ReadsWorkingWithRowAndDot(t *testing.T) {
	tui, s := panelWindow(t, 160, 40)
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close() })
	rp := NewRemotePane("eng1", WindowOnePeerName, client, nil, 40, 10)
	rp.Start()
	for i := 0; i < 2; i++ {
		if _, err := server.Write([]byte("tick\r\n")); err != nil {
			t.Fatal(err)
		}
	}
	tui.windowID = "window-2"
	tui.panes = []PaneView{rp}
	tui.ownershipServed = true
	tui.paneOwnership = map[string]string{agentKey(rp): "window-2"}
	tui.layoutState.Hidden = map[string]bool{agentKey(rp): true}
	s.Clear()
	tui.renderRigPanel(panelNow)
	s.Show()
	var rows []string
	for _, r := range overlayScreenRows(s)[:tui.planTop] {
		rows = append(rows, string(r))
	}
	all := strings.Join(rows, "\n")
	if !strings.Contains(all, "agents · 1 working") || !strings.Contains(all, "● eng1 "+hiddenBoxMarker+"  running") {
		t.Fatalf("a hidden printing agent does not read working with its row:\n%s", all)
	}
	dot := false
	for _, d := range tui.overlayBounds.dots {
		dot = dot || d.key == agentKey(rp)
	}
	if !dot {
		t.Error("its row has no dot")
	}
}
