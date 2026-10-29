package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ini-cnp: a long send into a narrow pane read "our text was never seen in
// the box" though it landed. Measured on Claude Code 2.1.312, Haiku (claude
// --model haiku), a pane at its prompt: in a session with a name, the input
// box's top border carries it ("──── pa ─"), and a 300-character one-line
// body is scrolled inside the box at 32 and 40 columns, so only its middle
// and end show. The check that reads a scrolled box as a run of our text
// found the box between two PLAIN dash rules, took in the rows above the
// labelled border, and the run was never ours. At 80 columns the whole line
// fits and a line proof read it.
//
// Fixtures (testdata/footer_corpus/haiku312_label_<body>_w<cols>.txt): the
// last screen before the Enter, three widths by three bodies, a session
// named "pa", account names replaced by a placeholder of the same length.

const cnpShort = "[from b] CNP03 short ping, no reply."

const cnpTwo = "[from b] CNP04 two lines: the rig moved to the new build.\nSecond line: no action needed; end CNP04."

// cnpLong is the exact 300-character body of the long captures.
var cnpLong = func() string {
	tail := " end CNP01."
	s := "[from b] CNP01 long one line: " + strings.TrimSuffix(strings.Repeat("the landing is on main and CI is green on the sha; ", 8), " ")
	return s[:300-len(tail)] + tail
}()

var cnpBodies = map[string]string{"short": cnpShort, "long": cnpLong, "two": cnpTwo}

func cnpName(body string, cols int) string {
	return "haiku312_label_" + body + "_w" + map[int]string{32: "32", 40: "40", 80: "80"}[cols] + ".txt"
}

// cnpEmptied is a capture with its box cleared, as after a taken Enter: the
// box's rows become one bare prompt between its two borders.
func cnpEmptied(t *testing.T, name string) []string {
	t.Helper()
	rows := dmiScreen(t, name)
	top, bottom, ok := lowestComposerFrame(rows)
	if !ok {
		t.Fatalf("%s: no input box", name)
	}
	out := append([]string{}, rows[:top+1]...)
	out = append(out, "❯ ")
	return append(out, rows[bottom:]...)
}

// cnpFake is the fake Claude around a capture: the paste shows the box as
// captured, a taken Enter clears it.
func cnpFake(t *testing.T, name, body string, takes func(int) bool) *pkinFake {
	t.Helper()
	captured, emptied := dmiScreen(t, name), cnpEmptied(t, name)
	return &pkinFake{
		body:  body,
		shows: "captured",
		after: "emptied",
		takes: takes,
		screen: func(composer string) string {
			rows := emptied
			if composer == "captured" {
				rows = captured
			}
			return "\x1b[2J\x1b[H" + strings.Join(rows, "\r\n")
		},
	}
}

// The captures are what the bead describes: a labelled top border at every
// width, and at 32 and 40 columns the long body scrolled so its start is not
// in the box.
func TestLabelledBox_TheCapturesAreTheNamedNarrowBoxes(t *testing.T) {
	for _, cols := range []int{32, 40, 80} {
		for body := range cnpBodies {
			rows := dmiScreen(t, cnpName(body, cols))
			top, _, ok := lowestComposerFrame(rows)
			if !ok || !strings.HasSuffix(strings.TrimSpace(rows[top]), "─ pa ─") {
				t.Errorf("%s: no labelled input box", cnpName(body, cols))
			}
		}
	}
	for _, cols := range []int{32, 40} {
		box, _ := composerBoxRows(dmiScreen(t, cnpName("long", cols)))
		if joined := strings.Join(box, " "); strings.Contains(joined, "[from b]") || !strings.Contains(joined, "end") {
			t.Errorf("w%d: the long body is not scrolled in the box: %q", cols, joined)
		}
	}
}

// AC1, AC2: every capture, read as the verdict reads it, shows our text.
func TestLabelledBox_EveryCaptureShowsOurText(t *testing.T) {
	for _, cols := range []int{32, 40, 80} {
		for body, text := range cnpBodies {
			p, _, _ := proofPaneSized(t, cols, 30)
			lvxwPaint(p, dmiScreen(t, cnpName(body, cols)))
			tail, found := composerTail(p)
			if !submitSighted(p, tail, found, newPendingSubmit(text, "bracketed", "")) {
				t.Errorf("%s: our text in the box was not seen", cnpName(body, cols))
			}
		}
	}
}

