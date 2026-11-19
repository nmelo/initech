package tui

// rig_panel.go is the rig panel (ini-fed0), drawn where the Agents overlay's
// pinned section was: a title row that says whether anything needs a person,
// and the agents box, one row per awake agent with the bead in its hands. The
// rules are pure functions over a snapshot (panelSnapshot) so each is tested
// without a screen or a clock; the draw only lays out what they return.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/rignumbers"
)

const (
	// panelAttentionAfter is how long an effort's bead may go unmoved before
	// the moved box flags it stale.
	panelAttentionAfter = 20 * time.Minute
	// panelDefaultRows is the boxes' height in rows, under their top border.
	panelDefaultRows = 8
	// panelMovedMinWidth is the narrowest the moved box gets before the
	// agents box gives up its bead column (the fold order).
	panelMovedMinWidth = 30
)

// panelBead is one bead as the panel reads it.
type panelBead struct {
	ID, Status string
	LastMoved  time.Time
}

// panelAgent is one agent as the panel reads it. Since is when it entered
// its current state; zero when not known (a remote agent).
type panelAgent struct {
	Name       string
	Key        string // agentKey: what a click on its dot hides or shows.
	Hidden     bool   // Hidden from this window's grid: the row reads [h].
	State      ActivityState
	Since      time.Time
	Suspended  bool
	Alive      bool
	Bead       string
	BeadStatus string
	Title      string // The bead's title, from the bead poll.
	Stuck      bool   // The agent holds a bead stuck by the pace rule.
}

// panelAlarm is one title-row count and what it counts.
type panelAlarm struct {
	Label string
	Names []string // Bead ids, or agent names for prompts.
}

// panelTitle is the title row's right side.
type panelTitle struct {
	Alarms           []panelAlarm // on Nelson, blocked, stale 20m, prompts.
	Awake, Suspended int
	CutWorking       int // Working agents omitted from the box.
	Idle             int // Awake agents with no work or bead to show.
}

// awake reports whether an agent counts as awake: alive and not suspended,
// as the machine ceiling counts it.
func (a panelAgent) awake() bool {
	return a.Alive && !a.Suspended && a.State != StateDead && a.State != StateSuspended
}

// atPrompt reports whether the agent waits on a person.
func (a panelAgent) atPrompt() bool { return a.State == StateWaitingInput }

// panelTitleOf counts what the title row shows.
func panelTitleOf(beads []panelBead, agents []panelAgent, now time.Time) panelTitle {
	var nelson, blocked, stale, prompts []string
	for _, b := range beads {
		switch {
		case b.Status == "waiting_on_nelson":
			nelson = append(nelson, b.ID)
		case b.Status == "blocked":
			blocked = append(blocked, b.ID)
		case b.Status == "in_progress" && !b.LastMoved.IsZero() && now.Sub(b.LastMoved) >= rignumbers.NoMoveAfter:
			stale = append(stale, b.ID)
		}
	}
	var t panelTitle
	for _, a := range agents {
		switch {
		case a.awake():
			t.Awake++
			if a.atPrompt() {
				prompts = append(prompts, a.Name)
			}
		case a.Suspended || a.State == StateSuspended:
			t.Suspended++
		}
	}
	t.Alarms = []panelAlarm{
		{Label: "on Nelson", Names: nelson},
		{Label: "blocked", Names: blocked},
		{Label: "stale 20m", Names: stale},
		{Label: "prompts", Names: prompts},
	}
	return t
}

// panelStateWord is an agent's state as the panel names it.
func panelStateWord(s ActivityState) string {
	switch s {
	case StateRunning:
		return "running"
	case StateBackground:
		return "background"
	case StateWaitingInput:
		return "prompt"
	default:
		return "idle" // Idle, and quiet with unsent text in the box.
	}
}

// panelAge is a duration as the panel prints it: 2m, 26m, 1h04m. Under a
// minute is 0m; an unknown start is blank.
func panelAge(since, now time.Time) string {
	if since.IsZero() {
		return ""
	}
	d := now.Sub(since)
	if d < 0 {
		d = 0
	}
	m := int(d / time.Minute)
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh%02dm", m/60, m%60)
}

