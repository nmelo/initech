package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nmelo/initech/internal/watch"
)

// wmT0 is the tests' now; no test reads the clock.
var wmT0 = time.Date(2026, 11, 19, 17, 0, 0, 0, time.UTC)

func wmAt(d time.Duration) time.Time { return wmT0.Add(d) }

// wmWatch is a watch for the view tests.
func wmWatch(id, owner string, kind watch.Kind, params map[string]string, why string, set, deadline time.Duration) watch.Watch {
	return watch.Watch{ID: id, Owner: owner, Kind: kind, Params: params, Why: why,
		Set: wmAt(set), Deadline: wmAt(deadline), State: watch.Waiting}
}

func wmEnded(w watch.Watch, st watch.State, ago time.Duration) watch.Watch {
	w.State, w.Ended = st, wmAt(-ago)
	return w
}

func wmDelivered(w watch.Watch, ago time.Duration) watch.Watch {
	w.Delivery.Handed, w.Delivery.Receipt, w.Delivery.Delivered = wmAt(-ago), "r1", wmAt(-ago)
	return w
}

func notStarting(string) bool { return false }

// wmFixture is the picture's mix: three waiting, five ended in all shapes.
func wmFixture() ([]watch.Watch, *fakeWorld) {
	world := newFakeWorld(wmT0)
	world.beads["ini-a"] = bdBead{ID: "ini-a", Title: "Signed-only metering routes", Status: "qa_passed"}
	world.beads["ini-c"] = bdBead{ID: "ini-c", Title: "Usage tiles fix", Status: "closed"}
	bead := map[string]string{"bead": "ini-a", "status": "closed"}
	ws := []watch.Watch{
		wmWatch("w1", "lead1", watch.KindBeadStatus, bead, "read eng8's change", -31*time.Minute, 29*time.Minute),
		wmWatch("w2", "pm", watch.KindDeadline, nil, "report the unused links", -62*time.Minute, 2*time.Hour),
		wmWatch("w3", "eng8", watch.KindSendDelivered, map[string]string{"pane": "pm", "receipt": "r9"}, "pm re-checks item 4", -2*time.Minute, 8*time.Minute),
		wmDelivered(wmEnded(wmWatch("w4", "lead3", watch.KindBeadStatus, map[string]string{"bead": "ini-b", "status": "closed"}, "read the Usage change", -66*time.Minute, -6*time.Minute), watch.Missed, 6*time.Minute), 5*time.Minute),
		wmDelivered(wmEnded(wmWatch("w5", "eng8", watch.KindSendDelivered, map[string]string{"pane": "pm", "receipt": "r2"}, "hand pm the proof", -10*time.Minute, time.Hour), watch.Fired, 4*time.Minute), 4*time.Minute),
		wmDelivered(wmEnded(wmWatch("w6", "super", watch.KindBeadStatus, map[string]string{"bead": "ini-c", "status": "closed"}, "tell theguy the fix is live", -50*time.Minute, time.Hour), watch.Fired, 38*time.Minute), 38*time.Minute),
		wmEnded(wmWatch("w7", "qa2", watch.KindDeadline, nil, "walk it", -30*time.Minute, time.Hour), watch.Cancelled, 12*time.Minute),
		// Delivered 90 minutes ago: past its hour, not shown.
		wmDelivered(wmEnded(wmWatch("w8", "eng4", watch.KindDeadline, nil, "old", -3*time.Hour, -2*time.Hour), watch.Fired, 2*time.Hour), 90*time.Minute),
		// Ended three hours ago and never handed: undelivered, so shown whatever its age, and not counted.
		wmEnded(wmWatch("w9", "eng6", watch.KindDeadline, nil, "still owed", -4*time.Hour, -3*time.Hour), watch.Fired, 3*time.Hour),
	}
	return ws, world
}

func wmIDs(rows []watchRow) []string {
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.w.ID)
	}
	return ids
}

func TestWatchesView_GroupsOrdersAndCounts(t *testing.T) {
	ws, world := wmFixture()
	v := buildWatchesView(ws, world, notStarting, wmT0)
	if got := strings.Join(wmIDs(v.waiting), ","); got != "w3,w1,w2" {
		t.Errorf("waiting %s, want nearest deadline first: w3,w1,w2", got)
	}
	// AC-O: missed, then fired, then cancelled, each most recent first; w8 is
	// past its hour; w9 is undelivered so it stays whatever its age.
	if got := strings.Join(wmIDs(v.ended), ","); got != "w4,w5,w6,w9,w7" {
		t.Errorf("ended %s, want w4,w5,w6,w9,w7", got)
	}
	if v.nWaiting != 3 || v.endedHour != 4 || v.missed != 1 {
		t.Errorf("counts waiting %d, ended in the hour %d, missed %d; want 3, 4 (delivered or cancelled only), 1", v.nWaiting, v.endedHour, v.missed)
	}
}

func TestWatchesView_DeadlineCellsAndMarks(t *testing.T) {
	ws, world := wmFixture()
	v := buildWatchesView(ws, world, notStarting, wmT0)
	got := map[string]watchRow{}
	for _, r := range v.rows() {
		got[r.w.ID] = r
	}
	for id, want := range map[string]struct {
		mark     rune
		deadline string
		age      string
	}{
		"w1": {'◌', "in 29m", "31m"},
		"w2": {'◌', "at 7:00 PM", "1h 2m"},
		"w3": {'◌', "in 8m", "2m"},
		"w4": {'!', "missed 6m ago", "1h 6m"},
		"w5": {'✓', "fired 4m ago", "10m"},
		"w7": {'×', "cancelled 12m ago", "30m"},
		"w9": {'✓', "fired 3h ago · held", "4h"},
	} {
		r := got[id]
		if r.mark != want.mark || r.deadline != want.deadline || r.age != want.age {
			t.Errorf("%s: mark %q deadline %q age %q, want %q %q %q", id, r.mark, r.deadline, r.age, want.mark, want.deadline, want.age)
		}
	}
}