// AC1, AC2 from the real send entry: a long and a short one-line send into
// the named pane read delivered at every width, with one Enter. (The fake
// reads a newline in the paste as an Enter, so the two-line body is read
// from the screen above.)
func TestLabelledBox_ASendIntoTheNamedPaneIsDelivered(t *testing.T) {
	for _, cols := range []int{32, 40, 80} {
		for _, body := range []string{"long", "short"} {
			name := cnpName(body, cols)
			t.Run(name, func(t *testing.T) {
				nma5Window(t, pkinBusyBound)
				f := cnpFake(t, name, cnpBodies[body], func(int) bool { return true })
				p, events := pkinSendRigSized(t, f, cols, 30)
				if v := pkinSend(p, cnpBodies[body]); v.Outcome != DeliveryDelivered {
					t.Fatalf("answered %q (%s)", v.Outcome, v.Reason)
				}
				if n := f.enters.Load(); n != 1 {
					t.Errorf("%d Enters for a send taken at the first", n)
				}
				pkinNoEvent(t, events)
			})
		}
	}
}

// AC3: the text stays in the box through the resend: not delivered.
func TestLabelledBox_LeftInTheBoxIsNotDelivered(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	f := cnpFake(t, cnpName("long", 32), cnpLong, func(int) bool { return false })
	p, _ := pkinSendRigSized(t, f, 32, 30)
	if v := pkinSend(p, cnpLong); v.Outcome != DeliveryNotDelivered {
		t.Fatalf("our text left in the box answered %q (%s), want not delivered", v.Outcome, v.Reason)
	}
	if n := f.enters.Load(); n != 2 {
		t.Errorf("%d Enters, want the send and one resend", n)
	}
}

// AC3: a box that never shows our text reads never seen, with the samples
// and the box's first line before and after the Enter.
func TestLabelledBox_NeverSeenNamesItsSamples(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	p, _, _ := proofPaneSized(t, 32, 30)
	other := cnpEmptied(t, cnpName("long", 32))
	for i, r := range other {
		if strings.HasPrefix(r, "❯") {
			other[i] = "❯ lunch order: two of the usual"
		}
	}
	lvxwPaint(p, other)
	s := startPreEnterSighting(p, newPendingSubmit(cnpLong, "bracketed", ""))
	time.Sleep(3 * composerEchoPoll)
	ps := newPendingSubmit(cnpLong, "bracketed", "")
	ps.seenInBox, ps.census = s.wait(), s.census
	lvxwPaint(p, cnpEmptied(t, cnpName("long", 32)))
	v := confirmAndRetrySubmit(p, ps)
	m := dmiNeverSeen.FindStringSubmatch(v.Reason)
	if v.Outcome != DeliveryUnconfirmed || m == nil || m[1] == "0" || m[2] != `"lunch order: two of the usual"` || m[3] != "an empty box" {
		t.Fatalf("other text in the named box answered %q (%s)", v.Outcome, v.Reason)
	}
}

// What a run in the named box is NOT: text of somebody else's in the box, or
// ours and a word of another, or our text only ABOVE the box. The last is
// the bug's twin: the reader that took in the rows above the labelled border
// must not now read them as the box.
func TestLabelledBox_WhatIsNotOurRun(t *testing.T) {
	long32 := dmiScreen(t, cnpName("long", 32))
	top, bottom, _ := lowestComposerFrame(long32)
	withBox := func(box ...string) []string {
		out := append([]string{}, long32[:top+1]...)
		out = append(out, box...)
		return append(out, long32[bottom:]...)
	}
	// Our text in the transcript above the named box, other text in it.
	above := []string{"  " + cnpLong[150:180], "  " + cnpLong[180:210], long32[top], "❯ please run the full suite and"}
	above = append(above, long32[bottom:]...)
	for name, screen := range map[string][]string{
		"unrelated text":             withBox("❯ please run the full suite and", "  report back when it is green"),
		"ours and a word of another": withBox("❯ CI is green on the sha; the", "  landing is on main XYZZY"),
		"an empty box":               withBox("❯ "),
		"our text above the box":     above,
	} {
		p, _, _ := proofPaneSized(t, 32, 30)
		lvxwPaint(p, screen)
		if boxRunIsOurs(p, newPendingSubmit(cnpLong, "bracketed", "")) {
			t.Errorf("%s: read as a run of our text in the box", name)
		}
	}
}

