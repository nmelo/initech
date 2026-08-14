package tui

// window_display_test.go — ini-9isx: the window-alias prefix drop and per-window
// display scoping, against the amended parity invariant in docs/spec.md
// (parity of FACTS + disclosed display scoping + attention never scoped).
//
// The two ACs with teeth, and why the tests below are shaped the way they are:
//
//   - AC11 demands that a mutation of the WINDOW-prefix strip must NOT be
//     killable by the cross-machine fixture alone. If it were, one code path
//     would be stripping both prefix families, and the "fix" would silently
//     merge workbench:intern onto a local intern. So the fixture holds both
//     forms and asserts them SEPARATELY: a shared path shows up as both
//     assertions moving together.
//   - AC7 says attention is never scoped. The scoping tests and the attention
//     test share one fixture on purpose — the same window, the same scope, the
//     opposite expectation — so the two rules are checked against each other
//     rather than in separate worlds where both can quietly be true.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ── AC1/AC2/AC11: the two prefix families ───────────────────────────

// waitingRemotePane is a RemotePane that also reports a wait, so the attention
// list can be exercised through the same PaneView the viewer really holds.
type waitingRemotePane struct {
	*RemotePane
	since   time.Time
	preview string
}

func (w *waitingRemotePane) WaitingInput() (bool, time.Time, string) {
	return !w.since.IsZero(), w.since, w.preview
}

// TestPaneDisplayName_DropsWindowAliasKeepsMachineHost is the AC2 fixture: BOTH
// prefix families in one pane set, asserted independently.
func TestPaneDisplayName_DropsWindowAliasKeepsMachineHost(t *testing.T) {
	cases := []struct {
		name string
		host string
		want string
	}{
		// The window-alias family is irregular by history and every form of it
		// is presentation, so every form drops.
		{"eng1", WindowOnePeerName, "eng1"},
		{"eng1", "window-2", "eng1"},
		{"eng1", "window3", "eng1"},
		{"eng1", "", "eng1"},
		// A real machine is identity and survives -- dropping it would merge
		// two different agents onto one row.
		{"intern", "workbench", "workbench:intern"},
		{"eng1", "spark1", "spark1:eng1"},
		// A host that merely starts with "window" is not the alias family.
		{"eng1", "windowbench", "windowbench:eng1"},
	}
	for _, c := range cases {
		got := paneDisplayName(&RemotePane{name: c.name, host: c.host})
		if got != c.want {
			t.Errorf("paneDisplayName(host=%q name=%q) = %q, want %q", c.host, c.name, got, c.want)
		}
	}
}

// TestPaneIsRemoteMachine_WindowAliasIsNotRemote pins the [R] badge's meaning.
// A window-1 alias was badged as a remote machine, which is the same conflation
// as the prefix itself: the operator was told an agent lives on another machine
// because it lives in another window.
func TestPaneIsRemoteMachine_WindowAliasIsNotRemote(t *testing.T) {
	if paneIsRemoteMachine(&RemotePane{name: "eng1", host: WindowOnePeerName}) {
		t.Error("a window-1 alias is badged [R] as a remote machine; it is the same fleet on the same host")
	}
	if !paneIsRemoteMachine(&RemotePane{name: "intern", host: "workbench"}) {
		t.Error("a genuine cross-machine agent lost its [R] badge; the badge is how the operator " +
			"tells a remote agent from a local one")
	}
}

// TestWindowAliasFamily_OneSpellingTwoAnchors guards the single-definition rule
// (ini-yc03): the key matcher and the host matcher are built from the SAME
// pattern, so they cannot drift the way ini-xq4r's two generators did.
func TestWindowAliasFamily_OneSpellingTwoAnchors(t *testing.T) {
	for _, host := range []string{"window1", "window-2", "window3"} {
		if !isWindowAliasHost(host) {
			t.Errorf("host %q is not recognised as a window alias", host)
		}
		if !windowAliasKeyRe.MatchString(host + ":eng1") {
			t.Errorf("key %q:eng1 is not recognised as window-aliased, but the host form is -- "+
				"the two matchers have drifted apart", host)
		}
	}
	for _, host := range []string{"workbench", "spark1", "windowbench"} {
		if isWindowAliasHost(host) {
			t.Errorf("cross-machine host %q matched the window-alias family", host)
		}
		if windowAliasKeyRe.MatchString(host + ":eng1") {
			t.Errorf("cross-machine key %q:eng1 matched the window-alias family", host)
		}
	}
}