// pm's G5: an ending whose line has not reached its owner never reads as plain
// fired or missed.
func TestWatchesView_AnUndeliveredLineSaysWhereItIs(t *testing.T) {
	world := newFakeWorld(wmT0)
	world.receipts["eng8/r3"] = ReceiptState{Pane: "eng8", ID: "r3", Kind: "dnd", State: ReceiptHeld}
	base := wmWatch("w1", "eng8", watch.KindDeadline, nil, "x", -time.Hour, -10*time.Minute)
	notHanded := wmEnded(base, watch.Fired, 10*time.Minute)
	handed := notHanded
	handed.Delivery.Handed, handed.Delivery.Receipt = wmAt(-9*time.Minute), "r3"
	missedHanded := handed
	missedHanded.State = watch.Missed
	toSuper := notHanded
	toSuper.Delivery.Handed, toSuper.Delivery.ToSuper = wmAt(-9*time.Minute), true
	delivered := wmDelivered(notHanded, 8*time.Minute)
	cancelled := wmEnded(base, watch.Cancelled, 10*time.Minute)
	dropped := handed
	dropped.Delivery.Dropped = "held five minutes and not typed"
	starting := func(owner string) bool { return owner == "eng8" }
	for _, c := range []struct {
		name     string
		w        watch.Watch
		starting func(string) bool
		deadline string
		where    string
	}{
		{"not handed", notHanded, notStarting, "fired 10m ago · held", "not yet handed to delivery"},
		{"not handed, the owner starting", notHanded, starting, "fired 10m ago · starting", "eng8's pane is starting"},
		{"handed, not delivered", handed, starting, "fired 10m ago · held", "receipt r3, held: dnd"},
		{"missed, handed, not delivered", missedHanded, notStarting, "missed 10m ago · held", "held: dnd"},
		{"sent to super", toSuper, starting, "fired 10m ago · to super", "sent to super"},
		// pm: a line initech dropped is not held.
		{"handed, then dropped", dropped, starting, "fired 10m ago · dropped", "dropped: held five minutes and not typed"},
		{"delivered", delivered, starting, "fired 10m ago", "delivered"},
		{"cancelled, owner starting", cancelled, starting, "cancelled 10m ago", ""},
	} {
		v := buildWatchesView([]watch.Watch{c.w}, world, c.starting, wmT0)
		if len(v.ended) != 1 {
			t.Fatalf("%s: %d ended rows", c.name, len(v.ended))
		}
		r := v.ended[0]
		if r.deadline != c.deadline || !strings.Contains(r.where, c.where) {
			t.Errorf("%s: %q / %q, want %q / containing %q", c.name, r.deadline, r.where, c.deadline, c.where)
		}
		if c.w.State == watch.Cancelled && v.missed != 0 {
			t.Errorf("%s: a cancelled watch counted as missed", c.name)
		}
	}
}

func TestWatchesView_WaitsForWording(t *testing.T) {
	world := newFakeWorld(wmT0)
	world.beads["ini-a"] = bdBead{ID: "ini-a", Title: "Status checker", Status: "in_progress"}
	for _, c := range []struct {
		w    watch.Watch
		want string
		id   string
	}{
		{wmWatch("w1", "s", watch.KindBeadStatus, map[string]string{"bead": "ini-a", "status": "closed"}, "x", 0, time.Hour), "▶ ini-a Status checker: closed", "▶ ini-a"},
		{wmWatch("w2", "s", watch.KindBeadComment, map[string]string{"bead": "ini-a", "author": "qa2"}, "x", 0, time.Hour), "▶ ini-a Status checker: a comment by qa2", "▶ ini-a"},
		{wmWatch("w3", "s", watch.KindBeadStatus, map[string]string{"bead": "ini-zz", "status": "closed"}, "x", 0, time.Hour), "ini-zz: closed", "ini-zz"},
		{wmWatch("w4", "s", watch.KindDeadline, nil, "x", 0, time.Hour), "a time only", ""},
		{wmWatch("w5", "s", watch.KindSendDelivered, map[string]string{"pane": "pm", "receipt": "r1"}, "x", 0, time.Hour), "my send to pm is delivered", ""},
		{wmWatch("w6", "s", watch.KindSendHeld, map[string]string{"pane": "pm", "receipt": "r1", "for": "15m"}, "x", 0, time.Hour), "my send to pm still held after 15m", ""},
		{wmWatch("w7", "s", watch.Kind("run_done"), nil, "x", 0, time.Hour), "run_done (not known to this initech)", ""},
	} {
		segs := watchWaitsFor(c.w, world)
		if got := watchSegsText(segs); got != c.want {
			t.Errorf("%s: %q, want %q", c.w.Kind, got, c.want)
		}
		var id string
		for _, s := range segs {
			if s.id {
				id = s.text
			}
		}
		if id != c.id {
			t.Errorf("%s: id run %q, want %q", c.w.Kind, id, c.id)
		}
	}
}

