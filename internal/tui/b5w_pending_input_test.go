package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-b5w: a pane whose input box holds text that was not sent reads pending
// input, not idle. Captures (testdata/footer_corpus/haiku312_state_*): Claude
// Code 2.1.312 on Haiku (claude --model haiku), 32 and 80 columns, read
// through initech peek: an empty box at rest, one line waiting, a two-line
// paste waiting, a turn running; and testdata/haiku312_trust_w*.txt, the
// trust dialog, whose cursor row "❯ No, exit" starts with the same marker and
// is not a numbered option (a dialog with no input box, so not in the footer
// corpus). Account names replaced by a placeholder of the same length.

func b5wName(state string, cols int) string {
	return "haiku312_state_" + state + "_w" + map[int]string{32: "32", 80: "80"}[cols] + ".txt"
}

// b5wPane is an idle pane the size of the capture, painted with it, and read
// once by the maintenance tick's pending reading.
func b5wPane(t *testing.T, rows []string, cols int) *Pane {
	t.Helper()
	p, _, _ := proofPaneSized(t, cols, 30)
	p.activity = StateIdle
	lvxwPaint(p, rows)
	p.observePendingInput(emuRowsBlocking(p.emu), cols)
	return p
}

// AC1, AC3, AC5: each capture reads its state from a stored idle.
func TestPendingInput_EachCaptureReadsItsState(t *testing.T) {
	for _, cols := range []int{32, 80} {
		for state, want := range map[string]ActivityState{
			"idle":     StateIdle,
			"pending1": StatePendingInput,
			"pending2": StatePendingInput,
			"running":  StateIdle, // its box is empty; the stored state decides running
		} {
			name := b5wName(state, cols)
			p := b5wPane(t, dmiScreen(t, name), cols)
			if got := p.Activity(); got != want {
				t.Errorf("%s: %v (pending %q), want %v", name, got, p.PendingInput(), want)
			}
		}
	}
}

