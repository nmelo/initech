package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-gt7w: the Agents overlay draws two columns, not working on the left and
// working on the right, each in the Agents panel's band order. Every test here
// reads the RENDERED frame: the claim is about what the operator sees.

// overlayFrameRows renders the overlay and returns the screen as rune rows.
func overlayFrameRows(tui *TUI, s tcell.SimulationScreen) [][]rune {
	tui.layoutState.Overlay = true
	s.Clear() // a frame left from an earlier draw must not read as this one
	tui.renderOverlay()
	return overlayScreenRows(s)
}

// overlayScreenRows returns what is on the screen now, as rune rows.
func overlayScreenRows(s tcell.SimulationScreen) [][]rune {
	w, h := s.Size()
	rows := make([][]rune, h)
	for y := 0; y < h; y++ {
		rows[y] = make([]rune, w)
		for x := 0; x < w; x++ {
			ch, _, _, _ := s.GetContent(x, y)
			if ch == 0 {
				ch = ' '
			}
			rows[y][x] = ch
		}
	}
	return rows
}

// overlayDotOf finds an agent's overlay row in a rendered frame: the cell
// holding its dot, two columns left of its name. The name must stand alone
// (a space before it, a space or a marker after), so eng1 never matches
// eng10. ok is false when the agent is not drawn.
func overlayDotOf(rows [][]rune, name string) (x, y int, ok bool) {
	n := []rune(name)
	for y, row := range rows {
		for x := 2; x+len(n) <= len(row); x++ {
			if string(row[x:x+len(n)]) != name || row[x-1] != ' ' {
				continue
			}
			if x+len(n) < len(row) && row[x+len(n)] != ' ' {
				continue
			}
			if d := row[x-2]; d == '\u25cf' || d == '\u25cb' {
				return x - 2, y, true
			}
		}
	}
	return 0, 0, false
}

// setOverlayActivity sets each named local pane's activity directly.
func setOverlayActivity(t *testing.T, tui *TUI, states map[string]ActivityState) {
	t.Helper()
	for _, p := range tui.panes {
		st, ok := states[p.Name()]
		if !ok {
			continue
		}
		lp := p.(*Pane)
		lp.mu.Lock()
		lp.activity = st
		lp.mu.Unlock()
	}
}

// columnOf returns, for each column x seen, the agents drawn there top to
// bottom. Agents are looked up by name; all must be drawn.
func overlayColumns(t *testing.T, rows [][]rune, names []string) map[int][]string {
	t.Helper()
	type at struct {
		name string
		y    int
	}
	byX := map[int][]at{}
	for _, n := range names {
		x, y, ok := overlayDotOf(rows, n)
		if !ok {
			t.Fatalf("%s is not drawn in the overlay:\n%s", n, framesText(rows))
		}
		byX[x] = append(byX[x], at{n, y})
	}
	out := map[int][]string{}
	for x, list := range byX {
		for i := 1; i < len(list); i++ {
			for j := i; j > 0 && list[j].y < list[j-1].y; j-- {
				list[j], list[j-1] = list[j-1], list[j]
			}
		}
		for _, a := range list {
			out[x] = append(out[x], a.name)
		}
	}
	return out
}

