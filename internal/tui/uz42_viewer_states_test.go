package tui

// A viewer must say why it is showing nothing (ini-uz42).
//
// viewerEmptyExplanation had ONE branch for three different states: owned
// agents all hidden, owned agents not yet arrived, and owned agents present
// and unplanned. It returned "" for all three and logged a warning for all
// three. The first two are LEGAL -- the operator hid them himself, or a
// stream is still in flight -- so the common case was a blank window
// explaining nothing and a log warning about correct behaviour.
//
// Reproduced by ini-g8n9 at 694985f from captured hover state: 41 streams, 10
// owned, all 10 hidden, zero planned, hint empty.

import (
	"strings"
	"testing"
)

// uz42Viewer returns a served window-2 that owns the eng group, with its plan
// empty -- the shape every state below starts from.
func uz42Viewer(t *testing.T) (*TUI, []string) {
	t.Helper()
	_, w2, a := placementTUIs(t, "group_window:\n    eng: window-2\n")
	serveViewer(t, w2, a)
	w2.plan = RenderPlan{}
	owned := ownershipKeysFor(w2.paneOwnership, w2.windowID)
	if len(owned) == 0 {
		t.Fatal("fixture owns nothing; every state below would be the unassigned one")
	}
	return w2, owned
}

// TestG8N9_AllHiddenViewerExplainsItsEmptyPlan is ini-g8n9's probe, promoted.
//
// The operator hid every agent assigned to this window. That is a legal state
// he created on purpose, and the window went black and silent about it.
func TestG8N9_AllHiddenViewerExplainsItsEmptyPlan(t *testing.T) {
	w2, owned := uz42Viewer(t)
	for _, k := range owned {
		w2.layoutState.Hidden[k] = true
	}

	got := w2.viewerEmptyExplanation()
	if got == "" {
		t.Fatalf("a viewer whose %d assigned agents are ALL hidden explains itself with "+
			"nothing at all -- the operator gets a black window and cannot tell it from "+
			"a crash", len(owned))
	}
	if want := allHiddenViewerHint(len(owned)); got != want {
		t.Errorf("explanation = %q, want %q", got, want)
	}
	if !strings.Contains(got, "hidden") {
		t.Errorf("the explanation does not say the agents are HIDDEN: %q", got)
	}
}

// TestUZ42_AllHiddenNamesTheLocalRecoveryFirst pins the ordering pm decided:
// the control in THIS window leads, the other window's panel follows.
func TestUZ42_AllHiddenNamesTheLocalRecoveryFirst(t *testing.T) {
	msg := allHiddenViewerHint(10)
	overlay, panel := strings.Index(msg, "overlay"), strings.Index(msg, "Agents panel")
	if overlay < 0 || panel < 0 {
		t.Fatalf("the copy names %d routes, want both the overlay and the Agents panel: %q",
			2-boolToInt(overlay < 0)-boolToInt(panel < 0), msg)
	}
	if overlay > panel {
		t.Errorf("the copy sends the operator to the OTHER window first; the overlay is "+
			"one click away in the window they are already looking at: %q", msg)
	}
}

// TestUZ42_AllHiddenIsSingularForOneAgent: "all 1 agents" is how copy tells an
// operator nobody read it.
func TestUZ42_AllHiddenIsSingularForOneAgent(t *testing.T) {
	one := allHiddenViewerHint(1)
	if !strings.Contains(one, "the 1 agent assigned here is hidden") {
		t.Errorf("one hidden agent reads %q", one)
	}
	if strings.Contains(one, "agents") || strings.Contains(one, "their") {
		t.Errorf("plural leaked into the single-agent copy: %q", one)
	}
	if many := allHiddenViewerHint(10); !strings.Contains(many, "all 10 agents") {
		t.Errorf("ten hidden agents read %q", many)
	}
}

// TestUZ42_AwaitingArrivalsIsNotADefect: owned agents that have not arrived
// are a legal in-flight state, not a bug, and must not be reported as one.
func TestUZ42_AwaitingArrivalsIsNotADefect(t *testing.T) {
	w2, owned := uz42Viewer(t)
	// One owned agent's stream has not arrived; the one that has is hidden.
	missing := owned[0]
	kept := w2.panes[:0]
	for _, p := range w2.panes {
		if agentKey(p) != missing {
			kept = append(kept, p)
		}
	}
	w2.panes = kept
	for _, k := range owned {
		if k != missing {
			w2.layoutState.Hidden[k] = true
		}
	}

	got := w2.viewerEmptyExplanation()
	if want := awaitingArrivalsViewerHint(1, len(owned)); got != want {
		t.Errorf("explanation = %q, want %q", got, want)
	}
	if strings.Contains(got, "bug") {
		t.Errorf("a stream still in flight is reported to the operator as a bug: %q", got)
	}
}

// TestUZ42_PresentAndVisibleButUnplannedNamesItselfABug is the one state that
// IS a defect -- and it must still never be a blank screen (x5ob canon).
func TestUZ42_PresentAndVisibleButUnplannedNamesItselfABug(t *testing.T) {
	w2, owned := uz42Viewer(t) // present, nothing hidden, plan empty

	got := w2.viewerEmptyExplanation()
	if got == "" {
		t.Fatal("the genuine planning defect renders a BLANK window; an operator staring " +
			"at nothing cannot tell a bug from an empty fleet")
	}
	if want := unplannedViewerHint(len(owned)); got != want {
		t.Errorf("explanation = %q, want %q", got, want)
	}
	if !strings.Contains(got, "bug") {
		t.Errorf("the defect state does not name itself a bug: %q", got)
	}
}