func TestWatchDurAndClock(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second: "<1m", 8 * time.Minute: "8m", 62 * time.Minute: "1h 2m",
		2 * time.Hour: "2h", 228 * time.Minute: "3h 48m", 50 * time.Hour: "2d 2h",
	} {
		if got := watchDur(d); got != want {
			t.Errorf("watchDur(%v) = %q, want %q", d, got, want)
		}
	}
	if got := watchClock(wmAt(-5*time.Hour), wmT0); got != "12:00 PM" {
		t.Errorf("today: %q", got)
	}
	if got := watchClock(wmAt(9*time.Hour), wmT0); got != "Nov 20 2:00 AM" {
		t.Errorf("another day: %q", got)
	}
}

// wmScreen is a TUI on a 200x40 simulated screen.
func wmScreen(t *testing.T) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	tu, s := newTestTUIWithScreen("eng8")
	s.SetSize(200, 40)
	tu.projectName = "hover"
	return tu, s
}

func wmRowText(s tcell.SimulationScreen, y int) string {
	w, _ := s.Size()
	var b strings.Builder
	for x := 0; x < w; x++ {
		r, _, _, _ := s.GetContent(x, y)
		b.WriteRune(r)
	}
	return b.String()
}

// wmFind is the first cell where text starts.
func wmFind(t *testing.T, s tcell.SimulationScreen, text string) (int, int) {
	t.Helper()
	_, h := s.Size()
	for y := 0; y < h; y++ {
		row := []rune(wmRowText(s, y))
		if i := strings.Index(string(row), text); i >= 0 {
			return len([]rune(string(row)[:i])), y
		}
	}
	t.Fatalf("%q is not on the screen", text)
	return 0, 0
}

func wmStyle(s tcell.SimulationScreen, x, y int) (fg, bg tcell.Color) {
	_, _, st, _ := s.GetContent(x, y)
	fg, bg, _ = st.Decompose()
	return fg, bg
}

func TestWatchesModal_DrawsThePicturesShape(t *testing.T) {
	tu, s := wmScreen(t)
	ws, world := wmFixture()
	v := buildWatchesView(ws, world, notStarting, wmT0)
	tu.drawWatchesModal(s, v, wmT0)

	_, ty := wmFind(t, s, "watches · hover · 3 waiting · 4 ended in the last hour · 1 missed its deadline")
	mx, my := wmFind(t, s, "1 missed its deadline")
	if fg, _ := wmStyle(s, mx, my); fg != wmBad {
		t.Errorf("the missed count is not red")
	}
	hx, hy := wmFind(t, s, "set by")
	boxW := watchesBoxWidth(200)
	x0 := (200 - boxW) / 2
	tableX := x0 + 1 + watchesPadX // the table's left edge; the mark is one cell in
	if _, bg := wmStyle(s, hx, hy); bg != wmHead {
		t.Errorf("the header is not its dark band")
	}
	bx, by := wmFind(t, s, "waiting  ")
	if by <= hy {
		t.Fatalf("the waiting band is above the header")
	}
	for _, x := range []int{bx - 1, bx + 60, bx + 150} {
		if _, bg := wmStyle(s, x, by); bg != wmBand {
			t.Errorf("the waiting band is not full width at x=%d", x)
		}
	}
	if _, y := wmFind(t, s, "ended in the last hour  "); y <= by {
		t.Errorf("ended band not below waiting")
	}

	// The second waiting row (not selected): waits for and age shaded, why not.
	wx, wy := wmFind(t, s, "◆ ini-a Signed-only")
	if fg, bg := wmStyle(s, wx, wy); fg != wmID || bg != wmShade {
		t.Errorf("the bead id: fg %v bg %v, want the id color on the shade", fg, bg)
	}
	yx, _ := wmFind(t, s, "read eng8's change")
	if _, bg := wmStyle(s, yx, wy); bg != wmBg {
		t.Errorf("the why column is shaded; columns must alternate")
	}
	ax, _ := wmFind(t, s, "31m ")
	if _, bg := wmStyle(s, ax, wy); bg != wmShade {
		t.Errorf("the age column is not shaded")
	}
	// The first row is selected.
	sx, sy := wmFind(t, s, "pm re-checks item 4")
	if _, bg := wmStyle(s, sx, sy); bg != wmSel {
		t.Errorf("the first row is not drawn selected")
	}
	// Marks and the missed cell.
	if r, _, _, _ := s.GetContent(tableX+1, sy); r != '◌' {
		t.Errorf("waiting mark %q", r)
	}
	dx, dy := wmFind(t, s, "missed 6m ago")
	if fg, _ := wmStyle(s, dx, dy); fg != wmBad {
		t.Errorf("the missed deadline cell is not red")
	}
	if r, _, st, _ := s.GetContent(tableX+1, dy); r != '!' {
		t.Errorf("missed mark %q", r)
	} else if fg, _, _ := st.Decompose(); fg != wmBad {
		t.Errorf("missed mark not red")
	}
	_, fy := wmFind(t, s, "fired 4m ago")
	if r, _, st, _ := s.GetContent(tableX+1, fy); r != '✓' {
		t.Errorf("fired mark %q", r)
	} else if fg, _, _ := st.Decompose(); fg != wmOK {
		t.Errorf("fired mark not green")
	}
	if fg, _ := wmStyle(s, hx, fy); fg != wmDim {
		t.Errorf("a fired row's text is not dim")
	}
	_, cy := wmFind(t, s, "cancelled 12m ago")
	if r, _, _, _ := s.GetContent(tableX+1, cy); r != '×' {
		t.Errorf("cancelled mark %q", r)
	}
	wmFind(t, s, "fired 3h ago · held")

	// No separator lines inside the table: between the header and the footer
	// rule, no box-drawing rune away from the border.
	_, ry := wmFind(t, s, "esc close")
	for y := hy; y < ry-1; y++ {
		row := []rune(wmRowText(s, y))
		inner := string(row[tableX : x0+boxW-1])
		if strings.ContainsAny(inner, "│─┼┬┴├┤") {
			t.Errorf("a separator line in the table at y=%d: %q", y, strings.TrimSpace(inner))
		}
	}
	if ty >= hy {
		t.Errorf("the title is not on the top border")
	}
}

