package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"

	"github.com/nmelo/initech/internal/watch"
)

// The watches modal (Watches 3, docs/design/watches.md §7, the picture
// docs/pictures/initech-watches-modal-mock.png): what every agent waits for,
// read-only, in the main window only. It keeps no copy of the watches: each
// draw reads Snapshot, the bead poll and the receipts again, so an ending or a
// new watch shows on the next frame. Its own state is in memory only.

// watchesModal is the modal's state: open or not, the selected watch by id
// (so a row that moves between groups keeps the cursor), and whether the
// detail box is open.
type watchesModal struct {
	active   bool
	selected string
	detail   bool
}

// The line marks an ended watch's deadline cell carries while its line has not
// reached its owner (pm's G5).
const (
	lineHeld     = "held"
	lineStarting = "starting"
	lineToSuper  = "to super"
	lineDropped  = "dropped"
)

// watchSeg is one run of a cell's text; id runs draw in the bead-id color.
type watchSeg struct {
	text string
	id   bool
}

// watchRow is one drawn row of the modal.
type watchRow struct {
	w        watch.Watch
	mark     rune
	waitsFor []watchSeg
	age      string
	deadline string
	// line is the undelivered ending's mark ("" when there is none) and where
	// is the detail box's sentence on where its line is.
	line  string
	where string
}

// watchesView is everything one draw shows.
type watchesView struct {
	waiting, ended []watchRow
	// endedHour counts the endings of the last hour that were delivered, sent
	// to super, dropped or cancelled, missed ones included; a held or starting
	// ending is listed with its mark but not counted.
	nWaiting, endedHour, missed int
}

// rows is the modal's rows in screen order.
func (v watchesView) rows() []watchRow {
	return append(append([]watchRow{}, v.waiting...), v.ended...)
}

// watchHistoryFrom is when an ended watch's hour starts, as the store prunes
// it: a cancel from its cancel, a fired or missed watch from its delivery, or
// from its handing when it went to super or was dropped; zero while its line
// is still on its way.
func watchHistoryFrom(w watch.Watch) time.Time {
	switch {
	case w.State == watch.Cancelled:
		return w.Ended
	case !w.Delivery.Delivered.IsZero():
		return w.Delivery.Delivered
	case w.Delivery.ToSuper, w.Delivery.Dropped != "":
		return w.Delivery.Handed
	}
	return time.Time{}
}

// buildWatchesView turns a snapshot into the modal's rows. world gives the
// beads (titles and icons) and the receipts (why a handed line is held);
// starting reports whether an owner's pane is coming up now.
func buildWatchesView(ws []watch.Watch, world World, starting func(owner string) bool, now time.Time) watchesView {
	var v watchesView
	for _, w := range ws {
		r := watchRow{w: w, waitsFor: watchWaitsFor(w, world), age: watchDur(now.Sub(w.Set))}
		switch w.State {
		case watch.Waiting:
			r.mark = '◌'
			if w.Kind == watch.KindDeadline {
				r.deadline = "at " + watchClock(w.Deadline, now)
			} else {
				r.deadline = "in " + watchDur(w.Deadline.Sub(now))
			}
			v.waiting = append(v.waiting, r)
			continue
		case watch.Fired:
			r.mark, r.deadline = '✓', "fired "+watchDur(now.Sub(w.Ended))+" ago"
		case watch.Missed:
			r.mark, r.deadline = '!', "missed "+watchDur(now.Sub(w.Ended))+" ago"
		case watch.Cancelled:
			r.mark, r.deadline = '×', "cancelled "+watchDur(now.Sub(w.Ended))+" ago"
		default:
			continue // the store refuses any other state at load
		}
		from := watchHistoryFrom(w)
		if !from.IsZero() && now.Sub(from) >= watch.History {
			continue // past its hour; the next tick prunes it
		}
		if w.State != watch.Cancelled {
			r.line, r.where = watchLine(w, world, starting(w.Owner))
		}
		if r.line != "" {
			r.deadline += " · " + r.line
		}
		if w.State == watch.Cancelled || !w.Delivery.Delivered.IsZero() || w.Delivery.ToSuper || w.Delivery.Dropped != "" {
			v.endedHour++
		}
		if w.State == watch.Missed {
			v.missed++
		}
		v.ended = append(v.ended, r)
	}
	sort.SliceStable(v.waiting, func(i, j int) bool {
		return v.waiting[i].w.Deadline.Before(v.waiting[j].w.Deadline)
	})
	// Missed, then fired, then cancelled, the most recent ending first within
	// each (AC-O, as the picture shows).
	rank := map[watch.State]int{watch.Missed: 0, watch.Fired: 1, watch.Cancelled: 2}
	sort.SliceStable(v.ended, func(i, j int) bool {
		a, b := v.ended[i].w, v.ended[j].w
		if rank[a.State] != rank[b.State] {
			return rank[a.State] < rank[b.State]
		}
		return a.Ended.After(b.Ended)
	})
	v.nWaiting = len(v.waiting)
	return v
}

