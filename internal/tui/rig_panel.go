package tui

// rig_panel.go is the rig panel (ini-fed0), drawn where the Agents overlay's
// pinned section was: a title row that says whether anything needs a person,
// and the agents box, one row per awake agent with the bead in its hands. The
// rules are pure functions over a snapshot (panelSnapshot) so each is tested
// without a screen or a clock; the draw only lays out what they return.

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/rignumbers"
)

const (
	// panelAttentionAfter is how long an agent holding a bead may sit in the
	// background or idle before it moves to the needs-attention column.
	panelAttentionAfter = 20 * time.Minute
	// panelAttentionHold keeps the needs-attention column drawn this long
	// after it empties, so it does not come and go with each tick.
	panelAttentionHold = time.Minute
	// panelDefaultRows is the boxes' height in rows, under their top border.
	panelDefaultRows = 8
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
	State      ActivityState
	Since      time.Time
	Suspended  bool
	Alive      bool
	Bead       string
	BeadStatus string
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

// needsAttention reports whether an agent belongs in the needs-attention
// column: at a prompt, or holding a bead and quiet for panelAttentionAfter.
func needsAttention(a panelAgent, now time.Time) bool {
	if a.atPrompt() {
		return true
	}
	if a.Bead == "" || a.Since.IsZero() || a.State == StateRunning {
		return false
	}
	return now.Sub(a.Since) >= panelAttentionAfter
}

// panelColumns splits the awake agents, in their given order, into the
// working and needs-attention columns.
func panelColumns(agents []panelAgent, now time.Time) (working, attention []panelAgent) {
	for _, a := range agents {
		if !a.awake() {
			continue
		}
		if needsAttention(a, now) {
			attention = append(attention, a)
		} else {
			working = append(working, a)
		}
	}
	return working, attention
}

// panelHold remembers when the needs-attention column last held anyone, so
// it is drawn for panelAttentionHold after it empties.
type panelHold struct {
	lastNonEmpty time.Time
}

// show reports whether to draw the column now.
func (h *panelHold) show(nonEmpty bool, now time.Time) bool {
	if nonEmpty {
		h.lastNonEmpty = now
		return true
	}
	return !h.lastNonEmpty.IsZero() && now.Sub(h.lastNonEmpty) < panelAttentionHold
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

// panelRowCells is one agent row's cells: dot, name, state, time, bead.
func panelRowCells(a panelAgent, now time.Time) (dot rune, name, state, age, bead string) {
	dot = '○'
	if a.State == StateRunning {
		dot = '●'
	}
	return dot, a.Name, panelStateWord(a.State), panelAge(a.Since, now), panelBeadCell(a)
}

// panelColumnWidths is the agents column layout: the widest name, state,
// time and bead cell of the rows, with or without the bead column.
type panelColumnWidths struct {
	name, state, age, bead int
	withBead               bool
}

// width is the column's whole width, from the dot to the last cell.
func (w panelColumnWidths) width() int {
	n := 2 + w.name + 2 + w.state + 2 + w.age
	if w.withBead {
		n += 2 + w.bead
	}
	return n
}

func panelWidthsOf(rows []panelAgent, now time.Time) panelColumnWidths {
	w := panelColumnWidths{name: 5, state: 7, age: 5, bead: 1, withBead: true}
	for _, a := range rows {
		_, name, state, age, bead := panelRowCells(a, now)
		w.name = max(w.name, len([]rune(name)))
		w.state = max(w.state, len([]rune(state)))
		w.age = max(w.age, len([]rune(age)))
		w.bead = max(w.bead, len([]rune(bead)))
	}
	return w
}

// panelTitleText is the title row's right side as text at fold level, and
// which rune spans are red, yellow or dim. Level 0 is whole; 1 gives each
// alarm its bare count; 2 also leaves out the suspended count, 3 the awake
// count too. The alarms themselves are never left out.
type panelSpan struct {
	from, to int
	kind     string // "red", "yellow", "dim"
}

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
	if level < 3 {
		write(" · ", "dim")
		write(fmt.Sprintf("%d awake", t.Awake), "")
	}
	if level < 2 {
		write(" · ", "dim")
		write(fmt.Sprintf("%d suspended", t.Suspended), "")
	}
	return b.String(), spans
}

// ── window 1's snapshot and the draw ────────────────────────────────

// panelSnapshot is what the panel reads from this window: its agents in the
// Agents panel's order, and the polled beads.
func (t *TUI) panelSnapshot(now time.Time) ([]panelAgent, []panelBead) {
	status := map[string]string{}
	var beads []panelBead
	if t.paceAnswered {
		for _, b := range t.paceBeads {
			status[b.ID] = b.Status
			beads = append(beads, panelBead{ID: b.ID, Status: b.Status, LastMoved: t.paceBead(b, now).LastMoved()})
		}
	}
	var agents []panelAgent
	for _, p := range t.overlayDisplayOrder(t.visiblePanesForWindow()) {
		a := panelAgent{Name: p.Name(), State: p.Activity(), Suspended: p.IsSuspended(), Alive: p.IsAlive(), Bead: p.BeadID()}
		if p.Host() != "" {
			a.Name = p.Host() + ":" + p.Name()
		}
		a.BeadStatus = status[a.Bead]
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

// renderRigPanel draws the panel across the top planTop rows.
func (t *TUI) renderRigPanel(now time.Time) {
	top := t.planTop
	if top == 0 {
		return
	}
	agents, beads := t.panelSnapshot(now)
	t.drawRigPanel(now, agents, beads)
}

// drawRigPanel lays out a snapshot across the top planTop rows.
func (t *TUI) drawRigPanel(now time.Time, agents []panelAgent, beads []panelBead) {
	top := t.planTop
	s := t.screen
	sw, _ := s.Size()
	title := panelTitleOf(beads, agents, now)
	working, attention := panelColumns(agents, now)
	showAttn := t.panelAttn.show(len(attention) > 0, now)

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
	// the right. The fold: the alarms lose their ids, then the suspended and
	// the awake counts go whole; past that the line is cut at the right edge.
	// The alarms, the row's job, are kept longest.
	left := t.projectBadgeEnd()
	var right string
	var spans []panelSpan
	for level := 0; level <= 3; level++ {
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

	// The boxes: the agents box (working, then needs attention), then the
	// moved box taking the rest of the width.
	rows := top - 3 // title, top border, bottom border
	if rows < 1 {
		return
	}
	all := append(append([]panelAgent(nil), working...), attention...)
	w := panelWidthsOf(all, now)
	colW := w.width()
	cols := 1
	if showAttn {
		cols = 2
	}
	box1W := cols*colW + (cols-1)*3 + 4
	if box1W > sw*2/3 {
		w.withBead = false // The fold: the bead column goes before anything else.
		colW = w.width()
		box1W = cols*colW + (cols-1)*3 + 4
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
	drawColumn := func(x0, limit int, list []panelAgent) {
		for i := 0; i < rows && i < len(list); i++ {
			a := list[i]
			dot, nm, state, age, bead := panelRowCells(a, now)
			y := yTop + 1 + i
			// The dots keep the terminal's named green and gray, as the
			// overlay drew them (ini-1ss).
			dotSt := bg.Foreground(tcell.ColorGray)
			if dot == '●' {
				dotSt = bg.Foreground(tcell.ColorGreen)
			}
			x := put(x0, y, string(dot)+" ", dotSt, limit)
			x = put(x, y, fmt.Sprintf("%-*s  ", w.name, nm), text, limit)
			stSt := dim
			switch a.State {
			case StateRunning:
				stSt = green
			case StateBackground:
				stSt = amber
			case StateWaitingInput:
				stSt = red
			}
			x = put(x, y, fmt.Sprintf("%-*s  ", w.state, state), stSt, limit)
			x = put(x, y, fmt.Sprintf("%*s", w.age, age), dim, limit)
			if w.withBead {
				beadSt := blue
				if a.Bead == "" {
					beadSt = dim
				}
				put(x+2, y, bead, beadSt, limit)
			}
		}
		if len(list) > rows {
			put(x0, yTop+rows, fmt.Sprintf("+%d more", len(list)-rows+1), dim, limit)
		}
	}
	x1 := box1W - 1
	drawBox(0, x1, "agents · working", text)
	drawColumn(2, 2+colW, working)
	if showAttn {
		ax := 2 + colW + 3
		put(ax, yTop, " needs attention ", red, x1)
		drawColumn(ax, x1, attention)
	}
	if box1W+2 < sw {
		drawBox(box1W+1, sw-1, "moved 30m", text)
	}
}