// The pending reading names the box's first line: for the paste, the first
// line of the paste, and the start of the line at 32 columns.
func TestPendingInput_ReadsTheFirstLineOfTheBox(t *testing.T) {
	for name, want := range map[string]string{
		b5wName("pending1", 80): "[from b] B5W one line typed and not sent yet",
		b5wName("pending1", 32): "[from b] B5W one line typed",
		b5wName("pending2", 80): "[from b] B5W two lines pasted, first line here",
		b5wName("pending2", 32): "[from b] B5W two lines",
	} {
		cols := 80
		if strings.HasSuffix(name, "w32.txt") {
			cols = 32
		}
		if got := b5wPane(t, dmiScreen(t, name), cols).PendingInput(); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}

// Running, waiting, suspended and dead stay what they are with text in the
// box: a suspended agent is never pending.
func TestPendingInput_StrongerStatesWin(t *testing.T) {
	for _, stored := range []ActivityState{StateRunning, StateWaitingInput, StateSuspended, StateDead} {
		p := &Pane{activity: stored, pendingInput: "half a line"}
		if got := p.Activity(); got != stored {
			t.Errorf("stored %v with text in the box reads %v", stored, got)
		}
	}
}

// Text stuck in the box outranks background work: it needs the operator.
func TestPendingInput_OutranksBackground(t *testing.T) {
	p := &Pane{activity: StateIdle, pendingInput: "half a line", bgWork: backgroundWork{shells: 1, since: time.Now()}}
	if got := p.Activity(); got != StatePendingInput {
		t.Errorf("pending with a background shell reads %v, want pending", got)
	}
	p.pendingInput = ""
	if got := p.Activity(); got != StateBackground {
		t.Errorf("the twin, no text: %v, want background", got)
	}
}

// AC2: the box empties (Enter taken) and the next tick reads idle: nothing
// stale.
func TestPendingInput_ClearsWhenTheBoxEmpties(t *testing.T) {
	p := b5wPane(t, dmiScreen(t, b5wName("pending1", 32)), 32)
	if p.Activity() != StatePendingInput {
		t.Fatal("precondition: pending")
	}
	lvxwPaint(p, dmiScreen(t, b5wName("idle", 32)))
	p.observePendingInput(emuRowsBlocking(p.emu), 32)
	if got := p.Activity(); got != StateIdle || p.PendingInput() != "" {
		t.Errorf("after the box emptied: %v, %q; want idle and nothing", got, p.PendingInput())
	}
}

// AC3: a picker's or a dialog's cursor row is never pending: the trust
// dialog (unnumbered "❯ No, exit"), the login picker ("❯ 1. Claude
// account"), a numbered option sitting where the box's prompt row would be,
// a permission prompt, and text in the box while a dialog is latched.
func TestPendingInput_DialogsAndPickersNeverPending(t *testing.T) {
	screens := map[string][]string{
		"trust dialog 32": lvxwFixture(t, "haiku312_trust_w32.txt"),
		"trust dialog 80": lvxwFixture(t, "haiku312_trust_w80.txt"),
		"login picker":    lvxwFixture(t, "haiku310_login.txt"),
		"permission":      lvxwFixture(t, "haiku312_write_prompt_bgshell_w36.txt"),
		"numbered option in the box's place": {
			"  a row of transcript above the box",
			"────────────────────────────────",
			"❯ 1. Yes",
			"────────────────────────────────",
			"  Haiku 5.5 │ Acct │ 5% │ 89%…",
		},
	}
	for name, rows := range screens {
		p := b5wPane(t, rows, 32)
		if got := p.Activity(); got == StatePendingInput {
			t.Errorf("%s: read pending (%q)", name, p.PendingInput())
		}
	}
	// A latched dialog: the box holds text, nothing is pending.
	p, _, _ := proofPaneSized(t, 32, 30)
	p.activity = StateIdle
	p.latchDialogOpen("Claude needs your permission")
	lvxwPaint(p, dmiScreen(t, b5wName("pending1", 32)))
	p.observePendingInput(emuRowsBlocking(p.emu), 32)
	if p.Activity() == StatePendingInput {
		t.Error("text in the box under a latched dialog read pending")
	}
	// Its twin without the latch is pending, so the latch is what decided.
	if b5wPane(t, dmiScreen(t, b5wName("pending1", 32)), 32).Activity() != StatePendingInput {
		t.Error("the twin with no latch is not pending")
	}
}

// AC6, feed it nothing: no screen, blank rows, the marker alone, the marker
// and spaces, the marker and a no-break space, Claude's queued-messages hint.
func TestPendingInput_NothingToRead(t *testing.T) {
	// A real screen always has rows above the box; the frame needs one.
	box := func(prompt string) []string {
		return []string{"  transcript", "────────────────────────────────", prompt, "────────────────────────────────", "  Haiku 5.5 │ Acct │ 5% │ 89%…"}
	}
	for name, rows := range map[string][]string{
		"no rows":          nil,
		"blank rows":       {"", "", ""},
		"only the marker":  {"❯"},
		"marker in a box":  box("❯"),
		"marker, spaces":   box("❯      "),
		"marker, nbsp":     box("❯  "),
		"queued hint":      box("❯ " + queuedMessagesHint),
		"a zero-width box": {"", "❯ x", ""},
	} {
		if text, ok := pendingInputText(rows, 32); ok {
			t.Errorf("%s: read pending %q", name, text)
		}
	}
	// The twin: a box holding a word is pending.
	if _, ok := pendingInputText(box("❯ ok"), 32); !ok {
		t.Error("a box holding one word is not pending")
	}
}

// AC5: every capture in the corpus keeps its reading. The ones whose box
// holds text are the captures taken with text waiting before the Enter.
func TestPendingInput_TheCorpusReadsAsCaptured(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "footer_corpus", "*.txt"))
	if err != nil || len(files) < 40 {
		t.Fatalf("corpus: %d files, %v", len(files), err)
	}
	holdsText := map[string]bool{
		"haiku312_busy_collapsed_w49.txt": true,
		"haiku312_state_pending1_w32.txt": true, "haiku312_state_pending1_w80.txt": true,
		"haiku312_state_pending2_w32.txt": true, "haiku312_state_pending2_w80.txt": true,
	}
	for _, b := range []string{"short", "long", "two"} {
		for _, c := range []string{"32", "40", "80"} {
			holdsText["haiku312_label_"+b+"_w"+c+".txt"] = true
		}
	}
	for _, f := range files {
		name := filepath.Base(f)
		rows, width := corpusFooter(t, name)
		_, got := pendingInputText(rows, width)
		if got != holdsText[name] {
			t.Errorf("%s: pending %v, want %v", name, got, holdsText[name])
		}
	}
}

