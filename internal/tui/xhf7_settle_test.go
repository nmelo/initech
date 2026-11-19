package tui

import (
	"strings"
	"testing"
	"time"
)

// ini-xhf7: the agents box takes a new state once it has held 3 seconds.
// Every instant is fixed; nothing waits.

func TestSettle_TheHoldIsThreeSeconds(t *testing.T) {
	if panelSettleAfter != 3*time.Second {
		t.Fatalf("panelSettleAfter = %v, want 3s", panelSettleAfter)
	}
}

// TestSettle_AStateHeldThreeSecondsShows is AC1: a change shows at 3 s, not
// before, and its time counts from when the state began.
func TestSettle_AStateHeldThreeSecondsShows(t *testing.T) {
	b := newBoxPanel(t, 160, 8)
	a := panelAgent{Name: "eng1", Key: "eng1", Alive: true, State: StateIdle, Since: ago(40 * time.Minute), Bead: "ini-b2", BeadStatus: "in_progress"}
	word := func(rows []string) string {
		i := rowOf(rows, "eng1")
		if i < 0 {
			return ""
		}
		for _, w := range []string{"running", "idle"} {
			if strings.Contains(rows[i], " "+w+" ") {
				return w
			}
		}
		return "?"
	}
	b.draw(panelNow, []panelAgent{a})
	a.State, a.Since = StateRunning, panelNow.Add(time.Second)
	if w := word(b.draw(panelNow.Add(3900*time.Millisecond), []panelAgent{a})); w != "idle" {
		t.Fatalf("2.9s into running the box shows %q, want idle still", w)
	}
	rows := b.draw(panelNow.Add(4*time.Second), []panelAgent{a})
	if w := word(rows); w != "running" {
		t.Fatalf("3s into running the box shows %q, want running:\n%s", w, strings.Join(rows, "\n"))
	}
	// From the state's start (+1s), not from when it showed (+4s).
	rows = b.draw(panelNow.Add(2*time.Minute+time.Second), []panelAgent{a})
	if i := rowOf(rows, "eng1"); i < 0 || !strings.Contains(rows[i], " 2m ") {
		t.Fatalf("time in state does not count from the start:\n%s", strings.Join(rows, "\n"))
	}
}

// TestSettle_ATwoSecondFlickerChangesNothing is AC1's flicker: under 3 s, no
// reorder and no reset of the time.
func TestSettle_ATwoSecondFlickerChangesNothing(t *testing.T) {
	b := newBoxPanel(t, 160, 8)
	a := panelAgent{Name: "alpha", Key: "alpha", Alive: true, State: StateRunning, Since: ago(10 * time.Minute)}
	z := panelAgent{Name: "zulu", Key: "zulu", Alive: true, State: StateRunning, Since: ago(5 * time.Minute)}
	order := func(rows []string) bool {
		return rowOf(rows, "alpha") > 0 && rowOf(rows, "alpha") < rowOf(rows, "zulu")
	}
	if rows := b.draw(panelNow, []panelAgent{z, a}); !order(rows) {
		t.Fatalf("longest first:\n%s", strings.Join(rows, "\n"))
	}
	// alpha flickers to the background for 2s and back.
	a.State, a.Since = StateBackground, panelNow.Add(time.Second)
	for _, at := range []time.Duration{time.Second, 2 * time.Second, 2900 * time.Millisecond} {
		if rows := b.draw(panelNow.Add(at), []panelAgent{z, a}); !order(rows) || !strings.Contains(rows[rowOf(rows, "alpha")], "running") {
			t.Fatalf("at +%v a flicker moved alpha:\n%s", at, strings.Join(rows, "\n"))
		}
	}
	a.State, a.Since = StateRunning, panelNow.Add(3*time.Second)
	rows := b.draw(panelNow.Add(5*time.Second), []panelAgent{z, a})
	if !order(rows) || !strings.Contains(rows[rowOf(rows, "alpha")], " 10m ") {
		t.Fatalf("after the flicker alpha lost its place or its time:\n%s", strings.Join(rows, "\n"))
	}
}

// TestSettle_TheHeaderAgreesWithTheBox is AC3: the header's idle count and
// the box move on the same draw.
func TestSettle_TheHeaderAgreesWithTheBox(t *testing.T) {
	b := newBoxPanel(t, 200, 8)
	a := panelAgent{Name: "alpha", Key: "alpha", Alive: true, State: StateRunning, Since: ago(10 * time.Minute)}
	keep := panelAgent{Name: "zulu", Key: "zulu", Alive: true, State: StateRunning, Since: ago(5 * time.Minute)}
	b.draw(panelNow, []panelAgent{keep, a})
	a.State, a.Since = StateIdle, panelNow.Add(time.Second) // no bead: counted idle once settled
	for _, at := range []time.Duration{2 * time.Second, 3900 * time.Millisecond, 4 * time.Second, 10 * time.Second} {
		rows := b.draw(panelNow.Add(at), []panelAgent{keep, a})
		inBox := rowOf(rows, "alpha") > 0
		counted := strings.Contains(rows[0], "1 idle")
		if inBox == counted {
			t.Fatalf("at +%v the header (1 idle: %v) and the box (alpha shown: %v) disagree:\n%s", at, counted, inBox, strings.Join(rows, "\n"))
		}
		if at >= 4*time.Second && inBox {
			t.Fatalf("at +%v alpha's idle, held 3s, is not shown", at)
		}
	}
}
