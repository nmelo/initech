package tui

import "testing"

// ini-sf5.1: the list answer carries each pane's agent type, so initech
// compact can refuse a non-Claude agent before typing anything. A pane with
// no type set is Claude Code.
func TestListAgentType_EachPaneSaysItsType(t *testing.T) {
	tu, _ := newTestTUIWithScreen("eng1", "eng2")
	for _, pv := range tu.panes {
		if pv.Name() == "eng2" {
			pv.(*Pane).agentType = "codex"
		}
	}
	panes, ok := tu.AllPanes()
	if !ok {
		t.Fatal("no list")
	}
	got := map[string]string{}
	for _, p := range panes {
		got[p.Name] = p.AgentType
	}
	if got["eng1"] != "claude-code" || got["eng2"] != "codex" {
		t.Errorf("agent types %v, want eng1 claude-code (unset), eng2 codex", got)
	}
}

// The daemon's list (a remote peer's) says the same.
func TestListAgentType_TheDaemonSaysItToo(t *testing.T) {
	d := newTestDaemon(t)
	a, _, _ := proofPaneSized(t, 80, 24)
	a.name = "eng1"
	b, _, _ := proofPaneSized(t, 80, 24)
	b.name, b.agentType = "eng2", "codex"
	d.panes = []*Pane{a, b}
	panes, _ := d.AllPanes()
	got := map[string]string{}
	for _, p := range panes {
		got[p.Name] = p.AgentType
	}
	if got["eng1"] != "claude-code" || got["eng2"] != "codex" {
		t.Errorf("daemon agent types %v, want eng1 claude-code (unset), eng2 codex", got)
	}
}
