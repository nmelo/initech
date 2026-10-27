package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ini-jpal: in a short pane (about 10 rows, a cell of a full grid) Claude
// Code scrolls a long message inside its box and the box's bottom rule and
// footer fall below the pane's edge. The box-run proof read only text between
// two rules, so it read the rows above the top rule, never sighted our text,
// and every long send read "our text was never seen in the box" though all of
// them landed (20 of 20, measured at load 3). The fixture is real: Claude Code
// 2.1.311 on Haiku 5.5, a 110x10 pane, read through initech peek.

// jpalBody is the exact text sent for haiku311_short_pane_long_box.txt.
var jpalBody = "[from rig] marker JPAL-F-01: fixture. " + strings.TrimSpace(strings.Repeat(
	"Long report body to match a real fleet message: the fix landed at main and CI is green on the sha; QA steps are on the bead. ", 4))

// jpalPane is a proof pane the size of the captured one, so the box's last
// row is the screen's last row, as it was.
func jpalPane(t *testing.T) *Pane {
	t.Helper()
	p, _, _ := proofPaneSized(t, 110, 10)
	return p
}

func jpalFixture(t *testing.T) []string {
	return lvxwFixture(t, "haiku311_short_pane_long_box.txt")
}

// jpalEmptied is the short pane after a taken Enter: the box is one bare
// prompt and its bottom rule and footer are back on screen.
func jpalEmptied(t *testing.T) []string {
	t.Helper()
	rows := jpalFixture(t)
	rule := -1
	for i, r := range rows {
		if isJpalRule(r) {
			rule = i
		}
	}
	if rule < 0 {
		t.Fatal("the fixture has no rule")
	}
	out := append([]string{}, rows[:rule+1]...)
	return append(out, "❯ ", rows[rule], "  Haiku 5.5 │ <user> │ 7%")
}

func isJpalRule(s string) bool {
	t := strings.TrimSpace(s)
	return len([]rune(t)) >= 10 && strings.Trim(t, "─") == ""
}

// The fixture is what the bead describes: the box's top rule is the last rule
// on screen, the prompt row follows it, and the box runs to the last row with
// only the end of our text in view.
func TestShortPane_TheFixtureIsABoxRunningOffTheBottomEdge(t *testing.T) {
	rows := jpalFixture(t)
	last := -1
	for i, r := range rows {
		if isJpalRule(r) {
			last = i
		}
	}
	if len(rows) != 10 || last < 0 || last == len(rows)-1 || !strings.HasPrefix(rows[last+1], "❯") {
		t.Fatalf("the fixture is not a 10-row pane whose box runs off the edge:\n%s", strings.Join(rows, "\n"))
	}
	if strings.Contains(strings.Join(rows, "\n"), "JPAL-F-01") {
		t.Fatal("the fixture shows the start of our text; it must show only the end")
	}
}

// Our long body, in a box whose bottom rule is off the screen, is seen.
func TestShortPane_ALongBodyInABoxRunningOffTheEdgeIsSeen(t *testing.T) {
	p := jpalPane(t)
	lvxwPaint(p, jpalFixture(t))
	ps := newPendingSubmit(jpalBody, "bracketed", "")
	tail, found := composerTail(p)
	if !submitSighted(p, tail, found, ps) {
		t.Fatalf("our body in a box running off the bottom edge was not seen (tail %q)", tail)
	}
}

// The measured failure: the box clears after the Enter, so the send is taken,
// not "never seen".
func TestShortPane_ALongSendThatClearsIsTaken(t *testing.T) {
	nma5Window(t, time.Second)
	p := jpalPane(t)
	lvxwPaint(p, jpalFixture(t))
	emptied := jpalEmptied(t)
	go func() { time.Sleep(150 * time.Millisecond); lvxwPaint(p, emptied) }()
	if got := confirmSubmit(p, newPendingSubmit(jpalBody, "bracketed", ""), time.Now()); got != submitTaken {
		t.Fatalf("a long send whose box cleared in a short pane read %v, want taken", got)
	}
}

// The twin: the box keeps our text, so the send is held, not taken.
func TestShortPane_ALongSendStillInTheBoxIsNotTaken(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	p := jpalPane(t)
	lvxwPaint(p, jpalFixture(t))
	if got := confirmSubmit(p, newPendingSubmit(jpalBody, "bracketed", ""), time.Now()); got != submitNotTaken {
		t.Fatalf("a long send still in a short pane's box read %v, want not taken", got)
	}
}

