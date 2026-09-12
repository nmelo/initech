// inbox_panel.go renders the operator's inbox: the list of items agents
// posted, the detail pane with its reply line, the corner count, and the
// keys that act on an item (ini-3wkl.4, child C of the operator-inbox epic).
//
// Spec: pm/specs/operator-inbox.md in the WORKSPACE repo
// nmelo/initech-workspace at c3a3795, plus the delivery-outcome rework at
// 1ad837b.
//
// THE LIST IS NEVER SCOPED. Canon (docs/spec.md in the workspace repo,
// "Attention is never scoped") applies here: the agents OVERLAY is scoped to
// a window's own agents, and this is not. An item an agent posted is the
// operator's to see in whichever window is in front of them, or the channel
// is only as reliable as the operator's memory of which window was serving
// whom.
//
// THE PANEL NEVER CLAIMS DELIVERY. An item's delivery status is the SEND
// path's own word, rendered verbatim; blank renders as "unconfirmed", never
// as anything that reads like success. The operator learns the truth late
// rather than a comfortable word early (pm's rework, on eng3's adversarial
// read). No wait, poll or retry runs here: this file reads a field.
package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
)

// inboxReader is the store surface this panel needs, named on the CONSUMER
// side (ini-3wkl.4). The panel is built against it rather than against
// *Inbox directly so the store's own rework costs an adapter line rather
// than a rewrite, and so the panel's tests run against a fake instead of a
// file on disk.
type inboxReader interface {
	Items() []InboxItem
	Item(id string) (InboxItem, bool)
	OpenCount(agent string) int
	Transition(id string, to InboxState, by inboxActor) error
	PersistenceReason() string
}

// inboxPanel is the panel's view state. The ITEMS are not here: they are read
// from the store on every render, so the panel cannot show a list the store
// has moved past — the same discipline the agents modal's geometry follows.
type inboxPanel struct {
	active bool
	// selected is the item id, not an index: the list is rebuilt from the
	// store every frame and an index would silently point at a different
	// item when one is answered from another window.
	selected string
	// detailScroll is the first body line shown in the detail pane.
	detailScroll int
	replyBuf     []rune
	// note is a transient footer line (e.g. why `a` did nothing). Cleared on
	// the next key, like the agents modal's error.
	note string

	// onAccept is child D's seam: pressing `a` on an item that has a default
	// calls it. Nil until D wires the send, which is why `a` reports rather
	// than pretends when it is unset.
	onAccept func(id string)
	// onReply is child D's seam for Enter.
	onReply func(id, reply string)
}

// inboxOpenForOperator reports whether an item belongs in the operator's
// list. Open items always; an ANSWERED item stays until the send path has
// confirmed delivery, because an answer the agent never received is exactly
// what the operator needs to still see — and it is the same set the store
// refuses to prune (spec: "prune never deletes an undelivered answer").
//
// Stated as a default in the PLAN rather than guessed silently: pm's rework
// says the delivery status is visible in the DETAIL PANE, which can only be
// true of an item still in the list.
func inboxOpenForOperator(it InboxItem) bool {
	switch it.State {
	case InboxUnread, InboxSeen:
		return true
	case InboxAnswered:
		return it.DeliveryStatus != InboxDelivered
	default:
		return false
	}
}

