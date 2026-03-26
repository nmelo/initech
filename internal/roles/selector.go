//go:build !windows

// Package roles owns role definitions, templates, and template rendering for
// initech projects.
package roles

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"
)

// ANSI escape codes used by the selector UI.
const (
	sAnsiReset    = "\033[0m"
	sAnsiBold     = "\033[1m"
	sAnsiDim      = "\033[2m"
	sAnsiReverse  = "\033[7m"
	sAnsiGreen    = "\033[32m"
	sAnsiGray     = "\033[90m"
	sAnsiHome     = "\033[H"
	sAnsiClearScr = "\033[2J\033[H"
	sAnsiClearEOL = "\033[K"
	sAnsiHideCur  = "\033[?25l"
	sAnsiShowCur  = "\033[?25h"
)

// SelectorItem describes one selectable entry in the role chooser UI.
type SelectorItem struct {
	Name        string // Role name displayed in the list (e.g. "eng1").
	Description string // Short description shown to the right of the name.
	Group       string // Section header this item belongs to (e.g. "ENGINEERS").
	Tag         string // Parenthetical annotation after the description (e.g. "supervised").
	Checked     bool   // Whether this item starts checked; mutated during selection.
}

// ErrCancelled is returned by RunSelector when the user presses Esc or Ctrl+C.
var ErrCancelled = errors.New("selection cancelled")

// RunSelector renders an interactive checkbox list in the terminal and returns
// the names of all checked items when the user presses Enter. Returns
// ErrCancelled if the user presses Esc or Ctrl+C.
//
// The items slice is mutated in-place to reflect final checked state. If items
// is empty, RunSelector returns immediately with a nil slice.
func RunSelector(title string, items []SelectorItem) ([]string, error) {
	if len(items) == 0 {
		return nil, nil
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("selector: open /dev/tty: %w", err)
	}
	defer tty.Close()

	fd := int(tty.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("selector: raw mode: %w", err)
	}
	defer func() {
		term.Restore(fd, oldState) //nolint:errcheck
		fmt.Fprint(tty, sAnsiShowCur+sAnsiReset)
	}()

	w, h, _ := term.GetSize(fd)
	if w < 20 {
		w = 80
	}
	if h < 5 {
		h = 24
	}

	s := &selectorState{
		title:  title,
		items:  items,
		rows:   buildDisplayRows(items),
		cursor: 0,
		scroll: 0,
		termW:  w,
		termH:  h,
	}
	scrollToCursor(s)

	fmt.Fprint(tty, sAnsiHideCur+sAnsiClearScr)

	// Listen for terminal resize signals.
	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	defer signal.Stop(sigwinch)

	// Key events from a reader goroutine so the main loop can also select on
	// resize signals.
	keyCh := make(chan keyType, 4)
	go func() {
		defer close(keyCh)
		for {
			k, err := nextKey(tty)
			if err != nil {
				return
			}
			keyCh <- k
			if k == keyEnter || k == keyEscape || k == keyCtrlC {
				return
			}
		}
	}()

	clearOnNext := false
	for {
		if clearOnNext {
			fmt.Fprint(tty, sAnsiClearScr)
			clearOnNext = false
		} else {
			fmt.Fprint(tty, sAnsiHome)
		}
		renderSelector(tty, s)

		select {
		case k, ok := <-keyCh:
			if !ok {
				fmt.Fprint(tty, sAnsiClearScr)
				return nil, ErrCancelled
			}
			switch k {
			case keyUp:
				moveCursor(s, -1)
			case keyDown:
				moveCursor(s, 1)
			case keySpace:
				s.items[s.cursor].Checked = !s.items[s.cursor].Checked
			case keyEnter:
				fmt.Fprint(tty, sAnsiClearScr)
				return selectedNames(s.items), nil
			case keyEscape, keyCtrlC:
				fmt.Fprint(tty, sAnsiClearScr)
				return nil, ErrCancelled
			}
		case <-sigwinch:
			s.termW, s.termH, _ = term.GetSize(fd)
			if s.termW < 20 {
				s.termW = 80
			}
			if s.termH < 5 {
				s.termH = 24
			}
			scrollToCursor(s)
			clearOnNext = true
		}
	}
}

// ── Display row model ─────────────────────────────────────────────────

// displayRowKind differentiates group headers from selectable items.
type displayRowKind int

const (
	rowHeader displayRowKind = iota
	rowItem
)

// displayRow represents one visual line in the selector: a group header or a
// selectable item.
type displayRow struct {
	kind    displayRowKind
	group   string // populated for rowHeader
	itemIdx int    // populated for rowItem: index into selectorState.items
}