// panelSettleAfter holds the box's order and times through idle/running
// flicker. Transitions into and out of a prompt are immediate.
const panelSettleAfter = 3 * time.Second

// panelSettledState is what the agents box remembers of one agent: the state
// word it shows, when that state began, and a newer word not yet held long
// enough.
type panelSettledState struct {
	word      string
	began     time.Time
	cand      string
	candSince time.Time
	seen      bool
}

// panelWord is the box's word for a raw state.
func panelWord(a panelAgent) string { return panelStateWord(a.State) }

// settle updates the memory with this draw's raw word and returns the word
// shown and when its state began.
func (st *panelSettledState) settle(word string, since, now time.Time) (string, time.Time) {
	if since.IsZero() || since.After(now) {
		since = now
	}
	switch {
	case !st.seen:
		st.word, st.began, st.seen = word, since, true
	case word == st.word:
		st.cand = ""
	case word == "prompt" || st.word == "prompt":
		st.word, st.began, st.cand = word, since, ""
	default:
		if st.cand != word {
			st.cand, st.candSince = word, since
		}
		if now.Sub(st.candSince) >= panelSettleAfter {
			st.word, st.began, st.cand = word, st.candSince, ""
		}
	}
	return st.word, st.began
}

// panelBoxRow is one agent row in the agents box: the agent, the word shown
// and when its state began.
type panelBoxRow struct {
	a     panelAgent
	word  string
	began time.Time
}

// panelSections splits the awake agents into section 1 (running or
// background, longest first) and section 2 (bead holders idle, at a prompt or
// stuck, longest first), and counts the remaining awake agents as idle.
// Ties keep the roster order.
func (t *TUI) panelSections(agents []panelAgent, now time.Time) (working, holding []panelBoxRow, idle int) {
	if t.panelSettled == nil {
		t.panelSettled = map[string]*panelSettledState{}
	}
	for _, a := range agents {
		if !a.awake() {
			continue
		}
		key := a.Key
		if key == "" {
			key = a.Name
		}
		st := t.panelSettled[key]
		if st == nil {
			st = &panelSettledState{}
			t.panelSettled[key] = st
		}
		word, began := st.settle(panelWord(a), a.Since, now)
		r := panelBoxRow{a: a, word: word, began: began}
		switch {
		case word == "running" || word == "background":
			working = append(working, r)
		case a.Bead != "":
			if word != "prompt" && a.Stuck {
				r.word = "stuck"
			}
			holding = append(holding, r)
		default:
			idle++
		}
	}
	longest := func(l []panelBoxRow) {
		sort.SliceStable(l, func(i, j int) bool { return l[i].began.Before(l[j].began) })
	}
	longest(working)
	longest(holding)
	return working, holding, idle
}

// panelBoxLayout gives every holder a cell, adding columns when needed.
// Working agents fill the remaining cells, longest-running first.
func panelBoxLayout(rows int, working, holding []panelBoxRow) (cells []panelBoxRow, cols, cutWorking int) {
	if rows < 1 {
		return nil, 0, len(working)
	}
	cols = max(1, (len(holding)+rows-1)/rows)
	shown := min(len(working), cols*rows-len(holding))
	cells = make([]panelBoxRow, 0, shown+len(holding))
	cells = append(cells, working[:shown]...)
	return append(cells, holding...), cols, len(working) - shown
}

// panelBeadCell is the bead column's cell: icon and id, or a dash.
func panelBeadCell(a panelAgent) string {
	if a.Bead == "" {
		return "—"
	}
	icon, ok := beadStateIcon(a.BeadStatus)
	if !ok {
		icon = beadBar
	}
	return string(icon) + " " + a.Bead
}

// panelSpan colors part of the header's right-hand text.
type panelSpan struct {
	from, to int
	kind     string // "red", "yellow", "dim"
}

