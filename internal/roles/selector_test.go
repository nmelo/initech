//go:build !windows

package roles

import (
	"fmt"
	"strings"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────

// newTestSelector creates a selectorState with n single-group items.
func newTestSelector(n int) *selectorState {
	items := make([]SelectorItem, n)
	for i := range items {
		items[i] = SelectorItem{
			Name:  fmt.Sprintf("item%d", i),
			Group: "GROUP",
		}
	}
	s := &selectorState{
		title:  "test",
		items:  items,
		rows:   buildDisplayRows(items),
		cursor: 0,
		scroll: 0,
		termW:  80,
		termH:  24,
	}
	return s
}

// ── buildDisplayRows ──────────────────────────────────────────────────

func TestBuildDisplayRows(t *testing.T) {
	items := []SelectorItem{
		{Name: "super", Group: "COORDINATORS"},
		{Name: "eng1", Group: "ENGINEERS"},
		{Name: "eng2", Group: "ENGINEERS"},
	}
	rows := buildDisplayRows(items)
	// Expected: header(COORDINATORS), item(0), header(ENGINEERS), item(1), item(2), customInput
	if len(rows) != 6 {
		t.Fatalf("len(rows) = %d, want 6", len(rows))
	}
	if rows[0].kind != rowHeader || rows[0].group != "COORDINATORS" {
		t.Errorf("rows[0] = %+v, want COORDINATORS header", rows[0])
	}
	if rows[1].kind != rowItem || rows[1].itemIdx != 0 {
		t.Errorf("rows[1] = %+v, want item 0", rows[1])
	}
	if rows[2].kind != rowHeader || rows[2].group != "ENGINEERS" {
		t.Errorf("rows[2] = %+v, want ENGINEERS header", rows[2])
	}
	if rows[3].kind != rowItem || rows[3].itemIdx != 1 {
		t.Errorf("rows[3] = %+v, want item 1", rows[3])
	}
	if rows[4].kind != rowItem || rows[4].itemIdx != 2 {
		t.Errorf("rows[4] = %+v, want item 2", rows[4])
	}
	if rows[5].kind != rowCustomInput {
		t.Errorf("rows[5] = %+v, want rowCustomInput", rows[5])
	}
}

func TestBuildDisplayRowsSameGroup(t *testing.T) {
	items := []SelectorItem{
		{Name: "a", Group: "G"},
		{Name: "b", Group: "G"},
	}
	rows := buildDisplayRows(items)
	// One header + two items + customInput = 4.
	if len(rows) != 4 {
		t.Fatalf("len(rows) = %d, want 4 (1 header + 2 items + customInput)", len(rows))
	}
	if rows[0].kind != rowHeader {
		t.Error("rows[0] should be a header")
	}
	if rows[3].kind != rowCustomInput {
		t.Errorf("last row should be rowCustomInput, got %+v", rows[3])
	}
}

func TestBuildDisplayRowsNoGroup(t *testing.T) {
	items := []SelectorItem{
		{Name: "a"},
		{Name: "b"},
	}
	rows := buildDisplayRows(items)
	// Empty Group: no headers emitted. Two item rows + customInput = 3.
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3 (2 items + customInput)", len(rows))
	}
	if rows[0].kind != rowItem || rows[1].kind != rowItem {
		t.Error("first two rows should be item rows when Group is empty")
	}
	if rows[2].kind != rowCustomInput {
		t.Errorf("last row should be rowCustomInput, got %+v", rows[2])
	}
}

func TestBuildDisplayRowsGroupTransition(t *testing.T) {
	items := []SelectorItem{
		{Name: "a", Group: "G1"},
		{Name: "b", Group: "G2"},
		{Name: "c", Group: "G2"},
	}
	rows := buildDisplayRows(items)
	// header(G1), item(0), header(G2), item(1), item(2), customInput = 6
	if len(rows) != 6 {
		t.Fatalf("len(rows) = %d, want 6", len(rows))
	}
	if rows[5].kind != rowCustomInput {
		t.Errorf("last row should be rowCustomInput, got %+v", rows[5])
	}
}

// ── itemDisplayRow ────────────────────────────────────────────────────

