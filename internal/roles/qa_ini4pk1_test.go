//go:build !windows

// QA tests for ini-4pk.1: Role selector widget with raw terminal checkbox UI.
// Covers SelectorItem struct, RunSelector contract, rendering, navigation,
// scrolling, string helpers, and edge cases.
package roles

import (
	"strings"
	"testing"
)

// RunSelector returns immediately with nil when items is empty.
func TestQARunSelector_EmptyItemsReturnsNil(t *testing.T) {
	// No TTY in test environment: RunSelector with empty slice must return before
	// attempting to open /dev/tty.
	got, err := RunSelector("title", nil)
	if err != nil {
		t.Errorf("RunSelector with nil items: err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("RunSelector with nil items: got = %v, want nil", got)
	}

	got2, err2 := RunSelector("title", []SelectorItem{})
	if err2 != nil {
		t.Errorf("RunSelector with empty slice: err = %v, want nil", err2)
	}
	if got2 != nil {
		t.Errorf("RunSelector with empty slice: got = %v, want nil", got2)
	}
}

// buildDisplayRows inserts a header whenever Group changes.
func TestQABuildDisplayRows_GroupHeaderInsertion(t *testing.T) {
	items := []SelectorItem{
		{Name: "super", Group: "COORDINATORS"},
		{Name: "eng1", Group: "ENGINEERS"},
		{Name: "eng2", Group: "ENGINEERS"},
	}
	rows := buildDisplayRows(items)
	// Expected: header(COORD), item(0), header(ENG), item(1), item(2) = 5
	if len(rows) != 5 {
		t.Fatalf("len(rows) = %d, want 5", len(rows))
	}
	if rows[0].kind != rowHeader || rows[0].group != "COORDINATORS" {
		t.Errorf("rows[0] should be COORDINATORS header, got %+v", rows[0])
	}
	if rows[2].kind != rowHeader || rows[2].group != "ENGINEERS" {
		t.Errorf("rows[2] should be ENGINEERS header, got %+v", rows[2])
	}
}

// Items with empty Group get no header row.
func TestQABuildDisplayRows_EmptyGroupNoHeader(t *testing.T) {
	items := []SelectorItem{{Name: "a"}, {Name: "b"}}
	rows := buildDisplayRows(items)
	// 2 item rows = 2
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2 (2 items)", len(rows))
	}
	for _, r := range rows {
		if r.kind == rowHeader {
			t.Error("no header expected for empty Group")
		}
	}
}

// moveCursor wraps circularly: last-item + forward → first item;
// first-item + backward → last item.
func TestQAMoveCursor_Wrapping(t *testing.T) {
	s := newTestSelector(3) // 3 items, cursor starts at 0
	s.cursor = 2            // position at last item

	moveCursor(s, 1) // from last item forward → wraps to first item
	if s.cursor != 0 {
		t.Errorf("forward from last item should wrap to 0, got %d", s.cursor)
	}

	moveCursor(s, -1) // from first item backward → wraps to last item
	if s.cursor != 2 {
		t.Errorf("backward from first item should wrap to 2, got %d", s.cursor)
	}
}

// contentHeight enforces minimum of 1 and uses overhead of 9.
func TestQAContentHeight_Minimum(t *testing.T) {
	for _, h := range []int{1, 5, 9} {
		got := contentHeight(h)
		if got < 1 {
			t.Errorf("contentHeight(%d) = %d, want >= 1", h, got)
		}
	}
	if got := contentHeight(24); got != 15 {
		t.Errorf("contentHeight(24) = %d, want 15 (24-9)", got)
	}
}

// scrollToCursor keeps cursor's display row within visible window.
func TestQAScrollToCursor_KeepsCursorVisible(t *testing.T) {
	s := newTestSelector(20)
	s.termH = 15 // contentHeight = 7
	for _, cursorIdx := range []int{0, 7, 15, 19} {
		s.cursor = cursorIdx
		scrollToCursor(s)
		visH := contentHeight(s.termH)
		drIdx := itemDisplayRow(s.rows, s.cursor)
		if drIdx < s.scroll || drIdx >= s.scroll+visH {
			t.Errorf("cursor %d: drIdx %d not in [%d, %d)", cursorIdx, drIdx, s.scroll, s.scroll+visH)
		}
	}
}