// panelTitleText drops alarm IDs, idle, cut workers, suspended, and awake,
// in that order as the fold level rises. The alarm counts stay.
func panelTitleText(t panelTitle, level int) (string, []panelSpan) {
	ids := level == 0
	var b strings.Builder
	var spans []panelSpan
	n := 0
	write := func(s, kind string) {
		start := n
		b.WriteString(s)
		n += len([]rune(s))
		if kind != "" {
			spans = append(spans, panelSpan{start, n, kind})
		}
	}
	for i, a := range t.Alarms {
		if i > 0 {
			write(" · ", "dim")
		}
		text := fmt.Sprintf("%s %d", a.Label, len(a.Names))
		if ids && len(a.Names) > 0 {
			text += " " + strings.Join(a.Names, " ")
		}
		kind := "dim"
		switch {
		case len(a.Names) > 0 && a.Label == "prompts":
			kind = "yellow"
		case len(a.Names) > 0:
			kind = "red"
		}
		write(text, kind)
	}
	if level < 5 {
		write(" · ", "dim")
		write(fmt.Sprintf("%d awake", t.Awake), "")
	}
	if level < 3 && t.CutWorking > 0 {
		write(" · ", "dim")
		write(fmt.Sprintf("+%d working", t.CutWorking), "")
	}
	if level < 2 && t.Idle > 0 {
		write(" · ", "dim")
		write(fmt.Sprintf("%d idle", t.Idle), "")
	}
	if level < 4 {
		write(" · ", "dim")
		kind := ""
		if t.Suspended == 0 {
			kind = "dim"
		}
		write(fmt.Sprintf("%d suspended", t.Suspended), kind)
	}
	return b.String(), spans
}

// ── window 1's snapshot and the draw ────────────────────────────────

// panelSnapshot is what the panel reads from this window: its agents in the
// Agents panel's order, and the polled beads.
func (t *TUI) panelSnapshot(now time.Time) ([]panelAgent, []panelBead) {
	status, titles := map[string]string{}, map[string]string{}
	// held is the bead each agent holds by bd's assignee, for an agent whose
	// ribbon is empty (ini-jmut): an unfinished bead, in_progress first, then
	// the most recently moved.
	held := map[string]bdBead{}
	heldMoved := map[string]time.Time{}
	var beads []panelBead
	if t.paceAnswered {
		for _, b := range t.paceBeads {
			status[b.ID] = b.Status
			titles[b.ID] = b.Title
			if b.Assignee != "" && panelHeldStatus[b.Status] {
				moved := t.paceBead(b, now).LastMoved()
				cur, ok := held[b.Assignee]
				switch {
				case !ok,
					b.Status == "in_progress" && cur.Status != "in_progress",
					(b.Status == "in_progress") == (cur.Status == "in_progress") && moved.After(heldMoved[b.Assignee]):
					held[b.Assignee], heldMoved[b.Assignee] = b, moved
				}
			}
			beads = append(beads, panelBead{ID: b.ID, Status: b.Status, LastMoved: t.paceBead(b, now).LastMoved()})
		}
	}
	var agents []panelAgent
	for _, p := range t.overlayDisplayOrder(t.visiblePanesForWindow()) {
		// The overlay's own naming: a real remote machine's agents carry
		// their host, window 1's alias is dropped in a second window.
		a := panelAgent{Name: paneDisplayName(p), Key: agentKey(p), State: p.Activity(),
			Suspended: p.IsSuspended(), Alive: p.IsAlive(), Bead: p.BeadID()}
		a.Hidden = t.layoutState.Hidden[a.Key]
		if a.Bead == "" {
			if b, ok := held[p.Name()]; ok {
				a.Bead = b.ID
			}
		}
		a.BeadStatus = status[a.Bead]
		a.Title = titles[a.Bead]
		// Stuck by the pace rule, the one the pane header and the moved box
		// read (ini-b0po).
		a.Stuck = t.paceStuck[p.Name()]
		if lp, ok := p.(*Pane); ok {
			a.Since = lp.ActivitySince()
		}
		if w, ok := p.(interface {
			WaitingInput() (bool, time.Time, string)
		}); ok && a.State == StateWaitingInput {
			if waiting, since, _ := w.WaitingInput(); waiting && !since.IsZero() {
				a.Since = since
			}
		}
		agents = append(agents, a)
	}
	return agents, beads
}