// inboxListFor returns the items the panel lists, oldest first — largest
// attention debt on top, as the attention popup does.
func inboxListFor(r inboxReader) []InboxItem {
	var out []InboxItem
	for _, it := range r.Items() {
		if inboxOpenForOperator(it) {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// inboxUnreadCount is the corner badge's number: UNREAD only, so opening an
// item drops the count even though the item stays listed.
func inboxUnreadCount(r inboxReader) int {
	n := 0
	for _, it := range r.Items() {
		if it.State == InboxUnread {
			n++
		}
	}
	return n
}

// inboxDeliveryLine is what the detail pane says about delivery. The send
// path's own vocabulary, verbatim; blank is reported as unconfirmed and never
// as success, which is the honest absence of a verdict rather than the panel
// inventing one.
func inboxDeliveryLine(it InboxItem) string {
	if it.State != InboxAnswered {
		return ""
	}
	if strings.TrimSpace(it.DeliveryStatus) == "" {
		return "delivery: unconfirmed"
	}
	return "delivery: " + it.DeliveryStatus
}

// inboxAgeText renders an age the way the attention popup does.
func inboxAgeText(since time.Time, now time.Time) string {
	if since.IsZero() {
		return "just now"
	}
	d := now.Sub(since)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// inboxRowText builds one list row: the agent name padded to the widest
// present, its open count when above one, the age, the item's first line, the
// chime mark, and the re-post marker.
func inboxRowText(it InboxItem, r inboxReader, nameWidth int, now time.Time) string {
	name := it.Agent
	if n := r.OpenCount(it.Agent); n > 1 {
		name = fmt.Sprintf("%s (%d)", it.Agent, n)
	}
	row := fmt.Sprintf("%-*s  %4s  %s", nameWidth, name, inboxAgeText(it.Created, now), it.FirstLine())
	if it.Chime {
		row += " " + inboxChimeMark
	}
	if it.RePostOfDismissed {
		row += " " + inboxRePostMarker
	}
	return row
}

const (
	inboxChimeMark    = "♪"
	inboxRePostMarker = "(re-post of a dismissed item)"
	// inboxEmptyLine teaches the affordance on the first open, so an operator
	// who has never seen an item learns how they arrive instead of reading a
	// blank box.
	inboxEmptyLine = `nothing posted — agents post with: initech post "…"`
	inboxFooter    = " Enter send   a accept default   d dismiss   ↑↓ select   Esc close "
	inboxNoDefault = "no default on this item"
)

// inboxNameWidth is the padding width: the longest name-with-count present,
// so columns line up without a fixed guess.
func inboxNameWidth(items []InboxItem, r inboxReader) int {
	w := 0
	for _, it := range items {
		n := runewidth.StringWidth(it.Agent)
		if c := r.OpenCount(it.Agent); c > 1 {
			n += runewidth.StringWidth(fmt.Sprintf(" (%d)", c))
		}
		if n > w {
			w = n
		}
	}
	return w
}

// ── rendering ───────────────────────────────────────────────────────

const (
	inboxBoxW       = 72
	inboxListRows   = 8 // rows of list before the divider
	inboxChromeRows = 4 // top border, divider, footer, bottom border
)

// renderInboxPanel draws the panel in the modal register the agents grid and
// the help card share (RGB(20,20,20) surface, Gray border, DodgerBlue title),
// so it reads as one of initech's modals rather than a new dialect.
func (t *TUI) renderInboxPanel() {
	s := t.screen
	if s == nil {
		return
	}
	r := t.inboxPanelStore()
	if r == nil {
		return
	}
	sw, sh := s.Size()

	boxW := inboxBoxW
	if sw-4 < boxW {
		boxW = sw - 4
	}
	if boxW < 24 {
		boxW = 24
	}
	items := inboxListFor(r)
	rows := len(items)
	if rows > inboxListRows {
		rows = inboxListRows
	}
	if rows < 1 {
		rows = 1 // the empty-state line occupies one row
	}
	boxH := rows + inboxChromeRows + inboxDetailRows
	if sh-2 < boxH {
		boxH = sh - 2
	}
	if boxH < 8 {
		boxH = 8
	}
	startX, startY := (sw-boxW)/2, (sh-boxH)/2
	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}

	bg := tcell.StyleDefault.Background(tcell.NewRGBColor(20, 20, 20)).Foreground(tcell.ColorSilver)
	border := bg.Foreground(tcell.ColorGray)
	title := bg.Foreground(tcell.ColorDodgerBlue).Bold(true)
	dim := bg.Foreground(tcell.ColorGray)
	selected := tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite)
	warn := bg.Foreground(tcell.ColorYellow)

	for y := startY; y < startY+boxH && y < sh; y++ {
		for x := startX; x < startX+boxW && x < sw; x++ {
			s.SetContent(x, y, ' ', nil, bg)
		}
	}
	s.SetContent(startX, startY, '╭', nil, border)
	s.SetContent(startX+boxW-1, startY, '╮', nil, border)
	s.SetContent(startX, startY+boxH-1, '╰', nil, border)
	s.SetContent(startX+boxW-1, startY+boxH-1, '╯', nil, border)
	for x := startX + 1; x < startX+boxW-1 && x < sw; x++ {
		s.SetContent(x, startY, '─', nil, border)
		s.SetContent(x, startY+boxH-1, '─', nil, border)
	}
	for y := startY + 1; y < startY+boxH-1 && y < sh; y++ {
		s.SetContent(startX, y, '│', nil, border)
		s.SetContent(startX+boxW-1, y, '│', nil, border)
	}

	innerX, innerW := startX+2, boxW-4
	put := func(y int, text string, st tcell.Style) {
		col := 0
		for _, ch := range text {
			w := runewidth.RuneWidth(ch)
			if col+w > innerW {
				break
			}
			s.SetContent(innerX+col, y, ch, nil, st)
			col += w
		}
	}

	// Title: the unread count lives in the corner badge, so the title names
	// the surface and how many items are waiting to be dealt with.
	head := fmt.Sprintf(" inbox (%d) ", len(items))
	tx := startX + (boxW-runewidth.StringWidth(head))/2
	for i, ch := range head {
		if tx+i > startX && tx+i < startX+boxW-1 {
			s.SetContent(tx+i, startY, ch, nil, title)
		}
	}

	y := startY + 1

	// PERSISTENCE FALLBACK, first line inside the box when the store cannot
	// save: the operator must know their replies are session-only BEFORE
	// they type one, not after a restart loses them. A owns the condition
	// and the wording; this renders it (ini-3wkl.4 AC 7).
	if reason := r.PersistenceReason(); reason != "" {
		put(y, InboxNotPersistingPrefix+reason, warn)
		y++
	}

	if len(items) == 0 {
		// AC 2: the first open teaches the affordance rather than showing a
		// blank box.
		put(y, inboxEmptyLine, dim)
		t.drawInboxFooter(startX, startY+boxH-2, boxW, put)
		return
	}

	now := time.Now()
	width := inboxNameWidth(items, r)
	sel := t.inboxSelectedIndex(items)
	for i, it := range items {
		if i >= inboxListRows {
			break
		}
		st := bg
		if i == sel {
			st = selected
			for x := innerX; x < innerX+innerW && x < sw; x++ {
				s.SetContent(x, y, ' ', nil, st)
			}
		}
		put(y, inboxRowText(it, r, width, now), st)
		y++
	}

	// Divider, then the detail pane for the selection.
	for x := innerX; x < innerX+innerW && x < sw; x++ {
		s.SetContent(x, y, '─', nil, border)
	}
	y++
	t.drawInboxDetail(items[sel], now, y, put, bg, dim)
	t.drawInboxFooter(startX, startY+boxH-2, boxW, put)
}

// inboxDetailRows is the detail pane's height: agent line, body window,
// default line, delivery line, reply line.
const inboxDetailRows = 8

// drawInboxDetail renders the selected item: who, how long ago, the body
// (scrolling, so a 40-line post does not need the row to grow), the default
// the agent stated, what the send path says about delivery, and the reply
// line the operator types into.
func (t *TUI) drawInboxDetail(it InboxItem, now time.Time, y int, put func(int, string, tcell.Style), bg, dim tcell.Style) {
	put(y, fmt.Sprintf("%s · %s ago", it.Agent, inboxAgeText(it.Created, now)), dim)
	y++

	body := strings.Split(it.Body, "\n")
	const bodyRows = 3
	start := t.inbox.detailScroll
	if start > len(body)-1 {
		start = len(body) - 1
	}
	if start < 0 {
		start = 0
	}
	for i := 0; i < bodyRows && start+i < len(body); i++ {
		put(y, body[start+i], bg)
		y++
	}
	if len(body) > bodyRows {
		put(y, fmt.Sprintf("  … %d more lines (↑↓ scrolls the body when an item is open)", len(body)-bodyRows), dim)
		y++
	}

	if it.DefaultText != "" {
		put(y, "default: "+it.DefaultText, dim)
		y++
	}
	if line := inboxDeliveryLine(it); line != "" {
		put(y, line, dim)
		y++
	}
	put(y, "> "+string(t.inbox.replyBuf)+"_", bg)
}

// drawInboxFooter renders the key legend, and the transient note (why `a`
// did nothing) in its place when there is one — never silence.
func (t *TUI) drawInboxFooter(startX, y, boxW int, put func(int, string, tcell.Style)) {
	style := tcell.StyleDefault.Background(tcell.NewRGBColor(20, 20, 20)).Foreground(tcell.ColorGray)
	text := inboxFooter
	if t.inbox.note != "" {
		text = " " + t.inbox.note + " "
	}
	put(y, text, style)
}

// inboxSelectedIndex resolves the selected item id to an index in the list,
// falling back to the first item. The selection is held as an ID, not an
// index, so an item answered from another window cannot silently move the
// operator's cursor onto a different one.
func (t *TUI) inboxSelectedIndex(items []InboxItem) int {
	for i, it := range items {
		if it.ID == t.inbox.selected {
			return i
		}
	}
	return 0
}

// ── TUI wiring ──────────────────────────────────────────────────────

// inboxPanelStore returns the store the panel reads, as the narrow
// consumer-side interface.
//
// CHILD B'S LOADER, NOT A SECOND ONE: t.inboxState() already loads the store
// once per TUI with the right authority and the right fallback, and two
// loaders would be two answers to "which inbox is this window's" — the
// duplicate-derivation class this codebase keeps paying for. The interface is
// the panel's own, so the store's rework still costs an adapter line rather
// than a rewrite.
func (t *TUI) inboxPanelStore() inboxReader {
	ib := t.inboxState()
	if ib == nil {
		return nil
	}
	return ib
}

// openInboxPanel opens the panel and anchors the selection on the first item.
func (t *TUI) openInboxPanel() {
	t.inbox.active = true
	t.inbox.note = ""
	t.inbox.detailScroll = 0
	t.inbox.replyBuf = nil
	r := t.inboxPanelStore()
	if r == nil {
		return
	}
	if items := inboxListFor(r); len(items) > 0 {
		t.inbox.selected = items[0].ID
		t.markInboxSeen(items[0].ID)
	}
}

// markInboxSeen flips an unread item to seen. AC 1 is one act by
// construction: the corner count is DERIVED from the store on every render,
// so there is no cached number that could lag this write.
//
// A secondary window is not the authority and its write is refused; that is
// child F's routing to add, and the panel must still render. Logged once
// rather than per keystroke.
func (t *TUI) markInboxSeen(id string) {
	r := t.inboxPanelStore()
	if r == nil {
		return
	}
	it, ok := r.Item(id)
	if !ok || it.State != InboxUnread {
		return
	}
	if err := r.Transition(id, InboxSeen, actorOperator); err != nil {
		if !t.inboxSeenWarned {
			t.inboxSeenWarned = true
			LogWarn("inbox", "could not mark an item seen from this window", "id", id, "err", err)
		}
	}
}

// handleInboxKey processes keys while the panel is open. It never waits on
// delivery: Enter and `a` record the operator's act and hand off to child D's
// seam, which owns the send.
func (t *TUI) handleInboxKey(ev *tcell.EventKey) bool {
	t.inbox.note = ""
	r := t.inboxPanelStore()
	if r == nil {
		t.inbox.active = false
		return false
	}
	items := inboxListFor(r)

	switch ev.Key() {
	case tcell.KeyEscape:
		t.inbox.active = false
		return false
	case tcell.KeyUp:
		t.moveInboxSelection(items, -1)
		return false
	case tcell.KeyDown:
		t.moveInboxSelection(items, 1)
		return false
	case tcell.KeyEnter:
		if len(items) == 0 {
			return false
		}
		id := items[t.inboxSelectedIndex(items)].ID
		if t.inbox.onReply != nil {
			t.inbox.onReply(id, string(t.inbox.replyBuf))
		}
		t.inbox.replyBuf = nil
		return false
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if n := len(t.inbox.replyBuf); n > 0 {
			t.inbox.replyBuf = t.inbox.replyBuf[:n-1]
		}
		return false
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'a':
			t.acceptInboxDefault(items)
			return false
		case 'd':
			if len(items) > 0 {
				id := items[t.inboxSelectedIndex(items)].ID
				if err := r.Transition(id, InboxDismissed, actorOperator); err != nil {
					t.inbox.note = "could not dismiss: " + err.Error()
				}
			}
			return false
		}
		t.inbox.replyBuf = append(t.inbox.replyBuf, ev.Rune())
		return false
	}
	return false
}

// acceptInboxDefault is the `a` key (seam 2). An item WITH a default invokes
// child D's accept path; an item WITHOUT one says why rather than doing
// nothing silently — a key that appears in the footer and does nothing reads
// as a broken build.
func (t *TUI) acceptInboxDefault(items []InboxItem) {
	if len(items) == 0 {
		return
	}
	it := items[t.inboxSelectedIndex(items)]
	if it.DefaultText == "" {
		t.inbox.note = inboxNoDefault
		return
	}
	if t.inbox.onAccept == nil {
		// D has not wired the send yet. Say so rather than marking the item
		// answered on the panel's own authority — the panel never claims
		// delivery it cannot observe.
		t.inbox.note = "accept is not wired yet"
		return
	}
	t.inbox.onAccept(it.ID)
}

// moveInboxSelection moves by delta and marks the new item seen.
func (t *TUI) moveInboxSelection(items []InboxItem, delta int) {
	if len(items) == 0 {
		return
	}
	i := t.inboxSelectedIndex(items) + delta
	if i < 0 {
		i = 0
	}
	if i > len(items)-1 {
		i = len(items) - 1
	}
	t.inbox.selected = items[i].ID
	t.inbox.detailScroll = 0
	t.markInboxSeen(items[i].ID)
}
