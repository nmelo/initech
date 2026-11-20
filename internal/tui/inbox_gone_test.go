package tui

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-jxjl.2: the operator inbox is gone. A rig's .initech/inbox.yaml is
// never read, rewritten, set aside or deleted; post and inbox_cmd are actions
// no initech knows; Option+I does nothing; no badge is drawn.

// inboxYAML is an inbox file as a release before the removal wrote it: one
// unread item, one answered.
const inboxYAML = `version: 1
next_id: 3
items:
  - id: p1
    from: eng1
    text: should I update both docs?
    default: update both
    state: unread
    created: 2026-11-18T10:00:00Z
  - id: p2
    from: eng2
    text: rig flaky
    state: answered
    answer: ok
    created: 2026-10-01T10:00:00Z
`

// rigFolderState is everything a test can see of a rig's .initech folder: each
// file's name, bytes' digest and modification time.
func rigFolderState(t *testing.T, root string) map[string]string {
	t.Helper()
	dir := filepath.Join(root, ".initech")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		out[e.Name()] = fmt.Sprintf("%s %x", info.ModTime(), sha256.Sum256(b))
	}
	return out
}

// fakeConnReply is the one reply written to conn.
func fakeConnReply(t *testing.T, conn *fakeConn) IPCResponse {
	t.Helper()
	var resp IPCResponse
	if err := json.Unmarshal(conn.written[:conn.findNewline()], &resp); err != nil {
		t.Fatalf("unparseable reply %q: %v", conn.written, err)
	}
	return resp
}

// holdStatusClock keeps the status bar's clock still, so two frames differ
// only by what the test did.
func holdStatusClock(t *testing.T) {
	t.Helper()
	orig := statusClock
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	statusClock = func() time.Time { return at }
	t.Cleanup(func() { statusClock = orig })
}

// frameCells is the screen's every cell, runes and style.
func frameCells(s tcell.SimulationScreen) []tcell.SimCell {
	cells, _, _ := s.GetContents()
	return slices.Clone(cells)
}

// What must be true 1 and AC3's unit: a window started on a rig holding an
// inbox.yaml (a whole one, and a damaged one an older release would have set
// aside) draws, answers a list, a send and do-not-disturb, and leaves the
// folder as it found it. The twin: a rig without one gets none.
func TestInboxGone_ARigsInboxFileIsLeftAlone(t *testing.T) {
	for name, content := range map[string]string{"whole": inboxYAML, "damaged": "items: [unclosed\n\x00"} {
		t.Run(name, func(t *testing.T) {
			root := tempProjectRoot(t)
			if err := os.WriteFile(filepath.Join(root, ".initech", "inbox.yaml"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			before := rigFolderState(t, root)
			tu, _ := newTestTUIWithScreen("eng1", "eng2")
			tu.projectRoot, tu.projectName = root, "demo" // the badge draws only with a project
			tu.render()
			for _, req := range []IPCRequest{
				{Action: "list", Detail: true},
				{Action: "dnd", Target: "eng1", Text: "on"},
				{Action: "dnd", Target: "eng1", Text: "off"},
			} {
				dispatchIPC(tu, &fakeConn{}, req, nil)
			}
			tu.render()
			if after := rigFolderState(t, root); !reflect.DeepEqual(after, before) {
				t.Errorf(".initech changed:\n before %v\n after  %v", before, after)
			}
		})
	}
	t.Run("none", func(t *testing.T) {
		root := tempProjectRoot(t)
		tu, _ := newTestTUIWithScreen("eng1")
		tu.projectRoot, tu.projectName = root, "demo" // the badge draws only with a project
		tu.render()
		dispatchIPC(tu, &fakeConn{}, IPCRequest{Action: "list", Detail: true}, nil)
		if _, err := os.Stat(filepath.Join(root, ".initech", "inbox.yaml")); !os.IsNotExist(err) {
			t.Errorf("a rig with no inbox.yaml has one now: %v", err)
		}
	})
}

// AC2: post and its subactions are actions no window knows, and the window
// still serves the next request.
func TestInboxGone_PostIsAnUnknownActionToTheWindow(t *testing.T) {
	tu := newTestTUI(newEmuPane("eng1", 80, 24))
	for _, action := range []string{"post", "post_check", "post_mine", "post_withdraw"} {
		conn := &fakeConn{}
		dispatchIPC(tu, conn, IPCRequest{Action: action, Text: "hello"}, nil)
		if resp := fakeConnReply(t, conn); resp.OK || resp.Error != `unknown action "`+action+`"` {
			t.Errorf("%s: %+v, want unknown action", action, resp)
		}
	}
	conn := &fakeConn{}
	dispatchIPC(tu, conn, IPCRequest{Action: "list"}, nil)
	if resp := fakeConnReply(t, conn); !resp.OK { // the twin: a known action still answers
		t.Errorf("list after post: %+v", resp)
	}
}

// AC2: the serve daemon does not know post either.
func TestInboxGone_PostIsAnUnknownActionToTheDaemon(t *testing.T) {
	d := &Daemon{headless: true, panes: []*Pane{newEmuPane("eng1", 80, 24)}}
	conn := &fakeConn{}
	dispatchIPC(d, conn, IPCRequest{Action: "post", Text: "hello"}, nil)
	if resp := fakeConnReply(t, conn); resp.OK || resp.Error != `unknown action "post"` {
		t.Errorf("post: %+v, want unknown action", resp)
	}
}

// AC1: Option+I does nothing: the frame after it is the frame before.
func TestInboxGone_OptionIDoesNothing(t *testing.T) {
	holdStatusClock(t)
	tu, s := newTestTUIWithScreen("eng1")
	tu.projectRoot, tu.projectName = tempProjectRoot(t), "demo"
	tu.render()
	before := frameCells(s)
	tu.handleKey(tcell.NewEventKey(tcell.KeyRune, 'i', tcell.ModAlt))
	tu.render()
	if after := frameCells(s); !reflect.DeepEqual(after, before) {
		t.Errorf("Option+I changed the frame")
	}
	// The twin: a bound chord (Option+a, the agents modal) does change it.
	tu.handleKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModAlt))
	tu.render()
	if after := frameCells(s); reflect.DeepEqual(after, before) {
		t.Errorf("Option+a did not change the frame: the check cannot see a change")
	}
}

// AC1 and What must be true 3: no badge. A rig whose inbox.yaml holds an
// unread item draws the frame a rig without one draws.
func TestInboxGone_NoBadgeForAnUnreadItem(t *testing.T) {
	holdStatusClock(t)
	frame := func(withInbox bool) string {
		root := tempProjectRoot(t)
		if withInbox {
			if err := os.WriteFile(filepath.Join(root, ".initech", "inbox.yaml"), []byte(inboxYAML), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		tu, s := newTestTUIWithScreen("eng1")
		tu.projectRoot, tu.projectName = root, "demo" // the badge draws only with a project
		tu.render()
		cells, w, _ := s.GetContents()
		var b strings.Builder
		for i, c := range cells {
			if i > 0 && i%w == 0 {
				b.WriteByte('\n')
			}
			if len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			} else {
				b.WriteByte(' ')
			}
		}
		return b.String()
	}
	if with, without := frame(true), frame(false); with != without {
		t.Errorf("an unread inbox item changes the frame:\nwith:\n%s\nwithout:\n%s", with, without)
	}
}
