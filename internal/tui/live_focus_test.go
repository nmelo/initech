package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-92wm: the live focus split, Option+Shift+F. One cell per spec rule.

// liveFocusTUI builds a four-pane fleet: a, b and c working, d idle. StateRunning
// is ActivityState's zero value, so only the idle pane needs setting.
//
// d is idle WITH A BEAD, deliberately: that scores 30 on live's conviction
// rule, above its keep threshold, so live mode would show d while "working"
// does not. Without the bead both rules agree on every pane and a test here
// cannot tell which rule chose the right grid — a mutant that dropped the
// working predicate survived exactly that way.
func liveFocusTUI(t *testing.T) *TUI {
	t.Helper()
	a, b, c, d := testPane("a"), testPane("b"), testPane("c"), testPane("d")
	d.activity = StateIdle
	d.beadIDs = []string{"ini-waiting"}
	tui := newTestTUI(a, b, c, d)
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 2
	tui.layoutState.Focused = "a"
	return tui
}

func setActivity(tui *TUI, name string, st ActivityState) {
	for _, p := range tui.panes {
		if p.Name() == name {
			p.(*Pane).activity = st
		}
	}
}

func altShiftF(tui *TUI) {
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'F', tcell.ModAlt|tcell.ModShift))
}

// planNames is the render plan's pane order: held pane first, then the right.
func liveFocusPlan(tui *TUI) []string {
	var out []string
	for _, pr := range tui.plan.Panes {
		out = append(out, pr.Pane.Name())
	}
	return out
}

func liveFocusJoined(s []string) string { return strings.Join(s, ",") }

// Rule 1: from a static layout the chord enters, holding the focused pane
// left with the WORKING others on the right; a second press restores the
// layout exactly.
func TestLiveFocus_FromStaticEntersAndRestores(t *testing.T) {
	tui := liveFocusTUI(t)
	altShiftF(tui)
	if tui.layoutState.Mode != Layout2Col || tui.liveFocus == nil {
		t.Fatalf("Option+Shift+F did not enter: mode=%v liveFocus=%v", tui.layoutState.Mode, tui.liveFocus != nil)
	}
	if got := liveFocusJoined(liveFocusPlan(tui)); got != "a,b,c" {
		t.Errorf("plan = %s, want held a then working b,c (idle d absent)", got)
	}
	altShiftF(tui)
	if tui.liveFocus != nil || tui.layoutState.RightSetActive {
		t.Error("second press did not leave the mode")
	}
	if tui.layoutState.Mode != LayoutGrid || tui.layoutState.GridCols != 2 || tui.layoutState.GridRows != 2 {
		t.Errorf("restored mode=%v grid=%dx%d, want grid 2x2", tui.layoutState.Mode, tui.layoutState.GridCols, tui.layoutState.GridRows)
	}
	if got := len(tui.plan.Panes); got != 4 {
		t.Errorf("restored grid draws %d panes, want all 4", got)
	}
}

// Rule 2: from a Focus split the right grid goes live, the left pane stays,
// and the second press restores the layout from BEFORE Option+f.
func TestLiveFocus_FromFocusSplitRestoresWhatPrecededOptionF(t *testing.T) {
	tui := liveFocusTUI(t)
	tui.toggleFocusSplit()
	tui.layoutState.Focused = "c"
	tui.applyLayout()
	altShiftF(tui)
	if got := liveFocusPlan(tui); len(got) == 0 || got[0] != "c" {
		t.Fatalf("held pane = %v, want c kept on the left", got)
	}
	if tui.focusSplitPrev != nil {
		t.Error("the Focus split snapshot was not adopted; a later Option+f would restore the wrong thing")
	}
	altShiftF(tui)
	if tui.layoutState.Mode != LayoutGrid || tui.layoutState.Focused != "a" {
		t.Errorf("restored mode=%v focused=%q, want the grid and focus from before Option+f", tui.layoutState.Mode, tui.layoutState.Focused)
	}
}

// Rule 3: from Live the focused pane goes left; exit restores Live with the
// same engine, which stayed dormant rather than being replaced.
func TestLiveFocus_FromLiveRestoresLiveWithItsEngine(t *testing.T) {
	tui := liveFocusTUI(t)
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.LiveAuto = true
	tui.liveEngine = NewLiveEngine(0, nil, nil)
	engine := tui.liveEngine
	altShiftF(tui)
	if tui.layoutState.Mode != Layout2Col {
		t.Fatalf("mode = %v, want the split", tui.layoutState.Mode)
	}
	altShiftF(tui)
	if tui.layoutState.Mode != LayoutLive || !tui.layoutState.LiveAuto {
		t.Errorf("restored mode=%v liveAuto=%v, want live auto", tui.layoutState.Mode, tui.layoutState.LiveAuto)
	}
	if tui.liveEngine != engine {
		t.Error("the live engine was replaced; live should resume as it was")
	}
}

