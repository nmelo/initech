package tui

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/watch"
)

// watchTUI is a window 1 with a real watch store, a fixed clock, and the
// connection's identity taken from the request's From (the stand-in for
// identifyPostConnection, which needs a real pane process behind the socket).
func watchTUI(t *testing.T, panes ...*Pane) *TUI {
	t.Helper()
	tui := newTestTUI(panes...)
	s, _ := watch.Open(filepath.Join(t.TempDir(), "watches.yaml"), opsT0)
	tui.watches = s
	origID, origNow, origVal := watchIdentify, watchHandlerNow, watchValidatorFor
	watchHandlerNow = func() time.Time { return opsT0 }
	watchValidatorFor = func(*TUI) watchValidator { return (&validateLog{}).ok }
	t.Cleanup(func() { watchIdentify, watchHandlerNow, watchValidatorFor = origID, origNow, origVal })
	return tui
}

func watchIPC(t *testing.T, tui *TUI, req IPCRequest) IPCResponse {
	t.Helper()
	watchIdentify = func(*TUI, net.Conn) string { return req.From }
	conn := &fakeConn{}
	tui.handleIPCWatch(conn, req)
	var resp IPCResponse
	if err := json.Unmarshal(conn.written[:conn.findNewline()], &resp); err != nil {
		t.Fatalf("unparseable reply %q: %v", conn.written, err)
	}
	return resp
}

func setIPC(from string, r WatchSetRequest) IPCRequest {
	body, _ := json.Marshal(r)
	return IPCRequest{Action: "watch_set", From: from, Text: string(body)}
}

// Through the real handler: the owner is the connection's, set stores it,
// list shows it, cancel ends it.
func TestWatchIPC_SetListCancel_OwnerFromTheConnection(t *testing.T) {
	tui := watchTUI(t)
	resp := watchIPC(t, tui, setIPC("eng6", beadReq("ini-a", "read it")))
	if !resp.OK || resp.Data != "w1" {
		t.Fatalf("set = %+v", resp)
	}
	if ws := tui.watches.Snapshot(opsT0); len(ws) != 1 || ws[0].Owner != "eng6" {
		t.Fatalf("stored = %+v", ws)
	}
	resp = watchIPC(t, tui, IPCRequest{Action: "watch_list", From: "eng6", Text: `{}`})
	var rows []WatchListing
	json.Unmarshal([]byte(resp.Data), &rows)
	if !resp.OK || len(rows) != 1 || rows[0].ID != "w1" {
		t.Fatalf("list = %+v", resp)
	}
	if resp = watchIPC(t, tui, IPCRequest{Action: "watch_cancel", From: "eng4", Target: "w1"}); resp.OK {
		t.Errorf("eng4 cancelled eng6's watch: %+v", resp)
	}
	if resp = watchIPC(t, tui, IPCRequest{Action: "watch_cancel", From: "eng6", Target: "w1"}); !resp.OK || resp.Data != "cancelled w1" {
		t.Errorf("own cancel = %+v", resp)
	}
}

// Outside any pane: set and cancel are refused, list --all is answered.
func TestWatchIPC_OutsideAPane(t *testing.T) {
	tui := watchTUI(t)
	watchIPC(t, tui, setIPC("eng6", beadReq("ini-a", "w")))
	if resp := watchIPC(t, tui, setIPC("", beadReq("ini-b", "w"))); resp.OK || !strings.Contains(resp.Error, "inside your agent's pane") {
		t.Errorf("set outside = %+v", resp)
	}
	if resp := watchIPC(t, tui, IPCRequest{Action: "watch_cancel", Target: "w1"}); resp.OK {
		t.Errorf("cancel outside = %+v", resp)
	}
	if resp := watchIPC(t, tui, IPCRequest{Action: "watch_list", Text: `{"all":true}`}); !resp.OK {
		t.Errorf("list --all outside = %+v", resp)
	}
	if n := len(tui.watches.Snapshot(opsT0)); n != 1 {
		t.Errorf("%d watches, want only eng6's", n)
	}
}

