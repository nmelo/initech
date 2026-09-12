package tui

// The empty-viewer defect warning fires on TRANSITIONS, not frames (ini-4dzh).
//
// ini-uz42 narrowed the warning to the one state that is actually a defect
// (owned, present, unhidden, unplanned) -- but that state still warned on
// every render frame while it persisted, which is how the operator's hover
// logs accumulated 148,513 identical lines. A log line that repeats 30 times a
// second is not a diagnostic; it is noise that buries the line before it.

import (
	"strings"
	"testing"
)

const viewerDefectWarn = "viewer owns agents but is rendering none"

// defectViewer is a served window-2 in state 5: owns eng1/eng2, both present,
// nothing hidden, plan empty.
func defectViewer(t *testing.T) *TUI {
	t.Helper()
	w2, _ := uz42Viewer(t)
	// Check the state through the PURE classifier, never the wrapper: the
	// wrapper is the thing under test, and calling it here would record the
	// first warning BEFORE the cell starts capturing -- every "30 frames"
	// count below would then be a continuation, not an entry. The first
	// version of this helper did exactly that.
	if got, defect := w2.classifyViewerEmpty(); defect == nil || !strings.Contains(got, "bug") {
		t.Fatalf("fixture is not in the defect state: %q", got)
	}
	return w2
}

func warnCount(logs interface{ String() string }) int {
	return strings.Count(logs.String(), viewerDefectWarn)
}

// TestViewerWarn_UnchangedDefectWarnsOnce is AC2: thirty frames of the same
// defect produce one line, not thirty.
func TestViewerWarn_UnchangedDefectWarnsOnce(t *testing.T) {
	w2 := defectViewer(t)
	logs := captureLogs(t)
	for i := 0; i < 30; i++ {
		w2.viewerEmptyExplanation()
	}
	if got := warnCount(logs); got != 1 {
		t.Fatalf("an UNCHANGED defect state warned %d times in 30 frames; want exactly 1. "+
			"This is the 148,513-line flood: a diagnostic repeated per frame buries the "+
			"line that would have explained it", got)
	}
}

// TestViewerWarn_CountChangeWarnsAgain: the state changing IS the meaningful
// transition. A second owned agent becoming visible is a different defect
// than the first, and deserves its own line.
func TestViewerWarn_CountChangeWarnsAgain(t *testing.T) {
	w2 := defectViewer(t)
	logs := captureLogs(t)
	for i := 0; i < 30; i++ {
		w2.viewerEmptyExplanation()
	}
	// Hide one owned agent: visible count changes, still the defect state.
	owned := ownershipKeysFor(w2.paneOwnership, w2.windowID)
	w2.layoutState.Hidden[owned[0]] = true
	if got, defect := w2.classifyViewerEmpty(); defect == nil || !strings.Contains(got, "bug") {
		t.Fatalf("after hiding one, fixture left the defect state: %q", got)
	}
	for i := 0; i < 30; i++ {
		w2.viewerEmptyExplanation()
	}
	if got := warnCount(logs); got != 2 {
		t.Fatalf("the counts changed once across 60 frames and the log has %d warning(s); "+
			"want exactly 2 -- one per distinct state", got)
	}
}

// TestViewerWarn_ReentryAfterRecoveryWarnsAgain: leaving the defect state and
// coming back is a new incident, not a continuation of the old one.
func TestViewerWarn_ReentryAfterRecoveryWarnsAgain(t *testing.T) {
	w2 := defectViewer(t)
	logs := captureLogs(t)
	w2.viewerEmptyExplanation()

	// Recovered: the plan is non-empty, no hint, no warning.
	w2.plan.Panes = append(w2.plan.Panes, PaneRender{})
	if got := w2.viewerEmptyExplanation(); got != "" {
		t.Fatalf("a non-empty plan still produced a hint: %q", got)
	}

	// Back into the same defect.
	w2.plan = RenderPlan{}
	for i := 0; i < 10; i++ {
		w2.viewerEmptyExplanation()
	}
	if got := warnCount(logs); got != 2 {
		t.Fatalf("defect -> recovered -> same defect logged %d warning(s); want 2. A "+
			"re-entry that stays silent hides the second incident behind the first", got)
	}
}

// TestViewerWarn_LegalStatesStayQuiet is AC2 across the states uz42 made
// legal, held for 30 frames each, with the defect as the positive control.
func TestViewerWarn_LegalStatesStayQuiet(t *testing.T) {
	t.Run("all hidden, 30 frames", func(t *testing.T) {
		w2, owned := uz42Viewer(t)
		for _, k := range owned {
			w2.layoutState.Hidden[k] = true
		}
		logs := captureLogs(t)
		for i := 0; i < 30; i++ {
			w2.viewerEmptyExplanation()
		}
		if got := warnCount(logs); got != 0 {
			t.Errorf("a fleet the operator hid himself warned %d times", got)
		}
	})
	t.Run("awaiting arrivals, 30 frames", func(t *testing.T) {
		w2, owned := uz42Viewer(t)
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
		if got := w2.viewerEmptyExplanation(); !strings.Contains(got, "waiting for") {
			t.Fatalf("not in the awaiting state: %q", got)
		}
		logs := captureLogs(t)
		for i := 0; i < 30; i++ {
			w2.viewerEmptyExplanation()
		}
		if got := warnCount(logs); got != 0 {
			t.Errorf("streams still arriving warned %d times", got)
		}
	})
	t.Run("the defect still warns (control)", func(t *testing.T) {
		w2 := defectViewer(t)
		logs := captureLogs(t)
		w2.viewerEmptyExplanation()
		if warnCount(logs) == 0 {
			t.Fatal("the defect no longer warns at all; the quiet assertions above measure " +
				"a deleted line, not a narrowed one")
		}
	})
}

// TestViewerWarn_CarriesTheCounts is AC3: one line must be enough to tell the
// four sets apart without reading the implementation.
func TestViewerWarn_CarriesTheCounts(t *testing.T) {
	w2 := defectViewer(t)
	logs := captureLogs(t)
	w2.viewerEmptyExplanation()
	line := logs.String()
	for _, field := range []string{"owned=", "panes_present=", "hidden=", "absent=", "visible=", "plan_panes="} {
		if !strings.Contains(line, field) {
			t.Errorf("the defect warning lacks %s; an operator reading one line cannot tell "+
				"which of owned/present/hidden/visible/planned is off\n%s", field, line)
		}
	}
}
