package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-jvrs: the ribbon follows the bd assignee deliver writes, never --to,
// and deliver's summary line says what happened to it.

// jvrsDeliver runs deliver as agent on ini-x, which bd holds in status with
// assignee and records impl as its implementer. listData is the TUI's list
// reply ("" answers list with no data). It returns the rig, stderr and the
// error.
func jvrsDeliver(t *testing.T, agent, status, assignee, impl, listData string, args ...string) (*q3xRig, string, error) {
	t.Helper()
	rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"Fix the ribbon", assignee, status}}, listData)
	resetDeliverFlags(t)
	origImpl := bdBeadImplementerFn
	t.Cleanup(func() { bdBeadImplementerFn = origImpl })
	bdBeadImplementerFn = func(string) (string, error) { return impl, nil }
	t.Setenv("INITECH_AGENT", agent)

	var stderr bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"deliver", "ini-x"}, args...))
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	return rig, stderr.String(), err
}

// lastLine is the deliver summary: the last non-empty stderr line.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

func beadTexts(reqs []tui.IPCRequest) []string {
	out := []string{}
	for _, q := range reqs {
		out = append(out, q.Target+"="+q.Text)
	}
	return out
}

// The hand-off: eng4's ribbon loses the bead, qa2 (the --to) gets nothing --
// bd names nobody until QA claims.
func TestDeliver_HandoffClearsTheDelivererAndSetsNothingOnTheRecipient(t *testing.T) {
	list := q3xList(t, tui.PaneInfo{Name: "eng4", Beads: []string{"ini-x"}}, tui.PaneInfo{Name: "qa2", Beads: []string{}})
	rig, stderr, err := jvrsDeliver(t, "eng4", "in_progress", "eng4", "", list, "--to", "qa2")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := beadTexts(rig.of("bead", "")); len(got) != 1 || got[0] != "eng4=" {
		t.Errorf("ribbon writes = %v, want only eng4 cleared (nothing on qa2)", got)
	}
	if s := lastLine(stderr); !strings.HasSuffix(s, "; ribbon cleared") {
		t.Errorf("summary = %q, want it to end \"; ribbon cleared\"", s)
	}
}

// A ribbon holding other beads keeps them: only the delivered bead comes off.
func TestDeliver_HandoffTakesOnlyThatBeadOffAFullerRibbon(t *testing.T) {
	list := q3xList(t, tui.PaneInfo{Name: "eng4", Beads: []string{"ini-a", "ini-x", "ini-b"}})
	rig, stderr, err := jvrsDeliver(t, "eng4", "in_progress", "eng4", "", list, "--to", "qa2")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := beadTexts(rig.of("bead", "")); len(got) != 1 || got[0] != "eng4=ini-a,ini-b" {
		t.Errorf("ribbon writes = %v, want eng4=ini-a,ini-b", got)
	}
	if s := lastLine(stderr); !strings.HasSuffix(s, "; ribbon cleared of ini-x") {
		t.Errorf("summary = %q, want \"; ribbon cleared of ini-x\"", s)
	}
}

// One other bead left: the action is sent as a single id, which the TUI
// would read as a title-less single bead, so it must not be "ini-a," etc.
func TestDeliver_HandoffLeavingOneBeadWritesItAlone(t *testing.T) {
	list := q3xList(t, tui.PaneInfo{Name: "eng4", Beads: []string{"ini-x", "ini-a"}})
	rig, _, err := jvrsDeliver(t, "eng4", "in_progress", "eng4", "", list)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := beadTexts(rig.of("bead", "")); len(got) != 1 || got[0] != "eng4=ini-a" {
		t.Errorf("ribbon writes = %v, want eng4=ini-a", got)
	}
}

// A TUI that cannot report ribbons (no list data): the whole ribbon is
// cleared, as deliver always did.
func TestDeliver_UnreadableRibbonIsClearedWhole(t *testing.T) {
	rig, stderr, err := jvrsDeliver(t, "eng4", "in_progress", "eng4", "", "")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := beadTexts(rig.of("bead", "")); len(got) != 1 || got[0] != "eng4=" {
		t.Errorf("ribbon writes = %v, want eng4 cleared", got)
	}
	if s := lastLine(stderr); !strings.HasSuffix(s, "; ribbon cleared") {
		t.Errorf("summary = %q", s)
	}
}

