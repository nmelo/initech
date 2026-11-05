package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/rignumbers"
)

// ini-9z0b: the bottom bar's parts. No clock; the colours are named.

func TestLoadStyle_DimBelowCoresYellowToTwiceRedAbove(t *testing.T) {
	bar := tcell.StyleDefault
	for load, want := range map[float64]tcell.Color{3.5: tcell.ColorGray, 15: tcell.ColorYellow, 30: tcell.ColorYellow, 31.4: tcell.ColorRed} {
		if fg, _, _ := loadStyle(load, 15, bar).Decompose(); fg != want {
			t.Errorf("load %v on 15 cores is %v, want %v", load, fg, want)
		}
	}
}

type fixedQuota string

func (q fixedQuota) QuotaText() (string, bool) { return string(q), q != "" }

func TestBarRight_TheMachinesNumbersAndTheQuotaDash(t *testing.T) {
	free, load, awake := 5.1, 3.5, 14
	f := &rignumbers.Fleet{FreeGB: &free, Load: &load, Awake: &awake,
		Ceiling: rignumbers.FleetCeiling{Sessions: 30, FreeMemoryGB: 8}}
	text := func(items []statusBarItem) string {
		var out []string
		for _, it := range items {
			out = append(out, it.text)
		}
		return strings.Join(out, " · ")
	}
	if got := text(barRightItems(f, nil, 15, tcell.StyleDefault)); got != "free 5.1 GB · floor 8 · sessions 14/30 · fleet quota — · load 3.5/15" {
		t.Fatalf("got %q", got)
	}
	if got := text(barRightItems(f, fixedQuota("58% ▓▓░░"), 15, tcell.StyleDefault)); !strings.Contains(got, "fleet quota 58% ▓▓░░") {
		t.Fatalf("a quota source's text is not shown: %q", got)
	}
	if barRightItems(nil, nil, 15, tcell.StyleDefault) != nil {
		t.Fatal("no reading drew numbers")
	}
	if got := text(barRightItems(&rignumbers.Fleet{Ceiling: rignumbers.FleetCeiling{Sessions: 30, FreeMemoryGB: 8}}, nil, 15, tcell.StyleDefault)); got != "free ? GB · floor 8 · sessions ?/30 · fleet quota — · load ?/15" {
		t.Fatalf("unread numbers: %q", got)
	}
}

func TestBarLeftItems_TheRedPartsAreRed(t *testing.T) {
	i := func(v int) *int { return &v }
	n := rignumbers.Numbers{Repo: true, MergesPerHour: i(0), MainPass: i(2), MainFail: i(4), MainLatestRed: true, ClosedToday: i(3)}
	var red, all []string
	for _, it := range barLeftItems(n, tcell.StyleDefault) {
		all = append(all, it.text)
		if fg, _, _ := it.style.Decompose(); fg == tcell.ColorRed {
			red = append(red, it.text)
		}
	}
	if strings.Join(all, "") != "merges/h 0 · main ✓2 ✗4 · median — · closed today 3" || strings.Join(red, "|") != "main ✓2 ✗4" {
		t.Fatalf("items %q red %q", all, red)
	}
}

// TestBarRight_FreeMemorySaysItIsFreeAgainstTheFloor (theguy, via super):
// the number the start refusal reads, as "free 11 GB · floor 8".
func TestBarRight_FreeMemorySaysItIsFreeAgainstTheFloor(t *testing.T) {
	free, load, awake := 11.0, 1.0, 1
	f := &rignumbers.Fleet{FreeGB: &free, Load: &load, Awake: &awake,
		Ceiling: rignumbers.FleetCeiling{Sessions: 30, FreeMemoryGB: 8}}
	items := barRightItems(f, nil, 15, tcell.StyleDefault)
	if len(items) < 2 || items[0].text != "free 11 GB" || items[1].text != "floor 8" {
		t.Fatalf("the memory items read %+v", items)
	}
}