// watchLine is where a fired or missed watch's line is: its mark ("" once
// delivered) and the detail box's sentence. The line is worked out live from
// the delivery record (S3) and the receipt (S4); nothing extra is stored.
func watchLine(w watch.Watch, world World, starting bool) (mark, where string) {
	d := w.Delivery
	switch {
	case d.ToSuper:
		return lineToSuper, "sent to super: the owner was gone"
	case d.Dropped != "":
		// initech dropped the handed line (its held mail ended undelivered):
		// it is not held, and it will not arrive.
		return lineDropped, "dropped: " + d.Dropped
	case !d.Delivered.IsZero():
		return "", "delivered " + d.Delivered.Local().Format("15:04")
	case d.Handed.IsZero() && starting:
		return lineStarting, "not yet handed: " + w.Owner + "'s pane is starting"
	case d.Handed.IsZero():
		return lineHeld, "not yet handed to delivery"
	}
	reason := "unknown"
	if world != nil {
		if r := world.Receipt(w.Owner, d.Receipt); r.Kind != "" {
			reason = r.Kind
		}
	}
	return lineHeld, fmt.Sprintf("handed %s as receipt %s, held: %s", d.Handed.Local().Format("15:04"), d.Receipt, reason)
}

// watchWaitsFor is a watch's condition in the modal's words (G3). A bead
// shows by its id with its state icon and title from the last bead poll, or
// by its id alone when the poll has not seen it.
func watchWaitsFor(w watch.Watch, world World) []watchSeg {
	p := w.Params
	bead := func(id, rest string) []watchSeg {
		if world != nil {
			if b, ok := world.Bead(id); ok {
				head := id
				if icon, ok := beadStateIcon(b.Status); ok {
					head = string(icon) + " " + id
				}
				return []watchSeg{{text: head, id: true}, {text: " " + b.Title + rest}}
			}
		}
		return []watchSeg{{text: id, id: true}, {text: rest}}
	}
	switch w.Kind {
	case watch.KindBeadStatus:
		return bead(p["bead"], ": "+p["status"])
	case watch.KindBeadComment:
		return bead(p["bead"], ": a comment by "+p["author"])
	case watch.KindDeadline:
		return []watchSeg{{text: "a time only"}}
	case watch.KindSendDelivered:
		return []watchSeg{{text: "my send to " + p["pane"] + " is delivered"}}
	case watch.KindSendHeld:
		return []watchSeg{{text: "my send to " + p["pane"] + " still held after " + p["for"]}}
	}
	return []watchSeg{{text: string(w.Kind) + " (not known to this initech)"}}
}

// watchSegsText is a cell's plain text.
func watchSegsText(segs []watchSeg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.text)
	}
	return b.String()
}

