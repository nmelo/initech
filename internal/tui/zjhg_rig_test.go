//go:build !windows

package tui

// zjhg_rig_test.go — the REAL-CLAUDE measurement leg for ini-zjhg.
//
// Two questions this rig exists to answer with bytes rather than reasoning,
// both of which decide code that ships:
//
//  1. BELT PREDICATE. The never-submit belt promotes promptHasContent from a
//     submit-RETRY optimisation to a submit PRECONDITION. That is only safe if
//     it is reliably TRUE after a normal body write; a false negative would
//     convert a correct drain into text sitting unsubmitted in the composer,
//     which is the delayed-delivery harm ini-zjhg AC item 2 forbids. Measured
//     here for a small body and for a large one (large pastes collapse to a
//     "[Pasted text #N +M lines]" reference), together with the body->composer
//     latency that sets the poll bound.
//
//  2. AC ITEM 4. Does a forwarded click landing on a real dialog's option row
//     SELECT that option directly? ini-543b could not answer it: its
//     real-Claude leg never reached a live dialog. If the answer is yes it is a
//     SECOND, independent mechanism for the same symptom and files separately
//     with pm -- focus-click vs intentional-click is a product question.
//
// Isolated by construction: a fresh temp CWD, its own claude process, and no
// contact with the live fleet, which has real dialogs pending. Nothing here
// writes to ~/.claude.
//
// Run: INITECH_ZJHG=1 go test ./internal/tui/ -run ZJHGRig -v -timeout 600s

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// zjhgClaudePane starts a real interactive Claude in an isolated temp dir.
//
// --permission-mode default is explicit, not decorative: run 4 of this rig
// spawned Claude with the fleet agent's own inherited environment, which
// allowlists Bash, so the tool call meant to raise a permission prompt was
// simply executed ("Ran 1 shell command") and the dialog leg measured nothing.
// A rig that inherits the operator's permissions cannot measure a permission
// dialog.
func zjhgClaudePane(t *testing.T, args ...string) *Pane {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/README.md", []byte("# zjhg probe\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// --permission-mode default was NOT enough (run 4): the operator's own
	// settings allowlist Bash, so the tool call meant to raise a permission
	// prompt was simply executed ("Ran 1 shell command"). An explicit ask rule
	// passed via --settings outranks an inherited allow rule; without it this
	// rig measures a permission dialog that never opens.
	settings := dir + "/zjhg-settings.json"
	if err := os.WriteFile(settings,
		[]byte(`{"permissions":{"ask":["Bash"],"defaultMode":"default"}}`), 0o644); err != nil {
		t.Fatalf("settings: %v", err)
	}
	p, err := NewPane(PaneConfig{
		Name:    "zjhg",
		Command: append([]string{"claude", "--permission-mode", "default", "--settings", settings}, args...),
		Dir:     dir,
	}, 44, 120)
	if err != nil {
		t.Fatalf("start claude pane: %v", err)
	}
	// REGION IS LOAD-BEARING, and its absence cost this rig two full runs.
	// checkAltScreenTransition (pane.go:593) resizes the emulator to
	// p.region.TerminalSize() on every alt-screen entry/exit. A bare Pane has a
	// zero Region, so TerminalSize() floors to 1x1 -- and Claude enters the
	// alternate screen the moment the trust prompt is answered. The emulator
	// collapsed to a single row and every screen read afterwards returned one
	// character, which reads exactly like a hung or dead child and is neither.
	// Any rig that constructs a Pane directly and expects a full-screen TUI
	// child must set this.
	p.region = Region{W: 120, H: 46} // TerminalSize() subtracts 2 -> 120x44
	p.Start()
	t.Cleanup(p.Close)
	return p
}