// selectedNames returns checked names in order.
func TestQASelectedNames_ReturnsCheckedInOrder(t *testing.T) {
	items := []SelectorItem{
		{Name: "super", Checked: false},
		{Name: "eng1", Checked: true},
		{Name: "eng2", Checked: false},
		{Name: "qa1", Checked: true},
	}
	got := selectedNames(items)
	if len(got) != 2 || got[0] != "eng1" || got[1] != "qa1" {
		t.Errorf("selectedNames = %v, want [eng1 qa1]", got)
	}
}

// Render output includes title, hint, group headers, item names, and status.
func TestQARender_ContainsAllSections(t *testing.T) {
	s := &selectorState{
		title: "Select agents for testproject",
		items: []SelectorItem{
			{Name: "super", Description: "Coordinator", Group: "COORDINATORS", Checked: true},
			{Name: "eng1", Description: "Engineer", Group: "ENGINEERS", Tag: "needs src"},
		},
		cursor: 0,
		scroll: 0,
		termW:  80,
		termH:  24,
	}
	s.rows = buildDisplayRows(s.items)

	var buf strings.Builder
	renderSelector(&buf, s)
	out := buf.String()

	for _, want := range []string{
		"Select agents for testproject", // title
		"Arrow keys",                    // hint
		"COORDINATORS",                  // group header
		"super",                         // item name
		"Coordinator",                   // description
		"ENGINEERS",                     // group header
		"eng1",                          // item name
		"needs src",                     // tag
		"[x]",                           // checked box
		"[ ]",                           // unchecked box
		"1 selected",                    // status count
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render output missing %q", want)
		}
	}
}

// Cursor row uses reverse-video; non-cursor rows use normal rendering.
func TestQARender_CursorIsReverseVideo(t *testing.T) {
	s := &selectorState{
		title:  "test",
		items:  []SelectorItem{{Name: "a"}, {Name: "b"}},
		cursor: 0,
		scroll: 0,
		termW:  80,
		termH:  24,
	}
	s.rows = buildDisplayRows(s.items)

	var buf strings.Builder
	renderSelector(&buf, s)
	out := buf.String()

	if !strings.Contains(out, sAnsiReverse) {
		t.Error("cursor row must use reverse-video ANSI code")
	}
}

// Scroll indicators appear only when content extends beyond visible window.
func TestQARender_ScrollIndicators_Conditional(t *testing.T) {
	items := make([]SelectorItem, 30)
	for i := range items {
		items[i] = SelectorItem{Name: "r%02d"}
	}

	// Short terminal: scroll=5 → both indicators visible.
	s := &selectorState{
		title:  "test",
		items:  items,
		cursor: 10,
		scroll: 5,
		termW:  80,
		termH:  15,
	}
	s.rows = buildDisplayRows(items)
	var buf strings.Builder
	renderSelector(&buf, s)
	out := buf.String()
	if !strings.Contains(out, "^ more above") {
		t.Error("should show '^ more above' when scroll > 0")
	}
	if !strings.Contains(out, "v more below") {
		t.Error("should show 'v more below' when rows extend beyond window")
	}

	// All items fit: no indicators.
	s2 := &selectorState{
		title:  "test",
		items:  []SelectorItem{{Name: "a"}, {Name: "b"}},
		cursor: 0, scroll: 0, termW: 80, termH: 24,
	}
	s2.rows = buildDisplayRows(s2.items)
	var buf2 strings.Builder
	renderSelector(&buf2, s2)
	out2 := buf2.String()
	if strings.Contains(out2, "more above") || strings.Contains(out2, "more below") {
		t.Error("no scroll indicators when all items fit")
	}
}

// truncateSel handles unicode runes correctly.
func TestQATruncate_Unicode(t *testing.T) {
	if got := truncateSel("αβγδε", 3); got != "αβγ" {
		t.Errorf("truncateSel unicode = %q, want αβγ", got)
	}
	if got := truncateSel("hello", -1); got != "" {
		t.Errorf("truncateSel(-1) = %q, want empty", got)
	}
}

// padRight pads with spaces to exact width.
func TestQAPadRight_Basic(t *testing.T) {
	if got := padRight("hi", 5); got != "hi   " {
		t.Errorf("padRight = %q, want 'hi   '", got)
	}
	if got := padRight("hello", 3); got != "hello" {
		t.Errorf("padRight no-truncate = %q, want 'hello'", got)
	}
}
