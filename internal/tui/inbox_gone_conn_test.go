//go:build !windows

package tui

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// ini-jxjl.2 AC2, the control stream: an older window 2 sending inbox_cmd
// gets the unknown-action answer, and the window keeps serving.

// controlReply sends cmd on ctrl and reads, from one scanner, until the reply
// carrying cmd's ID.
func controlReply(t *testing.T, ctrl net.Conn, cmd map[string]any) ControlResp {
	t.Helper()
	if err := writeJSON(ctrl, cmd); err != nil {
		t.Fatal(err)
	}
	ctrl.SetReadDeadline(time.Now().Add(30 * time.Second)) // a hang guard, not a timing
	defer ctrl.SetReadDeadline(time.Time{})
	sc := NewIPCScanner(ctrl)
	for sc.Scan() {
		var resp ControlResp
		if json.Unmarshal(sc.Bytes(), &resp) == nil && resp.ID == cmd["id"] {
			return resp
		}
	}
	t.Fatalf("no reply to %v: %v", cmd["action"], sc.Err())
	return ControlResp{}
}

func TestInboxGone_InboxCmdIsAnUnknownActionOnTheControlStream(t *testing.T) {
	_, addr := startTestWindowServer(t, []*Pane{windowServerTestPane("eng1")})
	session, ctrl, _ := dialWindow(t, addr, "window-2")
	defer session.Close()
	defer ctrl.Close()
	resp := controlReply(t, ctrl, map[string]any{"id": "c1", "action": "inbox_cmd", "op": "seen", "item": "p1"})
	if resp.OK || resp.Error != `unknown action "inbox_cmd"` {
		t.Errorf("inbox_cmd: %+v, want unknown action", resp)
	}
	// The twin, on the same stream: a known action answers in its own words.
	resp = controlReply(t, ctrl, map[string]any{"id": "c2", "action": "set_fleet_state", "text": "{}"})
	if resp.Error == "" || resp.Error == `unknown action "set_fleet_state"` {
		t.Errorf("set_fleet_state after inbox_cmd: %+v, want its own answer", resp)
	}
}