// panelDisclosure is the title row's left-side disclosures: "N visible, M
// hidden" when any of the window's agents are hidden from its grid, and
// "+N <window> · alt+a shows all" when agents are on another window; empty
// when neither applies.
func (t *TUI) panelDisclosure() string {
	c := t.overlayContent()
	var parts []string
	if c.hiddenCount > 0 {
		parts = append(parts, fmt.Sprintf("%d visible, %d hidden", c.agentRows-c.hiddenCount, c.hiddenCount))
	}
	if c.scopeHidden > 0 {
		parts = append(parts, fmt.Sprintf("+%d %s · alt+a shows all", c.scopeHidden, c.scopeWhere))
	}
	return strings.Join(parts, " · ")
}

// renderRigPanel draws the panel across the top planTop rows.
func (t *TUI) renderRigPanel(now time.Time) {
	top := t.planTop
	if top == 0 {
		return
	}
	agents, beads := t.panelSnapshot(now)
	t.panelMoved = t.movedSnapshot(now)
	t.drawRigPanel(now, agents, beads)
}

// movedSnapshot is the polled beads as the moved box reads them: each
// one's parent from a parent-child link, else from a dotted id whose prefix
// is a polled bead; its last move by the stuck rule's clock; its kind from
// panelKinds when set, else from its status.
func (t *TUI) movedSnapshot(now time.Time) []movedBead {
	if !t.paceAnswered {
		return nil
	}
	known := make(map[string]bool, len(t.paceBeads))
	for _, b := range t.paceBeads {
		known[b.ID] = true
	}
	out := make([]movedBead, 0, len(t.paceBeads))
	for _, b := range t.paceBeads {
		m := movedBead{ID: b.ID, Title: b.Title, Status: b.Status, Priority: b.Priority,
			LastMoved: t.paceBead(b, now).LastMoved(), ClosedAt: b.ClosedAt}
		for _, l := range b.Labels {
			if l == "archived" {
				m.Archived = true
			}
		}
		for _, d := range b.Dependencies {
			if d.Type == "parent-child" && d.DependsOn != "" {
				m.Parent = d.DependsOn
			}
		}
		if m.Parent == "" {
			if i := strings.LastIndex(b.ID, "."); i > 0 && known[b.ID[:i]] {
				m.Parent = b.ID[:i]
			}
		}
		m.Kind = kindFromStatus(m, b.CreatedAt, now)
		if t.panelKinds != nil {
			if k, ok := t.panelKinds.Kind(b.ID); ok {
				m.Kind = k
			}
		}
		out = append(out, m)
	}
	return out
}

