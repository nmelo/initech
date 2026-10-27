package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// ini-ctrw: peek returns the last N lines of an agent's output whatever the
// pane's height: scrollback above the screen, oldest first, as a tall
// terminal shows them. An alt screen (an editor, a pager) is the screen only.

// ctrwPane is a live pane rows tall that has printed lines 1..printed, one
// per row, so its scrollback holds the ones that left the top.
func ctrwPane(rows, printed int) *Pane {
	p := newEmuPane("eng1", 80, rows)
	var b strings.Builder
	for i := 1; i <= printed; i++ {
		fmt.Fprintf(&b, "line %d\r\n", i)
	}
	p.emu.Write([]byte(b.String()))
	return p
}

// ctrwNumbers parses peek output back into the line numbers it shows.
func ctrwNumbers(t *testing.T, out string) []int {
	t.Helper()
	var ns []int
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		n, err := strconv.Atoi(strings.TrimPrefix(l, "line "))
		if err != nil {
			t.Fatalf("unexpected line %q in:\n%s", l, out)
		}
		ns = append(ns, n)
	}
	return ns
}

func ctrwConsecutive(t *testing.T, what string, ns []int, first, last int) {
	t.Helper()
	if len(ns) != last-first+1 {
		t.Fatalf("%s: %d lines (%v), want %d..%d", what, len(ns), ns, first, last)
	}
	for i, n := range ns {
		if n != first+i {
			t.Fatalf("%s: line %d is %d, want %d (seam gap or duplicate): %v", what, i, n, first+i, ns)
		}
	}
}

// A 10-row pane with 200 lines of output: -n 50 gives the last 50, in order,
// across the seam between scrollback and screen.
func TestPeekHistory_AShortPaneReturnsTheLinesAskedFor(t *testing.T) {
	p := ctrwPane(10, 200)
	r := peekReadBlocking(p, 50)
	ctrwConsecutive(t, "-n 50", ctrwNumbers(t, r.text), 151, 200)
	if r.found != 50 || r.alt {
		t.Errorf("read %+v, want 50 found, not alt", r)
	}
}

// -n within the screen: the screen's last N, as before.
func TestPeekHistory_WithinTheScreenIsTheScreen(t *testing.T) {
	p := ctrwPane(10, 200)
	ctrwConsecutive(t, "-n 5", ctrwNumbers(t, peekReadBlocking(p, 5).text), 196, 200)
}

// Fewer lines than asked exist: all of them, and the reader says so.
func TestPeekHistory_AllThePaneKeeps(t *testing.T) {
	p := ctrwPane(10, 13) // 3 in scrollback, 10 on screen
	r := peekReadBlocking(p, 50)
	ctrwConsecutive(t, "13 lines", ctrwNumbers(t, r.text), 1, 13)
	if r.found != 13 || r.alt || r.kept <= 0 {
		t.Errorf("read %+v, want 13 found and the history cap", r)
	}
}

// An alt screen is the screen only, never the scrollback behind it.
func TestPeekHistory_AnAltScreenIsTheScreenOnly(t *testing.T) {
	p := ctrwPane(10, 200)
	p.emu.Write([]byte("\x1b[?1049h\x1b[2J\x1b[Hpager row 1\r\npager row 2"))
	r := peekReadBlocking(p, 50)
	if !r.alt || r.rows != 10 || strings.Contains(r.text, "line ") || !strings.Contains(r.text, "pager row 2") {
		t.Errorf("alt screen read %+v", r)
	}
}

// -n 0 keeps its meaning: the screen (its last row is the cursor's blank one).
func TestPeekHistory_NZeroIsTheScreen(t *testing.T) {
	p := ctrwPane(10, 200)
	ctrwConsecutive(t, "-n 0", ctrwNumbers(t, peekReadBlocking(p, 0).text), 192, 200)
}

// patrol reads the same source from the main loop: 20 lines of a 10-row pane.
func TestPeekHistory_PatrolReadsTwentyFromAShortPane(t *testing.T) {
	p := ctrwPane(10, 200)
	ctrwConsecutive(t, "patrol", ctrwNumbers(t, peekContent(p, 20)), 181, 200)
}

// The note (ini-jpal's, condition from ini-ctrw): only when the reply holds
// fewer lines than asked. A short pane whose scrollback fills the reply says
// nothing; a full-screen program says it reads the screen only; a pane whose
// whole output is shorter names the history a pane keeps.
func TestPeekHistory_TheNoteOnlyWhenTheReplyIsShort(t *testing.T) {
	full := ctrwPane(10, 200)
	if n := peekNotice(peekReadBlocking(full, 50), 50); n != "" {
		t.Errorf("scrollback filled the reply: note %q, want none", n)
	}
	alt := ctrwPane(10, 200)
	alt.emu.Write([]byte("\x1b[?1049h\x1b[2J\x1b[Hpager row 1\r\npager row 2"))
	if n := peekNotice(peekReadBlocking(alt, 50), 50); n != "pane is 10 rows; peek reads the screen only" {
		t.Errorf("alt screen: note %q", n)
	}
	short := ctrwPane(10, 13)
	if n := peekNotice(peekReadBlocking(short, 50), 50); n != "peek returned 13 of 50 lines: all the pane keeps (up to 10000 lines of history)" {
		t.Errorf("short history: note %q", n)
	}
	if n := peekNotice(peekReadBlocking(short, 13), 13); n != "" {
		t.Errorf("exactly what was asked: note %q, want none", n)
	}
}

// A negative -n is read as 0, the screen; never a panic in the handler.
func TestPeekHistory_ANegativeCountIsTheScreen(t *testing.T) {
	p := ctrwPane(10, 200)
	for _, n := range []int{-1, -50, -100000} {
		ctrwConsecutive(t, fmt.Sprintf("-n %d", n), ctrwNumbers(t, peekReadBlocking(p, n).text), 192, 200)
		if note := peekNotice(peekReadBlocking(p, n), n); note != "" {
			t.Errorf("-n %d: note %q, want none", n, note)
		}
	}
	ctrwConsecutive(t, "patrol -n -50", ctrwNumbers(t, peekContent(p, -50)), 192, 200)
}