// ── AC4/AC6/AC9: scoping and its disclosure ─────────────────────────

// TestScopeDisclosure_CountsAndNamesTheOtherWindow is AC6's substance: the
// count of what is hidden and where it is.
func TestScopeDisclosure_CountsAndNamesTheOtherWindow(t *testing.T) {
	_, w2, _ := placementTUIs(t, "group_window:\n    eng: window-2\n")

	hidden, where := w2.scopeDisclosure()
	if hidden != 6 {
		t.Errorf("window 2 hides %d agents, want 6 (8 total minus its own 2)", hidden)
	}
	if where != "on window 1" {
		t.Errorf("disclosure says %q; the operator needs to know WHERE the hidden agents are, "+
			"and they are all on window 1", where)
	}

	note := w2.agentsScopeNote()
	if !strings.Contains(note, "6") {
		t.Errorf("the modal's disclosure %q does not carry the hidden COUNT; a scoped surface that "+
			"does not say what it hides fails the amended parity invariant by definition", note)
	}
	if !strings.Contains(note, "a shows all") {
		t.Errorf("the modal's disclosure %q does not NAME the expand key; the invariant requires "+
			"the affordance that reveals the scope, not just an admission that one exists", note)
	}
}

// TestScopeDisclosure_ExpandedStateNamesTheWayBack: a mode the operator cannot
// tell they are in, or cannot leave, is a trap.
func TestScopeDisclosure_ExpandedStateNamesTheWayBack(t *testing.T) {
	_, w2, _ := placementTUIs(t, "group_window:\n    eng: window-2\n")
	w2.agents.expanded = true

	note := w2.agentsScopeNote()
	if !strings.Contains(note, "showing all") {
		t.Errorf("expanded disclosure %q does not say the view is unscoped", note)
	}
	if !strings.Contains(note, "a scopes to this window") {
		t.Errorf("expanded disclosure %q does not name the way back", note)
	}
}

// TestSingleWindowSession_HasNoScopeAndNoDisclosure is AC9: zero change for the
// operator who never split monitors. The disclosure is suppressed by there
// being nothing to disclose, not by a separate "is this single-window" branch —
// so a two-window session that happens to own everything behaves the same way.
func TestSingleWindowSession_HasNoScopeAndNoDisclosure(t *testing.T) {
	w1, _, _ := placementTUIs(t, "group_window: {}\n")

	if hidden, _ := w1.scopeDisclosure(); hidden != 0 {
		t.Errorf("a session with everything on window 1 reports %d hidden agents", hidden)
	}
	if note := w1.agentsScopeNote(); note != "" {
		t.Errorf("an unscoped surface drew a disclosure line %q -- AC9 is 'no scope, no disclosure "+
			"line, no expand hint'", note)
	}
	total := 0
	for _, idxs := range w1.agentsGroupMembers() {
		total += len(idxs)
	}
	if total != 8 {
		t.Errorf("window 1's modal shows %d of 8 agents with nothing assigned elsewhere", total)
	}
}

// ── AC7: attention is never scoped ──────────────────────────────────