// drawRigPanel lays out a snapshot across the top planTop rows.
func (t *TUI) drawRigPanel(now time.Time, agents []panelAgent, beads []panelBead) {
	top := t.planTop
	s := t.screen
	sw, _ := s.Size()
	// This frame's dot cells replace the last frame's, which the settle
	// rule reads.
	t.overlayBounds.prev, t.overlayBounds.dots = t.overlayBounds.dots, map[[2]int]overlayDot{}
	t.overlayBounds.effortTop, t.overlayBounds.effortEnd = -1, -1
	title := panelTitleOf(beads, agents, now)
	working, holding, idle := t.panelSections(agents, now)
	rows := top - 3 // title, top border, bottom border
	cells, cols, cutWorking := panelBoxLayout(rows, working, holding)
	title.Idle, title.CutWorking = idle, cutWorking

	bg := tcell.StyleDefault.Background(tcell.NewRGBColor(26, 28, 38))
	frame := bg.Foreground(tcell.NewRGBColor(130, 140, 210))
	text := bg.Foreground(tcell.NewRGBColor(220, 222, 230))
	dim := bg.Foreground(tcell.NewRGBColor(120, 124, 140))
	red := bg.Foreground(tcell.NewRGBColor(240, 100, 100))
	yellow := bg.Foreground(tcell.NewRGBColor(240, 200, 90))
	green := bg.Foreground(tcell.NewRGBColor(120, 210, 120))
	amber := bg.Foreground(tcell.NewRGBColor(240, 190, 80))
	blue := bg.Foreground(tcell.NewRGBColor(120, 170, 250))
	put := func(x, y int, line string, st tcell.Style, limit int) int {
		for _, ch := range line {
			if x >= limit {
				break
			}
			s.SetContent(x, y, ch, nil, st)
			x++
		}
		return x
	}
	for y := 0; y < top; y++ {
		for x := 0; x < sw; x++ {
			s.SetContent(x, y, ' ', nil, bg)
		}
	}

	// Title row: the rig's name on the left is the project badge, which
	// render draws over row 0 after the panel; the alarms and counts go on
	// the right. Keep the fullest header that fits beside the badge and
	// disclosures. Only the bare alarms can clip at the screen edge.
	left := t.projectBadgeEnd()
	// The overlay's disclosures stay on the title row after the name: how
	// many of the window's agents are hidden from its grid, and how many are
	// on another window. They are how a second window shows that window 1
	// hid an agent (6M4's propagation check).
	if disc := t.panelDisclosure(); disc != "" && left+2+len([]rune(disc)) < sw {
		put(left+2, 0, disc, dim, sw)
		left += 2 + len([]rune(disc))
	}
	var right string
	var spans []panelSpan
	for level := 0; level <= 5; level++ {
		right, spans = panelTitleText(title, level)
		if left+2+len([]rune(right))+1 <= sw {
			break
		}
	}
	rx := max(left+2, sw-1-len([]rune(right)))
	for i, ch := range []rune(right) {
		st := text
		for _, sp := range spans {
			if i >= sp.from && i < sp.to {
				switch sp.kind {
				case "red":
					st = red
				case "yellow":
					st = yellow
				case "dim":
					st = dim
				}
			}
		}
		if rx+i < sw {
			s.SetContent(rx+i, 0, ch, nil, st)
		}
	}

	// The boxes: the agents box (who works, then who holds a bead idle), then
	// the moved box taking the rest of the width.
	if rows < 1 {
		return
	}
	boxTitle := fmt.Sprintf("agents · %d working · %d holding", len(working), len(holding))
	// The fold, to keep the moved box at panelMovedMinWidth: the box title's
	// counts go, then the bead titles, then the time column; a bead id stays
	// while its row is shown. Past that the moved box gives way.
	type fold struct{ counts, titles, age bool }
	folds := []fold{{true, true, true}, {false, true, true}, {false, false, true}, {false, false, false}}
	var f fold
	var widths []panelBoxColumn
	box1W := 0
	fits := func(limit int) bool {
		for _, f = range folds {
			widths, box1W = panelBoxWidths(cells, rows, cols, f.titles, f.age, now)
			label := boxTitle
			if !f.counts {
				label = "agents"
			}
			box1W = max(box1W, len([]rune(label))+6)
			if box1W <= limit {
				return true
			}
		}
		return false
	}
	if !fits(sw-1-panelMovedMinWidth) && !fits(sw) {
		box1W = sw
	}
	if !f.counts {
		boxTitle = "agents"
	}
	box1W = min(box1W, sw)
	yTop, yBot := 1, 1+rows+1
	drawBox := func(x0, x1 int, label string, labelSt tcell.Style) {
		for x := x0; x <= x1 && x < sw; x++ {
			s.SetContent(x, yTop, '─', nil, frame)
			s.SetContent(x, yBot, '─', nil, frame)
		}
		for y := yTop; y <= yBot; y++ {
			if x0 < sw {
				s.SetContent(x0, y, '│', nil, frame)
			}
			if x1 < sw {
				s.SetContent(x1, y, '│', nil, frame)
			}
		}
		corner := func(x, y int, r rune) {
			if x < sw {
				s.SetContent(x, y, r, nil, frame)
			}
		}
		corner(x0, yTop, '┌')
		corner(x1, yTop, '┐')
		corner(x0, yBot, '└')
		corner(x1, yBot, '┘')
		put(x0+2, yTop, " "+label+" ", labelSt, x1)
	}
	x1 := box1W - 1
	drawBox(0, x1, boxTitle, text)
	colX := func(c int) int {
		x := 2
		for i := 0; i < c && i < len(widths); i++ {
			x += widths[i].width() + 3
		}
		return x
	}
	for i, r := range cells {
		c, y := i/rows, yTop+1+i%rows
		cw := widths[c]
		x0 := colX(c)
		limit := min(x0+cw.width(), x1)
		a := r.a
		dot := '○'
		dotSt := bg.Foreground(tcell.ColorGray)
		if r.word == "running" {
			dot = '●'
			dotSt = bg.Foreground(tcell.ColorGreen)
		}
		// The dot is a click target, as the overlay's was: it hides or shows
		// the agent; a cell that just changed agent swallows a click
		// (overlayClickSettle).
		if x0 < limit && a.Key != "" {
			cell := [2]int{x0, y}
			d := overlayDot{key: a.Key}
			if prev, ok := t.overlayBounds.prev[cell]; ok {
				if prev.key == a.Key {
					d.since = prev.since
				} else {
					d.since = overlayClickClock()
				}
			}
			t.overlayBounds.dots[cell] = d
		}
		name := a.Name
		if a.Hidden {
			name += " " + hiddenBoxMarker
		}
		x := put(x0, y, string(dot)+" ", dotSt, limit)
		x = put(x, y, fmt.Sprintf("%-*s  ", cw.name, name), text, limit)
		stSt := dim
		switch r.word {
		case "running":
			stSt = green
		case "background":
			stSt = amber
		case "prompt", "stuck":
			stSt = red
		}
		x = put(x, y, fmt.Sprintf("%-*s  ", cw.word, r.word), stSt, limit)
		if cw.age > 0 {
			x = put(x, y, fmt.Sprintf("%*s  ", cw.age, panelAge(r.began, now)), dim, limit)
		}
		beadSt := blue
		if a.Bead == "" {
			beadSt = dim
		}
		x = put(x, y, fmt.Sprintf("%-*s", cw.bead, panelBeadCell(a)), beadSt, limit)
		if cw.title > 0 && a.Title != "" {
			put(x+1, y, panelTitleWords(a.Title, cw.title), dim, limit)
		}
	}
	// The moved box gives way, rather than draw narrower than its minimum.
	if sw-box1W-1 >= panelMovedMinWidth {
		drawBox(box1W+1, sw-1, "moved 30m", text)
		// A second window does not run the bead poll this box is read from
		// (no new message between windows, ini-cdgj): it says where it is.
		if !t.isFleetAuthority() {
			put(box1W+3, yTop+1, "moved 30m is in window 1", dim, sw-1)
		} else {
			t.drawMoved(box1W+3, sw-2, yTop+1, rows, now, bg)
		}
	}
}