// watchDur is a length of time as the modal shows it: 8m, 3h 48m, 2d 4h.
func watchDur(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	m := int(d / time.Minute)
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	h, m := m/60, m%60
	if h < 24 {
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dd %dh", h/24, h%24)
}

// watchClock is a deadline's clock time in now's zone, with its date when it
// is not today.
func watchClock(at, now time.Time) string {
	at = at.In(now.Location())
	if y, d := at.YearDay(), now.YearDay(); y != d || at.Year() != now.Year() {
		return at.Format("Jan 2 3:04 PM")
	}
	return at.Format("3:04 PM")
}

// watchOwnerStarting reports whether owner's pane is coming up now: the
// send path's startup gate (awaitStartupSettled) read without waiting. Codex
// and agy panes check their composer instead, so they never read as
// starting. A variable so tests feed fixtures.
var watchOwnerStarting = func(t *TUI, owner string, now time.Time) bool {
	p := t.findPane(owner)
	return p != nil && p.startingNow(now)
}

// startingNow is awaitStartupSettled's gate without its wait.
func (p *Pane) startingNow(now time.Time) bool {
	if p.agentReader() != noAgentReader || !p.hasPTY() {
		return false
	}
	p.mu.Lock()
	settled, started := p.startupSettled, p.startedAt
	p.mu.Unlock()
	return !settled && !started.IsZero() && now.Sub(started) < startupGateWindow
}

// watchesNow builds the view from window 1's store at now.
func (t *TUI) watchesNow(now time.Time) watchesView {
	if t.watches == nil {
		return watchesView{}
	}
	return buildWatchesView(t.watches.Snapshot(now), t.watchWorld(now),
		func(owner string) bool { return watchOwnerStarting(t, owner, now) }, now)
}

// openWatchesModal opens the modal. Like the agents modal it is the main
// window's: the gate is here, so Alt+t, the command bar and any later opener
// share it.
func (t *TUI) openWatchesModal() {
	if !t.isFleetAuthority() {
		t.noticeMainWindowOnly("The watches modal")
		return
	}
	t.watchesM = watchesModal{active: true}
}

// handleWatchesKey is the modal's keys: up and down move, Enter opens and
// closes the detail box, Esc closes the box or the modal, Alt+t closes the
// modal. Everything else is swallowed: the modal is read-only and no key
// reaches a pane under it.
func (t *TUI) handleWatchesKey(ev *tcell.EventKey) bool {
	if ev.Modifiers()&tcell.ModAlt != 0 && ev.Key() == tcell.KeyRune && ev.Rune() == 't' {
		t.watchesM = watchesModal{}
		return false
	}
	if t.watchesM.detail {
		if ev.Key() == tcell.KeyEnter || ev.Key() == tcell.KeyEscape {
			t.watchesM.detail = false
		}
		return false
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		t.watchesM = watchesModal{}
	case tcell.KeyUp:
		t.moveWatchesSelection(-1)
	case tcell.KeyDown:
		t.moveWatchesSelection(1)
	case tcell.KeyEnter:
		if _, ok := t.selectedWatchRow(t.watchesNow(time.Now()).rows()); ok {
			t.watchesM.detail = true
		}
	}
	return false
}

// selectedWatchRow is the selected row's index, the first row when the
// selected watch is gone or none was chosen.
func (t *TUI) selectedWatchRow(rows []watchRow) (int, bool) {
	if len(rows) == 0 {
		return 0, false
	}
	for i, r := range rows {
		if r.w.ID == t.watchesM.selected {
			return i, true
		}
	}
	return 0, true
}

func (t *TUI) moveWatchesSelection(by int) {
	rows := t.watchesNow(time.Now()).rows()
	i, ok := t.selectedWatchRow(rows)
	if !ok {
		return
	}
	i = max(0, min(len(rows)-1, i+by))
	t.watchesM.selected = rows[i].w.ID
}

// The picture's colors.
var (
	wmBg     = tcell.NewRGBColor(0x1f, 0x21, 0x27)
	wmShade  = tcell.NewRGBColor(0x22, 0x25, 0x2c)
	wmHead   = tcell.NewRGBColor(0x10, 0x12, 0x16)
	wmBand   = tcell.NewRGBColor(0x3a, 0x40, 0x54)
	wmSel    = tcell.NewRGBColor(0x32, 0x35, 0x4b)
	wmSelSh  = tcell.NewRGBColor(0x35, 0x38, 0x4f)
	wmBorder = tcell.NewRGBColor(0x7f, 0x86, 0xd9)
	wmRule   = tcell.NewRGBColor(0x34, 0x38, 0x42)
	wmText   = tcell.NewRGBColor(0xc9, 0xcd, 0xd6)
	wmBright = tcell.NewRGBColor(0xe3, 0xe6, 0xec)
	wmDim    = tcell.NewRGBColor(0x8b, 0x90, 0x9b)
	wmBandFg = tcell.NewRGBColor(0xff, 0xff, 0xff)
	wmID     = tcell.NewRGBColor(0x6e, 0xa8, 0xff)
	wmOK     = tcell.NewRGBColor(0x8f, 0xbf, 0x6a)
	wmBad    = tcell.NewRGBColor(0xe0, 0x6c, 0x75)
	wmWarn   = tcell.NewRGBColor(0xe5, 0xc0, 0x7b)
)

// The columns: mark, set by, waits for, why, age, deadline. Waits for and age
// are shaded: the columns are told apart by shade, with no separator lines.
const (
	wcMark = iota
	wcSetBy
	wcWaits
	wcWhy
	wcAge
	wcDeadline
	wcCount
)

var watchesHeads = [wcCount]string{"", "set by", "waits for", "why (the line it will get)", "age", "deadline"}

func watchColShaded(c int) bool { return c == wcWaits || c == wcAge }

// watchesGrid is the picture's column grid in its pixels: mark 34, set by 180,
// waits for 570, why 430, age 80, and the rest of its 1508 for the deadline.
var watchesGrid = [wcCount]int{34, 180, 570, 430, 80, 214}

// watchesAgeCols is the age column's width in cells, the picture's 80 px.
const watchesAgeCols = 7

// watchesColumns sizes the columns for an inner width in the picture's
// proportions. Mark, set by, age and deadline keep a floor that fits their
// cells ("1h 2m", and the longest deadline cell, so "· held" is never cut);
// what the floors take comes out of waits for and why, in the picture's ratio
// between them.
func watchesColumns(inner int, rows []watchRow) [wcCount]int {
	total := 0
	for _, g := range watchesGrid {
		total += g
	}
	dl := len("deadline")
	for _, r := range rows {
		dl = max(dl, runewidth.StringWidth(r.deadline))
	}
	floor := [wcCount]int{2, 8, 0, 0, 7, min(dl, 28) + 2}
	var c [wcCount]int
	fixed := 0
	for _, i := range []int{wcMark, wcSetBy, wcDeadline} {
		c[i] = max(inner*watchesGrid[i]/total, floor[i])
		fixed += c[i]
	}
	// Age is as narrow as the picture's at every width (AC-W): its 80 px hold
	// "1h 2m" and the column's space, 7 cells.
	c[wcAge] = watchesAgeCols
	fixed += c[wcAge]
	rest := max(inner-fixed, 2)
	c[wcWaits] = rest * watchesGrid[wcWaits] / (watchesGrid[wcWaits] + watchesGrid[wcWhy])
	c[wcWhy] = rest - c[wcWaits]
	// In a narrow window the floors can add up to more than the table: the
	// widest column gives up a cell at a time until they fit, and its cells
	// clip with "…", so nothing crosses the border.
	for watchColsSum(c[:]) > inner {
		widest := 0
		for i := range c {
			if c[i] > c[widest] {
				widest = i
			}
		}
		if c[widest] == 0 {
			break
		}
		c[widest]--
	}
	return c
}

// watchColsSum is the columns' total width.
func watchColsSum(xs []int) int {
	n := 0
	for _, x := range xs {
		n += x
	}
	return n
}

// The window's margin from the pane's edges, and its padding inside the
// border, in cells, as the picture gives them.
const (
	watchesMarginX = 4 // columns on each side
	watchesMarginY = 2 // rows at the top and the bottom
	watchesPadX    = 3 // columns inside the border, left and right
)

// watchesBoxWidth is the modal's width: the window less its margin on each
// side, centred; the whole window when it is too narrow for a margin.
func watchesBoxWidth(sw int) int {
	if sw < 40 {
		return sw
	}
	return sw - 2*watchesMarginX
}

// dimBehind darkens every cell already drawn, under the picture's backdrop
// (rgba(6,7,9,.86) over everything behind the modal, panes and panel).
func dimBehind(s tcell.Screen) {
	sw, sh := s.Size()
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			mainc, combc, st, w := s.GetContent(x, y)
			fg, bg, attr := st.Decompose()
			s.SetContent(x, y, mainc, combc, tcell.StyleDefault.
				Foreground(watchDimColor(fg, wmText)).Background(watchDimColor(bg, wmScreenBg)).Attributes(attr))
			if w > 1 {
				x += w - 1
			}
		}
	}
}