// AC4: a delivered verdict notes text left in the box, by the same reading.
func TestPendingInput_ADeliveredSendNotesTextLeftInTheBox(t *testing.T) {
	p, _, _ := proofPaneSized(t, 32, 30)
	lvxwPaint(p, dmiScreen(t, b5wName("pending1", 32)))
	if got := leftInBoxNote(p); got != LeftInBoxPrefix+`"[from b] B5W one line typed"` {
		t.Errorf("note %q", got)
	}
	lvxwPaint(p, dmiScreen(t, b5wName("idle", 32)))
	if got := leftInBoxNote(p); got != "" {
		t.Errorf("an empty box noted %q", got)
	}
	lvxwPaint(p, dmiScreen(t, b5wName("pending1", 32)))
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()
	if got := leftInBoxNote(p); got != "" {
		t.Errorf("under a dialog, noted %q", got)
	}
}

// AC4 from the real send entry: our message taken, the operator's draft
// restored into the box: delivered, with the note.
func TestPendingInput_TheSendSaysWhenTheBoxHoldsADraft(t *testing.T) {
	nma5Window(t, pkinBusyBound)
	draft := dmiWithBox(t, b5wName("idle", 32), "❯ my own half-typed note")
	captured := dmiWithBox(t, b5wName("idle", 32), "❯ "+cnpShort)
	f := &pkinFake{
		body:  cnpShort,
		shows: "captured",
		after: "draft",
		takes: func(int) bool { return true },
		screen: func(composer string) string {
			rows := draft
			if composer == "captured" {
				rows = captured
			}
			return "\x1b[2J\x1b[H" + strings.Join(rows, "\r\n")
		},
	}
	p, _ := pkinSendRigSized(t, f, 32, 30)
	v := pkinSend(p, cnpShort)
	if v.Outcome != DeliveryDelivered || v.Reason != LeftInBoxPrefix+`"my own half-typed note"` {
		t.Fatalf("answered %q (%s), want delivered with the note", v.Outcome, v.Reason)
	}
}

// Auto-suspend never parks a pane holding unsent text: it would go with the
// process.
func TestPendingInput_AutoSuspendSkipsAPendingPane(t *testing.T) {
	tui := newResourceTestTUI(100000, 10000, 80)
	pending := newResourceTestPane("eng1", true, StateIdle, "", time.Now().Add(-30*time.Minute))
	pending.pendingInput = "half a line"
	tui.panes = toPaneViews([]*Pane{pending})
	tui.checkSuspendPolicy()
	if pending.Activity() == StateSuspended {
		t.Fatal("a pane with unsent text in its box was auto-suspended")
	}
	// The twin: the same pane with an empty box is suspended.
	tui = newResourceTestTUI(100000, 10000, 80)
	idle := newResourceTestPane("eng1", true, StateIdle, "", time.Now().Add(-30*time.Minute))
	tui.panes = toPaneViews([]*Pane{idle})
	tui.checkSuspendPolicy()
	if idle.Activity() != StateSuspended {
		t.Fatal("the twin with an empty box was not suspended: the test does not reach the policy")
	}
}