func TestWatchesModal_ADetailBoxShowsTheFullLine(t *testing.T) {
	tu, s := wmScreen(t)
	ws, world := wmFixture()
	world.receipts["eng6/r5"] = ReceiptState{Pane: "eng6", ID: "r5", Kind: "typing", State: ReceiptHeld}
	ws[8].Delivery.Handed, ws[8].Delivery.Receipt = wmAt(-2*time.Hour), "r5"
	v := buildWatchesView(ws, world, notStarting, wmT0)
	tu.watchesM = watchesModal{active: true, selected: "w9", detail: true}
	tu.drawWatchesModal(s, v, wmT0)
	wmFind(t, s, "w9 · set by eng6 · fired")
	wmFind(t, s, "why: still owed")
	wmFind(t, s, "set: Nov 19 1:00 PM (4h ago)")
	wmFind(t, s, "line: handed ")
	wmFind(t, s, "as receipt r5, held: typing")
}

// wmStoreTUI is a screen TUI with a real store holding one waiting and one
// ended watch, its clock pinned to the real one only through Add's "now".
func wmStoreTUI(t *testing.T) *TUI {
	t.Helper()
	tu, _ := wmScreen(t)
	now := time.Now()
	st, note := watch.Open(filepath.Join(t.TempDir(), "watches.yaml"), now)
	if note != "" {
		t.Fatal(note)
	}
	if _, err := st.Add(watch.Watch{Owner: "eng8", Kind: watch.KindDeadline, Why: "a", Deadline: now.Add(time.Hour)}, now); err != nil {
		t.Fatal(err)
	}
	id, _ := st.Add(watch.Watch{Owner: "pm", Kind: watch.KindDeadline, Why: "b", Deadline: now.Add(2 * time.Hour)}, now)
	if err := st.Cancel(id, "pm", now); err != nil {
		t.Fatal(err)
	}
	tu.watches = st
	return tu
}

func wmAltKey(r rune) *tcell.EventKey { return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModAlt) }

func TestWatchesModal_Keys(t *testing.T) {
	tu := wmStoreTUI(t)
	tu.handleKey(wmAltKey('t'))
	if !tu.watchesM.active {
		t.Fatal("Alt+t did not open the modal")
	}
	tu.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tu.watchesM.selected != "w2" {
		t.Errorf("down: selected %q, want w2", tu.watchesM.selected)
	}
	tu.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tu.watchesM.selected != "w2" {
		t.Errorf("down past the last row moved to %q", tu.watchesM.selected)
	}
	tu.handleKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	tu.handleKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if tu.watchesM.selected != "w1" {
		t.Errorf("up: selected %q, want w1", tu.watchesM.selected)
	}
	tu.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !tu.watchesM.detail {
		t.Fatal("Enter did not open the detail box")
	}
	tu.handleKey(tcell.NewEventKey(tcell.KeyRune, 'x', 0))
	if !tu.watchesM.detail || !tu.watchesM.active {
		t.Error("a rune in the detail box closed something")
	}
	tu.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if tu.watchesM.detail || !tu.watchesM.active {
		t.Error("Esc in the detail box must close the box only")
	}
	tu.handleKey(tcell.NewEventKey(tcell.KeyRune, '`', 0))
	if tu.cmd.active {
		t.Error("a key reached the command bar through the modal")
	}
	tu.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if tu.watchesM.active {
		t.Error("Esc did not close the modal")
	}
	tu.handleKey(wmAltKey('t'))
	tu.handleKey(wmAltKey('t'))
	if tu.watchesM.active {
		t.Error("Alt+t did not close the modal")
	}
	tu.execCmd("watches")
	if !tu.watchesM.active {
		t.Error("the command bar's watches did not open the modal")
	}
}

func TestWatchesModal_MainWindowOnly(t *testing.T) {
	tu := wmStoreTUI(t)
	tu.windowID = "2"
	tu.agentEvents = make(chan AgentEvent, 4)
	tu.handleKey(wmAltKey('t'))
	tu.execCmd("watches")
	if tu.watchesM.active {
		t.Fatal("a second window opened the watches modal")
	}
	select {
	case ev := <-tu.agentEvents:
		if !strings.Contains(ev.Detail, "main window only") {
			t.Errorf("notice %q", ev.Detail)
		}
	default:
		t.Error("no notice: a chord that does nothing reads as a broken build")
	}
}

