package tui

import (
	"testing"
	"time"
)

// ini-pi8p: a second window's peer manager hands agent_status's background
// work to its handler -- the receive half the window tests start after.
func TestBackgroundState_TheViewerReceivesTheWork(t *testing.T) {
	r := newBindRig(t)
	got := make(chan *AgentBackground, 2)
	r.pm.onAgentBackground = func(name string, bg *AgentBackground) {
		if name == "eng1" {
			got <- bg
		}
	}
	if err := writeJSON(r.ctrl, ControlResp{Action: agentStatusAction, Name: "eng1",
		BackgroundWork: &AgentBackground{Shells: 1, Monitors: 2, SinceSec: 40}}); err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := writeJSON(r.ctrl, ControlResp{Action: agentStatusAction, Name: "eng1"}); err != nil {
		t.Fatalf("status: %v", err)
	}
	for i, want := range []*AgentBackground{{Shells: 1, Monitors: 2, SinceSec: 40}, nil} {
		select {
		case bg := <-got:
			if (bg == nil) != (want == nil) || (bg != nil && *bg != *want) {
				t.Errorf("event %d: handler got %+v, want %+v", i, bg, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("event %d: handler never called", i)
		}
	}
}