// drawMoved lays the moved box's rows out between x0 and limit from row y.
func (t *TUI) drawMoved(x0, limit, y, rows int, now time.Time, bg tcell.Style) {
	list := movedRows(t.panelMoved, now, rows, t.panelRigName())
	text := bg.Foreground(tcell.NewRGBColor(220, 222, 230))
	dim := bg.Foreground(tcell.NewRGBColor(120, 124, 140))
	red := bg.Foreground(tcell.NewRGBColor(240, 100, 100))
	yellow := bg.Foreground(tcell.NewRGBColor(240, 200, 90))
	green := bg.Foreground(tcell.NewRGBColor(120, 210, 120))
	teal := bg.Foreground(tcell.NewRGBColor(90, 200, 200))
	blue := bg.Foreground(tcell.NewRGBColor(120, 170, 250))
	put := func(x, y int, s string, st tcell.Style) int {
		for _, ch := range s {
			if x >= limit {
				break
			}
			t.screen.SetContent(x, y, ch, nil, st)
			x++
		}
		return x
	}
	idW := 0
	for _, r := range list {
		if r.ID != "" {
			idW = max(idW, 2*r.Depth+2+len([]rune(r.ID)))
		}
	}
	const statW, rateW = 9, 7
	titleW := limit - x0 - idW - 2 - statW - rateW - 12
	titleW = min(max(titleW, 8), movedTitleCut)
	for i, r := range list {
		if i >= rows {
			break
		}
		yy := y + i
		pad := strings.Repeat("  ", r.Depth)
		if r.More > 0 {
			put(x0+len(pad), yy, fmt.Sprintf("+%d more", r.More), dim)
			continue
		}
		x := x0
		if r.ID == "" {
			put(x+len(pad), yy, r.Title, text)
			continue
		}
		icon, ok := beadStateIcon(r.Status)
		if !ok {
			icon = beadBar
		}
		put(x, yy, fmt.Sprintf("%s%c %s", pad, icon, r.ID), blue)
		x = x0 + idW + 2
		title := []rune(r.Title)
		if len(title) > titleW {
			title = append(title[:titleW-1], '…')
		}
		put(x, yy, string(title), text)
		x += titleW + 2
		if r.Effort {
			put(x, yy, r.Done, text)
			rateSt := green
			if r.RateZero || r.Rate == "—" {
				rateSt = dim
			}
			put(x+statW, yy, r.Rate, rateSt)
			fx := x + statW + rateW
			for j, f := range r.Flags {
				if j > 0 {
					fx = put(fx, yy, "  ", dim)
				}
				st := red
				if f.Waiting {
					st = yellow
				}
				fx = put(fx, yy, f.Text, st)
			}
			continue
		}
		put(x, yy, r.Kind, teal)
		put(x+statW, yy, r.Ago, dim)
	}
}