// Feed it nothing: no store, and an empty one.
func TestWatchesModal_NoStoreAndNoWatches(t *testing.T) {
	for _, name := range []string{"no store", "empty store"} {
		tu, s := wmScreen(t)
		if name == "empty store" {
			st, _ := watch.Open(filepath.Join(t.TempDir(), "watches.yaml"), wmT0)
			tu.watches = st
		}
		v := tu.watchesNow(wmT0)
		if len(v.rows()) != 0 || v.nWaiting+v.endedHour+v.missed != 0 {
			t.Errorf("%s: %+v", name, v)
		}
		tu.watchesM = watchesModal{active: true}
		tu.drawWatchesModal(s, v, wmT0)
		wmFind(t, s, "watches · hover · 0 waiting · 0 ended in the last hour ")
		wmFind(t, s, "no watches")
		if _, ty := wmFind(t, s, "watches · hover"); strings.Contains(wmRowText(s, ty), "missed") {
			t.Errorf("%s: a zero missed count is shown", name)
		}
		tu.handleWatchesKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
		tu.handleWatchesKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
		if tu.watchesM.detail {
			t.Errorf("%s: Enter opened a detail box with no row", name)
		}
	}
}

func TestPane_StartingNowIsTheStartupGateWithoutItsWait(t *testing.T) {
	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()
	p := &Pane{name: "eng8", ptmx: &filePty{w}, startedAt: wmT0}
	if !p.startingNow(wmT0.Add(10 * time.Second)) {
		t.Error("a pane 10s after start, not settled, is not starting")
	}
	if p.startingNow(wmT0.Add(startupGateWindow)) {
		t.Error("past the gate window it still reads starting")
	}
	p.startupSettled = true
	if p.startingNow(wmT0.Add(10 * time.Second)) {
		t.Error("a settled pane reads starting")
	}
	p.startupSettled = false
	p.ptmx = nil
	if p.startingNow(wmT0.Add(10 * time.Second)) {
		t.Error("a pane with no PTY reads starting")
	}
}

// Reachability: the frame draws the modal from the real store when it is open,
// and not when it is closed.
func TestWatchesModal_TheFrameDrawsItFromTheStore(t *testing.T) {
	tu := wmStoreTUI(t)
	s := tu.screen.(tcell.SimulationScreen)
	tu.renderFrame()
	s.Show()
	for y := 0; y < 40; y++ {
		if strings.Contains(wmRowText(s, y), "watches · hover") {
			t.Fatal("the modal is drawn while closed")
		}
	}
	tu.handleKey(wmAltKey('t'))
	tu.renderFrame()
	wmFind(t, s, "watches · hover · 1 waiting · 1 ended in the last hour ")
	wmFind(t, s, "cancelled ")
}

// The picture's frame: square thin corners, 4 columns in from each side,
// centred, and everything behind it dimmed (ini-44av fix, ini-9krg).
func TestWatchesModal_TheFrameIsThePictures(t *testing.T) {
	for sw, want := range map[int]int{200: 192, 340: 332, 100: 92, 50: 42, 30: 30} {
		if got := watchesBoxWidth(sw); got != want {
			t.Errorf("width at %d columns: %d, want %d", sw, got, want)
		}
	}
	tu, s := wmScreen(t)
	behind := tcell.StyleDefault.Foreground(tcell.NewRGBColor(200, 200, 200)).Background(tcell.NewRGBColor(100, 100, 100))
	s.SetContent(0, 0, 'A', nil, behind)
	s.SetContent(199, 39, 'B', nil, tcell.StyleDefault)
	ws, world := wmFixture()
	tu.drawWatchesModal(s, buildWatchesView(ws, world, notStarting, wmT0), wmT0)
	if r, _, st, _ := s.GetContent(0, 0); r != 'A' {
		t.Errorf("the cell behind changed rune: %q", r)
	} else if fg, bg, _ := st.Decompose(); fg != tcell.NewRGBColor(33, 34, 35) || bg != tcell.NewRGBColor(19, 20, 21) { // under rgba(6,7,9,.86)
		t.Errorf("behind the modal: fg %v bg %v, want dimmed under the backdrop", fg, bg)
	}
	if _, _, st, _ := s.GetContent(199, 39); true {
		if _, bg, _ := st.Decompose(); bg != tcell.NewRGBColor(8, 9, 11) { // the picture's #16181d under the backdrop
			t.Errorf("a default cell behind the modal is not dimmed: bg %v", bg)
		}
	}
	_, ty := wmFind(t, s, "watches · hover")
	x0 := (200 - 192) / 2
	for _, c := range []struct {
		x, y int
		want rune
	}{{x0, ty, '┌'}, {x0 + 191, ty, '┐'}} {
		if r, _, _, _ := s.GetContent(c.x, c.y); r != c.want {
			t.Errorf("corner at %d,%d: %q, want %q", c.x, c.y, r, c.want)
		}
	}
	ex, fy := wmFind(t, s, "esc close")
	if fg, _ := wmStyle(s, ex, fy); fg != wmText {
		t.Errorf("the esc key is not bright")
	}
	if rx, _ := wmFind(t, s, "read-only:"); true {
		if fg, _ := wmStyle(s, rx, fy); fg != wmDim {
			t.Errorf("read-only: is not dim, as pictured")
		}
	}
	if r, _, _, _ := s.GetContent(x0, fy+1); r != '└' {
		t.Errorf("bottom-left corner %q, want └", r)
	}
	// Columns in the picture's grid: why starts at 784 of 1508 of the table.
	hx, hy := wmFind(t, s, "why (the line it will get)")
	inner := 192 - 2 - 2*watchesPadX
	if got, want := float64(hx-1-(x0+1+watchesPadX))/float64(inner), 784.0/1508; got < want-0.02 || got > want+0.02 {
		t.Errorf("why starts at %.3f of the table, want about %.3f", got, want)
	}
	// On the header row only: "deadline" is also in the title.
	head := []rune(wmRowText(s, hy))
	ax := strings.Index(string(head), " age ")
	dx := strings.Index(string(head), " deadline")
	if ax < 0 || dx < 0 {
		t.Fatalf("header %q has no age or deadline", string(head))
	}
	ax, dx = len([]rune(string(head)[:ax])), len([]rune(string(head)[:dx]))
	if dx-ax != watchesAgeCols || watchesAgeCols != 7 || hy < 0 {
		t.Errorf("age is %d columns, want the picture's 7", dx-ax)
	}
}