func framesText(rows [][]rune) string {
	var b strings.Builder
	for _, r := range rows {
		if line := strings.TrimRight(string(r), " "); line != "" {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// leftRight splits overlayColumns' result into the left and right columns.
func leftRight(t *testing.T, cols map[int][]string, rows [][]rune) (left, right []string) {
	t.Helper()
	if len(cols) > 2 {
		t.Fatalf("dots in %d columns, want at most 2:\n%s", len(cols), framesText(rows))
	}
	lx, rx := -1, -1
	for x := range cols {
		if lx < 0 || x < lx {
			lx = x
		}
		if x > rx {
			rx = x
		}
	}
	left = cols[lx]
	if rx != lx {
		right = cols[rx]
	}
	return left, right
}

// (a) One agent in each state: suspended, dead, idle and waiting on the left,
// running and background on the right, each column in the panel's order.
// Red on the one-column overlay: every dot shares one column.
func TestOverlayColumns_NotWorkingLeftWorkingRightInPanelOrder(t *testing.T) {
	tui, s := newTestTUIWithScreen("super", "pm", "eng1", "eng2", "qa1", "qa2")
	setOverlayActivity(t, tui, map[string]ActivityState{
		"super": StateSuspended, "pm": StateRunning, "eng1": StateDead,
		"eng2": StateIdle, "qa1": StateWaitingInput, "qa2": StateIdle,
	})
	qa2 := tui.panes[5].(*Pane)
	qa2.observeBackground(time.Now().Add(-90*time.Second), true, parseFooter(lvxwFixture(t, "haiku310_landing_shell.txt"), 160))
	if qa2.Activity() != StateBackground {
		t.Fatalf("fixture: qa2 is %v, want background", qa2.Activity())
	}

	rows := overlayFrameRows(tui, s)
	left, right := leftRight(t, overlayColumns(t, rows, []string{"super", "pm", "eng1", "eng2", "qa1", "qa2"}), rows)
	if got, want := strings.Join(left, ","), "super,eng1,eng2,qa1"; got != want {
		t.Errorf("left column %s, want %s (suspended, dead, idle, waiting in panel order)\n%s", got, want, framesText(rows))
	}
	if got, want := strings.Join(right, ","), "pm,qa2"; got != want {
		t.Errorf("right column %s, want %s (running, background in panel order)\n%s", got, want, framesText(rows))
	}
	_, superY, _ := overlayDotOf(rows, "super")
	_, pmY, _ := overlayDotOf(rows, "pm")
	if superY != tui.overlayBounds.y+1 || pmY != superY {
		t.Errorf("each column starts on the box's first row: super y=%d pm y=%d, want both %d", superY, pmY, tui.overlayBounds.y+1)
	}
	if frame := framesText(rows); !strings.Contains(frame, "background 1m") || !strings.Contains(frame, "waiting") {
		t.Errorf("rows lost their status words:\n%s", frame)
	}
}

// mixedFleet is three bands, each with one working and one idle agent.
func mixedFleet(t *testing.T) (*TUI, tcell.SimulationScreen) {
	tui, s := newTestTUIWithScreen("super", "pm", "eng1", "eng2", "qa1", "qa2")
	setOverlayActivity(t, tui, map[string]ActivityState{
		"super": StateRunning, "pm": StateIdle, "eng1": StateRunning,
		"eng2": StateIdle, "qa1": StateRunning, "qa2": StateIdle,
	})
	return tui, s
}

// (b) The band order is the overlay's order: moving the eng band ahead of core
// reorders both columns the same way, at the next draw.
func TestOverlayColumns_BandOrderReordersBothColumns(t *testing.T) {
	names := []string{"super", "pm", "eng1", "eng2", "qa1", "qa2"}
	for _, c := range []struct {
		groups      []string
		left, right string
	}{
		{[]string{"core", "eng", "qa"}, "pm,eng2,qa2", "super,eng1,qa1"},
		{[]string{"eng", "core", "qa"}, "eng2,pm,qa2", "eng1,super,qa1"},
	} {
		tui, s := mixedFleet(t)
		tui.ensureGroups(false)
		tui.layoutState.Groups = c.groups
		rows := overlayFrameRows(tui, s)
		left, right := leftRight(t, overlayColumns(t, rows, names), rows)
		if strings.Join(left, ",") != c.left || strings.Join(right, ",") != c.right {
			t.Errorf("bands %v: left %v right %v, want %s | %s\n%s", c.groups, left, right, c.left, c.right, framesText(rows))
		}
	}
	// Through the panel's own key: > on the core band moves it after eng.
	tui, s := mixedFleet(t)
	tui.ensureGroups(false)
	tui.agents.selected = 0 // super, in core
	tui.agentsMoveBand(+1)
	rows := overlayFrameRows(tui, s)
	left, right := leftRight(t, overlayColumns(t, rows, names), rows)
	if strings.Join(left, ",") != "eng2,pm,qa2" || strings.Join(right, ",") != "eng1,super,qa1" {
		t.Errorf("after > on core: left %v right %v, want eng2,pm,qa2 | eng1,super,qa1\n%s", left, right, framesText(rows))
	}
}

// (c) The pane slice is not the order: panes listed eng1, qa1, super, eng2
// still draw core, then eng, then qa.
func TestOverlayColumns_FollowTheBandsNotThePaneSlice(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "qa1", "super", "eng2")
	setOverlayActivity(t, tui, map[string]ActivityState{
		"eng1": StateIdle, "qa1": StateIdle, "super": StateIdle, "eng2": StateIdle,
	})
	tui.ensureGroups(false)
	tui.layoutState.Groups = []string{"core", "eng", "qa"}
	rows := overlayFrameRows(tui, s)
	left, right := leftRight(t, overlayColumns(t, rows, []string{"eng1", "qa1", "super", "eng2"}), rows)
	if got := strings.Join(left, ","); got != "super,eng1,eng2,qa1" || len(right) != 0 {
		t.Errorf("left %s right %v, want super,eng1,eng2,qa1 and an empty right column\n%s", got, right, framesText(rows))
	}
}

// (d) Feed it nothing: with no agents the overlay draws what it drew before
// this change, which is nothing, and leaves no clickable dot. The twin: one
// agent draws a box.
func TestOverlayColumns_NoAgentsDrawNothing(t *testing.T) {
	tui, s := newTestTUIWithScreen()
	rows := overlayFrameRows(tui, s)
	if frame := framesText(rows); frame != "" {
		t.Errorf("zero agents drew:\n%s", frame)
	}
	if len(tui.overlayBounds.dots) != 0 {
		t.Errorf("zero agents left %d clickable dots", len(tui.overlayBounds.dots))
	}
	tui, s = newTestTUIWithScreen("eng1")
	if frame := framesText(overlayFrameRows(tui, s)); !strings.Contains(frame, "Agents") || !strings.Contains(frame, "eng1") {
		t.Errorf("one agent drew no box:\n%s", frame)
	}
}

// overlayBoxWidth measures the drawn box on its title row, corner to corner.
func overlayBoxWidth(t *testing.T, rows [][]rune, y int) int {
	t.Helper()
	row := string(rows[y])
	l, r := strings.IndexRune(row, '\u256d'), strings.IndexRune(row, '\u256e')
	if l < 0 || r < 0 {
		t.Fatalf("no box on row %d:\n%s", y, framesText(rows))
	}
	return len([]rune(row[:r])) - len([]rune(row[:l])) + 1
}

// (e) All idle: the right column is blank and the box is exactly as wide as
// when agents are working, so it does not jump sideways.
func TestOverlayColumns_AllIdleKeepsTheWidthWithABlankRightColumn(t *testing.T) {
	names := []string{"super", "pm", "eng1", "eng2", "qa1", "qa2"}
	tui, s := mixedFleet(t)
	mixedRows := overlayFrameRows(tui, s)
	mixedW := overlayBoxWidth(t, mixedRows, tui.overlayBounds.y)
	_, rightCols := leftRight(t, overlayColumns(t, mixedRows, names), mixedRows)
	if len(rightCols) == 0 {
		t.Fatalf("mixed fleet drew no right column:\n%s", framesText(mixedRows))
	}
	rx, _, _ := overlayDotOf(mixedRows, rightCols[0])

	tui, s = newTestTUIWithScreen(names...)
	all := map[string]ActivityState{}
	for _, n := range names {
		all[n] = StateIdle
	}
	setOverlayActivity(t, tui, all)
	idleRows := overlayFrameRows(tui, s)
	if w := overlayBoxWidth(t, idleRows, tui.overlayBounds.y); w != mixedW {
		t.Errorf("all-idle box is %d wide, mixed is %d: the box jumps when the last agent goes idle", w, mixedW)
	}
	for y := tui.overlayBounds.y + 1; y < tui.overlayBounds.y+1+tui.overlayBounds.agentCount; y++ {
		if ch := idleRows[y][rx]; ch != ' ' {
			t.Errorf("right column row %d holds %q, want blank\n%s", y, ch, framesText(idleRows))
		}
	}
	left, right := leftRight(t, overlayColumns(t, idleRows, names), idleRows)
	if len(left) != 6 || len(right) != 0 {
		t.Errorf("all idle: left %v right %v, want all six left", left, right)
	}
}

// stepOverlayClock makes every read of the dot-settle clock a settle period
// later than the last, as real time passes between a draw and a click aimed
// at it. Restored when the test ends.
func stepOverlayClock(t *testing.T) {
	now := time.Now()
	overlayClickClock = func() time.Time {
		now = now.Add(2 * overlayClickSettle)
		return now
	}
	t.Cleanup(func() { overlayClickClock = time.Now })
}

// (f) A click on a dot acts on the agent drawn there, in either column. A
// click one cell off the dot acts on nobody.
func TestOverlayColumns_ClickActsOnTheAgentInEitherColumn(t *testing.T) {
	stepOverlayClock(t)
	tui, s := mixedFleet(t)
	rows := overlayFrameRows(tui, s)
	// click presses and releases, as a real mouse does: a press beside the
	// dot lands on a pane and starts a selection, which only the release ends.
	click := func(x, y int) {
		tui.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
		tui.handleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	}
	for _, name := range []string{"eng1", "pm"} { // right, then left
		x, y, ok := overlayDotOf(rows, name)
		if !ok {
			t.Fatalf("%s not drawn", name)
		}
		click(x+1, y)
		for k, h := range tui.layoutState.Hidden {
			if h {
				t.Fatalf("a click beside %s's dot hid %s", name, k)
			}
		}
		click(x, y)
		for _, other := range []string{"super", "pm", "eng1", "eng2", "qa1", "qa2"} {
			if want := other == name; tui.layoutState.Hidden[other] != want {
				t.Errorf("click on %s's dot: %s hidden=%v, want %v", name, other, tui.layoutState.Hidden[other], want)
			}
		}
		click(x, y) // unhide
		rows = overlayFrameRows(tui, s)
	}
}

// (g) The footer counts the whole window's list and spans the box, below the
// longer column; a hidden agent stays in its state's column, dimmed, [h].
func TestOverlayColumns_FooterCountsTheWholeListBelowTheColumns(t *testing.T) {
	tui, s := mixedFleet(t)
	tui.layoutState.Hidden["eng1"] = true // a running agent, hidden
	rows := overlayFrameRows(tui, s)
	footerY := tui.overlayBounds.y + 1 + tui.overlayBounds.agentCount
	if tui.overlayBounds.agentCount != 3 {
		t.Errorf("list rows %d, want 3 (the longer column)", tui.overlayBounds.agentCount)
	}
	if line := string(rows[footerY]); !strings.Contains(line, " 5 visible, 1 hidden") {
		t.Errorf("footer row %q, want \" 5 visible, 1 hidden\"\n%s", strings.TrimSpace(line), framesText(rows))
	}
	_, right := leftRight(t, overlayColumns(t, rows, []string{"super", "pm", "eng1", "eng2", "qa1", "qa2"}), rows)
	if strings.Join(right, ",") != "super,eng1,qa1" {
		t.Errorf("hidden eng1 left the right column: right %v", right)
	}
	x, y, _ := overlayDotOf(rows, "eng1")
	if !strings.Contains(string(rows[y][x:]), "eng1 [h]") {
		t.Errorf("hidden eng1 row lost its [h] marker: %q", string(rows[y][x:]))
	}
}

// (g) With windows, the scope line stays below the footer and spans the box.
func TestOverlayColumns_ScopeLineStaysBelowTheList(t *testing.T) {
	tui, s := scopedOverlayTUI(t)
	rows := overlayFrameRows(tui, s)
	y := tui.overlayBounds.y + 1 + tui.overlayBounds.agentCount
	if line := string(rows[y]); !strings.Contains(line, "+3") || !strings.Contains(line, "alt+a shows all") {
		t.Errorf("row below the list %q, want the scope line\n%s", strings.TrimSpace(line), framesText(rows))
	}
}

// AC7: a remote machine's agents sit under its divider, split the same way.
func TestOverlayColumns_RemoteMachineSplitsUnderItsDivider(t *testing.T) {
	tui, s := newTestTUIWithScreen("super", "eng1")
	setOverlayActivity(t, tui, map[string]ActivityState{"super": StateIdle, "eng1": StateRunning})
	busy := &RemotePane{name: "intern", host: "workbench", alive: true, activity: StateRunning, lastOut: time.Now()}
	quiet := &RemotePane{name: "arch", host: "workbench", alive: true}
	tui.panes = append(tui.panes, busy, quiet)
	rows := overlayFrameRows(tui, s)

	divY := -1
	for y, r := range rows {
		if strings.Contains(string(r), "\u2500 workbench \u2500") {
			divY = y
		}
	}
	sx, sy, _ := overlayDotOf(rows, "super")
	ex, ey, _ := overlayDotOf(rows, "eng1")
	ix, iy, iok := overlayDotOf(rows, "workbench:intern")
	ax, ay, aok := overlayDotOf(rows, "workbench:arch")
	if divY < 0 || !iok || !aok {
		t.Fatalf("remote block not drawn (divider row %d):\n%s", divY, framesText(rows))
	}
	if !(sy < divY && ey < divY && iy > divY && ay > divY) {
		t.Errorf("rows: super %d eng1 %d divider %d intern %d arch %d, want locals above the divider and the machine's agents below\n%s", sy, ey, divY, iy, ay, framesText(rows))
	}
	if ax != sx || ix != ex || iy != ay {
		t.Errorf("remote split: arch x=%d (want left %d), intern x=%d (want right %d), same row %d/%d\n%s", ax, sx, ix, ex, iy, ay, framesText(rows))
	}
}

// AC9: the box is as wide as the two columns need, with a gap between them.
// The fixture puts the longest status in the left column, so a left row fills
// its column and a right column drawn even one cell early would touch or
// overwrite it.
func TestOverlayColumns_ColumnsKeepAGapWhenTheLeftRowIsFull(t *testing.T) {
	tui, s := newTestTUIWithScreen("super", "eng1")
	setOverlayActivity(t, tui, map[string]ActivityState{"super": StateSuspended, "eng1": StateRunning})
	rows := overlayFrameRows(tui, s)
	lx, ly, _ := overlayDotOf(rows, "super")
	rx, ry, ok := overlayDotOf(rows, "eng1")
	if !ok || ly != ry || rx <= lx {
		t.Fatalf("want super left and eng1 right on one row:\n%s", framesText(rows))
	}
	row := rows[ly]
	if got := strings.TrimRight(string(row[lx:rx]), " "); got != "○ super [P] suspended" {
		t.Errorf("left row reads %q, want %q intact", got, "○ super [P] suspended")
	}
	if row[rx-1] != ' ' || row[rx-2] != ' ' {
		t.Errorf("columns touch: %q before the right dot, want two blanks\n%s", string(row[rx-2:rx]), framesText(rows))
	}
}

// The release rig's failure (rigs 40739760528), in-process: the operator reads
// the overlay, eng1 starts running and moves to the right column, eng2 slides
// up into the cell the operator is aiming at, and the click lands. A click
// must never act on an agent other than the one the operator aimed at.
func TestOverlayColumns_AClickOnACellWhoseAgentJustMovedHidesNobody(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2", "eng3")
	setOverlayActivity(t, tui, map[string]ActivityState{"eng1": StateIdle, "eng2": StateIdle, "eng3": StateIdle})
	x, y, ok := overlayDotOf(overlayFrameRows(tui, s), "eng1") // what the operator saw
	if !ok {
		t.Fatal("eng1 not drawn")
	}
	setOverlayActivity(t, tui, map[string]ActivityState{"eng1": StateRunning})
	rows := overlayFrameRows(tui, s) // the next frame: eng2 now sits in eng1's old cell
	if _, y2, _ := overlayDotOf(rows, "eng2"); y2 != y {
		t.Fatalf("fixture: eng2 did not move into eng1's old cell\n%s", framesText(rows))
	}
	focused := tui.layoutState.Focused
	tui.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	tui.handleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	if tui.layoutState.Focused != focused {
		t.Errorf("the swallowed click fell through to the pane below: focus %q -> %q", focused, tui.layoutState.Focused)
	}
	for _, n := range []string{"eng1", "eng2", "eng3"} {
		if tui.layoutState.Hidden[n] {
			t.Errorf("a click aimed at eng1's dot hid %s, which had just moved under the cursor\n%s", n, framesText(rows))
		}
	}

	// The twin: once eng2 has held the cell for the settle time, the same
	// click hides eng2, the agent now drawn there.
	at := time.Now().Add(overlayClickSettle + time.Millisecond)
	overlayClickClock = func() time.Time { return at }
	defer func() { overlayClickClock = time.Now }()
	overlayFrameRows(tui, s)
	tui.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	tui.handleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	if !tui.layoutState.Hidden["eng2"] || tui.layoutState.Hidden["eng1"] {
		t.Errorf("after the settle a click on the cell hid %v, want only eng2", tui.layoutState.Hidden)
	}
}