// Feed it nothing, and near misses: none of these is proof our text is in a
// box that runs off the edge.
func TestShortPane_WhatIsNotProof(t *testing.T) {
	rule := strings.Repeat("─", 40)
	for name, screen := range map[string][]string{
		"an empty screen":       {""},
		"a bare open box":       {"⏺ ok", rule, "❯ "},
		"unrelated text":        {"⏺ ok", rule, "❯ please run the full suite and report back when", "  it is green on the sha"},
		"ours and another word": {"⏺ ok", rule, "❯ real fleet message: the fix landed at main and CI is green on the sha; QA steps are on the bead.", "  XYZZY"},
		"our text after a rule, no prompt row": {"⏺ ok", rule,
			"  real fleet message: the fix landed at main and CI is green on the sha; QA steps are on the bead."},
		"eleven runes of ours": {"⏺ ok", rule, "❯ on the bead."},
	} {
		p := jpalPane(t)
		lvxwPaint(p, screen)
		ps := newPendingSubmit(jpalBody, "bracketed", "")
		tail, found := composerTail(p)
		if submitSighted(p, tail, found, ps) {
			t.Errorf("%s: read as our text in the box", name)
		}
	}
}

// Never under a dialog, like the closed box.
func TestShortPane_NotUnderADialog(t *testing.T) {
	p := jpalPane(t)
	lvxwPaint(p, jpalFixture(t))
	ps := newPendingSubmit(jpalBody, "bracketed", "")
	if !boxRunIsOurs(p, ps) {
		t.Fatal("precondition: with no dialog the open box's run is ours")
	}
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()
	if boxRunIsOurs(p, ps) {
		t.Error("the open box's run was read as ours under an open dialog")
	}
}

// Verdict only: the belt, which decides whether to press Enter, does not read
// the open box. It saw nothing of ours on this screen before and still sees
// nothing.
func TestShortPane_TheBeltDoesNotReadTheOpenBox(t *testing.T) {
	p := jpalPane(t)
	lvxwPaint(p, jpalFixture(t))
	if bodyTextVisible(p, newPendingSubmit(jpalBody, "bracketed", "")) {
		t.Error("the belt's proof read our text in the open box; only the verdict may")
	}
}

// peek -n N on a pane shorter than N returns the screen and says why it is
// short; asking for no more than the pane holds, or for everything (0), adds
// nothing.
func TestShortPane_PeekSaysWhenThePaneIsShorterThanAsked(t *testing.T) {
	for _, tc := range []struct {
		lines  int
		notice string
	}{
		{50, "pane is 10 rows; peek reads the screen only"},
		{11, "pane is 10 rows; peek reads the screen only"},
		{10, ""},
		{3, ""},
		{0, ""},
	} {
		p := newEmuPane("eng7", 110, 10)
		lvxwPaint(p, jpalFixture(t))
		conn := &dispatchConn{}
		dispatchIPC(&dispatchHost{findPane: p, findOK: true}, conn, IPCRequest{Action: "peek", Target: "eng7", Lines: tc.lines}, nil)
		resp := decodeDispatchResponse(t, conn)
		if !resp.OK || !strings.Contains(resp.Data, "on the bead.") {
			t.Fatalf("-n %d: peek did not return the screen: ok=%v %q", tc.lines, resp.OK, resp.Data)
		}
		got := strings.Join(resp.Notices, "|")
		if got != tc.notice {
			t.Errorf("-n %d: notices %q, want %q", tc.lines, got, tc.notice)
		}
	}
}

// Feed it nothing: a pane with no emulator returns no notice and no panic.
func TestShortPane_PeekNoticeWithNoEmulatorIsEmpty(t *testing.T) {
	p := newEmuPane("eng7", 110, 10)
	p.emu = nil
	if got := peekShortPaneNotice(p, 50); got != "" {
		t.Errorf("no emulator: notice %q, want none", got)
	}
}

// Verdict only, structurally: the open-box read is called from boxRunIsOurs
// and nothing else, so the belt and every Enter decision stay as they were.
func TestShortPane_OnlyTheBoxRunReadsTheOpenBox(t *testing.T) {
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
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "openBottomBox" {
						callers[fn.Name.Name] = true
					}
				}
				return true
			})
		}
	}
	if len(callers) != 1 || !callers["boxRunIsOurs"] {
		t.Errorf("openBottomBox is called from %v; only boxRunIsOurs may call it", callers)
	}
}