// Every surface's words: the state, the header tag, the suspend refusal.
func TestPendingInput_TheWordsEachSurfaceShows(t *testing.T) {
	if got := StatePendingInput.String(); got != "pending" {
		t.Errorf("String %q", got)
	}
	p := &Pane{activity: StateIdle, pendingInput: "x"}
	if got := pendingTag(p); got != "[pending input] " {
		t.Errorf("tag %q", got)
	}
	if got := pendingTag(&Pane{activity: StateIdle}); got != "" {
		t.Errorf("an idle pane's tag %q", got)
	}
	if got := describeActivity(StatePendingInput); got != "holding unsent text in its input box" {
		t.Errorf("suspend refusal %q", got)
	}
}

// AC1, the overlay: the row says pending input, with a yellow hollow dot
// (quiet, but not at rest), not idle's grey.
func TestPendingInput_TheOverlayRowSaysPendingInput(t *testing.T) {
	tui, screen := newTestTUIWithScreen("eng1")
	lp := tui.panes[0].(*Pane)
	lp.mu.Lock()
	lp.activity, lp.pendingInput = StateIdle, "half a line"
	lp.mu.Unlock()
	tui.renderOverlay()
	screen.Show()
	w, h := screen.Size()
	for y := 0; y < h; y++ {
		row := make([]rune, 0, w)
		for x := 0; x < w; x++ {
			ch, _, _, _ := screen.GetContent(x, y)
			row = append(row, ch)
		}
		line := string(row)
		if !strings.Contains(line, "eng1") {
			continue
		}
		if !strings.Contains(line, "pending input") {
			t.Errorf("overlay row %q, want it to say pending input", strings.TrimSpace(line))
		}
		x := strings.IndexRune(line, '○')
		if x < 0 {
			t.Fatalf("overlay row %q has no hollow dot", strings.TrimSpace(line))
		}
		_, _, style, _ := screen.GetContent(len([]rune(line[:x])), y)
		if fg, _, _ := style.Decompose(); fg != tcell.ColorYellow {
			t.Errorf("dot colour %v, want yellow", fg)
		}
		return
	}
	t.Fatal("no overlay row for eng1")
}

// The maintenance tick is what reads the box: an idle pane with text in it
// is idle until the tick, pending after.
func TestPendingInput_TheTickReadsTheBox(t *testing.T) {
	p, _, _ := proofPaneSized(t, 32, 30)
	p.activity, p.alive = StateIdle, true
	p.lastEmuCols = 32
	lvxwPaint(p, dmiScreen(t, b5wName("pending1", 32)))
	if p.Activity() != StateIdle {
		t.Fatal("precondition: idle before any tick")
	}
	var last time.Time
	maintainModal([]PaneView{p}, time.Now(), &last)
	if got := p.Activity(); got != StatePendingInput {
		t.Errorf("after one tick: %v (pending %q), want pending", got, p.PendingInput())
	}
}

// A dialog drawn below a box that holds text: the box alone reads text, so
// the dialog on screen (a permission prompt) and the banner (the login
// picker, which the dialog check does not name) are what keep it from
// pending.
func TestPendingInput_ADialogBelowABoxHoldingTextIsNotPending(t *testing.T) {
	box := []string{"  transcript", "────────────────────────────────", "❯ half a line", "────────────────────────────────"}
	for name, below := range map[string][]string{
		"permission prompt": {" Do you want to proceed?", " ❯ 1. Yes", "   2. No", " Esc to cancel"},
		"login picker":      {" Select login method:", " ❯ 1. Claude account with subscription", "   2. Anthropic Console account"},
		// Unnumbered options: the dialog check names it, the banner does not.
		"unnumbered dialog": {" Do you want to proceed?", " ❯ Yes", "   No", " Esc to cancel"},
	} {
		rows := append(append([]string{}, box...), below...)
		if _, ok := pendingInputText(rows, 32); !ok {
			t.Fatalf("%s: precondition: the box alone reads text", name)
		}
		p := &Pane{activity: StateIdle}
		p.observePendingInput(rows, 32)
		if p.Activity() == StatePendingInput {
			t.Errorf("%s: a dialog below the box read pending", name)
		}
	}
	// The twin: the box with nothing below it is pending.
	p := &Pane{activity: StateIdle}
	p.observePendingInput(box, 32)
	if p.Activity() != StatePendingInput {
		t.Error("the twin, the box alone, is not pending")
	}
}