// Narrow (100 columns): a header cell clips with an ellipsis and keeps a space
// before the next column; the footer drops whole hints from the right.
func TestWatchesModal_NarrowClipsHeadersAndDropsWholeHints(t *testing.T) {
	tu, s := wmScreen(t)
	s.SetSize(100, 40)
	ws, world := wmFixture()
	tu.drawWatchesModal(s, buildWatchesView(ws, world, notStarting, wmT0), wmT0)
	x, y := wmFind(t, s, "why (the")
	row := []rune(wmRowText(s, y))
	i := x
	for i < len(row) && row[i] != '…' && row[i] != ' ' || (i < len(row) && row[i] == ' ' && row[i+1] != ' ' && row[i+1] != 'a') {
		i++
	}
	cell := string(row[x : i+1])
	if !strings.HasSuffix(cell, "…") || row[i+1] != ' ' {
		t.Errorf("the why header %q: want it clipped with … and a space before the next column", cell)
	}
	_, fy := wmFind(t, s, "esc close")
	foot := strings.TrimSpace(strings.Trim(wmRowText(s, fy), "│ "))
	if !strings.HasSuffix(foot, "enter the full line and when the watch was set") || strings.Contains(foot, "read") {
		t.Errorf("footer %q: want whole hints, the last one dropped", foot)
	}
	// A narrow modal still shows a whole deadline cell: "· held" is never cut.
	wmFind(t, s, "fired 3h ago · held")
	wmFind(t, s, "cancelled 12m ago")
}

// lead1's read: the renderer keeps last frame's cells where nothing redraws
// them (ini-ysr.2/.3), so a backdrop that reads the screen back must start
// from undimmed cells every frame. On a parked fleet, whose idle panes are not
// redrawn between frames, a cell behind the open modal is dimmed exactly once
// however many frames are drawn, and closing the modal puts it back.
func TestWatchesModal_TheBackdropDimsOnceAcrossFrames(t *testing.T) {
	tu, s := ysr3Parked(t)
	st, _ := watch.Open(filepath.Join(t.TempDir(), "watches.yaml"), wmT0)
	tu.watches = st
	x, y := wmFind(t, s, "p01 body")
	if x >= (160-watchesBoxWidth(160))/2 {
		t.Fatalf("the probe cell %d,%d is under the modal, not behind it", x, y)
	}
	cellAt := func() tcell.Style {
		_, _, st, _ := s.GetContent(x, y)
		return st
	}
	before := cellAt()
	tu.handleKey(wmAltKey('t'))
	tu.render()
	first := cellAt()
	if first == before {
		t.Fatal("the cell behind the modal is not dimmed at all")
	}
	for i := 2; i <= 4; i++ {
		tu.render()
		if got := cellAt(); got != first {
			fg, bg, _ := got.Decompose()
			t.Fatalf("frame %d dimmed the cell again: fg %v bg %v", i, fg, bg)
		}
	}
	tu.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	tu.render()
	tu.render()
	if got := cellAt(); got != before {
		t.Errorf("closing the modal left the cell dimmed")
	}
}

// super: at 70 columns the title was cut mid-word. It is cut at a word, with
// "…", like the header and footer, and never leaves a separator dangling.
func TestWatchesModal_ANarrowTitleIsCutAtAWord(t *testing.T) {
	full := "watches · hover · 3 waiting · 4 ended in the last hour · 1 missed its deadline"
	tu, s := wmScreen(t)
	s.SetSize(70, 30)
	ws, world := wmFixture()
	tu.drawWatchesModal(s, buildWatchesView(ws, world, notStarting, wmT0), wmT0)
	x, y := wmFind(t, s, "watches · hover")
	row := []rune(wmRowText(s, y))
	end := x
	for end < len(row) && row[end] != '…' {
		end++
	}
	if end == len(row) {
		t.Fatalf("the title has no ellipsis: %q", string(row))
	}
	shown := string(row[x:end])
	if !strings.HasPrefix(full, shown) {
		t.Fatalf("the title %q is not the start of %q", shown, full)
	}
	if next := full[len(shown)]; next != ' ' {
		t.Errorf("the title is cut inside a word: %q then %q", shown, full[len(shown):])
	}
	if strings.HasSuffix(strings.TrimSpace(shown), "·") {
		t.Errorf("the title ends on a dangling separator: %q", shown)
	}
	// It keeps every whole word that fits: at 70 columns, up to the hour count.
	if shown != "watches · hover · 3 waiting · 4 ended in the last hour" {
		t.Errorf("title %q, want it cut after \"hour\", the last whole word that fits", shown)
	}
	if row[end+1] != ' ' {
		t.Errorf("no space after the ellipsis before the border")
	}
	// Wide enough, the title is whole.
	if got := fitWatchTitle([]string{" ", "watches", " ·"}, 40); strings.Join(got, "") != " watches ·" {
		t.Errorf("a title that fits was changed: %q", got)
	}
}

