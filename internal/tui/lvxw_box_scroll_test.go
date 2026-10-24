package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ini-lvxw: Claude Code 2.1.310 caps the input box at a few rows and scrolls
// a long message inside it, keeping the end in view. In a narrow pane the
// box starts mid-message and the start of our text is never on screen, so a
// proof that needs a whole body line visible never fires and every long send
// read "could not confirm". The fixtures are real: Claude Code 2.1.310 on
// Haiku 5.5, read through initech's own emulator (initech peek) in a 34x18
// and a 140x45 pane, user name replaced.

// lvxwBody is the exact text sent for haiku310_narrow_long_box.txt.
const lvxwBody = "[from super] Delivery check for the orange-warning investigation: reply with ok when you see this, then stay idle. Marker QZX310 is here so the message is long enough to wrap across several rows of a narrow box."

func lvxwFixture(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// lvxwEmptied is the narrow fixture with the box cleared, as after a taken
// Enter: the rows between the two rules become one bare prompt.
func lvxwEmptied(t *testing.T) []string {
	t.Helper()
	rows := lvxwFixture(t, "haiku310_narrow_long_box.txt")
	var rules []int
	for i, r := range rows {
		if s := strings.TrimSpace(r); len([]rune(s)) >= 10 && strings.Trim(s, "─") == "" {
			rules = append(rules, i)
		}
	}
	if len(rules) < 2 {
		t.Fatalf("the fixture has %d rules, want the box's two", len(rules))
	}
	top, bottom := rules[len(rules)-2], rules[len(rules)-1]
	out := append([]string{}, rows[:top+1]...)
	out = append(out, "❯ ")
	return append(out, rows[bottom:]...)
}

func lvxwPaint(p *Pane, rows []string) { nma5Paint(p, strings.Join(rows, "\r\n")) }

// The fixture is what the bead describes: the box starts mid-message.
func TestBoxScroll_TheFixtureShowsOnlyTheEndOfOurText(t *testing.T) {
	joined := strings.Join(lvxwFixture(t, "haiku310_narrow_long_box.txt"), "\n")
	if !strings.Contains(joined, "❯ the message is long enough to") || strings.Contains(joined, "Delivery check") {
		t.Fatalf("the fixture is not the scrolled box this test is about:\n%s", joined)
	}
}

// Our long body, scrolled inside the 2.1.310 box, is seen.
func TestBoxScroll_ALongBodyScrolledInTheBoxIsSeen(t *testing.T) {
	p, _, _ := proofPane(t)
	lvxwPaint(p, lvxwFixture(t, "haiku310_narrow_long_box.txt"))
	ps := newPendingSubmit(lvxwBody, "bracketed", "")
	tail, found := composerTail(p)
	if !submitSighted(p, tail, found, ps) {
		t.Fatalf("our body scrolled in the box was not seen (tail %q)", tail)
	}
}

// Criterion 1: the box empties after the Enter, so the send is taken.
func TestBoxScroll_ALongSendThatClearsIsTaken(t *testing.T) {
	nma5Window(t, time.Second)
	p, _, _ := proofPane(t)
	lvxwPaint(p, lvxwFixture(t, "haiku310_narrow_long_box.txt"))
	emptied := lvxwEmptied(t)
	go func() { time.Sleep(150 * time.Millisecond); lvxwPaint(p, emptied) }()
	if got := confirmSubmit(p, newPendingSubmit(lvxwBody, "bracketed", ""), time.Now()); got != submitTaken {
		t.Fatalf("a long send whose box cleared read %v, want taken", got)
	}
}

// Criterion 2, the negative twin: the box keeps our text, so it is held.
func TestBoxScroll_ALongSendStillInTheBoxIsNotTaken(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	p, _, _ := proofPane(t)
	lvxwPaint(p, lvxwFixture(t, "haiku310_narrow_long_box.txt"))
	if got := confirmSubmit(p, newPendingSubmit(lvxwBody, "bracketed", ""), time.Now()); got != submitNotTaken {
		t.Fatalf("a long send still in the box read %v, want not taken", got)
	}
}

// The wide 2.1.310 box shows the whole short message: seen, as before.
func TestBoxScroll_AWideBoxWithTheWholeMessageIsSeen(t *testing.T) {
	p, _, _ := proofPane(t)
	lvxwPaint(p, lvxwFixture(t, "haiku310_wide_short_box.txt"))
	ps := newPendingSubmit("[from super] short marker QZX310 in a wide box", "bracketed", "")
	tail, found := composerTail(p)
	if !submitSighted(p, tail, found, ps) {
		t.Fatalf("the whole short message in a wide 2.1.310 box was not seen (tail %q)", tail)
	}
}

// Feed it nothing, and near misses: none of these is proof our text is in
// the box.
func TestBoxScroll_WhatIsNotProof(t *testing.T) {
	box := func(content ...string) []string {
		rows := []string{"────────────────────"}
		rows = append(rows, content...)
		return append(rows, "────────────────────", "  Haiku 5.5 │ <user>")
	}
	for name, screen := range map[string][]string{
		"an empty screen":            {""},
		"a box with no text":         box("❯ "),
		"unrelated text":             box("❯ please run the full suite and", "  report back when it is green"),
		"eleven runes of ours":       box("❯ a narrow box."),
		"ours and a word of another": box("❯ the message is long enough to", "  wrap across several rows of a", "  narrow box. XYZZY"),
	} {
		p, _, _ := proofPane(t)
		lvxwPaint(p, screen)
		ps := newPendingSubmit(lvxwBody, "bracketed", "")
		tail, found := composerTail(p)
		if submitSighted(p, tail, found, ps) {
			t.Errorf("%s: read as our text in the box", name)
		}
	}
}

// Verdict only: the box-run proof is read by submitSighted and nothing else,
// so nothing that decides whether to press Enter widens with it.
func TestBoxScroll_OnlyTheVerdictReadsTheBoxRun(t *testing.T) {
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	callers := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range af.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "boxRunIsOurs" {
						callers[fn.Name.Name] = true
					}
				}
				return true
			})
		}
	}
	if len(callers) != 1 || !callers["submitSighted"] {
		t.Errorf("boxRunIsOurs is called from %v; only submitSighted may call it", callers)
	}
}

// Never under a dialog: with a dialog latched, our scrolled text in the box
// is not read as the box holding it.
func TestBoxScroll_NotUnderADialog(t *testing.T) {
	p, _, _ := proofPane(t)
	lvxwPaint(p, lvxwFixture(t, "haiku310_narrow_long_box.txt"))
	ps := newPendingSubmit(lvxwBody, "bracketed", "")
	if !boxRunIsOurs(p, ps) {
		t.Fatal("precondition: with no dialog the box run is ours")
	}
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()
	if boxRunIsOurs(p, ps) {
		t.Error("the box run was read as ours under an open dialog")
	}
}