// wmScreenBg stands for a default background behind the modal: the picture's.
var wmScreenBg = tcell.NewRGBColor(0x16, 0x18, 0x1d)

// watchDimColor is c under the backdrop; a default or palette color without RGB
// is dimmed from fallback.
func watchDimColor(c, fallback tcell.Color) tcell.Color {
	r, g, b := c.RGB()
	if r < 0 {
		r, g, b = fallback.RGB()
	}
	mix := func(v, o int32) int32 { return (v*14 + o*86) / 100 }
	return tcell.NewRGBColor(mix(r, 6), mix(g, 7), mix(b, 9))
}

// renderWatchesModal draws the modal and, over it, the detail box.
func (t *TUI) renderWatchesModal() {
	s := t.screen
	if s == nil {
		return
	}
	now := time.Now()
	v := t.watchesNow(now)
	t.drawWatchesModal(s, v, now)
}

// wmLine is one line of the table: the header, a band, a row or a spacer.
type wmLine struct {
	kind int // wmHeadLine, wmBandLine, wmRowLine, wmSpaceLine, wmNoneLine
	band string
	row  int // index into rows for wmRowLine
}

const (
	wmHeadLine = iota
	wmBandLine
	wmRowLine
	wmSpaceLine
	wmNoneLine
	wmBandPadLine // a band's first row: tinted and blank
	wmPlainLine   // the window's own blank row above the rule, unshaded
)