// ini-9krg, the roomier picture: the window sits 4 columns and at least 2 rows
// in from the edges; inside the border a blank row above the column names and
// 3 columns each side; each band is a tinted blank row then its bold white
// label, with the first data row straight after; no blank row between the
// column names and the first band; one blank row after the last data row,
// then the key line, its hints 5 columns apart.
func TestWatchesModal_TheRoomierLayout(t *testing.T) {
	tu, s := wmScreen(t)
	ws, world := wmFixture()
	tu.drawWatchesModal(s, buildWatchesView(ws, world, notStarting, wmT0), wmT0)
	x0 := watchesMarginX
	_, ty := wmFind(t, s, "watches · hover")
	if ty < watchesMarginY {
		t.Errorf("the window starts at row %d, inside the %d-row margin", ty, watchesMarginY)
	}
	if r, _, _, _ := s.GetContent(x0, ty); r != '┌' {
		t.Errorf("the window's corner is not 4 columns in: %q at %d", r, x0)
	}
	if strings.TrimSpace(strings.Trim(string([]rune(wmRowText(s, ty+1))[x0:x0+192]), "│")) != "" {
		t.Errorf("no blank row between the title and the column names")
	}
	_, hy := wmFind(t, s, "set by")
	if hy != ty+2 {
		t.Errorf("column names at row %d, want %d", hy, ty+2)
	}
	for x := x0 + 1; x <= x0+watchesPadX; x++ {
		if _, bg := wmStyle(s, x, hy); bg != wmBg {
			t.Errorf("column %d inside the border is not padding", x)
		}
	}
	if _, bg := wmStyle(s, x0+1+watchesPadX, hy); bg != wmHead {
		t.Errorf("the header band does not start 3 columns in")
	}
	lx, ly := wmFind(t, s, "waiting  ")
	if ly != hy+2 {
		t.Errorf("the waiting label at row %d, want %d: the band's blank row straight under the column names", ly, hy+2)
	}
	if _, bg := wmStyle(s, lx, ly-1); bg != wmBand || strings.TrimSpace(string([]rune(wmRowText(s, ly-1))[x0+1+watchesPadX:x0+150])) != "" {
		t.Errorf("the band's first row is not tinted and blank")
	}
	if fg, _ := wmStyle(s, lx, ly); fg != tcell.NewRGBColor(0xff, 0xff, 0xff) {
		t.Errorf("the band label is not white")
	} else if _, _, st, _ := s.GetContent(lx, ly); func() bool { _, _, a := st.Decompose(); return a&tcell.AttrBold == 0 }() {
		t.Errorf("the band label is not bold")
	}
	if _, y := wmFind(t, s, "pm re-checks item 4"); y != ly+1 {
		t.Errorf("the first data row at %d, want straight after the band (%d)", y, ly+1)
	}
	_, el := wmFind(t, s, "ended in the last hour  ")
	if _, y := wmFind(t, s, "at 7:00 PM"); el != y+2 {
		t.Errorf("the ended label at %d, want the last waiting row %d plus the band's blank row", el, y)
	}
	_, last := wmFind(t, s, "cancelled 12m ago")
	_, ky := wmFind(t, s, "esc close")
	// After the last data row: a shaded blank row, then an unshaded one (the
	// picture's footer margin), then the rule, then the keys.
	if ky != last+4 {
		t.Errorf("key line at %d, want two blank rows and the rule after the last data row (%d)", ky, last+4)
	}
	if _, bg := wmStyle(s, x0+1+watchesPadX+60, last+2); bg != wmBg {
		t.Errorf("the second blank row above the rule is shaded; the picture has it plain")
	}
	if _, bg := wmStyle(s, x0+1+watchesPadX+60, last+1); bg != wmShade {
		t.Errorf("the blank row after the last data row does not keep the column shades")
	}
	if !strings.Contains(wmRowText(s, ky), "esc close     ↑↓ move     enter") ||
		!strings.Contains(wmRowText(s, ky), "was set     ·     read-only:") {
		t.Errorf("hints are not 5 columns apart: %q", strings.TrimSpace(wmRowText(s, ky)))
	}
}

// ini-9krg AC2: while the window is open, keys other than its own do nothing
// and reach no pane; the twin: closed, the same key reaches the focused pane.
func TestWatchesModal_KeysReachNoPaneWhileOpen(t *testing.T) {
	tu := wmStoreTUI(t)
	p := tu.panes[0].(*Pane)
	drainPaneInput(t, p)
	tu.handleKey(wmAltKey('t'))
	for _, k := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, 'x', 0),
		tcell.NewEventKey(tcell.KeyRune, 'q', 0),
		tcell.NewEventKey(tcell.KeyBackspace2, 0, 0),
		tcell.NewEventKey(tcell.KeyTab, 0, 0),
	} {
		tu.handleKey(k)
	}
	if !tu.watchesM.active {
		t.Fatal("a key that is not the window's closed it")
	}
	if p.lastOperatorKey.Load() != 0 {
		t.Error("a key reached the pane under the open window")
	}
	tu.handleKey(wmAltKey('t'))
	if tu.watchesM.active {
		t.Fatal("Option+T did not close the window")
	}
	tu.handleKey(tcell.NewEventKey(tcell.KeyRune, 'x', 0))
	if p.lastOperatorKey.Load() == 0 {
		t.Error("the twin: with the window closed, the key did not reach the pane, so the check above proves nothing")
	}
}