// AC4 on the resend path: the first Enter is not taken, the second is, and
// the box then holds the operator's restored draft: delivered, with the note.
func TestPendingInput_TheResendSaysWhenTheBoxHoldsADraft(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	draft := dmiWithBox(t, b5wName("idle", 32), "❯ my own half-typed note")
	captured := dmiWithBox(t, b5wName("idle", 32), "❯ "+cnpShort)
	f := &pkinFake{
		body:  cnpShort,
		shows: "captured",
		after: "draft",
		takes: func(n int) bool { return n == 2 },
		screen: func(composer string) string {
			rows := draft
			if composer == "captured" {
				rows = captured
			}
			return "\x1b[2J\x1b[H" + strings.Join(rows, "\r\n")
		},
	}
	p, _ := pkinSendRigSized(t, f, 32, 30)
	v := pkinSend(p, cnpShort)
	if f.enters.Load() != 2 {
		t.Fatalf("%d Enters, want the send and one resend", f.enters.Load())
	}
	if v.Outcome != DeliveryDelivered || v.Reason != LeftInBoxPrefix+`"my own half-typed note"` {
		t.Fatalf("answered %q (%s), want delivered with the note", v.Outcome, v.Reason)
	}
}

// The Agents panel names a pending agent in yellow, driven through the real
// model: quiet for an hour (idle by output recency), its screen showing text
// in the box, read by the render loop's own maintenance tick.
func TestPendingInput_TheAgentsPanelNameIsYellow(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	for _, pv := range tui.panes {
		if pv.Name() == "eng2" {
			p := pv.(*Pane)
			p.emu.Resize(80, 24)
			lvxwPaint(p, dmiScreen(t, b5wName("pending1", 80)))
			p.mu.Lock()
			p.lastOutputTime = time.Now().Add(-time.Hour)
			p.lastEmuCols = 80
			p.mu.Unlock()
		}
	}
	openAgentsList(tui)
	tui.agents.selected = 0
	// Two frames: the first frame's tick reads the box after the panel is
	// drawn, so the second is the first to show it, as in the running TUI.
	tui.render()
	tui.render()
	c := locateCells(t, tui, s, "eng2")["eng2"]
	_, _, st, _ := s.GetContent(c.x+8, c.y) // "%3d " and the "[x] " box, then the name
	if fg, _, _ := st.Decompose(); fg != tcell.ColorYellow {
		t.Errorf("pending agent's name fg = %v, want yellow", fg)
	}
}

// The delivered note never waits on the pane (ini-psjt): with the render lock
// held it gives up at once and says nothing; the pane's own reading follows
// at the next tick.
func TestPendingInput_TheNoteNeverWaitsOnAHeldScreen(t *testing.T) {
	p, _, _ := proofPaneSized(t, 32, 30)
	lvxwPaint(p, dmiScreen(t, b5wName("pending1", 32)))
	if leftInBoxNote(p) == "" {
		t.Fatal("precondition: with the lock free the note is given")
	}
	p.renderMu.Lock()
	done := make(chan string, 1)
	go func() { done <- leftInBoxNote(p) }()
	select {
	case got := <-done:
		p.renderMu.Unlock()
		if got != "" {
			t.Errorf("with the screen held, noted %q", got)
		}
	case <-time.After(2 * time.Second):
		p.renderMu.Unlock()
		t.Fatal("the note waited on a held screen")
	}
}