func TestItemDisplayRow(t *testing.T) {
	items := []SelectorItem{
		{Name: "super", Group: "COORDINATORS"},
		{Name: "eng1", Group: "ENGINEERS"},
	}
	rows := buildDisplayRows(items)
	// rows: [header(COORD), item(0), header(ENG), item(1)]

	if got := itemDisplayRow(rows, 0); got != 1 {
		t.Errorf("itemDisplayRow(rows, 0) = %d, want 1", got)
	}
	if got := itemDisplayRow(rows, 1); got != 3 {
		t.Errorf("itemDisplayRow(rows, 1) = %d, want 3", got)
	}
}

// ── moveCursor ────────────────────────────────────────────────────────

func TestMoveCursorForward(t *testing.T) {
	s := newTestSelector(3)
	moveCursor(s, 1)
	if s.cursor != 1 {
		t.Errorf("cursor = %d, want 1", s.cursor)
	}
}

func TestMoveCursorWrapForward(t *testing.T) {
	s := newTestSelector(3)
	s.cursor = 2
	moveCursor(s, 1)
	// Moving forward from the last item now goes to the custom row, not item 0.
	if !s.onCustomRow {
		t.Errorf("moving forward from last item should go to custom row")
	}
	// Another forward step from custom row wraps to item 0.
	moveCursor(s, 1)
	if s.onCustomRow || s.cursor != 0 {
		t.Errorf("moving forward from custom row should go to item 0, got cursor=%d onCustomRow=%v", s.cursor, s.onCustomRow)
	}
}

func TestMoveCursorWrapBackward(t *testing.T) {
	s := newTestSelector(3)
	s.cursor = 0
	moveCursor(s, -1)
	// Moving backward from the first item now goes to the custom row.
	if !s.onCustomRow {
		t.Errorf("moving backward from first item should go to custom row")
	}
	// Another backward step from custom row goes to the last item.
	moveCursor(s, -1)
	if s.onCustomRow || s.cursor != 2 {
		t.Errorf("moving backward from custom row should go to last item, got cursor=%d onCustomRow=%v", s.cursor, s.onCustomRow)
	}
}

func TestMoveCursorSingleItem(t *testing.T) {
	s := newTestSelector(1)
	moveCursor(s, 1) // item 0 -> custom row
	if !s.onCustomRow {
		t.Errorf("moving forward from only item should enter custom row")
	}
	moveCursor(s, -1) // custom row -> item 0
	if s.onCustomRow || s.cursor != 0 {
		t.Errorf("moving backward from custom row should return to item 0")
	}
	moveCursor(s, -1) // item 0 -> custom row (backward wrap)
	if !s.onCustomRow {
		t.Errorf("moving backward from first item should enter custom row")
	}
}

// ── contentHeight ─────────────────────────────────────────────────────

func TestContentHeight(t *testing.T) {
	// Overhead is now 9 rows (added preset hint line).
	tests := []struct {
		termH int
		want  int
	}{
		{24, 15},
		{15, 6},
		{10, 1},
		{9, 1}, // minimum clamp (9-9=0, clamped to 1)
		{5, 1}, // minimum clamp
	}
	for _, tt := range tests {
		got := contentHeight(tt.termH)
		if got != tt.want {
			t.Errorf("contentHeight(%d) = %d, want %d", tt.termH, got, tt.want)
		}
	}
}

// ── scrollToCursor ────────────────────────────────────────────────────

func TestScrollToCursorBelowView(t *testing.T) {
	s := newTestSelector(20)
	s.termH = 15 // contentHeight = 7
	s.cursor = 15
	scrollToCursor(s)
	visH := contentHeight(s.termH)
	drIdx := itemDisplayRow(s.rows, s.cursor)
	if drIdx < s.scroll || drIdx >= s.scroll+visH {
		t.Errorf("cursor display row %d not in [%d, %d)", drIdx, s.scroll, s.scroll+visH)
	}
}

func TestScrollToCursorAboveView(t *testing.T) {
	s := newTestSelector(20)
	s.termH = 15
	s.cursor = 3
	s.scroll = 10 // cursor is above the visible window
	scrollToCursor(s)
	drIdx := itemDisplayRow(s.rows, s.cursor)
	if drIdx < s.scroll {
		t.Errorf("scroll %d is below cursor display row %d", s.scroll, drIdx)
	}
}

