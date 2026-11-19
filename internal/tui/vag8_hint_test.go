package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ini-vag8: the all-hidden hint names only a way that exists (the main
// window's Agents panel), never dots an idle agent does not have nor a
// command bar this window does not have, and says how many of the hidden
// agents are idle, the same count status --json gives.

func vag8AllHidden(t *testing.T) (*TUI, []string) {
	t.Helper()
	w2, owned := uz42Viewer(t)
	for _, k := range owned {
		w2.layoutState.Hidden[k] = true
	}
	return w2, owned
}

// vag8Working makes one pane read running for the whole test without a
// clock: its last output is an hour ahead, so it is never quiet long enough.
func vag8Working(w2 *TUI, key string) {
	for _, p := range w2.panes {
		if rp, ok := p.(*RemotePane); ok && agentKey(rp) == key {
			rp.lastOut = time.Now().Add(time.Hour)
			rp.activity = StateRunning
		}
	}
}

// statusIdle counts the owned agents status --json reports idle: the same
// Activity().String() its PaneInfo carries (ipc.go allPanesWithViews).
func statusIdle(w2 *TUI, owned []string) int {
	mine, n := ownedSet(owned), 0
	for _, p := range w2.panes {
		if mine[agentKey(p)] && p.Activity().String() == "idle" {
			n++
		}
	}
	return n
}

// AC3: every agent hidden and idle: the hint names the main window's Agents
// panel and its key, and no dots or command bar.
func TestVag8_AllHiddenAndIdle_HintNamesTheAgentsPanel_NotDots(t *testing.T) {
	w2, owned := vag8AllHidden(t)
	got := w2.viewerEmptyExplanation()
	for _, want := range []string{"main window's Agents panel (Option+a there)", fmt.Sprintf("%d idle", len(owned))} {
		if !strings.Contains(got, want) {
			t.Errorf("hint lacks %q: %q", want, got)
		}
	}
	for _, gone := range []string{"dot", "overlay", "click", "command bar", "focus"} {
		if strings.Contains(got, gone) {
			t.Errorf("hint points at %q, which this window or an idle agent with no bead does not have: %q", gone, got)
		}
	}
}

// AC5: the idle count is the one status --json gives, not the owned count.
func TestVag8_AllHidden_IdleCountEqualsStatusJSON(t *testing.T) {
	w2, owned := vag8AllHidden(t)
	if len(owned) < 2 {
		t.Fatalf("fixture owns %d; the count needs a working and an idle agent", len(owned))
	}
	vag8Working(w2, owned[0])
	idle := statusIdle(w2, owned)
	if idle != len(owned)-1 {
		t.Fatalf("fixture: status --json idle = %d, want %d", idle, len(owned)-1)
	}
	got := w2.viewerEmptyExplanation()
	if want := fmt.Sprintf("all %d agents assigned here are hidden, %d idle", len(owned), idle); !strings.Contains(got, want) {
		t.Errorf("hint = %q, want it to say %q", got, want)
	}
}

// AC4: the one-agent form names the agent, its state and the way back.
func TestVag8_OneHiddenAgent_HintNamesItAndTheWayBack(t *testing.T) {
	w2, owned := vag8AllHidden(t)
	keep := owned[0]
	var panes []PaneView
	for _, p := range w2.panes {
		if k := agentKey(p); k == keep || !ownedSet(owned)[k] {
			panes = append(panes, p)
		}
	}
	w2.panes = panes
	for k := range w2.paneOwnership {
		if k != keep && w2.paneOwnership[k] == w2.windowID {
			delete(w2.paneOwnership, k)
		}
	}
	var name string
	for _, p := range w2.panes {
		if agentKey(p) == keep {
			name = paneDisplayName(p)
		}
	}
	got := w2.viewerEmptyExplanation()
	want := fmt.Sprintf("%s, the 1 agent assigned here, is hidden and idle — use the main window's Agents panel (Option+a there) to bring it back", name)
	if got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}
	vag8Working(w2, keep)
	if got := w2.viewerEmptyExplanation(); strings.Contains(got, "idle") || !strings.Contains(got, name+", the 1 agent assigned here, is hidden — use the main window's Agents panel") {
		t.Errorf("a working hidden agent reads %q, want it named, not idle, and the way back", got)
	}
}

// A hidden idle agent that holds a bead is not counted idle in the all-hidden
// hint, as the agents box's hidden line leaves it out (qa3's survivor).
func TestVag8_AllHidden_ABeadHolderIsNotCountedIdle(t *testing.T) {
	w2, owned := vag8AllHidden(t)
	before := statusIdle(w2, owned)
	for _, p := range w2.panes {
		if rp, ok := p.(*RemotePane); ok && agentKey(rp) == owned[0] {
			rp.beadIDs = []string{"ini-held"}
		}
	}
	got := w2.viewerEmptyExplanation()
	if want := fmt.Sprintf("all %d agents assigned here are hidden, %d idle", len(owned), before-1); !strings.Contains(got, want) {
		t.Errorf("hint = %q, want it to say %q", got, want)
	}
}