func watchesLines(v watchesView) []wmLine {
	var ls []wmLine
	n := 0
	if len(v.waiting) > 0 {
		ls = append(ls, wmLine{kind: wmBandPadLine}, wmLine{kind: wmBandLine, band: "waiting"})
		for range v.waiting {
			ls = append(ls, wmLine{kind: wmRowLine, row: n})
			n++
		}
	}
	if len(v.ended) > 0 {
		ls = append(ls, wmLine{kind: wmBandPadLine}, wmLine{kind: wmBandLine, band: "ended in the last hour"})
		for range v.ended {
			ls = append(ls, wmLine{kind: wmRowLine, row: n})
			n++
		}
	}
	if n == 0 {
		ls = append(ls, wmLine{kind: wmNoneLine})
	}
	return append(ls, wmLine{kind: wmSpaceLine}, wmLine{kind: wmPlainLine})
}

func (t *TUI) drawWatchesModal(s tcell.Screen, v watchesView, now time.Time) {
	sw, sh := s.Size()
	rows := v.rows()
	sel, _ := t.selectedWatchRow(rows)
	lines := watchesLines(v)

	dimBehind(s)
	boxW := watchesBoxWidth(sw)
	// Border, a blank line, the header, the table lines, the footer rule, the
	// footer and the border.
	boxH := min(len(lines)+6, sh-2*watchesMarginY)
	if boxH < 8 {
		boxH = min(sh, 8)
	}
	x0, y0 := max((sw-boxW)/2, 0), max((sh-boxH)/2, 0)
	inner := max(boxW-2-2*watchesPadX, 1)
	cols := watchesColumns(inner, rows)

	base := tcell.StyleDefault.Background(wmBg).Foreground(wmText)
	fill := func(x, y, w int, st tcell.Style) {
		for i := 0; i < w; i++ {
			if x+i < sw && y < sh {
				s.SetContent(x+i, y, ' ', nil, st)
			}
		}
	}
	put := func(x, y, w int, text string, st tcell.Style) int {
		col := 0
		for _, ch := range text {
			cw := runewidth.RuneWidth(ch)
			if col+cw > w {
				break
			}
			if x+col < sw && y < sh {
				s.SetContent(x+col, y, ch, nil, st)
			}
			col += cw
		}
		return col
	}
	clip := func(text string, w int) string {
		if runewidth.StringWidth(text) <= w {
			return text
		}
		return runewidth.Truncate(text, w, "…")
	}

	for y := y0; y < y0+boxH; y++ {
		fill(x0, y, boxW, base)
	}
	border := base.Foreground(wmBorder)
	for x := x0 + 1; x < x0+boxW-1; x++ {
		s.SetContent(x, y0, '─', nil, border)
		s.SetContent(x, y0+boxH-1, '─', nil, border)
	}
	for y := y0 + 1; y < y0+boxH-1; y++ {
		s.SetContent(x0, y, '│', nil, border)
		s.SetContent(x0+boxW-1, y, '│', nil, border)
	}
	s.SetContent(x0, y0, '┌', nil, border)
	s.SetContent(x0+boxW-1, y0, '┐', nil, border)
	s.SetContent(x0, y0+boxH-1, '└', nil, border)
	s.SetContent(x0+boxW-1, y0+boxH-1, '┘', nil, border)

	// The title line on the top border, with the counts; the missed count red.
	tx, tmax := x0+2, boxW-4
	title := []watchSeg{{text: " "}, {text: "watches"}}
	styles := []tcell.Style{base, base.Foreground(tcell.ColorWhite).Bold(true)}
	sep := base.Foreground(wmDim)
	add := func(text string, st tcell.Style) {
		title = append(title, watchSeg{text: text})
		styles = append(styles, st)
	}
	if t.projectName != "" {
		add(" · "+t.projectName+" ·", sep)
	} else {
		add(" ·", sep)
	}
	add(fmt.Sprintf(" %d waiting", v.nWaiting), base)
	add(" ·", sep)
	add(fmt.Sprintf(" %d ended in the last hour", v.endedHour), base)
	if v.missed > 0 {
		add(" ·", sep)
		add(fmt.Sprintf(" %d missed its deadline", v.missed), base.Foreground(wmBad))
	}
	add(" ", base)
	texts := make([]string, len(title))
	for i, seg := range title {
		texts[i] = seg.text
	}
	for i, text := range fitWatchTitle(texts, tmax) {
		n := put(tx, y0, tmax, text, styles[i])
		tx, tmax = tx+n, tmax-n
	}

	// The table: the header band, then the lines, scrolled to keep the
	// selected row in view.
	tableX, y := x0+1+watchesPadX, y0+2
	head := tcell.StyleDefault.Background(wmHead).Foreground(wmBright).Bold(true)
	fill(tableX, y, inner, head)
	cx := tableX
	for c := 0; c < wcCount; c++ {
		pad := 1
		put(cx+pad, y, cols[c]-pad, clip(watchesHeads[c], cols[c]-pad), head)
		cx += cols[c]
	}
	y++
	room := boxH - 6 // border 2, blank 1, header 1, footer rule and footer 2
	selLine := 0
	for i, l := range lines {
		if l.kind == wmRowLine && l.row == sel {
			selLine = i
		}
	}
	off := 0
	if len(lines) > room && room > 0 {
		off = min(max(selLine-room+2, 0), len(lines)-room)
	}
	for i := off; i < len(lines) && i < off+room; i++ {
		l := lines[i]
		switch l.kind {
		case wmBandPadLine:
			fill(tableX, y, inner, tcell.StyleDefault.Background(wmBand))
		case wmBandLine:
			band := tcell.StyleDefault.Background(wmBand).Foreground(wmBandFg).Bold(true)
			fill(tableX, y, inner, band)
			put(tableX+1, y, inner-1, clip(l.band, inner-1), band)
		case wmNoneLine:
			t.drawWatchCells(fill, put, clip, tableX, y, cols, false, watchRow{}, base, true)
		case wmSpaceLine:
			t.drawWatchCells(fill, put, clip, tableX, y, cols, false, watchRow{}, base, false)
		case wmRowLine:
			t.drawWatchCells(fill, put, clip, tableX, y, cols, l.row == sel, rows[l.row], base, false)
		}
		y++
	}

	// The footer, under a rule, as in the picture.
	ry := y0 + boxH - 3
	for x := x0 + 1; x < x0+boxW-1; x++ {
		s.SetContent(x, ry, '─', nil, base.Foreground(wmRule))
	}
	fx, fmax := tableX, inner
	word := base.Foreground(wmDim)
	// The keys are bright; the last hint's "read-only:" is dim, as pictured.
	keyStyle := func(i int) tcell.Style {
		if i == len(watchesHints)-1 {
			return word
		}
		return base
	}
	for i, h := range watchesHints {
		sepText := "     "
		if i == len(watchesHints)-1 {
			sepText = "     ·     "
		}
		if i == 0 {
			sepText = ""
		}
		need := runewidth.StringWidth(sepText + h.key + " " + h.words)
		if need > fmax {
			break // a hint shows whole or not at all
		}
		for _, part := range []struct {
			text string
			st   tcell.Style
		}{{sepText, word}, {h.key, keyStyle(i)}, {" " + h.words, word}} {
			n := put(fx, ry+1, fmax, part.text, part.st)
			fx, fmax = fx+n, fmax-n
		}
	}

	if t.watchesM.detail && len(rows) > 0 {
		t.drawWatchDetail(s, rows[sel], now, put, fill, base, border)
	}
}