// panelRigName names the moved box's last row, the beads under no effort:
// the project's name.
func (t *TUI) panelRigName() string {
	if t.projectName != "" {
		return t.projectName
	}
	return "initech"
}

// panelBoxTitleMax is the most a row gives its bead's title.
const panelBoxTitleMax = 32

// panelBoxColumn is one column of the agents box: its cells' widths; age and
// title are 0 when folded away.
type panelBoxColumn struct{ name, word, age, bead, title int }

func (c panelBoxColumn) width() int {
	n := 2 + c.name + 2 + c.word + 2 + c.bead
	if c.age > 0 {
		n += c.age + 2
	}
	if c.title > 0 {
		n += 1 + c.title
	}
	return n
}

// panelBoxWidths sizes each column from its agent rows and returns the total box width.
func panelBoxWidths(cells []panelBoxRow, rows, cols int, titles, age bool, now time.Time) ([]panelBoxColumn, int) {
	widths := make([]panelBoxColumn, cols)
	for i, r := range cells {
		c := &widths[i/rows]
		name := len([]rune(r.a.Name))
		if r.a.Hidden {
			name += 1 + len([]rune(hiddenBoxMarker))
		}
		c.name = max(c.name, name)
		c.word = max(c.word, len([]rune(r.word)))
		if age {
			c.age = max(c.age, len([]rune(panelAge(r.began, now))), 2)
		}
		c.bead = max(c.bead, len([]rune(panelBeadCell(r.a))))
		if titles && r.a.Title != "" {
			c.title = max(c.title, min(len([]rune(r.a.Title)), panelBoxTitleMax))
		}
	}
	w := 4 + 3*(cols-1)
	for _, col := range widths {
		w += col.width()
	}
	return widths, w
}

// panelTitleWords is as many whole words of title as fit in n runes.
func panelTitleWords(title string, n int) string {
	var b strings.Builder
	for _, w := range strings.Fields(title) {
		add := len([]rune(w))
		if b.Len() > 0 {
			add++
		}
		if len([]rune(b.String()))+add > n {
			break
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(w)
	}
	return b.String()
}

// panelHeldStatus is the statuses of a bead its assignee holds (ini-jmut):
// every unfinished one but open and deferred.
var panelHeldStatus = map[string]bool{
	"in_progress": true, "waiting": true, "blocked": true, "waiting_on_nelson": true,
	"ready_for_qa": true, "in_qa": true, "qa_passed": true, "ready_to_ship": true,
}