// selectorState holds all mutable UI state for a running selector session.
type selectorState struct {
	title  string
	items  []SelectorItem
	rows   []displayRow // flattened: group header rows interleaved with item rows
	cursor int          // index into items[]; always a selectable item
	scroll int          // first visible display-row index
	termW  int
	termH  int
}

// buildDisplayRows produces a flat display-row list from items, inserting a
// group header row whenever the Group field changes. Items with an empty Group
// are emitted without a header.
func buildDisplayRows(items []SelectorItem) []displayRow {
	var rows []displayRow
	var lastGroup string
	for i, item := range items {
		if item.Group != lastGroup {
			if item.Group != "" {
				rows = append(rows, displayRow{kind: rowHeader, group: item.Group})
			}
			lastGroup = item.Group
		}
		rows = append(rows, displayRow{kind: rowItem, itemIdx: i})
	}
	return rows
}

// itemDisplayRow returns the index in rows of the display row for the item at
// itemIdx. Returns 0 if not found.
func itemDisplayRow(rows []displayRow, itemIdx int) int {
	for i, r := range rows {
		if r.kind == rowItem && r.itemIdx == itemIdx {
			return i
		}
	}
	return 0
}

// contentHeight returns the number of scrollable content rows available for
// the item list given the terminal height.
//
// Fixed overhead (8 rows):
//
//	row 0: title
//	row 1: blank
//	row 2: hint
//	row 3: blank
//	row 4: "^ more above" indicator (or blank)
//	row N+5: "v more below" indicator (or blank)
//	row N+6: blank
//	row N+7: status
func contentHeight(termH int) int {
	h := termH - 8
	if h < 1 {
		h = 1
	}
	return h
}

// moveCursor advances the cursor by delta positions (wrapping at the ends).
func moveCursor(s *selectorState, delta int) {
	n := len(s.items)
	if n == 0 {
		return
	}
	s.cursor = ((s.cursor + delta) % n + n) % n
	scrollToCursor(s)
}

// scrollToCursor adjusts s.scroll so that the cursor's display row is within
// the visible content window.
func scrollToCursor(s *selectorState) {
	visH := contentHeight(s.termH)
	drIdx := itemDisplayRow(s.rows, s.cursor)

	if drIdx < s.scroll {
		s.scroll = drIdx
	}
	if drIdx >= s.scroll+visH {
		s.scroll = drIdx - visH + 1
	}

	maxScroll := len(s.rows) - visH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if s.scroll > maxScroll {
		s.scroll = maxScroll
	}
	if s.scroll < 0 {
		s.scroll = 0
	}
}

// selectedNames returns the Name of every checked item.
func selectedNames(items []SelectorItem) []string {
	var names []string
	for _, it := range items {
		if it.Checked {
			names = append(names, it.Name)
		}
	}
	return names
}

// ── Rendering ─────────────────────────────────────────────────────────

// renderSelector writes the full selector UI to w. Each line ends with
// \033[K\r\n so the terminal clears any previous content to the right.
func renderSelector(w io.Writer, s *selectorState) {
	termW := s.termW

	// Header: title + blank + hint + blank.
	printSelLine(w, " "+s.title)
	printSelLine(w, "")
	printSelLine(w, "  Arrow keys: move    Space: toggle    Enter: confirm    Esc: cancel")
	printSelLine(w, "")

	// Scroll indicator: above.
	if s.scroll > 0 {
		printSelLine(w, sAnsiDim+"  ^ more above"+sAnsiReset)
	} else {
		printSelLine(w, "")
	}

	// Content window.
	visH := contentHeight(s.termH)
	end := s.scroll + visH
	if end > len(s.rows) {
		end = len(s.rows)
	}
	visible := s.rows[s.scroll:end]
	for _, dr := range visible {
		isCursor := dr.kind == rowItem && dr.itemIdx == s.cursor
		renderDisplayRow(w, dr, s.items, isCursor, termW)
	}
	// Pad remaining content rows with blank lines.
	for i := len(visible); i < visH; i++ {
		printSelLine(w, "")
	}

	// Scroll indicator: below.
	if end < len(s.rows) {
		printSelLine(w, sAnsiDim+"  v more below"+sAnsiReset)
	} else {
		printSelLine(w, "")
	}

	// Footer: blank + status.
	printSelLine(w, "")
	count := 0
	for _, it := range s.items {
		if it.Checked {
			count++
		}
	}
	printSelLine(w, sAnsiDim+fmt.Sprintf("  %d selected", count)+sAnsiReset)
}