// fitWatchTitle fits the title's runs into max cells. A title that fits is
// unchanged. One that does not is cut between its phrases ("3 waiting",
// "1 missed its deadline"), never inside a word or between a count and what
// it counts: whole runs are kept while they fit, a separator left at the end
// is dropped, and "…" and the closing space follow, like the header and
// footer. The result has one entry per run, emptied past the cut.
func fitWatchTitle(runs []string, max int) []string {
	total := 0
	for _, r := range runs {
		total += runewidth.StringWidth(r)
	}
	if total <= max {
		return runs
	}
	out := make([]string, len(runs))
	budget := max - 2 // "…" and the closing space
	used, last := 0, -1
	for i, r := range runs[:len(runs)-1] {
		w := runewidth.StringWidth(r)
		if used+w > budget {
			break
		}
		out[i], used, last = r, used+w, i
	}
	// A cut right after a separator would read "· …": drop it.
	for last >= 0 {
		trimmed := strings.TrimSuffix(out[last], " ·")
		if trimmed == out[last] {
			break
		}
		out[last] = trimmed
		if strings.TrimSpace(trimmed) == "" {
			out[last] = ""
			last--
		}
	}
	if last < 0 {
		return out
	}
	out[last] += "…"
	out[len(out)-1] = " "
	return out
}