// Criterion 3: a refused clear is named on the summary line, bd is still
// written and the exit code is unchanged (nil).
func TestDeliver_RefusedClearIsNamedOnTheSummaryLine(t *testing.T) {
	list := q3xList(t, tui.PaneInfo{Name: "eng4", Beads: []string{"ini-x"}})
	rig, stderr, err := jvrsHandoff(t, list, func(r *q3xRig) {
		r.answer("bead", tui.IPCResponse{Error: `pane "eng4" is suspended`})
	})
	if err != nil {
		t.Fatalf("deliver returned %v; a ribbon failure must not change the exit code", err)
	}
	if len(rig.of("bead", "eng4")) != 1 {
		t.Fatalf("no clear was attempted")
	}
	if s := lastLine(stderr); !strings.HasSuffix(s, `; ribbon NOT updated: pane "eng4" is suspended`) {
		t.Errorf("summary = %q, want the refusal named", s)
	}
	if !strings.Contains(stderr, "warning: TUI bead clear") {
		t.Errorf("stderr = %q, want today's warning kept", stderr)
	}
}

// jvrsHandoff runs eng4's hand-off of ini-x --to qa2, letting tweak adjust
// the rig (its answers, or the socket) first.
func jvrsHandoff(t *testing.T, list string, tweak func(*q3xRig)) (*q3xRig, string, error) {
	t.Helper()
	rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"Fix the ribbon", "eng4", "in_progress"}}, list)
	tweak(rig)
	resetDeliverFlags(t)
	t.Setenv("INITECH_AGENT", "eng4")
	var buf bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"deliver", "ini-x", "--to", "qa2"})
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	return rig, buf.String(), err
}

// No TUI at all: bd is written, and the summary says the ribbon was not
// updated. Deliver exits non-zero: with no TUI the hand-off report did not
// reach the recipient either, and a success exit there is the parked-bead
// shape ini-3x0d closed (this cell said "exit code unchanged" before it).
func TestDeliver_NoTUINamesTheRibbonNotUpdated(t *testing.T) {
	_, stderr, err := jvrsHandoff(t, "", func(*q3xRig) {
		t.Setenv("INITECH_SOCKET", "/tmp/initech-jvrs-nobody-listens.sock")
	})
	if err == nil || !strings.Contains(err.Error(), "was not delivered") {
		t.Fatalf("deliver with no TUI: err = %v, want the undelivered hand-off", err)
	}
	// The returned error is printed after the summary, so the summary is no
	// longer the last line: find it by its prefix.
	var s string
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, "delivered ") {
			s = l
		}
	}
	if !strings.Contains(s, "; ribbon NOT updated: ") {
		t.Errorf("summary = %q, want ribbon NOT updated", s)
	}
}

// A QA FAIL returns the bead to its implementer: QA's ribbon loses it and the
// implementer's gains it, id and title, keeping what it already shows.
func TestDeliver_QAFailSetsTheImplementersRibbon(t *testing.T) {
	list := q3xList(t,
		tui.PaneInfo{Name: "qa2", Beads: []string{"ini-x"}},
		tui.PaneInfo{Name: "eng4", Beads: []string{}})
	rig, stderr, err := jvrsDeliver(t, "qa2", "in_qa", "qa2", "eng4", list, "--verdict", "FAIL", "--reason", "red", "--to", "eng4")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	got := beadTexts(rig.of("bead", ""))
	want := []string{"qa2=", "eng4=ini-x\tFix the ribbon"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("ribbon writes = %q, want %q (clear QA, then set the implementer)", got, want)
	}
	if s := lastLine(stderr); !strings.HasSuffix(s, "; ribbon cleared, ribbon set on eng4") {
		t.Errorf("summary = %q", s)
	}
}

// The implementer's ribbon keeps its other beads when the FAIL adds this one.
func TestDeliver_QAFailAddsToAnImplementerRibbonWithOtherBeads(t *testing.T) {
	list := q3xList(t,
		tui.PaneInfo{Name: "qa2", Beads: []string{"ini-x"}},
		tui.PaneInfo{Name: "eng4", Beads: []string{"ini-a"}})
	rig, _, err := jvrsDeliver(t, "qa2", "in_qa", "qa2", "eng4", list, "--verdict", "FAIL", "--reason", "red", "--to", "eng4")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := beadTexts(rig.of("bead", "eng4")); len(got) != 1 || got[0] != "eng4=ini-a,ini-x" {
		t.Errorf("implementer ribbon writes = %q, want eng4=ini-a,ini-x", got)
	}
}

