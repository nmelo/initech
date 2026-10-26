//go:build !windows

package tui

import (
	"encoding/json"
	"testing"
	"time"
)

// ini-pi8p: every window agrees on the background state. Window 1 reads the
// footer and sends the work on agent_status, both edges and only on change;
// a second window applies it. Unix-only like no_bead_window_test.go.

func TestBackgroundState_RidesAgentStatusToOtherWindows(t *testing.T) {
	p := windowServerTestPane("eng1")
	ws, cleanup, err := startWindowServer(
		testWindowProject("127.0.0.1:0"), "test",
		[]*Pane{p}, func(f func()) { go f() }, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("startWindowServer: %v", err)
	}
	t.Cleanup(cleanup)
	session, ctrl, _ := dialWindow(t, ws.Addr(), "window-2")
	defer session.Close()
	frames := make(chan ControlResp, 16)
	go func() {
		sc := NewIPCScanner(ctrl)
		for sc.Scan() {
			var ev ControlResp
			if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Action == agentStatusAction {
				frames <- ev
			}
		}
	}()
	tu := newTestTUI()
	tu.windowSrv = ws
	tu.panes = []PaneView{p}
	tu.broadcastAgentStatusChanges()
	drainAgentStatus(frames, 200*time.Millisecond)

	viewer := newTestTUI()
	rp := &RemotePane{name: "eng1", alive: true}
	viewer.panes = []PaneView{rp}

	// Work seen 90s ago in window 1.
	p.observeBackground(time.Now().Add(-90*time.Second), true, parseFooter(lvxwFixture(t, "haiku310_landing_shell.txt"), 160))
	tu.broadcastAgentStatusChanges()
	ev := awaitAgentStatus(t, frames, "work seen")
	if b := ev.BackgroundWork; b == nil || b.Shells != 1 || b.SinceSec < 89 || b.SinceSec > 95 {
		t.Fatalf("frame background_work = %+v, want 1 shell for about 90s", b)
	}
	now := time.Now()
	viewer.applyAgentBackground(ev.Name, ev.BackgroundWork, now)
	if got := rp.Activity(); got != StateBackground {
		t.Errorf("viewer reads %v, want background", got)
	}
	if age, ok := backgroundAge(rp, now); !ok || age < 89*time.Second || age > 95*time.Second {
		t.Errorf("viewer age %v (%v), want about 90s", age, ok)
	}

	// Nothing changed: nothing sent, though the age moved on.
	tu.broadcastAgentStatusChanges()
	if extra := drainAgentStatus(frames, 200*time.Millisecond); extra != 0 {
		t.Errorf("%d frames with nothing changed, want 0", extra)
	}

	// The work ends: the clear edge is sent and applied.
	p.observeBackground(time.Now(), true, parseFooter(lvxwFixture(t, "haiku310_landed.txt"), 160))
	tu.broadcastAgentStatusChanges()
	ev = awaitAgentStatus(t, frames, "work ended")
	if ev.BackgroundWork != nil {
		t.Fatalf("clear frame still carries %+v", ev.BackgroundWork)
	}
	viewer.applyAgentBackground(ev.Name, ev.BackgroundWork, time.Now())
	if got := rp.Activity(); got != StateIdle {
		t.Errorf("viewer after the clear reads %v, want idle", got)
	}
}

// Mixed versions: an older window 1 sends no background_work, and the viewer
// reads idle as before; an older viewer decodes a new frame, the extra key
// ignored and the rest intact.
func TestBackgroundState_MixedWindowVersions(t *testing.T) {
	var ev ControlResp
	if err := json.Unmarshal([]byte(`{"action":"agent_status","name":"eng1","suspended":false}`), &ev); err != nil {
		t.Fatal(err)
	}
	viewer := newTestTUI()
	rp := &RemotePane{name: "eng1", alive: true}
	viewer.panes = []PaneView{rp}
	viewer.applyAgentBackground(ev.Name, ev.BackgroundWork, time.Now())
	if got := rp.Activity(); got != StateIdle {
		t.Errorf("older window 1: viewer reads %v, want idle", got)
	}

	data, err := json.Marshal(ControlResp{Action: agentStatusAction, Name: "eng1", Beads: []string{"ini-a"},
		BackgroundWork: &AgentBackground{Shells: 1, SinceSec: 30}})
	if err != nil {
		t.Fatal(err)
	}
	var old struct {
		Action string   `json:"action"`
		Name   string   `json:"name"`
		Beads  []string `json:"beads"`
	}
	if err := json.Unmarshal(data, &old); err != nil || old.Name != "eng1" || len(old.Beads) != 1 {
		t.Errorf("older viewer read %+v (%v)", old, err)
	}
}