// watchesHints is the footer, left to right; a narrow modal drops whole hints
// from the right.
var watchesHints = []struct{ key, words string }{
	{"esc", "close"},
	{"↑↓", "move"},
	{"enter", "the full line and when the watch was set"},
	{"read-only:", "an agent cancels its own watch"},
}

// drawWatchCells draws one table line's cells: a row, or a blank line that
// keeps the column shades (none true: the "no watches" line).
func (t *TUI) drawWatchCells(fill func(x, y, w int, st tcell.Style), put func(x, y, w int, text string, st tcell.Style) int,
	clip func(string, int) string, x, y int, cols [wcCount]int, selected bool, r watchRow, base tcell.Style, none bool) {
	cx := x
	for c := 0; c < wcCount; c++ {
		bg := wmBg
		if watchColShaded(c) {
			bg = wmShade
		}
		if selected {
			bg = wmSel
			if watchColShaded(c) {
				bg = wmSelSh
			}
		}
		cell := base.Background(bg)
		fill(cx, y, cols[c], cell)
		pad := 1
		w := cols[c] - pad
		text := cell
		ended := r.w.State == watch.Fired || r.w.State == watch.Cancelled
		if ended {
			text = cell.Foreground(wmDim)
		}
		switch {
		case none && c == wcWaits:
			put(cx+pad, y, w, "no watches", cell.Foreground(wmDim))
		case none || r.mark == 0:
		case c == wcMark:
			mk := cell.Foreground(wmWarn)
			switch r.w.State {
			case watch.Fired:
				mk = cell.Foreground(wmOK)
			case watch.Missed:
				mk = cell.Foreground(wmBad)
			case watch.Cancelled:
				mk = cell.Foreground(wmDim)
			}
			put(cx+pad, y, w, string(r.mark), mk)
		case c == wcSetBy:
			put(cx+pad, y, w, clip(r.w.Owner, w), text)
		case c == wcWaits:
			left, px := w, cx+pad
			full := watchSegsText(r.waitsFor)
			clipped := runewidth.StringWidth(full) > w
			for _, seg := range r.waitsFor {
				st := text
				if seg.id {
					st = cell.Foreground(wmID)
				}
				segText := seg.text
				if clipped && runewidth.StringWidth(segText) >= left {
					segText = clip(segText, left)
				}
				n := put(px, y, left, segText, st)
				px, left = px+n, left-n
				if left <= 0 {
					break
				}
			}
		case c == wcWhy:
			put(cx+pad, y, w, clip(r.w.Why, w), text)
		case c == wcAge:
			put(cx+pad, y, w, clip(r.age, w), cell.Foreground(wmDim))
		case c == wcDeadline:
			st := text
			if r.w.State == watch.Missed {
				st = cell.Foreground(wmBad)
			}
			put(cx+pad, y, w, clip(r.deadline, w), st)
		}
		cx += cols[c]
	}
}