// Rule 4: Option+f inside the mode drops to the static Focus split with the
// right grid frozen; the next Option+f restores the original layout.
func TestLiveFocus_OptionFFreezesThenRestoresTheOriginal(t *testing.T) {
	tui := liveFocusTUI(t)
	altShiftF(tui)
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt))
	if tui.liveFocus != nil {
		t.Fatal("Option+f did not leave the live mode")
	}
	if tui.layoutState.Mode != Layout2Col {
		t.Fatalf("mode = %v, want the static Focus split", tui.layoutState.Mode)
	}
	// Frozen: d starts working, and the right grid does not change.
	setActivity(tui, "d", StateRunning)
	tui.applyLayout()
	if got := liveFocusJoined(liveFocusPlan(tui)); got != "a,b,c" {
		t.Errorf("frozen right grid changed: plan = %s, want a,b,c", got)
	}
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt))
	if tui.layoutState.Mode != LayoutGrid || tui.layoutState.RightSetActive {
		t.Errorf("next Option+f: mode=%v rightSet=%v, want the original grid with no right set", tui.layoutState.Mode, tui.layoutState.RightSetActive)
	}
}

// Rule 4: any Option+N or Shift+Option+N leaves the mode.
func TestLiveFocus_PresetsLeaveTheMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  *tcell.EventKey
	}{
		{"Option+2", tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModAlt)},
		{"Shift+Option+2", tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModAlt|tcell.ModShift)},
		// A slot bound to "main" lands on Layout2Col — the same mode the live
		// focus split uses — so only an explicit clear leaves the mode here;
		// the mode-changed safety net in tickLiveFocus cannot see it.
		{"Option+2 bound to main", tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModAlt)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tui := liveFocusTUI(t)
			tui.layoutPresets = defaultLayoutPresets()
			if strings.HasSuffix(tc.name, "main") {
				tui.layoutPresets[1] = LayoutPreset{Kind: presetMain}
			}
			altShiftF(tui)
			tui.handleKey(tc.key)
			if tui.liveFocus != nil || tui.layoutState.RightSetActive {
				t.Errorf("%s did not leave the live focus split", tc.name)
			}
		})
	}
}

// Rule 5: Option+arrows re-hold a different pane and the right grid re-fills
// around it; the previously held pane, still working, is not stranded.
func TestLiveFocus_OptionArrowReHoldsAndRefills(t *testing.T) {
	tui := liveFocusTUI(t)
	altShiftF(tui)
	tui.handleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt))
	held := tui.layoutState.Focused
	if held == "a" {
		t.Fatal("Option+Right did not move the held pane")
	}
	got := liveFocusPlan(tui)
	if got[0] != held {
		t.Errorf("plan = %v, want %q held on the left", got, held)
	}
	for _, n := range got[1:] {
		if n == held {
			t.Errorf("the held pane %q is also on the right: %v", held, got)
		}
	}
	// a is still working: it rejoins the right on the next tick.
	tui.tickLiveFocus(time.Now())
	if !strings.Contains(liveFocusJoined(tui.layoutState.RightSet), "a") {
		t.Errorf("right set %v does not hold the previously held, still-working a", tui.layoutState.RightSet)
	}
}

// Rule 6: membership is exactly the running, non-hidden others, under live's
// hold — an agent that stops stays until its hold expires, then leaves; one
// that starts appears.
func TestLiveFocus_IdleLeavesAfterTheHoldAndNotBefore(t *testing.T) {
	tui := liveFocusTUI(t)
	altShiftF(tui)
	t0 := time.Now()
	setActivity(tui, "b", StateIdle)
	tui.tickLiveFocus(t0.Add(time.Second))
	if !strings.Contains(liveFocusJoined(tui.layoutState.RightSet), "b") {
		t.Fatalf("b left before its hold expired: %v", tui.layoutState.RightSet)
	}
	tui.tickLiveFocus(t0.Add(liveHoldDuration + time.Second))
	if strings.Contains(liveFocusJoined(tui.layoutState.RightSet), "b") {
		t.Errorf("b is still on the right after its hold: %v", tui.layoutState.RightSet)
	}
	setActivity(tui, "d", StateRunning)
	tui.tickLiveFocus(t0.Add(liveHoldDuration + 2*time.Second))
	if !strings.Contains(liveFocusJoined(tui.layoutState.RightSet), "d") {
		t.Errorf("d started working and did not appear: %v", tui.layoutState.RightSet)
	}
}

// Rule 6: the held pane shows regardless of its own state.
func TestLiveFocus_HeldPaneShowsWhileIdle(t *testing.T) {
	tui := liveFocusTUI(t)
	tui.layoutState.Focused = "d" // the idle one
	altShiftF(tui)
	if got := liveFocusPlan(tui); len(got) == 0 || got[0] != "d" {
		t.Errorf("plan = %v, want the idle held pane d on the left", got)
	}
}

