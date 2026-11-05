package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ini-1yp5 (b): a send whose text was never seen in the box before the Enter
// (a stale or mid-redraw screen after a resume or a resize) is submitted when
// two equal samples after the restore window show an empty composer with our
// body's first and last lines above it, where the screen before the Enter did
// not have them. The two live UNKNOWN captures are the fixtures.

type u1Capture struct {
	file, body, staleBox string
	cols, rows           int
}

var u1Captures = []u1Capture{
	{"inject_unknown_shipper.txt",
		"[from eng4] ◆ ini-t0y1 (peer, eviction and resume tests off real deadlines) landed, main=495080e (23c968f, f431289; CI and rigs green). qa2 PASS.",
		"[from pm] ◆ ini-61by landed at be5dea4: a new", 52, 36},
	{"inject_unknown_super.txt",
		"[from initech] ☾ ini-l1r waits on ini-r0q.18: ini-r0q.18 closed; move it on",
		"[from theguy] B. v2.59.0 isdnesday 10:07 PMcountingerulee", 78, 51},
}

func u1Rows(t *testing.T, file string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// u1Before is the screen before the Enter: the capture without our body
// above the prompt, and the stale text the log named in the box.
func u1Before(rows []string, c u1Capture) []string {
	out := append([]string(nil), rows...)
	top, _ := composerSpan(out)
	lead := "❯ " + string([]rune(c.body)[:12])
	for i := top - 1; i >= 0; i-- {
		if !strings.Contains(out[i], lead) {
			continue
		}
		for j := i; j < top; j++ {
			t := strings.TrimSpace(out[j])
			if j > i && (strings.HasPrefix(t, "✻") || strings.HasPrefix(t, "⎿") || strings.HasPrefix(t, "✢") || strings.HasPrefix(t, "◐")) {
				break
			}
			out[j] = ""
		}
		break
	}
	out[top] = "❯ " + c.staleBox
	return out
}

func u1Paint(p *Pane, rows []string) {
	var b strings.Builder
	b.WriteString("\x1b[2J\x1b[H")
	for i, r := range rows {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(strings.TrimRight(r, " "))
	}
	p.emu.Write([]byte(b.String()))
}

func u1Windows(t *testing.T, restore time.Duration) {
	t.Helper()
	nma5Window(t, 150*time.Millisecond)
	old := submitRestoreWindow
	submitRestoreWindow = restore
	t.Cleanup(func() { submitRestoreWindow = old })
}

// Both live captures read submitted.
func TestSubmitted1yp5_BothCapturesReadSubmitted(t *testing.T) {
	u1Windows(t, 0)
	for _, c := range u1Captures {
		rows := u1Rows(t, c.file)
		p := newEmuPane("x", c.cols, c.rows)
		u1Paint(p, rows)
		ps := newPendingSubmit(c.body, "bracketed", "")
		ps.lastRows = u1Before(rows, c)
		if !bodyShownAbove(rows, ps) || bodyShownAbove(ps.lastRows, ps) {
			t.Fatalf("%s: fixture: above-after %v, above-before %v", c.file, bodyShownAbove(rows, ps), bodyShownAbove(ps.lastRows, ps))
		}
		if got, why := watchSubmit(p, ps, time.Now()); got != submitTaken {
			t.Errorf("%s: outcome %v (%s), want submitted", c.file, got, why)
		}
	}
}

// Where the screen before the Enter already showed our body above the
// prompt (a resend of the same words), that is no proof: not submitted.
func TestSubmitted1yp5_ABodyAlreadyAboveIsNoProof(t *testing.T) {
	u1Windows(t, 0)
	c := u1Captures[0]
	rows := u1Rows(t, c.file)
	p := newEmuPane("x", c.cols, c.rows)
	u1Paint(p, rows)
	ps := newPendingSubmit(c.body, "bracketed", "")
	ps.lastRows = append([]string(nil), rows...)
	if got, _ := watchSubmit(p, ps, time.Now()); got == submitTaken {
		t.Fatal("submitted with our body already above before the Enter")
	}
}

// A composer that is not empty (it still holds other text) is not this proof.
func TestSubmitted1yp5_AComposerNotEmptyIsNoProof(t *testing.T) {
	u1Windows(t, 0)
	c := u1Captures[0]
	rows := u1Rows(t, c.file)
	after := append([]string(nil), rows...)
	top, _ := composerSpan(after)
	after[top] = "❯ an operator's draft still in the box"
	p := newEmuPane("x", c.cols, c.rows)
	u1Paint(p, after)
	ps := newPendingSubmit(c.body, "bracketed", "")
	ps.lastRows = u1Before(rows, c)
	if got, _ := watchSubmit(p, ps, time.Now()); got == submitTaken {
		t.Fatal("submitted with the composer holding text")
	}
}

// Before the restore window ends nothing is taken by this proof.
func TestSubmitted1yp5_NotBeforeTheRestoreWindow(t *testing.T) {
	u1Windows(t, time.Hour)
	c := u1Captures[0]
	rows := u1Rows(t, c.file)
	p := newEmuPane("x", c.cols, c.rows)
	u1Paint(p, rows)
	ps := newPendingSubmit(c.body, "bracketed", "")
	ps.lastRows = u1Before(rows, c)
	if got, _ := watchSubmit(p, ps, time.Now()); got == submitTaken {
		t.Fatal("submitted inside the restore window")
	}
}

// Two equal samples: a screen that changes on every sample (a spinner row
// above the box) never gives two equal ones, so it is not taken.
func TestSubmitted1yp5_TwoEqualSamplesNeeded(t *testing.T) {
	u1Windows(t, 0)
	c := u1Captures[0]
	rows := u1Rows(t, c.file)
	p := newEmuPane("x", c.cols, c.rows)
	u1Paint(p, rows)
	n := 0
	hook := func(p *Pane) {
		n++
		next := append([]string(nil), rows...)
		next[0] = "spinner frame " + strconv.Itoa(n)
		u1Paint(p, next)
	}
	submitSampleMidpoint.Store(&hook)
	t.Cleanup(func() { submitSampleMidpoint.Store(nil) })
	ps := newPendingSubmit(c.body, "bracketed", "")
	ps.lastRows = u1Before(rows, c)
	if got, _ := watchSubmit(p, ps, time.Now()); got == submitTaken {
		t.Fatal("submitted on samples that never matched")
	}
}

// Feed it nothing: a screen with no composer at all is not submitted, and a
// body too short to prove itself is never matched above.
func TestSubmitted1yp5_NothingToReadIsNoProof(t *testing.T) {
	u1Windows(t, 0)
	p := newEmuPane("x", 52, 36)
	u1Paint(p, []string{"just some output", "no prompt here"})
	ps := newPendingSubmit(u1Captures[0].body, "bracketed", "")
	ps.lastRows = []string{"before"}
	if got, _ := watchSubmit(p, ps, time.Now()); got == submitTaken {
		t.Fatal("submitted with no composer on screen")
	}
	short := newPendingSubmit("ok", "bracketed", "")
	if bodyShownAbove([]string{"ok", "ok", "❯ ", "────────────"}, short) {
		t.Fatal("a two-letter body matched above")
	}
}

// The restore window is the ruled 200 ms, not zero.
func TestSubmitted1yp5_RestoreWindowDefault(t *testing.T) {
	if submitRestoreWindow != 200*time.Millisecond {
		t.Fatalf("submitRestoreWindow = %v, want 200ms", submitRestoreWindow)
	}
}

// Both ends are needed: the first line above with the last line missing (a
// body cut off, or someone else's message that starts the same) is no proof.
func TestSubmitted1yp5_TheLastLineIsNeededToo(t *testing.T) {
	c := u1Captures[0]
	rows := u1Rows(t, c.file)
	ps := newPendingSubmit(c.body+"\nand a closing line that never reached the screen", "bracketed", "")
	if bodyShownAbove(rows, ps) {
		t.Fatal("matched with the last line absent")
	}
}
