package tui

// window_display.go — how a window PRESENTS the fleet, as distinct from what
// the fleet IS (ini-9isx).
//
// The amended parity invariant (docs/spec.md) draws the line this file lives
// on: every window agrees on the same FACTS — membership, groups, numbering,
// marks — while a window MAY deliberately display only its own slice of them.
// Scoping is a decision about which facts to show, never a divergence in what
// the facts are. Two rules keep that distinction from rotting into the
// accidental divergence ini-6m4 was purchased to kill:
//
//   - Any agent a scoped surface DOES show is shown identically everywhere.
//   - A scoped surface MUST disclose its scope — a visible count of what it
//     hides and the affordance that reveals it. Silent scoping is
//     indistinguishable from a bug.
//
// And its companion, which this file must never be used to violate:
// ATTENTION IS NEVER SCOPED. The needs-input chime and list stay fleet-wide in
// every window. Nothing here is called from those paths.

import (
	"fmt"
	"regexp"
)

// windowAliasHostPattern is THE spelling of the window-alias host family, and
// the only one. The family is irregular by history — window1 (no hyphen, the
// wire name from ini-1ch) alongside window-2/window-3 (viewer identities) and
// the legacy windowN form — which is precisely why it gets one definition that
// every matcher is built from. ini-xq4r was two generators drifting on this
// exact irregularity; ini-yc03's rule is that a second spelling of a prefix is
// a second translation edge.
const windowAliasHostPattern = `window(?:1|-?[0-9]+)`

// windowAliasHostRe matches a HOST that is a window alias. Cross-machine hosts
// (workbench) must not match: see paneDisplayName for why that separation is
// load-bearing rather than cosmetic.
var windowAliasHostRe = regexp.MustCompile(`^` + windowAliasHostPattern + `$`)

// isWindowAliasHost reports whether a host names another WINDOW of this fleet
// rather than another MACHINE.
func isWindowAliasHost(host string) bool {
	return windowAliasHostRe.MatchString(host)
}

// paneDisplayName is the name to draw for a pane, on any surface.
//
// THE TWO PREFIX FAMILIES MEAN OPPOSITE THINGS, and this function keeps them in
// separate branches on purpose (ini-9isx AC2/AC11):
//
//   - A WINDOW ALIAS is presentation. A viewer reaches window 1's agents
//     through a peer literally named "window1", so the same agent is "eng1" to
//     window 1 and "window1:eng1" to a viewer. The prefix describes the
//     OBSERVER, tells the operator nothing they do not already know (they are
//     looking at that window), and at 39 agents it is pure clutter. Dropped.
//   - A CROSS-MACHINE HOST is identity. "workbench:intern" and a local "intern"
//     are different agents on different machines; dropping that prefix would
//     merge two agents on screen. Kept.
//
// The tempting one-liner here is `return p.Name()`, which is shorter, passes a
// window-prefix test, and silently collapses every cross-machine agent onto its
// local namesake. That is why the two families never share a branch: a mutation
// of the alias branch cannot be caught by a cross-machine fixture, and if it
// ever is, the branches have been merged and the bug is back.
func paneDisplayName(p PaneView) string {
	host := p.Host()
	if host == "" || isWindowAliasHost(host) {
		return p.Name()
	}
	return host + ":" + p.Name()
}

// paneIsRemoteMachine reports whether a pane lives on another MACHINE, which is
// what the overlay's [R] badge means. A window-1 alias is not a remote machine;
// badging it as one was the same conflation as the prefix itself.
func paneIsRemoteMachine(p PaneView) bool {
	host := p.Host()
	return host != "" && !isWindowAliasHost(host)
}

// windowDisplayLabel renders a window identity for the operator: "window 1",
// "window 2". The assignment model's identities (WindowOne is the empty string;
// viewers are "window-N") are internal and never shown.
func windowDisplayLabel(windowID string) string {
	switch windowID {
	case WindowOne, WindowOnePeerName:
		return "window 1"
	}
	if m := regexp.MustCompile(`^window-?([0-9]+)$`).FindStringSubmatch(windowID); m != nil {
		return "window " + m[1]
	}
	return windowID
}

// scopeDisclosure describes what this window's scoped surfaces are hiding.
//
// Returns the number of agents scoped OUT of this window's surfaces and a
// human phrase naming where they are. hidden==0 means nothing is being hidden,
// which is the single-window case and the every-group-on-this-window case
// alike — and in that state no disclosure line is drawn at all, because a
// surface that hides nothing is not a scoped surface (ini-9isx AC9).
func (t *TUI) scopeDisclosure() (hidden int, where string) {
	shown := make(map[string]bool)
	for _, p := range t.visiblePanesForWindow() {
		shown[agentKey(p)] = true
	}
	windows := make(map[string]int)
	for _, p := range t.panes {
		key := agentKey(p)
		if shown[key] {
			continue
		}
		hidden++
		if t.assignment != nil {
			windows[t.assignment.WindowOfAgent(key, t.layoutState.GroupOf)]++
		}
	}
	if hidden == 0 {
		return 0, ""
	}
	// Name the window when every hidden agent is on the same one -- which is
	// the two-monitor case the operator actually has. Beyond that, saying
	// "window 1" would be false, so it says something true instead.
	if len(windows) == 1 {
		for w := range windows {
			return hidden, "on " + windowDisplayLabel(w)
		}
	}
	return hidden, "on other windows"
}

// agentsScopeNote is the modal's scope disclosure (ini-9isx AC6), drawn into
// the bottom border the way the title is drawn into the top.
//
// Returns "" when this window hides nothing — a single-window session, or every
// group already on this window. A surface that hides nothing is not a scoped
// surface, and a disclosure line there would announce a scope the operator does
// not have (AC9's "no scope, no disclosure line, no expand hint").
//
// Both states are disclosed, not just the scoped one: once expanded, the
// operator is looking at agents this window does not own, and the way back is
// the same key. A one-way hint is how a mode becomes a trap.
func (t *TUI) agentsScopeNote() string {
	hidden, where := t.scopeDisclosure()
	if hidden == 0 {
		return ""
	}
	if t.agents.expanded {
		return fmt.Sprintf(" showing all %d · a scopes to this window ", len(t.panes))
	}
	return fmt.Sprintf(" +%d %s · a shows all ", hidden, where)
}