func TestScrollToCursorAlreadyVisible(t *testing.T) {
	s := newTestSelector(20)
	s.termH = 24 // contentHeight = 16
	s.cursor = 5
	s.scroll = 0
	scrollToCursor(s)
	if s.scroll != 0 {
		t.Errorf("scroll should stay 0 when cursor is in view, got %d", s.scroll)
	}
}

// ── selectedNames ─────────────────────────────────────────────────────

func TestSelectedNames(t *testing.T) {
	items := []SelectorItem{
		{Name: "super", Checked: true},
		{Name: "eng1", Checked: false},
		{Name: "eng2", Checked: true},
	}
	got := selectedNames(items)
	if len(got) != 2 {
		t.Fatalf("len(selectedNames) = %d, want 2", len(got))
	}
	if got[0] != "super" || got[1] != "eng2" {
		t.Errorf("selectedNames = %v, want [super eng2]", got)
	}
}

func TestSelectedNamesNone(t *testing.T) {
	items := []SelectorItem{{Name: "a"}, {Name: "b"}}
	got := selectedNames(items)
	if len(got) != 0 {
		t.Errorf("selectedNames with none checked = %v, want empty", got)
	}
}

func TestSelectedNamesAll(t *testing.T) {
	items := []SelectorItem{
		{Name: "a", Checked: true},
		{Name: "b", Checked: true},
		{Name: "c", Checked: true},
	}
	got := selectedNames(items)
	if len(got) != 3 {
		t.Fatalf("len(selectedNames) = %d, want 3", len(got))
	}
}

// ── string helpers ────────────────────────────────────────────────────