// TestAttentionIsNeverScoped_WaitingAgentOnAnotherWindowStillListed is the
// non-negotiable one, and it shares its fixture with the scoping tests
// deliberately: the SAME window 2 whose modal and overlay hide eng1 must still
// surface eng1 when eng1 is waiting on the operator.
//
// The assertion is on the unscoped surface's OUTPUT, not on my having refrained
// from editing it. waitingRows and the chime both walk t.panes; a future edit
// that "helpfully" routes either through visiblePanesForWindow would leave an
// agent blocked on a human with no way to discover it from the monitor the
// human is looking at.
func TestAttentionIsNeverScoped_WaitingAgentOnAnotherWindowStillListed(t *testing.T) {
	_, w2, _ := placementTUIs(t, "group_window:\n    eng: window-2\n")

	// super lives on window 1's core group, so window 2's display scope hides
	// it. Make it wait on the operator.
	var target int = -1
	for i, p := range w2.panes {
		if p.Name() == "super" {
			target = i
		}
	}
	if target < 0 {
		t.Fatal("fixture has no super pane")
	}
	rp, ok := w2.panes[target].(*RemotePane)
	if !ok {
		t.Fatalf("fixture pane is %T, want *RemotePane", w2.panes[target])
	}
	w2.panes[target] = &waitingRemotePane{RemotePane: rp, since: time.Now(), preview: "Claude needs your permission"}

	// Precondition: this agent IS scoped out of window 2's display surfaces.
	scopedIn := false
	for _, p := range w2.visiblePanesForWindow() {
		if p.Name() == "super" {
			scopedIn = true
		}
	}
	if scopedIn {
		t.Fatal("precondition: super should be scoped out of window 2; the test proves nothing otherwise")
	}

	rows := w2.waitingRows()
	found := false
	for _, r := range rows {
		if r.Name == "super" {
			found = true
		}
	}
	if !found {
		t.Errorf("an agent waiting on the operator is MISSING from window 2's needs-input list "+
			"because display scoping reached into it. Attention is never scoped (docs/spec.md): "+
			"the operator looking at this monitor would never learn that super is blocked. rows=%+v",
			rows)
	}
}

// TestAttentionList_DropsWindowAliasPrefix covers the second display site of
// the prefix — the needs-input list — which is the one place AC1 and AC7 meet:
// strip the prefix, keep every agent.
func TestAttentionList_DropsWindowAliasPrefix(t *testing.T) {
	_, w2, _ := placementTUIs(t, "group_window: {}\n")
	rp := w2.panes[0].(*RemotePane)
	w2.panes[0] = &waitingRemotePane{RemotePane: rp, since: time.Now(), preview: "waiting"}

	rows := w2.waitingRows()
	if len(rows) != 1 {
		t.Fatalf("want exactly one waiting row, got %d", len(rows))
	}
	if strings.HasPrefix(rows[0].Name, WindowOnePeerName+":") {
		t.Errorf("the needs-input list still carries the window-1 alias prefix: %q", rows[0].Name)
	}
	if rows[0].Name != rp.name {
		t.Errorf("waiting row shows %q, want the bare canonical name %q", rows[0].Name, rp.name)
	}
}

// TestAttentionList_KeepsCrossMachineHost is the same AC2 separation applied to
// the attention list: the prefix that carries information survives here too.
func TestAttentionList_KeepsCrossMachineHost(t *testing.T) {
	_, w2, _ := placementTUIs(t, "group_window: {}\n")
	w2.panes = []PaneView{&waitingRemotePane{
		RemotePane: &RemotePane{name: "intern", host: "workbench", alive: true},
		since:      time.Now(),
		preview:    "waiting",
	}}

	rows := w2.waitingRows()
	if len(rows) != 1 || rows[0].Name != "workbench:intern" {
		t.Errorf("needs-input list shows %+v, want workbench:intern -- the host names which MACHINE "+
			"the operator has to go to, which is exactly what an attention row is for", rows)
	}
}

// ── AC8: fact parity under scoping ──────────────────────────────────

// TestFactParityUnderScoping_ExpandedWindow2MatchesWindow1 is the amended
// invariant's one-question test as a literal assertion: does revealing the full
// fleet show exactly what window 1 shows?
//
// Numbering is included because it is the fact most likely to be derived from
// list position by accident — the moment it is, scoping renumbers the fleet and
// two windows disagree about which agent is #4.
func TestFactParityUnderScoping_ExpandedWindow2MatchesWindow1(t *testing.T) {
	w1, w2, _ := placementTUIs(t, "group_window:\n    eng: window-2\n")
	w2.agents.expanded = true

	facts := func(tui *TUI) map[string]string {
		out := map[string]string{}
		for label, idxs := range tui.agentsGroupMembers() {
			for _, i := range idxs {
				p := tui.panes[i]
				key := agentKey(p)
				out[key] = label + "|" +
					strconv.Itoa(fleetIdxOf(p, i)) + "|" +
					btoa(tui.layoutState.Hidden[key]) + "|" +
					btoa(tui.layoutState.Protected[key])
			}
		}
		return out
	}

	a, b := facts(w1), facts(w2)
	if len(a) != len(b) {
		t.Fatalf("window 1 shows %d agents, expanded window 2 shows %d: revealing the full fleet "+
			"must show exactly window 1's fleet", len(a), len(b))
	}
	for key, want := range a {
		got, ok := b[key]
		if !ok {
			t.Errorf("agent %q is in window 1's modal and missing from expanded window 2's", key)
			continue
		}
		if got != want {
			t.Errorf("agent %q: window 1 says %q, expanded window 2 says %q -- two windows "+
				"disagreeing about the same agent is a defect by definition, scoped or not",
				key, want, got)
		}
	}
}