// Rule 7: a hidden pane leaves at once, without waiting out the hold.
func TestLiveFocus_HiddenLeavesAtOnce(t *testing.T) {
	tui := liveFocusTUI(t)
	altShiftF(tui)
	if tui.layoutState.Hidden == nil {
		tui.layoutState.Hidden = map[string]bool{}
	}
	tui.layoutState.Hidden["b"] = true
	tui.tickLiveFocus(time.Now()) // well inside b's hold
	if strings.Contains(liveFocusJoined(tui.layoutState.RightSet), "b") {
		t.Errorf("hidden b is still on the right: %v", tui.layoutState.RightSet)
	}
}

// Rule 8: with nobody else working the left column keeps its 40% and the
// right region says so — never blank, and the held pane does not jump to
// full width.
func TestLiveFocus_NobodyWorkingShowsTheLine(t *testing.T) {
	tui, s := newTestTUIWithScreen("a", "b", "c")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.Focused = "a"
	setActivity(tui, "b", StateIdle)
	setActivity(tui, "c", StateIdle)
	altShiftF(tui)
	if n := len(tui.plan.Panes); n != 1 {
		t.Fatalf("plan holds %d panes, want the held pane alone", n)
	}
	w, _ := s.Size()
	if r := tui.plan.Panes[0].Region; r.W >= w/2 {
		t.Errorf("held pane is %d of %d columns wide; it should keep the 40%% column", r.W, w)
	}
	tui.render()
	if got := simScreenText(s); !strings.Contains(got, liveFocusNoneWorking) {
		t.Errorf("empty right region does not say %q", liveFocusNoneWorking)
	}
}

// AC 5: the help overlay carries the chord, beside Option+f.
func TestLiveFocus_HelpLineSitsBesideOptionF(t *testing.T) {
	lines := getHelpLines()
	for i, l := range lines {
		if strings.Contains(l, "+f") && strings.Contains(l, "Focus split: focused pane left") {
			if i+1 >= len(lines) || !strings.Contains(lines[i+1], "Focus split with a live right grid: working agents only (toggle)") {
				t.Errorf("line after Option+f = %q, want the live focus split chord", lines[i+1])
			}
			if !strings.HasPrefix(strings.TrimSpace(lines[i+1]), "Shift+") {
				t.Errorf("chord line %q does not start with Shift+", lines[i+1])
			}
			return
		}
	}
	t.Fatal("no Option+f line in the help overlay")
}

// AC 6: nothing this mode does reaches layout.yaml — not its own entry and
// exit, and not a save some OTHER action triggers while it is on.
func TestLiveFocus_NeverWritesItsModeToLayoutYAML(t *testing.T) {
	tui := liveFocusTUI(t)
	tui.projectRoot = t.TempDir()
	// Settle first: the first applyLayout fills default groups, which would
	// otherwise show up as a diff that has nothing to do with this mode.
	tui.applyLayout()
	tui.saveLayoutIfConfigured()
	path := layoutPathFor(t, tui)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	altShiftF(tui)
	tui.saveLayoutIfConfigured() // as an unrelated action would
	mid, _ := os.ReadFile(path)
	if string(mid) != string(before) {
		t.Errorf("layout.yaml changed while the mode was on:\nbefore:\n%s\nafter:\n%s", before, mid)
	}
	altShiftF(tui)
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Errorf("layout.yaml changed across enter/exit:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// The engine change is opt-in: with Eligible unset, TickAuto decides exactly
// as before (score threshold), so live mode is unaffected. Two panes where the
// two rules disagree, so the test can tell which rule decided:
//   - justStarted is running, but its run began this instant and it has no
//     other signal: score 0, so the score rule rejects it and "working"
//     admits it.
//   - beadIdle has a bead (score 30) but is idle: the score rule admits it
//     and "working" rejects it.
func TestLiveEngine_EligibleUnsetKeepsTheScoreThreshold(t *testing.T) {
	now := time.Now()
	justStarted := testPane("justStarted")
	justStarted.activeRunStart = now
	beadIdle := testPane("beadIdle")
	beadIdle.activity = StateIdle
	beadIdle.beadIDs = []string{"ini-x"}
	panes := []PaneView{justStarted, beadIdle}

	plain := NewLiveEngine(0, nil, nil)
	plain.TickAuto(panes, now)
	if got := liveFocusJoined(plain.TickAuto(panes, now)); got != "beadIdle" {
		t.Errorf("unset Eligible chose %q, want the score rule's beadIdle", got)
	}
	gated := NewLiveEngine(0, nil, nil)
	gated.Eligible = paneIsWorking
	gated.TickAuto(panes, now)
	if got := liveFocusJoined(gated.TickAuto(panes, now)); got != "justStarted" {
		t.Errorf("Eligible=working chose %q, want justStarted", got)
	}
}

func layoutPathFor(t *testing.T, tui *TUI) string {
	t.Helper()
	var found string
	_ = filepath.Walk(tui.projectRoot, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".yaml") && strings.Contains(filepath.Base(p), "layout") {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatal("no layout file written by the baseline save")
	}
	return found
}
