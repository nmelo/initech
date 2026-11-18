package tui

import (
	"strings"
	"testing"
	"time"
)

func headerCountRoster(working, idle, suspended int) []panelAgent {
	var r []panelAgent
	for i := 0; i < working; i++ {
		n := "w" + string(rune('a'+i))
		r = append(r, panelAgent{Name: n, Key: n, Alive: true, State: StateRunning, Since: ago(time.Duration(i+1) * time.Minute)})
	}
	r = append(r, panelAgent{Name: "eng1", Key: "eng1", Alive: true, State: StateIdle, Since: ago(40 * time.Minute), Bead: "ini-b2", BeadStatus: "in_progress"})
	for i := 0; i < idle; i++ {
		n := "i" + string(rune('a'+i))
		r = append(r, panelAgent{Name: n, Key: n, Alive: true, State: StateIdle, Since: ago(time.Minute)})
	}
	for i := 0; i < suspended; i++ {
		n := "s" + string(rune('a'+i))
		r = append(r, panelAgent{Name: n, Key: n, Alive: true, Suspended: true, State: StateSuspended})
	}
	return r
}

// TestHeaderCounts_TheCountsAreInTheHeaderNotTheBox is AC1 and AC2: the counts
// in the header row, in the same words, and the box's last row is an agent.
func TestHeaderCounts_TheCountsAreInTheHeaderNotTheBox(t *testing.T) {
	rows := newBoxPanel(t, 200, 3).draw(panelNow, headerCountRoster(2, 2, 1))
	t.Logf("\n%s", strings.Join(rows, "\n"))
	if !strings.HasSuffix(rows[0], "prompts 0 · 5 awake · 2 idle · 1 suspended") {
		t.Fatalf("header %q", rows[0])
	}
	for _, r := range rows[1:] {
		if strings.Contains(r, "idle ·") || strings.Contains(r, "suspended") {
			t.Fatalf("a count is still a box row: %q", r)
		}
	}
	// The two workers and the holder each get a row.
	if rowOf(rows, "wb") != 2 || rowOf(rows, "wa") != 3 || rowOf(rows, "eng1") != 4 {
		t.Fatalf("the box's last row is not an agent:\n%s", strings.Join(rows, "\n"))
	}
}

// TestHeaderCounts_ACutWorkingListAndZeroParts: a cut working list shows
// "+N working" in the header; a zero part is not written.
func TestHeaderCounts_ACutWorkingListAndZeroParts(t *testing.T) {
	rows := newBoxPanel(t, 200, 3).draw(panelNow, headerCountRoster(5, 0, 0))
	t.Logf("\n%s", strings.Join(rows, "\n"))
	// Three rows: eng1 (never cut) and the two longest-running of five.
	if !strings.HasSuffix(rows[0], "· 6 awake · +3 working · 0 suspended") || strings.Contains(rows[0], "idle") {
		t.Fatalf("header %q", rows[0])
	}
	if rowOf(rows, "we") != 2 || rowOf(rows, "wd") != 3 || rowOf(rows, "eng1") != 4 {
		t.Fatalf("want the two longest-running workers and the holder:\n%s", strings.Join(rows, "\n"))
	}
	rows = newBoxPanel(t, 200, 0).draw(panelNow, headerCountRoster(5, 0, 0))
	if !strings.HasSuffix(rows[0], "· 6 awake · +5 working · 0 suspended") {
		t.Fatalf("with no box rows all five workers count in the header: %q", rows[0])
	}
}

// TestHeaderCounts_TheFoldOrder is AC3: narrowing, the alarm ids go, then
// idle, then +N working, then suspended, then awake; the alarms stay.
func TestHeaderCounts_TheFoldOrder(t *testing.T) {
	roster := headerCountRoster(5, 2, 1)
	roster[5].State = StateWaitingInput // eng1, the bead holder
	const alarms = "on Nelson 0 · blocked 0 · stale 20m 0 · prompts 1"
	for _, tc := range []struct {
		width  int
		suffix string
	}{
		{120, " eng1 · 8 awake · +3 working · 2 idle · 1 suspended"},
		{110, " · 8 awake · +3 working · 2 idle · 1 suspended"},
		{100, " · 8 awake · +3 working · 1 suspended"},
		{90, " · 8 awake · 1 suspended"},
		{80, " · 8 awake"},
		{65, ""},
	} {
		header := newBoxPanel(t, tc.width, 3).draw(panelNow, roster)[0]
		if got, want := strings.TrimSpace(header), alarms+tc.suffix; got != want {
			t.Errorf("width %d: got %q, want %q", tc.width, got, want)
		}
	}
}