// A window with no store (a second window, initech serve, before load)
// refuses every action with one line and sets nothing.
func TestWatchIPC_NoStore_RefusesEveryAction(t *testing.T) {
	tui := watchTUI(t)
	tui.watches = nil
	for _, req := range []IPCRequest{setIPC("eng6", beadReq("ini-a", "w")),
		{Action: "watch_list", From: "eng6"}, {Action: "watch_cancel", From: "eng6", Target: "w1"}} {
		if resp := watchIPC(t, tui, req); resp.OK || !strings.Contains(resp.Error, "keeps no watches") {
			t.Errorf("%s = %+v", req.Action, resp)
		}
	}
}

// The validator's refusal is the answer, and nothing is stored.
func TestWatchIPC_TheValidatorsRefusal_SetsNothing(t *testing.T) {
	tui := watchTUI(t)
	watchValidatorFor = func(*TUI) watchValidator {
		return func(watch.Kind, map[string]string, string) error { return errUnknownBeadForTest }
	}
	if resp := watchIPC(t, tui, setIPC("eng6", beadReq("ini-zzz", "w"))); resp.OK || resp.Error != errUnknownBeadForTest.Error() {
		t.Errorf("set = %+v", resp)
	}
	if n := len(tui.watches.Snapshot(opsT0)); n != 0 {
		t.Errorf("%d stored", n)
	}
}

var errUnknownBeadForTest = errorString("ini-zzz is not a bead bd knows; nothing set")

type errorString string

func (e errorString) Error() string { return string(e) }

// A sleeping owner's watch says it wakes, unless it holds until the owner wakes.
func TestWatchIPC_ListSaysWhenALineWillWakeItsOwner(t *testing.T) {
	p := &Pane{name: "eng6"}
	p.suspended = true
	tui := watchTUI(t, p)
	watchIPC(t, tui, setIPC("eng6", beadReq("ini-a", "wakes me")))
	held := beadReq("ini-b", "holds")
	held.HoldIfAsleep = true
	watchIPC(t, tui, setIPC("eng6", held))
	var rows []WatchListing
	json.Unmarshal([]byte(watchIPC(t, tui, IPCRequest{Action: "watch_list", From: "eng6", Text: `{}`}).Data), &rows)
	for _, r := range rows {
		if r.Wakes != !r.HoldIfAsleep {
			t.Errorf("%s (%s): wakes=%v", r.ID, r.Why, r.Wakes)
		}
	}
}

// watch_count is how many waiting watches an agent holds, for the kept line
// of clear and restart; ended watches do not count.
func TestWatchIPC_Count_IsTheAgentsWaitingWatches(t *testing.T) {
	tui := watchTUI(t)
	watchIPC(t, tui, setIPC("eng6", beadReq("ini-a", "w")))
	watchIPC(t, tui, setIPC("eng6", beadReq("ini-b", "w")))
	watchIPC(t, tui, setIPC("eng4", beadReq("ini-c", "w")))
	watchIPC(t, tui, IPCRequest{Action: "watch_cancel", From: "eng6", Target: "w2"})
	if resp := watchIPC(t, tui, IPCRequest{Action: "watch_count", Target: "eng6"}); !resp.OK || resp.Data != "1" {
		t.Errorf("count = %+v, want 1", resp)
	}
}

// The owner is the connection's, never what the request says about itself:
// a request claiming eng4 from eng6's pane sets eng6's watch.
func TestWatchIPC_TheOwnerIsTheConnections_NotTheRequests(t *testing.T) {
	tui := watchTUI(t)
	watchIdentify = func(*TUI, net.Conn) string { return "eng6" }
	body, _ := json.Marshal(beadReq("ini-a", "w"))
	conn := &fakeConn{}
	tui.handleIPCWatch(conn, IPCRequest{Action: "watch_set", From: "eng4", Text: string(body)})
	if ws := tui.watches.Snapshot(opsT0); len(ws) != 1 || ws[0].Owner != "eng6" {
		t.Errorf("stored = %+v, want eng6's", ws)
	}
}