// A named box whose bottom border is off a short pane: the labelled top
// border opens it (isBoxBorder), and the run is ours.
func TestLabelledBox_ANamedBoxRunningOffTheBottom(t *testing.T) {
	rows := dmiScreen(t, cnpName("long", 32))
	_, bottom, _ := lowestComposerFrame(rows)
	cut := rows[:bottom]
	p, _, _ := proofPaneSized(t, 32, len(cut))
	lvxwPaint(p, cut)
	if !boxRunIsOurs(p, newPendingSubmit(cnpLong, "bracketed", "")) {
		t.Fatal("our run in a named box with its bottom border off screen was not read")
	}
	for _, s := range []struct {
		row  string
		want bool
	}{
		{"─────────────────────────── pa ─", true},
		{"────────── pa ─", true},
		{"───────── pa ─", false}, // nine dashes
		{"── a long title that is not a border at all ─", false},
		{"────────────────────────────────", true},
		{"", false},
	} {
		if got := isBoxBorder(s.row); got != s.want {
			t.Errorf("isBoxBorder(%q) = %v, want %v", s.row, got, s.want)
		}
	}
}

// AC4, feed it nothing: no screen, blank rows, a zero-width screen, a screen
// with no box, and an empty body against a box holding text.
func TestLabelledBox_NothingToRead(t *testing.T) {
	for name, screen := range map[string][]string{
		"no rows":           nil,
		"blank rows":        {"", "", ""},
		"zero-width screen": make([]string, 24),
		"no box on screen":  {"  Running 1 shell command…", "  ⎿  $ sleep 4"},
	} {
		if box, ok := composerBoxRows(screen); ok {
			t.Errorf("%s: read a box %q", name, box)
		}
	}
	p, _, _ := proofPaneSized(t, 32, 30)
	lvxwPaint(p, dmiScreen(t, cnpName("long", 32)))
	if boxRunIsOurs(p, newPendingSubmit("", "bracketed", "")) {
		t.Error("an empty body read as the run in the box")
	}
}

// AC5: every capture in the corpus keeps its verdict for a body that is not
// in it: none reads as our text.
func TestLabelledBox_NoCaptureReadsAsSomebodyElsesText(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "footer_corpus", "*.txt"))
	if err != nil || len(files) < 30 {
		t.Fatalf("corpus: %d files, %v", len(files), err)
	}
	unrelated := "[from qa1] PASS on the dashboard bead at the landed commit; nothing for you to do here, thanks"
	for _, f := range files {
		rows, width := corpusFooter(t, filepath.Base(f))
		if width == 0 {
			t.Fatalf("%s: no border to size the pane by", filepath.Base(f))
		}
		p, _, _ := proofPaneSized(t, width, len(rows)+1)
		lvxwPaint(p, rows)
		tail, found := composerTail(p)
		if submitSighted(p, tail, found, newPendingSubmit(unrelated, "bracketed", "")) {
			t.Errorf("%s: read as somebody else's text", filepath.Base(f))
		}
	}
}

// Verdict only: the box reader is called by the box-run check and nothing
// else, so nothing that decides whether to press Enter widens with it.
func TestLabelledBox_OnlyTheVerdictReadsTheWholeBox(t *testing.T) {
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	callers := map[string]bool{}
	call := regexp.MustCompile(`^(composerBoxRows|isBoxBorder)$`)
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
				if c, ok := n.(*ast.CallExpr); ok {
					if id, ok := c.Fun.(*ast.Ident); ok && call.MatchString(id.Name) {
						callers[fn.Name.Name+"→"+id.Name] = true
					}
				}
				return true
			})
		}
	}
	allowed := map[string]bool{"boxRunIsOurs→composerBoxRows": true, "openBottomBox→isBoxBorder": true}
	for c := range callers {
		if !allowed[c] {
			t.Errorf("unexpected caller %s", c)
		}
	}
	if !callers["boxRunIsOurs→composerBoxRows"] {
		t.Error("boxRunIsOurs no longer reads composerBoxRows")
	}
}