// lead1's read: at 30 to 52 columns the table crossed the window's right
// border. At every width from 20 to 60, every row inside the window ends in
// its border, and nothing is drawn past it.
func TestWatchesModal_NothingCrossesTheBorderAtAnyWidth(t *testing.T) {
	ws, world := wmFixture()
	for sw := 20; sw <= 60; sw++ {
		tu, s := wmScreen(t)
		s.SetSize(sw, 30)
		s.Clear()
		tu.drawWatchesModal(s, buildWatchesView(ws, world, notStarting, wmT0), wmT0)
		boxW := watchesBoxWidth(sw)
		x0 := (sw - boxW) / 2
		right := x0 + boxW - 1
		_, ty := wmFind(t, s, "watches")
		_, ky := wmFind(t, s, "esc close")
		for y := ty + 1; y <= ky; y++ {
			if r, _, _, _ := s.GetContent(right, y); r != '│' {
				t.Errorf("width %d, row %d: the right border is %q: %q", sw, y, r, wmRowText(s, y))
				break
			}
			for x := right + 1; x < sw; x++ {
				if r, _, _, _ := s.GetContent(x, y); r != ' ' {
					t.Errorf("width %d, row %d: %q drawn past the border at column %d", sw, y, r, x)
					break
				}
			}
		}
	}
}

// The columns never add up to more than the table, at any width.
func TestWatchesColumns_FitTheTable(t *testing.T) {
	ws, world := wmFixture()
	rows := buildWatchesView(ws, world, notStarting, wmT0).rows()
	for inner := 0; inner <= 330; inner++ {
		if got := watchColsSum(func() []int { c := watchesColumns(inner, rows); return c[:] }()); got > inner {
			t.Errorf("inner %d: columns add up to %d", inner, got)
		}
	}
}

// super, from qa2's differences: at 30 columns a band label too long for the
// band is cut with an ellipsis, like the header cells.
func TestWatchesModal_ANarrowBandLabelEndsInAnEllipsis(t *testing.T) {
	tu, s := wmScreen(t)
	s.SetSize(30, 30)
	s.Clear()
	ws, world := wmFixture()
	tu.watchesM.selected = "w7" // scroll to the ended rows
	tu.drawWatchesModal(s, buildWatchesView(ws, world, notStarting, wmT0), wmT0)
	_, y := wmFind(t, s, "ended in")
	label := strings.TrimSpace(strings.Trim(wmRowText(s, y), "│ "))
	if !strings.HasSuffix(label, "…") || !strings.HasPrefix("ended in the last hour", strings.TrimSuffix(label, "…")) {
		t.Errorf("the narrow band label %q, want a start of \"ended in the last hour\" ending in …", label)
	}
}

// pm (f): "N ended in the last hour" counts the endings delivered, sent to
// super, or cancelled, missed ones included; only held or starting ones are
// left out.
func TestWatchesView_TheHourCountIncludesSentToSuper(t *testing.T) {
	base := wmWatch("w1", "eng8", watch.KindDeadline, nil, "x", -time.Hour, -10*time.Minute)
	missed := wmEnded(base, watch.Missed, 10*time.Minute)
	toSuper := missed
	toSuper.Delivery.Handed, toSuper.Delivery.ToSuper = wmAt(-9*time.Minute), true
	delivered := wmDelivered(wmEnded(base, watch.Fired, 10*time.Minute), 8*time.Minute)
	cancelled := wmEnded(base, watch.Cancelled, 10*time.Minute)
	held := wmEnded(base, watch.Fired, 10*time.Minute)
	starting := held
	for _, c := range []struct {
		name     string
		w        watch.Watch
		starting bool
		want     int
	}{
		{"missed, sent to super", toSuper, false, 1},
		{"fired, delivered", delivered, false, 1},
		{"cancelled", cancelled, false, 1},
		{"fired, its line dropped", func() watch.Watch {
			w := held
			w.Delivery.Handed, w.Delivery.Receipt, w.Delivery.Dropped = wmAt(-9*time.Minute), "r3", "the agent no longer exists"
			return w
		}(), false, 1},
		{"fired, held", held, false, 0},
		{"fired, owner starting", starting, true, 0},
	} {
		st := func(string) bool { return c.starting }
		if got := buildWatchesView([]watch.Watch{c.w}, newFakeWorld(wmT0), st, wmT0).endedHour; got != c.want {
			t.Errorf("%s: counted %d, want %d", c.name, got, c.want)
		}
	}
}

// A dropped line's hour runs from its handing, as the store prunes it: past
// the hour it leaves the window; the twin, within the hour, it is listed.
func TestWatchesView_ADroppedLineLeavesAfterItsHour(t *testing.T) {
	drop := func(id string, ago time.Duration) watch.Watch {
		w := wmEnded(wmWatch(id, "eng8", watch.KindDeadline, nil, "x", -3*time.Hour, -ago), watch.Fired, ago)
		w.Delivery.Handed, w.Delivery.Receipt, w.Delivery.Dropped = wmAt(-ago), "r1", "the agent no longer exists"
		return w
	}
	v := buildWatchesView([]watch.Watch{drop("w1", 90*time.Minute), drop("w2", 30*time.Minute)}, newFakeWorld(wmT0), notStarting, wmT0)
	if got := strings.Join(wmIDs(v.ended), ","); got != "w2" {
		t.Errorf("ended %q, want only w2: w1 was dropped 90 minutes ago", got)
	}
}
