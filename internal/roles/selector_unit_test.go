//go:build !windows

package roles

import (
	"bytes"
	"strings"
	"testing"
)

// These tests fill gaps not covered by the existing selector_test.go.

// ── buildDisplayRows edge cases ─────────────────────────────────────

func TestBuildDisplayRows_Nil(t *testing.T) {
	rows := buildDisplayRows(nil)
	if len(rows) != 0 {
		t.Errorf("got %d rows for nil items, want 0", len(rows))
	}
}

// ── itemDisplayRow edge cases ───────────────────────────────────────

func TestItemDisplayRow_NotFound(t *testing.T) {
	rows := []displayRow{{kind: rowItem, itemIdx: 0}}
	if got := itemDisplayRow(rows, 99); got != 0 {
		t.Errorf("itemDisplayRow(_, 99) = %d, want 0 (not found)", got)
	}
}

func TestItemDisplayRow_EmptyRows(t *testing.T) {
	if got := itemDisplayRow(nil, 0); got != 0 {
		t.Errorf("itemDisplayRow(nil, 0) = %d, want 0", got)
	}
}

// ── moveCursor edge cases ───────────────────────────────────────────

func TestMoveCursor_EmptyItems(t *testing.T) {
	s := &selectorState{termH: 30}
	moveCursor(s, 1)
	if s.cursor != 0 {
		t.Errorf("cursor = %d, want 0 on empty items", s.cursor)
	}
}

func TestMoveCursor_LargeDelta(t *testing.T) {
	s := &selectorState{
		items:  make([]SelectorItem, 5),
		rows:   buildDisplayRows(make([]SelectorItem, 5)),
		cursor: 0,
		termH:  30,
	}
	moveCursor(s, 12) // 12 % 5 = 2
	if s.cursor != 2 {
		t.Errorf("cursor = %d, want 2 after +12 with 5 items", s.cursor)
	}
}

func TestMoveCursor_NegativeDelta(t *testing.T) {
	s := &selectorState{
		items:  make([]SelectorItem, 5),
		rows:   buildDisplayRows(make([]SelectorItem, 5)),
		cursor: 1,
		termH:  30,
	}
	moveCursor(s, -3) // (1-3)%5 = -2 -> +5 = 3
	if s.cursor != 3 {
		t.Errorf("cursor = %d, want 3 after -3 from 1 with 5 items", s.cursor)
	}
}

// ── contentHeight edge cases ────────────────────────────────────────

func TestContentHeight_VerySmall(t *testing.T) {
	for _, h := range []int{0, 1, 2, 5, 9} {
		if got := contentHeight(h); got < 1 {
			t.Errorf("contentHeight(%d) = %d, should be >= 1", h, got)
		}
	}
}

// ── scrollToCursor clamping ─────────────────────────────────────────

func TestScrollToCursor_NegativeClamp(t *testing.T) {
	s := &selectorState{
		items:  make([]SelectorItem, 3),
		rows:   buildDisplayRows(make([]SelectorItem, 3)),
		cursor: 0,
		scroll: -5,
		termH:  30,
	}
	scrollToCursor(s)
	if s.scroll < 0 {
		t.Errorf("scroll = %d, should be >= 0", s.scroll)
	}
}

func TestScrollToCursor_MaxScrollClamp(t *testing.T) {
	// Few items, large termH -> maxScroll is 0.
	s := &selectorState{
		items:  make([]SelectorItem, 3),
		rows:   buildDisplayRows(make([]SelectorItem, 3)),
		cursor: 2,
		scroll: 100,
		termH:  50,
	}
	scrollToCursor(s)
	if s.scroll > len(s.rows) {
		t.Errorf("scroll = %d, should not exceed rows count %d", s.scroll, len(s.rows))
	}
}

// ── renderSelector smoke ────────────────────────────────────────────

func TestRenderSelector_GroupHeaders(t *testing.T) {
	items := []SelectorItem{
		{Name: "eng1", Description: "Engineer", Group: "ENGINEERS", Checked: true},
		{Name: "qa1", Description: "QA", Group: "QUALITY"},
	}
	s := &selectorState{
		title: "Select",
		items: items,
		rows:  buildDisplayRows(items),
		termW: 80,
		termH: 30,
	}
	var buf bytes.Buffer
	renderSelector(&buf, s)
	out := buf.String()

	if !strings.Contains(out, "ENGINEERS") {
		t.Error("render should include ENGINEERS group header")
	}
	if !strings.Contains(out, "QUALITY") {
		t.Error("render should include QUALITY group header")
	}
}

// ── RunSelector empty items ─────────────────────────────────────────

func TestRunSelector_EmptyItems(t *testing.T) {
	names, err := RunSelector("test", nil)
	if err != nil {
		t.Fatalf("RunSelector(nil) error: %v", err)
	}
	if names != nil {
		t.Errorf("RunSelector(nil) = %v, want nil", names)
	}
}

func TestRunSelector_EmptySlice(t *testing.T) {
	names, err := RunSelector("test", []SelectorItem{})
	if err != nil {
		t.Fatalf("RunSelector([]) error: %v", err)
	}
	if names != nil {
		t.Errorf("RunSelector([]) = %v, want nil", names)
	}
}