// zjhgScreen returns every rendered row, newline-joined.
func zjhgScreen(p *Pane) string {
	cols, rows := p.emu.Width(), p.emu.Height()
	var b strings.Builder
	for r := 0; r < rows; r++ {
		b.WriteString(strings.TrimRight(p.emu.RowText(r, cols), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// zjhgWait polls cond every 25ms and reports how long it took to become true.
func zjhgWait(cond func() bool, limit time.Duration) (time.Duration, bool) {
	start := time.Now()
	for time.Since(start) < limit {
		if cond() {
			return time.Since(start), true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return limit, false
}

// zjhgComposerRow returns the last row carrying a prompt glyph, which is what
// promptHasContent scans. Reported so a false negative can be diagnosed from
// the row itself rather than from the boolean.
func zjhgComposerRow(p *Pane) string {
	cols, rows := p.emu.Width(), p.emu.Height()
	for r := rows - 1; r >= 0; r-- {
		text := p.emu.RowText(r, cols)
		for _, glyph := range []string{"\u276f", "\u203a", ">"} {
			if strings.Contains(text, glyph) {
				return strings.TrimRight(text, " ")
			}
		}
	}
	return "(no prompt glyph on screen)"
}

// zjhgComposerTail returns the text AFTER the last prompt glyph -- exactly the
// substring promptHasContent tests for emptiness. The candidate belt predicate
// is a DELTA on this value, so the rig has to report the value itself.
func zjhgComposerTail(p *Pane) string {
	cols, rows := p.emu.Width(), p.emu.Height()
	for r := rows - 1; r >= 0; r-- {
		text := p.emu.RowText(r, cols)
		for _, glyph := range []string{"\u276f", "\u203a", ">"} {
			if idx := strings.LastIndex(text, glyph); idx >= 0 {
				return strings.TrimSpace(text[idx+len(glyph):])
			}
		}
	}
	return ""
}

// zjhgProbe runs one measurement: sample the composer tail, write a body
// straight to the PTY, sample again, and report every candidate predicate's
// answer for that state. One place, so composer legs and dialog legs are
// measured by identical means and can be compared honestly.
func zjhgProbe(t *testing.T, p *Pane, label, body string, settle time.Duration) (before, after string) {
	t.Helper()
	before = zjhgComposerTail(p)
	beforeScreen := zjhgScreen(p)
	zjhgWriteBodyRaw(p, body)
	time.Sleep(settle)
	after = zjhgComposerTail(p)

	bodyVisible := strings.Contains(zjhgScreen(p), body)
	if len(body) > 40 {
		bodyVisible = strings.Contains(zjhgScreen(p), body[:40])
	}
	t.Logf("%s\n"+
		"    promptHasContent = %v\n"+
		"    tail BEFORE      = %q\n"+
		"    tail AFTER       = %q\n"+
		"    tail CHANGED     = %v\n"+
		"    body visible     = %v\n"+
		"    screen changed   = %v",
		label, promptHasContent(p), before, after, before != after,
		bodyVisible, beforeScreen != zjhgScreen(p))
	return before, after
}

// zjhgWriteBodyRaw writes a bracketed-paste body DIRECTLY to the PTY, with no
// submit key and bypassing the modal guard entirely.
//
// Bypassing the guard is the point, not a shortcut: the failing state under
// measurement is precisely "the guard was blind and the body went out anyway".
// Routing through SendText would defer the body and measure nothing.
func zjhgWriteBodyRaw(p *Pane, body string) {
	_, _ = p.ptmx.Write([]byte("\x1b[200~" + body + "\x1b[201~"))
}

// zjhgClearComposer empties the composer between legs (ctrl+u).
func zjhgClearComposer(p *Pane) {
	_, _ = p.ptmx.Write([]byte{0x15})
	time.Sleep(400 * time.Millisecond)
}


// zjhgTrustVisible reports whether the fresh-workspace trust prompt is on
// screen, by its own literal rather than by the predicate under measurement.
func zjhgTrustVisible(p *Pane) bool {
	return strings.Contains(zjhgScreen(p), "Yes, I trust this folder")
}

func TestZJHGRig_ComposerEchoAndDialogClick(t *testing.T) {
	if os.Getenv("INITECH_ZJHG") != "1" {
		t.Skip("set INITECH_ZJHG=1 to run the real-Claude measurement leg for ini-zjhg")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}

	// PHASE 1 gets its OWN Claude on its own workspace, and is then thrown away.
	// Run 3 measured why: pasting a body into the trust dialog leaves the child
	// in a state the rig could not steer back to a composer, so a single-pane
	// rig spends its dialog probe and loses every composer leg behind it. A
	// contaminating probe belongs on a disposable child.
	zjhgPhase1TrustDialog(t)
	zjhgPhase2ComposerAndPermissionDialog(t)
}

// zjhgPhase1TrustDialog measures the state that is GUARANTEED to exist: a real
// Claude option-picker, open and waiting, on a fresh workspace.
func zjhgPhase1TrustDialog(t *testing.T) {
	p := zjhgClaudePane(t)

	dialogUp := func() bool {
		return isModalPrompt(emulatorBottomText(p.emu, modalScanWholePane))
	}

	// D0: the trust-folder dialog.
	//
	// A fresh CWD makes Claude open "Do you trust the files in this folder?"
	// before anything else. That is a REAL Claude option-picker dialog, free and
	// guaranteed, so it gets probed BEFORE it is answered -- the permission
	// prompt below is the faithful fixture but it is not guaranteed to appear,
	// and ini-543b already lost its entire dialog leg to exactly that.
	//
	// It is also why the first run of this rig measured nothing: the
	// wait-for-composer check looked for the prompt glyph, and the option picker
	// uses that same glyph as its SELECTION CURSOR. The rig believed a dialog
	// was a composer and took both composer measurements against a dialog.
	// The rig detects this dialog by its OWN literal, never by isModalPrompt:
	// the rig must not depend on the predicate it exists to measure. Run 2 of
	// this rig proved why -- see the detection report immediately below.
	trustUp := func() bool {
		return strings.Contains(zjhgScreen(p), "Yes, I trust this folder")
	}
	if _, ok := zjhgWait(trustUp, 60*time.Second); !ok {
		t.Fatalf("no trust dialog appeared; rig cannot proceed.\n%s", zjhgScreen(p))
	}
	t.Logf("D0: real trust dialog open; prompt-glyph row = %q", zjhgComposerRow(p))

	// MEASURED GAP, not a rig detail: does the SHIPPING guard see this dialog?
	// The trust prompt is a real blocking option picker whose footer reads
	// "Enter to confirm · Esc to cancel" -- modalPromptPatterns carries
	// "press enter to confirm", which that text does not contain, and the prompt
	// has no "to navigate" and no "do you want to proceed" either. If this logs
	// false the dialog is undetected on a fresh workspace and a drained message
	// would paste a body and fire Enter straight into the highlighted option.
	t.Logf("D0 DETECTION: paneHasModal sees the trust dialog = %v", dialogUp())
	zjhgProbe(t, p, "D0 BODY INTO OPEN TRUST DIALOG", "zjhg-body-into-trust-dialog", 2*time.Second)
	if !trustUp() {
		t.Errorf("D0: the trust dialog closed when a body was pasted into it -- submission by paste alone.")
	}

	// ── D1 (AC item 4): click a real dialog's option row ───────────────
	//
	// This dialog is a BETTER fixture for the click question than the
	// permission prompt, and not merely a more available one: its second option
	// is "No, exit", so the three possible answers are separately observable on
	// one child.
	//   - click ACTIVATES  -> the child exits / the dialog goes away
	//   - click SELECTS    -> the ❯ cursor moves to the clicked row, and a later
	//                         stray Enter would then answer the WRONG option
	//   - click is INERT   -> nothing moves
	// The middle outcome matters as much as the first: ini-543b proved a drained
	// message ends in a submit key, so a click that only moves the highlight
	// still turns a mis-timed drain into a wrong answer rather than a stray line.
	//
	// Delivery is not in question. ini-543b captured, on this same product path
	// and a real Claude child, the click arriving on the wire as SGR bytes
	// (ESC[<0;11;18M) -- Claude enables mouse reporting, so SendMouse encodes
	// rather than dropping. An inert result here therefore means the dialog
	// ignored a click it received, not that no click was sent.
	exitRow := zjhgRowContaining(p, "No, exit")
	if exitRow < 0 {
		t.Logf("D1: no 'No, exit' row found; AC item 4 UNMEASURED this run.\n%s", zjhgScreen(p))
		return
	}
	cursorBefore := zjhgRowContaining(p, "❯")
	t.Logf("D1: cursor on row %d; clicking the 'No, exit' row %d", cursorBefore, exitRow)

	m := uv.Mouse{X: 4, Y: exitRow, Button: uv.MouseLeft}
	p.ForwardMouse(uv.MouseClickEvent(m))
	p.ForwardMouse(uv.MouseReleaseEvent(m))
	time.Sleep(3 * time.Second)

	cursorAfter := zjhgRowContaining(p, "❯")
	stillUp, alive := trustUp(), p.IsAlive()
	t.Logf("D1 RESULT: dialog still open=%v child alive=%v cursor %d -> %d (clicked row %d)",
		stillUp, alive, cursorBefore, cursorAfter, exitRow)
	switch {
	case !stillUp || !alive:
		t.Logf("D1 FINDING: the click ANSWERED the dialog by itself -- a second, independent " +
			"mechanism for the ini-543b symptom. Files separately with pm.")
	case cursorAfter == exitRow && cursorAfter != cursorBefore:
		t.Logf("D1 FINDING: the click MOVED THE SELECTION onto the clicked row without activating " +
			"it. No submission by click alone, but any later submit key now answers the clicked " +
			"option instead of the default. Files separately with pm.")
	default:
		t.Logf("D1: the click did not move the selection or close the dialog. Clicks are inert " +
			"for this dialog, so click-as-answerer is ruled out as an independent mechanism.")
	}
}

// zjhgRowContaining returns the index of the first row containing needle.
func zjhgRowContaining(p *Pane, needle string) int {
	cols, rows := p.emu.Width(), p.emu.Height()
	for r := 0; r < rows; r++ {
		if strings.Contains(p.emu.RowText(r, cols), needle) {
			return r
		}
	}
	return -1
}

// zjhgPhase2ComposerAndPermissionDialog answers the trust prompt cleanly on a
// FRESH child, measures the composer states, then drives the permission dialog
// that is the bug report's own fixture.
func zjhgPhase2ComposerAndPermissionDialog(t *testing.T) {
	p := zjhgClaudePane(t)
	dialogUp := func() bool {
		return isModalPrompt(emulatorBottomText(p.emu, modalScanWholePane))
	}
	trustUp := func() bool {
		return strings.Contains(zjhgScreen(p), "Yes, I trust this folder")
	}

	if _, ok := zjhgWait(trustUp, 60*time.Second); !ok {
		t.Fatalf("phase 2: no trust dialog appeared.\n%s", zjhgScreen(p))
	}
	// Answer it (option 1, "Yes, I trust this folder") to reach a real composer.
	_, _ = p.ptmx.Write([]byte("\r"))
	if _, ok := zjhgWait(func() bool {
		return !trustUp() && strings.Contains(zjhgScreen(p), "\u276f")
	}, 90*time.Second); !ok {
		t.Fatalf("no composer after answering the trust dialog; rig cannot proceed.\n%s", zjhgScreen(p))
	}
	zjhgClearComposer(p)
	t.Logf("composer up; row = %q", zjhgComposerRow(p))

	// ── M1a: small body into a live composer ───────────────────────────
	zjhgProbe(t, p, "M1a SMALL BODY INTO LIVE COMPOSER", "zjhg-small-body", 2*time.Second)
	if !promptHasContent(p) {
		t.Errorf("M1a: promptHasContent false for a small body in a live composer.\n%s", zjhgScreen(p))
	}
	zjhgClearComposer(p)

	// ── M1b: large body (collapses to a paste reference) ───────────────
	large := strings.TrimSpace(strings.Repeat("zjhg large body line for the paste-collapse probe\n", 40))
	zjhgProbe(t, p, "M1b LARGE BODY INTO LIVE COMPOSER", large, 3*time.Second)
	if !promptHasContent(p) {
		t.Errorf("M1b: promptHasContent false for a large body. A collapsed paste reference does not "+
			"read as composer content, so a belt built on it would drop submits on exactly the big "+
			"messages agents send.\n%s", zjhgScreen(p))
	}
	zjhgClearComposer(p)

	// ── Drive a REAL permission dialog ─────────────────────────────────
	// A Bash tool call needs operator approval under default permissions.
	sendPaneTextLocked(p, "Run the bash command: echo zjhg-probe", true)
	if _, ok := zjhgWait(dialogUp, 180*time.Second); !ok {
		t.Logf("no permission dialog appeared; permission-dialog legs UNMEASURED this run "+
			"(rig fault, not a product finding).\n%s", zjhgScreen(p))
		return
	}
	t.Log("real permission dialog is open")

	// Does tier 1 fire for THIS dialog? The latch raises from exactly this
	// signal, so the answer decides how much of layer 1's coverage is real.
	//
	// POLLED, not sampled once: run 5 read the mailbox the instant the dialog
	// painted and reported notified=false, which would have been recorded as
	// "the permission dialog does not raise tier 1" on the strength of a race
	// between a screen repaint and an OSC byte. Sampling a mailbox at one
	// instant measures the sampling instant.
	notified, msg := false, ""
	_, _ = zjhgWait(func() bool {
		if got, m := p.attn.takeNotify(); got {
			notified, msg = true, m
			return true
		}
		return false
	}, 45*time.Second)
	t.Logf("OSC 777 for this dialog: notified=%v message=%q (allowlisted=%v)",
		notified, msg, raisesAttention(msg))
	if !notified {
		t.Logf("TIER-1 GAP: a stock permission prompt raised no allowlisted OSC 777 within 45s, so the " +
			"ini-zjhg state latch never latches for it. Coverage for this dialog is the screen term " +
			"plus the belt -- which is exactly the layering the belt exists for, but it means layer 1 " +
			"is NOT the primary protection for the bug's own fixture. Report this as measured.")
	}

	dialogScreen := zjhgScreen(p)

	// ── M2: body into an OPEN dialog ───────────────────────────────────
	// THE SHIPPED DISCRIMINATOR IS THE DELTA, so the delta is what is asserted.
	// promptHasContent is logged by zjhgProbe as a measured FALSE POSITIVE:
	// Claude's pickers use ❯ as their selection cursor, so it answers true with
	// an open dialog and no composer anywhere. That measurement is why the belt
	// is a change-detector rather than a content-test, and the assertion below
	// pins the property the belt actually relies on.
	tailBefore, tailAfter := zjhgProbe(t, p, "M2 BODY INTO OPEN PERMISSION DIALOG", "zjhg-body-into-dialog", 2*time.Second)
	if tailBefore != tailAfter {
		t.Errorf("M2: the composer tail CHANGED when a body was written into an open permission "+
			"dialog (%q -> %q). The belt's discriminator does not discriminate on the bug's own "+
			"fixture and the belt must not ship in this shape.", tailBefore, tailAfter)
	}
	if !dialogUp() {
		t.Errorf("M2: the dialog closed when a body was pasted into it. That is a submission by paste alone " +
			"and is a finding in its own right.")
	}

	// ── M3 (AC item 4): click on a real option row ─────────────────────
	optionRow, optionText := -1, ""
	cols, rows := p.emu.Width(), p.emu.Height()
	for r := 0; r < rows; r++ {
		text := p.emu.RowText(r, cols)
		low := strings.ToLower(text)
		if strings.Contains(low, "yes") && !strings.Contains(low, "do you want") {
			optionRow, optionText = r, strings.TrimRight(text, " ")
			break
		}
	}
	if optionRow < 0 {
		t.Logf("M3: no option row located on screen; AC item 4 UNMEASURED this run.\n%s", dialogScreen)
		return
	}
	t.Logf("M3: clicking option row %d = %q", optionRow, optionText)

	before := zjhgScreen(p)
	m := uv.Mouse{X: 4, Y: optionRow, Button: uv.MouseLeft}
	p.ForwardMouse(uv.MouseClickEvent(m))
	p.ForwardMouse(uv.MouseReleaseEvent(m))
	time.Sleep(3 * time.Second)
	after := zjhgScreen(p)

	stillOpen := dialogUp()
	t.Logf("M3 RESULT: dialog still open after clicking its option row = %v", stillOpen)
	if !stillOpen {
		t.Logf("M3 FINDING: the click ANSWERED the dialog by itself. Second independent mechanism; "+
			"files separately with pm.\nBEFORE:\n%s\nAFTER:\n%s", before, after)
	}
	if before == after {
		t.Log("M3: screen unchanged by the click (no visible selection movement)")
	} else {
		t.Logf("M3: screen CHANGED after the click; diff of interest:\n%s", zjhgFirstDiff(before, after))
	}

	fmt.Fprintln(os.Stderr, "zjhg rig complete")
}

// zjhgFirstDiff reports the first few differing rows between two screens.
func zjhgFirstDiff(a, b string) string {
	ar, br := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out []string
	for i := 0; i < len(ar) && i < len(br) && len(out) < 12; i++ {
		if ar[i] != br[i] {
			out = append(out, fmt.Sprintf("row %d:\n  - %q\n  + %q", i, ar[i], br[i]))
		}
	}
	if len(out) == 0 {
		return "(no row-level differences)"
	}
	return strings.Join(out, "\n")
}

// TestPZX0Rig_FocusFirstOnARealPermissionDialog is the ini-pzx0 live leg: the
// end-to-end claim, on a REAL open permission dialog, through the REAL mouse
// entry point.
//
// The unit tests in mouse_focus_test.go prove no bytes leave the TUI for a
// first click, and "no bytes reach the child" does imply "the child cannot
// act". This exists because that implication is exactly the kind of reasoning
// ini-543b punished: the whole bug was discovered by measuring a click that was
// assumed harmless. So the first click is checked against a dialog that is
// genuinely open and genuinely answerable, and then the second click is
// required to actually answer it -- proving the protection is a DELAY, not a
// wall.
//
// Run: INITECH_ZJHG=1 go test ./internal/tui/ -run PZX0Rig -v -timeout 900s
func TestPZX0Rig_FocusFirstOnARealPermissionDialog(t *testing.T) {
	if os.Getenv("INITECH_ZJHG") != "1" {
		t.Skip("set INITECH_ZJHG=1 to run the real-Claude focus-first leg for ini-pzx0")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}

	p := zjhgClaudePane(t)
	dialogUp := func() bool {
		return isModalPrompt(emulatorBottomText(p.emu, modalScanWholePane))
	}

	// Trust prompt -> composer.
	if _, ok := zjhgWait(func() bool { return zjhgTrustVisible(p) }, 60*time.Second); !ok {
		t.Fatalf("no trust dialog appeared.\n%s", zjhgScreen(p))
	}
	_, _ = p.ptmx.Write([]byte("\r"))
	if _, ok := zjhgWait(func() bool {
		return !zjhgTrustVisible(p) && strings.Contains(zjhgScreen(p), "❯")
	}, 90*time.Second); !ok {
		t.Fatalf("no composer after the trust dialog.\n%s", zjhgScreen(p))
	}

	// A real permission prompt, the fixture this bead REQUIRES. The trust
	// dialog above is inert to clicks (measured N=2) and would produce a
	// confident false negative here.
	sendPaneTextLocked(p, "Run the bash command: echo zjhg-probe", true)
	if _, ok := zjhgWait(dialogUp, 180*time.Second); !ok {
		t.Fatalf("no permission dialog; the leg cannot run (rig fault, not a product finding).\n%s",
			zjhgScreen(p))
	}
	t.Log("real permission dialog open")

	// Wrap the live pane in a TUI so clicks travel the product path.
	dummy := &Pane{name: "other", emu: vt.NewSafeEmulator(80, 24), alive: true, visible: true}
	views := []PaneView{p, dummy}
	ls := DefaultLayoutState([]string{p.Name(), "other"})
	tui := &TUI{panes: views, layoutState: ls, lastW: 200, lastH: 60}
	tui.plan = computeLayout(ls, views, 200, 58)
	ls.Focused = agentKey(dummy)
	tui.layoutState = ls

	// Locate the "1. Yes" row and invert forwardMouseEvent's translation:
	// emuY = startRow + (ly - renderOffset), and my = region.Y + 1 + ly.
	clickOptionRow := func() (int, int, bool) {
		emuRow := -1
		cols, rows := p.emu.Width(), p.emu.Height()
		for r := 0; r < rows; r++ {
			if strings.Contains(p.emu.RowText(r, cols), "1. Yes") {
				emuRow = r
				break
			}
		}
		if emuRow < 0 {
			return 0, 0, false
		}
		var region Region
		for _, pr := range tui.plan.Panes {
			if agentKey(pr.Pane) == agentKey(p) {
				region = pr.Region
			}
		}
		startRow, renderOffset := p.contentOffset()
		ly := emuRow - startRow + renderOffset
		return region.X + 4, region.Y + 1 + ly, true
	}

	mx, my, ok := clickOptionRow()
	if !ok {
		t.Fatalf("could not locate the option row.\n%s", zjhgScreen(p))
	}

	// FIRST CLICK on the unfocused pane: focuses only.
	tui.handleMouse(tcell.NewEventMouse(mx, my, tcell.Button1, tcell.ModNone))
	tui.handleMouse(tcell.NewEventMouse(mx, my, tcell.ButtonNone, tcell.ModNone))
	time.Sleep(3 * time.Second)

	if !dialogUp() {
		t.Fatalf("THE FIRST CLICK ANSWERED A REAL PERMISSION DIALOG. focus-first is not holding on "+
			"the live fixture, whatever the unit tests say.\n%s", zjhgScreen(p))
	}
	if strings.Contains(zjhgScreen(p), "zjhg-probe\n") && !dialogUp() {
		t.Error("the tool call ran on the first click")
	}
	if tui.layoutState.Focused != agentKey(p) {
		t.Errorf("the first click did not focus the pane; focused=%q", tui.layoutState.Focused)
	}
	t.Log("first click: dialog still open, pane focused — focus-first holds on a live dialog")

	// SECOND CLICK, now focused: this MUST answer it. A protection that never
	// lets the operator act is a different bug, not a fix.
	mx, my, ok = clickOptionRow()
	if !ok {
		t.Fatalf("option row vanished before the second click.\n%s", zjhgScreen(p))
	}
	tui.handleMouse(tcell.NewEventMouse(mx, my, tcell.Button1, tcell.ModNone))
	tui.handleMouse(tcell.NewEventMouse(mx, my, tcell.ButtonNone, tcell.ModNone))

	if _, ok := zjhgWait(func() bool { return !dialogUp() }, 20*time.Second); !ok {
		t.Errorf("the SECOND click did not answer the dialog. The operator was told 'click again to "+
			"act'; if the second click is inert the rule has cost them the ability to answer at "+
			"all.\n%s", zjhgScreen(p))
	} else {
		t.Log("second click: dialog answered — the rule delays action, it does not prevent it")
	}
}

// TestZJHGRig_StashPathStillSubmits is the ini-4bf2 measurement: does the
// never-submit belt withhold a legitimate PRIMARY submit when the operator has
// half-typed text in the composer and a message arrives?
//
// This is the path zjhg's own measurement did not cover, and it is the
// delivery-regression direction its AC forbade, so it gets measured on real
// Claude rather than argued about. The unit fixture cannot answer it: it has no
// child, so nothing ever renders the body, and it simulates the POST-submit
// restored text at the PRE-send moment.
//
// The sequence under test is the real one: operator types, a message arrives,
// sendPaneTextLocked fires Ctrl+S (stash), writes the body, and must submit.
func TestZJHGRig_StashPathStillSubmits(t *testing.T) {
	if os.Getenv("INITECH_ZJHG") != "1" {
		t.Skip("set INITECH_ZJHG=1 to run the real-Claude stash-path measurement for ini-4bf2")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}

	p := zjhgClaudePane(t)
	if _, ok := zjhgWait(func() bool { return zjhgTrustVisible(p) }, 60*time.Second); !ok {
		t.Fatalf("no trust dialog appeared.\n%s", zjhgScreen(p))
	}
	_, _ = p.ptmx.Write([]byte("\r"))
	if _, ok := zjhgWait(func() bool {
		return !zjhgTrustVisible(p) && strings.Contains(zjhgScreen(p), "❯")
	}, 90*time.Second); !ok {
		t.Fatalf("no composer after the trust dialog.\n%s", zjhgScreen(p))
	}
	zjhgClearComposer(p)

	// THE OPERATOR IS MID-SENTENCE. Typed one character at a time, as a human
	// types -- a bracketed paste would be a different input path and would not
	// reproduce the state the bug report describes.
	for _, ch := range []byte("half written thought") {
		_, _ = p.ptmx.Write([]byte{ch})
		time.Sleep(30 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)
	tailWithOperatorText, _ := composerTail(p)
	t.Logf("composer BEFORE the send (operator's own text): %q", tailWithOperatorText)
	if !strings.Contains(tailWithOperatorText, "half written") {
		t.Fatalf("the operator's typed text never reached the composer; the rig cannot measure "+
			"the stash path.\n%s", zjhgScreen(p))
	}

	// A message arrives on the real send path: stash, body, submit.
	sendPaneTextLocked(p, "zjhg-stash-probe-message", true)

	// The submit is observable by its effect: Claude accepts the message and
	// starts a turn, so the composer no longer holds our body.
	submitted, ok := zjhgWait(func() bool {
		screen := zjhgScreen(p)
		return strings.Contains(screen, "zjhg-stash-probe-message") &&
			!strings.Contains(zjhgComposerRow(p), "zjhg-stash-probe-message")
	}, 30*time.Second)

	t.Logf("STASH PATH: submitted=%v after %v; composer row now %q",
		ok, submitted.Round(time.Millisecond), zjhgComposerRow(p))
	t.Logf("screen after the send:\n%s", zjhgScreen(p))
	if !ok {
		t.Errorf("THE BELT WITHHELD A LEGITIMATE SUBMIT on the stash path: the message reached the " +
			"composer and was never sent. This is the delivery-regression direction ini-zjhg AC " +
			"item 2 forbade, and it is a real product defect rather than a fixture artifact.")
	}
}