// renderDisplayRow writes one display row to w.
func renderDisplayRow(w io.Writer, dr displayRow, items []SelectorItem, cursor bool, termW int) {
	switch dr.kind {
	case rowHeader:
		fmt.Fprintf(w, "%s  %s%s%s\r\n", sAnsiBold, dr.group, sAnsiReset, sAnsiClearEOL)
	case rowItem:
		renderItemRow(w, items[dr.itemIdx], cursor, termW)
	}
}

// renderItemRow writes one item row to w. The cursor row is rendered with
// reverse-video; non-cursor rows use individual ANSI color segments.
func renderItemRow(w io.Writer, item SelectorItem, cursor bool, termW int) {
	const nameW = 9 // name column visual width: max name "shipper"(7) + 2 padding

	cb := "[ ]"
	if item.Checked {
		cb = "[x]"
	}
	namePadded := padRight(item.Name, nameW)

	tag := ""
	if item.Tag != "" {
		tag = "(" + item.Tag + ")"
	}

	// Visual widths for layout.
	prefixW := 2 + len(cb) + 1 + nameW // "  [x] namePad "
	tagW := 0
	if tag != "" {
		tagW = 2 + len(tag) // "  (tag)"
	}
	descW := termW - prefixW - tagW
	if descW < 0 {
		descW = 0
	}

	desc := truncateSel(item.Description, descW)

	if cursor {
		// Reverse-video: build plain text row, rely on \033[K to fill background.
		plain := "  " + cb + " " + namePadded + padRight(desc, descW)
		if tag != "" {
			plain += "  " + tag
		}
		plain = truncateSel(plain, termW)
		fmt.Fprintf(w, "%s%s%s%s\r\n", sAnsiReverse, plain, sAnsiClearEOL, sAnsiReset)
		return
	}

	// Non-cursor: colored segments.
	var out strings.Builder
	out.WriteString("  ")
	if item.Checked {
		out.WriteString(sAnsiGreen + "[x]" + sAnsiReset)
	} else {
		out.WriteString(sAnsiGray + "[ ]" + sAnsiReset)
	}
	out.WriteByte(' ')
	out.WriteString(namePadded)
	out.WriteString(sAnsiDim + padRight(desc, descW) + sAnsiReset)
	if tag != "" {
		out.WriteString("  " + sAnsiGray + tag + sAnsiReset)
	}
	fmt.Fprintf(w, "%s%s\r\n", out.String(), sAnsiClearEOL)
}

// printSelLine writes a line with clear-to-EOL and CR+LF terminator.
func printSelLine(w io.Writer, s string) {
	fmt.Fprintf(w, "%s%s\r\n", s, sAnsiClearEOL)
}

// padRight right-pads s with spaces to n rune positions.
func padRight(s string, n int) string {
	runes := []rune(s)
	if len(runes) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(runes))
}

// truncateSel shortens s to at most n runes.
func truncateSel(s string, n int) string {
	if n < 0 {
		n = 0
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// ── Key input ─────────────────────────────────────────────────────────

// keyType represents a parsed keypress from the terminal.
type keyType int

const (
	keyUp    keyType = iota
	keyDown
	keySpace
	keyEnter
	keyEscape
	keyCtrlC
	keyOther
)

// nextKey reads and parses one keypress from tty.
func nextKey(tty *os.File) (keyType, error) {
	b, err := readSelByte(tty)
	if err != nil {
		return keyOther, err
	}
	switch b {
	case 0x03: // Ctrl+C
		return keyCtrlC, nil
	case '\r', '\n':
		return keyEnter, nil
	case ' ':
		return keySpace, nil
	case 0x1B: // Esc or start of CSI escape sequence
		b2, ok := tryReadSelByte(tty)
		if !ok || b2 != '[' {
			return keyEscape, nil
		}
		b3, ok2 := tryReadSelByte(tty)
		if !ok2 {
			return keyEscape, nil
		}
		switch b3 {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		}
		return keyOther, nil
	}
	return keyOther, nil
}

// readSelByte reads exactly one byte from f (blocking).
func readSelByte(f *os.File) (byte, error) {
	buf := [1]byte{}
	_, err := f.Read(buf[:])
	return buf[0], err
}

// tryReadSelByte attempts a non-blocking read from f. Returns (byte, true)
// if a byte was immediately available, or (0, false) if the read would block.
// Used to distinguish a bare Esc from the start of an escape sequence.
func tryReadSelByte(f *os.File) (byte, bool) {
	fd := int(f.Fd())
	if err := syscall.SetNonblock(fd, true); err != nil {
		return 0, false
	}
	defer syscall.SetNonblock(fd, false) //nolint:errcheck
	var buf [1]byte
	n, err := syscall.Read(fd, buf[:])
	if n > 0 && err == nil {
		return buf[0], true
	}
	return 0, false
}