// TestUZ42_OnlyTheDefectStateWarns is AC3, and it is what removes ini-4dzh's
// two largest flood sources at the source rather than rate-limiting them.
func TestUZ42_OnlyTheDefectStateWarns(t *testing.T) {
	const warnMsg = "viewer owns agents but is rendering none"

	t.Run("all hidden is quiet", func(t *testing.T) {
		w2, owned := uz42Viewer(t)
		for _, k := range owned {
			w2.layoutState.Hidden[k] = true
		}
		logs := captureLogs(t)
		w2.viewerEmptyExplanation()
		if strings.Contains(logs.String(), warnMsg) {
			t.Error("a fleet the operator hid himself warns on every frame; this state is legal")
		}
	})

	t.Run("awaiting arrivals is quiet", func(t *testing.T) {
		w2, owned := uz42Viewer(t)
		// Drop only the OWNED panes, keeping the rest: emptying t.panes
		// entirely takes the early unserved branch and never reaches the
		// partition -- the first version of this subtest did exactly that
		// and was quiet for the wrong reason.
		isOwned := map[string]bool{}
		for _, k := range owned {
			isOwned[k] = true
		}
		kept := w2.panes[:0]
		for _, p := range w2.panes {
			if !isOwned[agentKey(p)] {
				kept = append(kept, p)
			}
		}
		w2.panes = kept
		if len(w2.panes) == 0 {
			t.Fatal("fixture left no unowned panes; the unserved branch would answer instead")
		}
		if got := w2.viewerEmptyExplanation(); !strings.Contains(got, "waiting for") {
			t.Fatalf("subtest is not in the awaiting-arrivals state (%q); its silence "+
				"would prove nothing", got)
		}
		logs := captureLogs(t)
		w2.viewerEmptyExplanation()
		if strings.Contains(logs.String(), warnMsg) {
			t.Error("streams still arriving warn on every frame; this state is legal")
		}
	})

	// POSITIVE CONTROL: the warning must still fire where it belongs, or the
	// two assertions above would pass against a warning that was simply gone.
	t.Run("the defect still warns", func(t *testing.T) {
		w2, _ := uz42Viewer(t)
		logs := captureLogs(t)
		w2.viewerEmptyExplanation()
		if !strings.Contains(logs.String(), warnMsg) {
			t.Error("the genuine defect no longer warns at all; the quiet assertions above " +
				"are measuring a deleted log line rather than a narrowed one")
		}
	})
}

// TestUZ42_RecoveryInstructionSurvivesTruncation is AC5. The hint is ONE
// centered line cut at the screen's right edge, so an instruction that starts
// past column 80 is invisible on a narrow window -- which is exactly the
// window an operator with a blank screen is likely looking at.
func TestUZ42_RecoveryInstructionSurvivesTruncation(t *testing.T) {
	cases := []struct{ name, msg, act string }{
		{"all hidden, two-digit count", allHiddenViewerHint(10), "click"},
		{"all hidden, single agent", allHiddenViewerHint(1), "click"},
		{"unassigned", emptyViewerHint, "main window"},
		{"the defect", unplannedViewerHint(10), "bug"},
	}
	for _, c := range cases {
		at := strings.Index(c.msg, c.act)
		if at < 0 {
			t.Errorf("%s: %q never says %q", c.name, c.msg, c.act)
			continue
		}
		if runesBefore := len([]rune(c.msg[:at])); runesBefore >= 80 {
			t.Errorf("%s: the part the operator must act on (%q) starts at column %d, past "+
				"the 80-column truncation point, so a narrow window shows only the "+
				"problem and none of the fix: %q", c.name, c.act, runesBefore, c.msg)
		}
	}
}

// TestUZ42_TheOverlayStillListsHiddenOwnedPanes is AC4: the copy sends the
// operator to a control in THIS window, so that control must actually be
// reachable from the blank state.
//
// visiblePanesForWindow is what the overlay draws from. It must keep
// returning every OWNED pane when the plan is empty and all of them are
// hidden -- a mutant gating it on plan pane count reds this.
func TestUZ42_TheOverlayStillListsHiddenOwnedPanes(t *testing.T) {
	w2, owned := uz42Viewer(t)
	for _, k := range owned {
		w2.layoutState.Hidden[k] = true
	}
	if len(w2.plan.Panes) != 0 {
		t.Fatal("fixture has a non-empty plan; the blank-window state is not under test")
	}

	rows := w2.visiblePanesForWindow()
	if len(rows) != len(owned) {
		t.Fatalf("the overlay would list %d of %d owned agents while all are hidden -- the "+
			"hint tells the operator to click a dot that is not drawn, which is a dead "+
			"end pointing at itself", len(rows), len(owned))
	}
	for _, p := range rows {
		if !w2.layoutState.Hidden[agentKey(p)] {
			t.Errorf("overlay row %q is not one of the hidden owned agents", agentKey(p))
		}
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