// watchDetailLines is the detail box's text for one row: the whole condition
// and reason, when it was set, its deadline, and for an ended watch how it
// ended and where its line is.
func watchDetailLines(r watchRow, now time.Time) []string {
	w := r.w
	ls := []string{
		fmt.Sprintf("%s · set by %s · %s", w.ID, w.Owner, w.State),
		"waits for: " + watchSegsText(r.waitsFor),
		"why: " + w.Why,
		fmt.Sprintf("set: %s (%s ago)", w.Set.In(now.Location()).Format("Jan 2 3:04 PM"), watchDur(now.Sub(w.Set))),
		"deadline: " + w.Deadline.In(now.Location()).Format("Jan 2 3:04 PM"),
	}
	if w.State != watch.Waiting {
		ended := fmt.Sprintf("ended: %s %s ago", w.State, watchDur(now.Sub(w.Ended)))
		if w.Detail != "" {
			ended += " (" + w.Detail + ")"
		}
		ls = append(ls, ended)
		if w.Late {
			ls = append(ls, "late: it ended while initech was down")
		}
	}
	if r.where != "" {
		ls = append(ls, "line: "+r.where)
	}
	return ls
}

func (t *TUI) drawWatchDetail(s tcell.Screen, r watchRow, now time.Time,
	put func(x, y, w int, text string, st tcell.Style) int, fill func(x, y, w int, st tcell.Style), base, border tcell.Style) {
	sw, sh := s.Size()
	w := min(sw-12, 100)
	if w < 20 {
		w = min(sw, 20)
	}
	var body []string
	for _, l := range watchDetailLines(r, now) {
		body = append(body, wrapToWidth(l, w-4)...)
	}
	h := min(len(body)+4, sh)
	x0, y0 := max((sw-w)/2, 0), max((sh-h)/2, 0)
	for y := y0; y < y0+h; y++ {
		fill(x0, y, w, base)
	}
	for x := x0 + 1; x < x0+w-1; x++ {
		s.SetContent(x, y0, '─', nil, border)
		s.SetContent(x, y0+h-1, '─', nil, border)
	}
	for y := y0 + 1; y < y0+h-1; y++ {
		s.SetContent(x0, y, '│', nil, border)
		s.SetContent(x0+w-1, y, '│', nil, border)
	}
	s.SetContent(x0, y0, '┌', nil, border)
	s.SetContent(x0+w-1, y0, '┐', nil, border)
	s.SetContent(x0, y0+h-1, '└', nil, border)
	s.SetContent(x0+w-1, y0+h-1, '┘', nil, border)
	for i, l := range body {
		if y0+1+i >= y0+h-2 {
			break
		}
		put(x0+2, y0+1+i, w-4, l, base)
	}
	put(x0+2, y0+h-2, w-4, "enter or esc: back", base.Foreground(wmDim))
}