func btoa(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// ── AC4/AC6 on the DRAWN surface ────────────────────────────────────

// overlayScreen renders the overlay onto a simulation screen and returns what
// is actually on it.
//
// This tests the DRAWN list rather than the computed one, and that distinction
// is the whole reason the function exists: a mutation of the overlay's source
// list (scoped := t.panes[:0]) survived every predicate-level test in this file
// while hiding every agent from the window that owns them. A scoped surface has
// to be asserted where it is scoped.
func overlayScreen(t *testing.T, tui *TUI, s tcell.SimulationScreen) string {
	t.Helper()
	tui.layoutState.Overlay = true
	tui.renderOverlay()
	w, h := s.Size()
	var b strings.Builder
	for y := 0; y < h; y++ {
		var line strings.Builder
		for x := 0; x < w; x++ {
			ch, _, _, _ := s.GetContent(x, y)
			if ch == 0 {
				ch = ' '
			}
			line.WriteRune(ch)
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// scopedOverlayTUI is a window-2 TUI with a real screen: eng on window 2, the
// rest on window 1.
func scopedOverlayTUI(t *testing.T) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	os.WriteFile(filepath.Join(root, ".initech", "assignments.yaml"),
		[]byte("group_window:\n    eng: window-2\n"), 0o644)
	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	tui, s := newTestTUIWithScreen("super", "pm", "qa1", "eng1", "eng2")
	tui.projectRoot, tui.windowID, tui.assignment = root, WindowPeerName(2), a
	tui.ensureGroups(false)
	return tui, s
}

// TestOverlay_DrawsOnlyThisWindowsAgents is AC4 asserted on pixels, and it is
// the test that kills the over-scope mutant: both directions in one screen.
func TestOverlay_DrawsOnlyThisWindowsAgents(t *testing.T) {
	tui, s := scopedOverlayTUI(t)
	screen := overlayScreen(t, tui, s)

	for _, own := range []string{"eng1", "eng2"} {
		if !strings.Contains(screen, own) {
			t.Errorf("window 2's overlay does not draw %q, an agent this window OWNS. Over-scoping "+
				"is worse than the clutter it replaces: the operator loses the agents in front of "+
				"them.\n%s", own, screen)
		}
	}
	for _, other := range []string{"super", "pm", "qa1"} {
		if strings.Contains(screen, other) {
			t.Errorf("window 2's overlay draws %q, which belongs to window 1 -- the unscoped list "+
				"the operator asked to be rid of.\n%s", other, screen)
		}
	}
}

// TestOverlay_DrawsTheScopeDisclosure is AC6 on pixels: computing the
// disclosure and drawing it are different claims, and only the second one is
// what the invariant requires.
func TestOverlay_DrawsTheScopeDisclosure(t *testing.T) {
	tui, s := scopedOverlayTUI(t)
	screen := overlayScreen(t, tui, s)

	if !strings.Contains(screen, "+3") {
		t.Errorf("the overlay hides 3 agents and does not draw the count.\n%s", screen)
	}
	if !strings.Contains(screen, "on window 1") {
		t.Errorf("the overlay does not draw WHERE the hidden agents are.\n%s", screen)
	}
	if !strings.Contains(screen, "shows all") {
		t.Errorf("the overlay does not draw the affordance that reveals the hidden agents. A scoped "+
			"surface with no way out fails the amended parity invariant by definition.\n%s", screen)
	}
}

// TestOverlay_UnscopedWindowDrawsNoDisclosure is AC9 on pixels.
func TestOverlay_UnscopedWindowDrawsNoDisclosure(t *testing.T) {
	tui, s := newTestTUIWithScreen("super", "pm", "eng1")
	screen := overlayScreen(t, tui, s)

	if strings.Contains(screen, "shows all") {
		t.Errorf("a single-window session drew an expand hint for a scope it does not have.\n%s", screen)
	}
	for _, n := range []string{"super", "pm", "eng1"} {
		if !strings.Contains(screen, n) {
			t.Errorf("single-window overlay lost %q; AC9 is zero change for this session.\n%s", n, screen)
		}
	}
}

// TestOverlayClick_ResolvesAgainstTheDrawnList guards the coupling that scoping
// introduced: the overlay's hide/unhide dot is addressed by ROW, and the rows
// are now this window's agents rather than t.panes. Indexing the wrong list
// silently toggles a bystander -- on the one surface whose job is telling the
// operator what is where.
//
// The fixture is chosen so the two lists DISAGREE at row 0 (drawn row 0 is
// eng1; t.panes[0] is super). A test where they happen to agree would pass
// against the bug.
func TestOverlayClick_ResolvesAgainstTheDrawnList(t *testing.T) {
	tui, s := scopedOverlayTUI(t)
	overlayScreen(t, tui, s) // renders, and stamps t.overlayBounds

	rows := tui.visiblePanesForWindow()
	if len(rows) != 2 || rows[0].Name() != "eng1" {
		t.Fatalf("fixture: window 2's drawn rows are %v, want eng1 first", paneNames(rows))
	}
	if tui.panes[0].Name() == rows[0].Name() {
		t.Fatal("fixture no longer distinguishes the drawn list from t.panes; the test cannot " +
			"detect the bug it exists for")
	}

	// The click's OUTCOME is not observable here and deliberately so: this is a
	// viewer, and a viewer never writes fleet state (ini-civ single-writer). It
	// routes the request to window 1, which this fixture has no connection to,
	// so the write is refused. What IS observable -- and what this test is
	// actually about -- is WHICH AGENT the click resolved to, which the refusal
	// notice names.
	events := make(chan AgentEvent, 8)
	tui.agentEvents = events

	dotCol := tui.overlayBounds.x + 2
	firstRowY := tui.overlayBounds.y + 1
	tui.handleMouse(tcell.NewEventMouse(dotCol, firstRowY, tcell.Button1, tcell.ModNone))

	select {
	case ev := <-events:
		if !strings.Contains(ev.Detail, "eng1") {
			t.Errorf("clicking row 0's dot resolved to the wrong agent: %q. Row 0 draws eng1; the "+
				"handler indexed t.panes, whose row 0 is %q, so the operator would hide a "+
				"bystander on another monitor.", ev.Detail, tui.panes[0].Name())
		}
		if strings.Contains(ev.Detail, tui.panes[0].Name()) {
			t.Errorf("clicking row 0's dot targeted %q, the first entry of t.panes rather than the "+
				"first DRAWN row: %q", tui.panes[0].Name(), ev.Detail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the overlay dot click produced no fleet-write attempt at all; the click did not " +
			"resolve to any agent")
	}
}

func paneNames(ps []PaneView) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name()
	}
	return out
}

// TestRemotePaneRibbon_DropsWindowAliasKeepsMachineHost covers the THIRD
// display site of the prefix (ini-9isx AC1 names overlay, modal AND ribbon).
//
// It exists because a grep for the composition pattern did not find this one:
// the ribbon builds its name inside RemotePane.Render rather than at the
// surface that draws it. The composed two-window rig found it, and only after
// its capture was widened to the screen's real width -- the ribbon sits at the
// far right of row 41, in the ten columns a 120-wide snapshot of a 130-wide
// screen was silently dropping.
func TestRemotePaneRibbon_DropsWindowAliasKeepsMachineHost(t *testing.T) {
	cases := []struct {
		host      string
		wantName  string
		wantBadge bool
	}{
		{WindowOnePeerName, "eng1", false},
		{"window-2", "eng1", false},
		{"workbench", "workbench:eng1", true},
	}
	for _, c := range cases {
		rp := &RemotePane{name: "eng1", host: c.host, alive: true}
		got := paneDisplayName(rp)
		if got != c.wantName {
			t.Errorf("ribbon name for host %q = %q, want %q", c.host, got, c.wantName)
		}
		if paneIsRemoteMachine(rp) != c.wantBadge {
			t.Errorf("ribbon [R] badge for host %q = %v, want %v -- the badge means another "+
				"MACHINE, and a window alias is not one", c.host, !c.wantBadge, c.wantBadge)
		}
	}
}