func TestTruncateSel(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "hel"},
		{"hello", 0, ""},
		{"", 5, ""},
		{"αβγδε", 3, "αβγ"}, // unicode rune truncation
		{"hello", -1, ""},
	}
	for _, tt := range tests {
		got := truncateSel(tt.s, tt.n)
		if got != tt.want {
			t.Errorf("truncateSel(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}

func TestPadRight(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"hi", 5, "hi   "},
		{"hello", 3, "hello"},
		{"", 3, "   "},
		{"ab", 2, "ab"},
	}
	for _, tt := range tests {
		got := padRight(tt.s, tt.n)
		if got != tt.want {
			t.Errorf("padRight(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}

// ── renderSelector ────────────────────────────────────────────────────

func TestRenderSelectorContainsExpectedContent(t *testing.T) {
	s := &selectorState{
		title: "Select agents for myproject",
		items: []SelectorItem{
			{Name: "super", Description: "Coordinator", Group: "COORDINATORS", Checked: true},
			{Name: "eng1", Description: "Engineer", Group: "ENGINEERS", Checked: false},
			{Name: "eng2", Description: "Engineer", Group: "ENGINEERS", Checked: true},
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
		"Select agents for myproject",
		"Arrow keys",
		"Presets",
		"s=small",
		"COORDINATORS",
		"super",
		"ENGINEERS",
		"eng1",
		"eng2",
		"2 selected",
		"Add custom role",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render output missing %q", want)
		}
	}
}

func TestRenderSelectorCheckedUnchecked(t *testing.T) {
	s := &selectorState{
		title: "test",
		items: []SelectorItem{
			{Name: "a", Checked: true},
			{Name: "b", Checked: false},
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

	if !strings.Contains(out, "[x]") {
		t.Error("checked item should render [x]")
	}
	if !strings.Contains(out, "[ ]") {
		t.Error("unchecked item should render [ ]")
	}
}

func TestRenderSelectorTagInOutput(t *testing.T) {
	s := &selectorState{
		title: "test",
		items: []SelectorItem{
			{Name: "super", Tag: "supervised", Checked: true},
		},
		cursor: 0,
		scroll: 0,
		termW:  80,
		termH:  24,
	}
	s.rows = buildDisplayRows(s.items)

	var buf strings.Builder
	renderSelector(&buf, s)
	if !strings.Contains(buf.String(), "supervised") {
		t.Error("tag should appear in render output")
	}
}

func TestRenderScrollIndicators(t *testing.T) {
	// 30 items without a group (no headers), scroll=5 so there are rows above
	// and below the visible window.
	items := make([]SelectorItem, 30)
	for i := range items {
		items[i] = SelectorItem{Name: fmt.Sprintf("r%02d", i)}
	}
	s := &selectorState{
		title:  "test",
		items:  items,
		cursor: 10,
		scroll: 5,
		termW:  80,
		termH:  15, // contentHeight = 7
	}
	s.rows = buildDisplayRows(items)

	var buf strings.Builder
	renderSelector(&buf, s)
	out := buf.String()

	if !strings.Contains(out, "^ more above") {
		t.Error("should show '^ more above' when scroll > 0")
	}
	if !strings.Contains(out, "v more below") {
		t.Error("should show 'v more below' when rows extend beyond visible window")
	}
}

func TestRenderNoScrollIndicatorsWhenAllVisible(t *testing.T) {
	s := &selectorState{
		title: "test",
		items: []SelectorItem{
			{Name: "a"},
			{Name: "b"},
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

	if strings.Contains(out, "more above") || strings.Contains(out, "more below") {
		t.Error("should not show scroll indicators when all items fit on screen")
	}
}

func TestRenderSelectorStatusCount(t *testing.T) {
	s := &selectorState{
		title: "test",
		items: []SelectorItem{
			{Name: "a", Checked: true},
			{Name: "b", Checked: true},
			{Name: "c", Checked: false},
		},
		cursor: 0,
		scroll: 0,
		termW:  80,
		termH:  24,
	}
	s.rows = buildDisplayRows(s.items)

	var buf strings.Builder
	renderSelector(&buf, s)
	if !strings.Contains(buf.String(), "2 selected") {
		t.Error("status line should show '2 selected'")
	}
}

// ── applyPreset ────────────────────────────────────────────────────────

func TestApplyPresetSmall(t *testing.T) {
	s := newTestSelector(0) // will be replaced with catalog items
	s.items = []SelectorItem{
		{Name: "super"}, {Name: "eng1"}, {Name: "eng2"}, {Name: "qa1"}, {Name: "pm"},
	}
	s.rows = buildDisplayRows(s.items)
	applyPreset(s, "small")
	wantChecked := map[string]bool{"super": true, "eng1": true, "qa1": true}
	for _, it := range s.items {
		if it.Checked != wantChecked[it.Name] {
			t.Errorf("small preset: %q checked=%v, want %v", it.Name, it.Checked, wantChecked[it.Name])
		}
	}
}

func TestApplyPresetStandard(t *testing.T) {
	s := newTestSelector(0)
	s.items = []SelectorItem{
		{Name: "super"}, {Name: "pm"}, {Name: "eng1"}, {Name: "eng2"},
		{Name: "qa1"}, {Name: "qa2"}, {Name: "shipper"}, {Name: "arch"},
	}
	s.rows = buildDisplayRows(s.items)
	applyPreset(s, "standard")
	wantChecked := map[string]bool{
		"super": true, "pm": true, "eng1": true, "eng2": true,
		"qa1": true, "qa2": true, "shipper": true,
	}
	for _, it := range s.items {
		if it.Checked != wantChecked[it.Name] {
			t.Errorf("standard preset: %q checked=%v, want %v", it.Name, it.Checked, wantChecked[it.Name])
		}
	}
}

func TestApplyPresetFull(t *testing.T) {
	// Full checks all items in the Catalog; custom items are not checked.
	s := newTestSelector(0)
	s.items = []SelectorItem{
		{Name: "eng1"}, {Name: "qa1"}, {Name: "infra", Group: "CUSTOM"},
	}
	s.rows = buildDisplayRows(s.items)
	applyPreset(s, "full")
	for _, it := range s.items {
		_, inCatalog := Catalog[it.Name]
		if it.Checked != inCatalog {
			t.Errorf("full preset: %q checked=%v, want %v (catalog=%v)", it.Name, it.Checked, inCatalog, inCatalog)
		}
	}
}

func TestApplyPresetAll(t *testing.T) {
	s := newTestSelector(3)
	applyPreset(s, "all")
	for _, it := range s.items {
		if !it.Checked {
			t.Errorf("'all' preset: %q should be checked", it.Name)
		}
	}
}

func TestApplyPresetNone(t *testing.T) {
	s := newTestSelector(3)
	for i := range s.items {
		s.items[i].Checked = true
	}
	applyPreset(s, "none")
	for _, it := range s.items {
		if it.Checked {
			t.Errorf("'none' preset: %q should be unchecked", it.Name)
		}
	}
}

// ── moveCursor custom row ─────────────────────────────────────────────

func TestMoveCursorToCustomRowOnDownFromLast(t *testing.T) {
	s := newTestSelector(3)
	s.cursor = 2 // last item
	moveCursor(s, 1)
	if !s.onCustomRow {
		t.Error("moving down from last item should enter custom row")
	}
}

func TestMoveCursorToCustomRowOnUpFromFirst(t *testing.T) {
	s := newTestSelector(3)
	s.cursor = 0
	moveCursor(s, -1)
	if !s.onCustomRow {
		t.Error("moving up from first item should enter custom row")
	}
}

func TestMoveCursorFromCustomRowDown(t *testing.T) {
	s := newTestSelector(3)
	s.onCustomRow = true
	moveCursor(s, 1)
	if s.onCustomRow || s.cursor != 0 {
		t.Errorf("moving down from custom row should go to item 0, got cursor=%d onCustomRow=%v", s.cursor, s.onCustomRow)
	}
}

func TestMoveCursorFromCustomRowUp(t *testing.T) {
	s := newTestSelector(3)
	s.onCustomRow = true
	moveCursor(s, -1)
	if s.onCustomRow || s.cursor != 2 {
		t.Errorf("moving up from custom row should go to last item, got cursor=%d onCustomRow=%v", s.cursor, s.onCustomRow)
	}
}

// ── scrollToCursor with onCustomRow ───────────────────────────────────

func TestScrollToCursorOnCustomRow(t *testing.T) {
	// With 20 items and a small terminal, the custom row may be off-screen.
	// After scrollToCursor with onCustomRow=true, it should be visible.
	s := newTestSelector(20)
	s.termH = 15 // contentHeight = 6
	s.onCustomRow = true
	s.scroll = 0 // start at top
	scrollToCursor(s)
	visH := contentHeight(s.termH)
	lastRowIdx := len(s.rows) - 1
	if lastRowIdx < s.scroll || lastRowIdx >= s.scroll+visH {
		t.Errorf("custom row (display idx %d) not visible in scroll window [%d, %d)",
			lastRowIdx, s.scroll, s.scroll+visH)
	}
}

// ── addCustomRole ─────────────────────────────────────────────────────

func TestAddCustomRoleSuccess(t *testing.T) {
	s := newTestSelector(2)
	errMsg := addCustomRole(s, "infra")
	if errMsg != "" {
		t.Errorf("addCustomRole returned error %q, want empty", errMsg)
	}
	if len(s.items) != 3 {
		t.Fatalf("items len = %d, want 3", len(s.items))
	}
	added := s.items[2]
	if added.Name != "infra" {
		t.Errorf("added item name = %q, want 'infra'", added.Name)
	}
	if added.Group != "CUSTOM" {
		t.Errorf("added item group = %q, want 'CUSTOM'", added.Group)
	}
	if !added.Checked {
		t.Error("added item should be checked")
	}
	// Cursor should be on the new item, not on the custom row.
	if s.onCustomRow {
		t.Error("after adding, cursor should not be on custom row")
	}
	if s.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (new item index)", s.cursor)
	}
	// customInput should be cleared.
	if s.customInput != "" {
		t.Errorf("customInput = %q, want empty after add", s.customInput)
	}
}

func TestAddCustomRoleDuplicate(t *testing.T) {
	s := newTestSelector(2)
	s.items[0].Name = "eng1"
	errMsg := addCustomRole(s, "eng1")
	if errMsg == "" {
		t.Error("expected error for duplicate role name, got empty")
	}
	if len(s.items) != 2 {
		t.Errorf("items len = %d, want 2 (no item added on duplicate)", len(s.items))
	}
}

func TestAddCustomRoleEmptyName(t *testing.T) {
	s := newTestSelector(2)
	errMsg := addCustomRole(s, "")
	if errMsg != "" {
		t.Errorf("addCustomRole with empty name should return empty, got %q", errMsg)
	}
	if len(s.items) != 2 {
		t.Errorf("items len = %d, want 2 (nothing added for empty name)", len(s.items))
	}
}

func TestAddCustomRoleWhitespaceOnly(t *testing.T) {
	s := newTestSelector(2)
	errMsg := addCustomRole(s, "   ")
	// Trimmed to empty -> no item added, no error.
	if errMsg != "" {
		t.Errorf("whitespace-only name should return empty error, got %q", errMsg)
	}
	if len(s.items) != 2 {
		t.Errorf("items len = %d, want 2", len(s.items))
	}
}

func TestAddCustomRoleInvalidNames(t *testing.T) {
	cases := []string{
		"my role",     // space in middle
		"eng 1",       // space
		"../escape",   // path traversal
		"eng/hack",    // path separator
		"a\\b",        // backslash
		"my.role",     // dot
		"v1.0",        // dot in version
	}
	for _, name := range cases {
		s := newTestSelector(2)
		errMsg := addCustomRole(s, name)
		if errMsg == "" {
			t.Errorf("addCustomRole(%q) should return error, got empty", name)
		}
		if len(s.items) != 2 {
			t.Errorf("addCustomRole(%q) should not add item on invalid name, len=%d", name, len(s.items))
		}
	}
}

func TestAddCustomRoleValidNames(t *testing.T) {
	cases := []string{"my-custom-role", "test_2", "qa-lead", "eng1"}
	for _, name := range cases {
		s := newTestSelector(2)
		errMsg := addCustomRole(s, name)
		if errMsg != "" {
			t.Errorf("addCustomRole(%q) returned unexpected error: %q", name, errMsg)
		}
		if len(s.items) != 3 {
			t.Errorf("addCustomRole(%q) should add item, len=%d", name, len(s.items))
		}
	}
}

func TestAddCustomRoleRebuildsRows(t *testing.T) {
	s := newTestSelector(2)
	before := len(s.rows)
	addCustomRole(s, "dba")
	if len(s.rows) <= before {
		t.Errorf("rows len should increase after add: before=%d after=%d", before, len(s.rows))
	}
}

// ── renderCustomRow ────────────────────────────────────────────────────

func TestRenderCustomRowCursor(t *testing.T) {
	s := &selectorState{customInput: "infra"}
	var buf strings.Builder
	renderCustomRow(&buf, s, true, 80)
	out := buf.String()
	if !strings.Contains(out, "infra") {
		t.Error("custom row with cursor should show the typed input")
	}
	if !strings.Contains(out, "Add custom role") {
		t.Error("custom row should contain 'Add custom role'")
	}
}

func TestRenderCustomRowNonCursor(t *testing.T) {
	s := &selectorState{customInput: "shouldnotshow"}
	var buf strings.Builder
	renderCustomRow(&buf, s, false, 80)
	out := buf.String()
	if strings.Contains(out, "shouldnotshow") {
		t.Error("non-cursor custom row should not show current input")
	}
	if !strings.Contains(out, "Add custom role") {
		t.Error("non-cursor custom row should still show the affordance text")
	}
}

func TestRenderCustomRowWithError(t *testing.T) {
	s := &selectorState{customInput: "eng1", customErr: "eng1 already in list."}
	var buf strings.Builder
	renderCustomRow(&buf, s, true, 120)
	out := buf.String()
	if !strings.Contains(out, "already in list") {
		t.Error("custom row should show validation error when cursor is on it")
	}
}

// ── renderSelector custom row visible ────────────────────────────────

func TestRenderSelectorCustomRowInOutput(t *testing.T) {
	s := &selectorState{
		title:  "test",
		items:  []SelectorItem{{Name: "super"}},
		cursor: 0,
		termW:  80,
		termH:  24,
	}
	s.rows = buildDisplayRows(s.items)

	var buf strings.Builder
	renderSelector(&buf, s)
	if !strings.Contains(buf.String(), "Add custom role") {
		t.Error("renderSelector output should contain 'Add custom role'")
	}
}

func TestRenderSelectorPresetHintInOutput(t *testing.T) {
	s := newTestSelector(2)
	var buf strings.Builder
	renderSelector(&buf, s)
	out := buf.String()
	for _, want := range []string{"s=small", "m=standard", "f=full", "a=all", "n=none"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderSelector output missing preset hint %q", want)
		}
	}
}
