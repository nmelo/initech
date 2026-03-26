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
	keyCh := make(chan keyPress, 4)
	go func() {
		defer close(keyCh)
		for {
			kp, err := nextKey(tty)
			if err != nil {
				return
			}
			keyCh <- kp
			// Stop on Escape/Ctrl+C. Do not stop on Enter: when the cursor
			// is on the custom-role row, Enter adds a role rather than
			// confirming, so RunSelector must continue.
			if kp.kind == keyEscape || kp.kind == keyCtrlC {
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
			// Any keypress clears the inline validation error.
			s.customErr = ""

			switch k.kind {
			case keyUp:
				moveCursor(s, -1)
			case keyDown:
				moveCursor(s, 1)
			case keySpace:
				// Space toggles items. On the custom row it is ignored
				// (role names must be single words; spaces are not allowed).
				if !s.onCustomRow {
					s.items[s.cursor].Checked = !s.items[s.cursor].Checked
				}
			case keyEnter:
				if s.onCustomRow && s.customInput != "" {
					// Non-empty custom input: attempt to add the role.
					if errMsg := addCustomRole(s, s.customInput); errMsg != "" {
						s.customErr = errMsg
					}
					// Stay in the selector regardless of success/failure.
				} else {
					// Confirm selection (also handles empty custom input).
					fmt.Fprint(tty, sAnsiClearScr)
					return selectedNames(s.items), nil
				}
			case keyEscape, keyCtrlC:
				fmt.Fprint(tty, sAnsiClearScr)
				return nil, ErrCancelled
			case keyBackspace:
				if s.onCustomRow && len(s.customInput) > 0 {
					runes := []rune(s.customInput)
					s.customInput = string(runes[:len(runes)-1])
				}
			case keyChar:
				if s.onCustomRow {
					s.customInput += string(rune(k.ch))
				} else {
					// Preset hotkeys (only active when not on the custom row).
					switch k.ch {
					case 's':
						applyPreset(s, "small")
					case 'm':
						applyPreset(s, "standard")
					case 'f':
						applyPreset(s, "full")
					case 'a':
						applyPreset(s, "all")
					case 'n':
						applyPreset(s, "none")
					}
				}
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

// displayRowKind differentiates group headers, selectable items, and the
// custom role entry row.
type displayRowKind int

const (
	rowHeader      displayRowKind = iota
	rowItem                        // a selectable role item
	rowCustomInput                 // the "Add custom role" entry at the bottom
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
	cursor int          // index into items[]; ignored when onCustomRow
	scroll int          // first visible display-row index
	termW  int
	termH  int

	// Custom role entry state.
	onCustomRow bool   // cursor is on the "Add custom role" row
	customInput string // text being typed for the new role name
	customErr   string // validation error shown next to the input
}

// buildDisplayRows produces a flat display-row list from items, inserting a
// group header row whenever the Group field changes. Items with an empty Group
// are emitted without a header. A rowCustomInput row is always appended at
// the end so the user can add custom roles.
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
	// Always end with the custom role entry row.
	rows = append(rows, displayRow{kind: rowCustomInput})
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
// Fixed overhead (9 rows):
//
//	row 0: title
//	row 1: blank
//	row 2: keys hint
//	row 3: presets hint
//	row 4: blank
//	row 5: "^ more above" indicator (or blank)
//	row N+6: "v more below" indicator (or blank)
//	row N+7: blank
//	row N+8: status
func contentHeight(termH int) int {
	h := termH - 9
	if h < 1 {
		h = 1
	}
	return h
}

// moveCursor advances the cursor by delta positions. The "Add custom role" row
// sits between the last and first items in the circular navigation order:
// last-item -> custom-row -> first-item (going forward) and vice versa.
func moveCursor(s *selectorState, delta int) {
	n := len(s.items)
	if n == 0 {
		return
	}
	if s.onCustomRow {
		// Leave the custom row.
		s.onCustomRow = false
		if delta > 0 {
			s.cursor = 0 // wrap forward to first item
		} else {
			s.cursor = n - 1 // wrap backward to last item
		}
	} else if delta > 0 && s.cursor == n-1 {
		// At last item, moving forward: enter custom row.
		s.onCustomRow = true
	} else if delta < 0 && s.cursor == 0 {
		// At first item, moving backward: enter custom row.
		s.onCustomRow = true
	} else {
		s.cursor = ((s.cursor + delta) % n + n) % n
	}
	scrollToCursor(s)
}

// scrollToCursor adjusts s.scroll so that the cursor's display row is within
// the visible content window.
func scrollToCursor(s *selectorState) {
	visH := contentHeight(s.termH)
	var drIdx int
	if s.onCustomRow {
		// The custom row is always the last display row.
		drIdx = len(s.rows) - 1
	} else {
		drIdx = itemDisplayRow(s.rows, s.cursor)
	}

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

	// Header: title + blank + keys hint + presets hint + blank.
	printSelLine(w, " "+s.title)
	printSelLine(w, "")
	printSelLine(w, "  Arrow keys: move    Space: toggle    Enter: confirm    Esc: cancel")
	printSelLine(w, "  Presets: s=small m=standard f=full  a=all n=none")
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
		var isCursor bool
		if s.onCustomRow {
			isCursor = dr.kind == rowCustomInput
		} else {
			isCursor = dr.kind == rowItem && dr.itemIdx == s.cursor
		}
		renderDisplayRow(w, dr, s, isCursor, termW)
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
func renderDisplayRow(w io.Writer, dr displayRow, s *selectorState, cursor bool, termW int) {
	switch dr.kind {
	case rowHeader:
		fmt.Fprintf(w, "%s  %s%s%s\r\n", sAnsiBold, dr.group, sAnsiReset, sAnsiClearEOL)
	case rowItem:
		renderItemRow(w, s.items[dr.itemIdx], cursor, termW)
	case rowCustomInput:
		renderCustomRow(w, s, cursor, termW)
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

// renderCustomRow writes the "Add custom role" row to w.
// When cursor is true, it shows the inline text input with a block cursor
// and any validation error. When false, it renders as a dim affordance.
func renderCustomRow(w io.Writer, s *selectorState, cursor bool, termW int) {
	const prefix = "  [+] Add custom role"
	if cursor {
		line := prefix + " > " + s.customInput + "\u2588" // block cursor
		if s.customErr != "" {
			line += "   " + s.customErr
		}
		line = truncateSel(line, termW)
		fmt.Fprintf(w, "%s%s%s%s\r\n", sAnsiReverse, line, sAnsiClearEOL, sAnsiReset)
		return
	}
	fmt.Fprintf(w, "%s%s%s%s\r\n", sAnsiDim, prefix, sAnsiReset, sAnsiClearEOL)
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

// ── Presets and custom roles ──────────────────────────────────────────

// presetSmall is the "small" preset: coordinator + one engineer + one QA.
var presetSmall = map[string]bool{"super": true, "eng1": true, "qa1": true}

// presetStandard is the "standard" preset: the typical 7-agent team.
var presetStandard = map[string]bool{
	"super": true, "pm": true,
	"eng1": true, "eng2": true,
	"qa1": true, "qa2": true,
	"shipper": true,
}

// applyPreset sets Checked on items according to the named preset.
// "small", "standard", and "full" only check catalog roles; custom items are
// left unchecked. "all" checks everything; "none" clears everything.
func applyPreset(s *selectorState, preset string) {
	switch preset {
	case "small":
		for i := range s.items {
			s.items[i].Checked = presetSmall[s.items[i].Name]
		}
	case "standard":
		for i := range s.items {
			s.items[i].Checked = presetStandard[s.items[i].Name]
		}
	case "full":
		for i := range s.items {
			_, inCatalog := Catalog[s.items[i].Name]
			s.items[i].Checked = inCatalog
		}
	case "all":
		for i := range s.items {
			s.items[i].Checked = true
		}
	case "none":
		for i := range s.items {
			s.items[i].Checked = false
		}
	}
}

// addCustomRole validates name, appends a new SelectorItem with Group="CUSTOM"
// and Checked=true, rebuilds the display rows, and moves the cursor to the new
// item. Returns a non-empty error message if validation fails.
func addCustomRole(s *selectorState, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.ContainsAny(name, " \t") {
		return "role name must be a single word"
	}
	for _, it := range s.items {
		if it.Name == name {
			return name + " already in list."
		}
	}
	s.items = append(s.items, SelectorItem{Name: name, Group: "CUSTOM", Checked: true})
	s.rows = buildDisplayRows(s.items)
	s.onCustomRow = false
	s.cursor = len(s.items) - 1
	s.customInput = ""
	scrollToCursor(s)
	return ""
}

// ── Key input ─────────────────────────────────────────────────────────

// keyType classifies a terminal keypress.
type keyType int

const (
	keyUp       keyType = iota
	keyDown
	keySpace
	keyEnter
	keyEscape
	keyCtrlC
	keyOther
	keyBackspace // 0x08 (BS) or 0x7F (DEL)
	keyChar      // printable ASCII 0x21–0x7E; keyPress.ch has the byte
)

// keyPress is the result of parsing one terminal keypress. ch is populated
// only when kind == keyChar.
type keyPress struct {
	kind keyType
	ch   byte
}

// nextKey reads and parses one keypress from tty.
func nextKey(tty *os.File) (keyPress, error) {
	b, err := readSelByte(tty)
	if err != nil {
		return keyPress{kind: keyOther}, err
	}
	switch b {
	case 0x03: // Ctrl+C
		return keyPress{kind: keyCtrlC}, nil
	case '\r', '\n':
		return keyPress{kind: keyEnter}, nil
	case ' ':
		return keyPress{kind: keySpace}, nil
	case 0x08, 0x7F: // Backspace / Delete
		return keyPress{kind: keyBackspace}, nil
	case 0x1B: // Esc or start of CSI escape sequence
		b2, ok := tryReadSelByte(tty)
		if !ok || b2 != '[' {
			return keyPress{kind: keyEscape}, nil
		}
		b3, ok2 := tryReadSelByte(tty)
		if !ok2 {
			return keyPress{kind: keyEscape}, nil
		}
		switch b3 {
		case 'A':
			return keyPress{kind: keyUp}, nil
		case 'B':
			return keyPress{kind: keyDown}, nil
		}
		return keyPress{kind: keyOther}, nil
	}
	// Printable ASCII (excluding space, which is handled above).
	if b >= 0x21 && b <= 0x7E {
		return keyPress{kind: keyChar, ch: b}, nil
	}
	return keyPress{kind: keyOther}, nil
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