// Feed it nothing: an empty or unreadable set is refused and sets nothing.
func TestWatchIPC_AnEmptyOrUnreadableSet_SetsNothing(t *testing.T) {
	tui := watchTUI(t)
	for _, text := range []string{"", "{", "[]"} {
		if resp := watchIPC(t, tui, IPCRequest{Action: "watch_set", From: "eng6", Text: text}); resp.OK {
			t.Errorf("%q was set: %+v", text, resp)
		}
	}
	if n := len(tui.watches.Snapshot(opsT0)); n != 0 {
		t.Errorf("%d stored", n)
	}
}

// Reachability: through 2B's real watchValidate (not a stand-in), against
// a bead poll that knows ini-a: a plain deadline and ini-a are set, a bead
// the poll does not know is refused, and so is a status ini-a already has.
// (Before window 1's first poll 2B cannot know which beads exist and lets the
// id through; that window is 2B's, by design.)
func TestWatchIPC_TheRealValidator_IsTheOneSetRuns(t *testing.T) {
	tui := watchTUI(t)
	tui.watchBeads = map[string]bdBead{"ini-a": {ID: "ini-a", Status: "in_progress"}}
	watchValidatorFor = func(t *TUI) watchValidator { return t.watchValidate }
	if resp := watchIPC(t, tui, setIPC("eng6", beadReq("ini-a", "read it"))); !resp.OK {
		t.Fatalf("ini-a refused: %+v", resp)
	}
	already := beadReq("ini-a", "w")
	already.Params["status"] = "in_progress"
	if resp := watchIPC(t, tui, setIPC("eng6", already)); resp.OK || !strings.Contains(resp.Error, "already in_progress") {
		t.Errorf("a status the bead already has: %+v", resp)
	}
	dl := WatchSetRequest{Kind: watch.KindDeadline, Why: "tell me at eleven", Deadline: opsT0.Add(time.Hour)}
	if resp := watchIPC(t, tui, setIPC("eng6", dl)); !resp.OK {
		t.Fatalf("plain deadline refused: %+v", resp)
	}
	resp := watchIPC(t, tui, setIPC("eng6", beadReq("ini-nope", "w")))
	if resp.OK {
		t.Fatalf("a bead the poll never saw was accepted: %+v", resp)
	}
	if !strings.Contains(resp.Error, "no bead ini-nope") {
		t.Errorf("refusal = %q", resp.Error)
	}
	if n := len(tui.watches.Snapshot(opsT0)); n != 2 {
		t.Errorf("%d stored, want ini-a's and the deadline", n)
	}
}

// An agent under initech serve keeps no watches: the daemon answers every
// watch action with the no-store line (exit 1 at the command), never
// "unknown action", which the command would read as an older initech and
// tell the agent to restart (exit 2), which cannot help.
func TestDaemon_WatchActions_AreTheNoStoreLine_NotUnknownAction(t *testing.T) {
	d := &Daemon{}
	for _, action := range []string{"watch_set", "watch_list", "watch_cancel", "watch_count"} {
		conn := &fakeConn{}
		if !d.HandleExtended(conn, IPCRequest{Action: action, From: "eng6"}, nil) {
			t.Errorf("%s: the daemon left it unhandled, so it reads as unknown action", action)
			continue
		}
		var resp IPCResponse
		if err := json.Unmarshal(conn.written[:conn.findNewline()], &resp); err != nil {
			t.Fatalf("%s: unparseable reply %q", action, conn.written)
		}
		if resp.OK || resp.Error != watchNoStoreLine {
			t.Errorf("%s = %+v, want %q", action, resp, watchNoStoreLine)
		}
	}
}