// An implementer with no pane in this fleet: nothing is set, and the summary
// says so.
func TestDeliver_QAFailToAnImplementerWithNoPaneSetsNothing(t *testing.T) {
	list := q3xList(t, tui.PaneInfo{Name: "qa2", Beads: []string{"ini-x"}})
	rig, stderr, err := jvrsDeliver(t, "qa2", "in_qa", "qa2", "eng9", list, "--verdict", "FAIL", "--reason", "red")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := rig.of("bead", "eng9"); len(got) != 0 {
		t.Errorf("set a ribbon on a pane that does not exist: %+v", got)
	}
	if s := lastLine(stderr); !strings.HasSuffix(s, "; ribbon cleared, ribbon NOT updated on eng9: no pane eng9 in this initech") {
		t.Errorf("summary = %q", s)
	}
}

// A PASS sets no ribbon on anyone else; the QA's own comes off because the
// bead leaves the statuses a holder is shown for (qa_passed).
func TestDeliver_QAPassSetsNoRibbonOnAnyoneElse(t *testing.T) {
	list := q3xList(t,
		tui.PaneInfo{Name: "qa2", Beads: []string{"ini-x"}},
		tui.PaneInfo{Name: "eng4", Beads: []string{}})
	rig, _, err := jvrsDeliver(t, "qa2", "in_qa", "qa2", "eng4", list, "--verdict", "PASS", "--to", "eng4")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := beadTexts(rig.of("bead", "")); len(got) != 1 || got[0] != "qa2=" {
		t.Errorf("ribbon writes = %v, want only qa2 cleared", got)
	}
}

// A deliver that leaves the deliverer holding the bead in a shown status
// leaves its ribbon alone: an implementer --fail from in_qa walks back to
// ready_for_qa with the assignee kept.
func TestDeliver_DelivererStillHoldingTheBeadKeepsItsRibbon(t *testing.T) {
	list := q3xList(t, tui.PaneInfo{Name: "eng4", Beads: []string{"ini-x"}})
	rig, stderr, err := jvrsDeliver(t, "eng4", "in_qa", "eng4", "", list, "--fail", "--reason", "x")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := rig.of("bead", ""); len(got) != 0 {
		t.Errorf("ribbon writes = %+v, want none: bd still has eng4 holding ini-x", got)
	}
	if s := lastLine(stderr); !strings.HasSuffix(s, "; ribbon unchanged") {
		t.Errorf("summary = %q", s)
	}
}

// The report deliver types into the recipient is byte-for-byte what it was
// before this change (strings captured at main 8f90b3d).
func TestDeliver_ReportTextUnchangedByTheRibbonWork(t *testing.T) {
	for _, tc := range []struct {
		agent, status, assignee, impl string
		args                          []string
		to, want                      string
	}{
		{"eng4", "in_progress", "eng4", "", []string{"--to", "qa2"}, "qa2", "[from eng4] ini-x: Fix the ribbon ready for QA"},
		{"eng4", "in_progress", "eng4", "", []string{"--to", "qa2", "-m", "DONE: it works"}, "qa2", "[from eng4] ini-x: Fix the ribbon ready for QA. DONE: it works"},
		{"qa2", "in_qa", "qa2", "eng4", []string{"--verdict", "PASS", "--to", "eng4"}, "eng4", "[from qa2] ini-x: PASS: Fix the ribbon"},
		{"qa2", "in_qa", "qa2", "eng4", []string{"--verdict", "FAIL", "--reason", "red", "--to", "eng4"}, "eng4", "[from qa2] ini-x: FAIL: Fix the ribbon — red"},
	} {
		rig, _, err := jvrsDeliver(t, tc.agent, tc.status, tc.assignee, tc.impl, "", tc.args...)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		sends := rig.of("send", tc.to)
		if len(sends) != 1 || sends[0].Text != tc.want {
			t.Errorf("%v: report = %+v, want exactly %q", tc.args, sends, tc.want)
		}
	}
}

// A ribbon that does not show the delivered bead is left alone: deliver only
// takes off what it moved, never another bead the agent holds.
func TestDeliver_RibbonWithoutTheBeadIsLeftAlone(t *testing.T) {
	list := q3xList(t, tui.PaneInfo{Name: "eng4", Beads: []string{"ini-a"}})
	rig, stderr, err := jvrsDeliver(t, "eng4", "in_progress", "eng4", "", list, "--to", "qa2")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := rig.of("bead", ""); len(got) != 0 {
		t.Errorf("ribbon writes = %+v, want none: ini-x was not on eng4's ribbon", got)
	}
	if s := lastLine(stderr); !strings.HasSuffix(s, "; ribbon unchanged") {
		t.Errorf("summary = %q", s)
	}
}
