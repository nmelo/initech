package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ── ini-1q3x.1: assign folds in, refuses taking another's bead without --move ──

// q3xBead is what the fake bd says about one bead.
type q3xBead struct{ title, assignee, status string }

// q3xRig is a fake bd plus a fake TUI that records every IPC request and
// answers list with the panes it was given.
type q3xRig struct {
	mu         sync.Mutex
	reqs       []tui.IPCRequest
	dispatched []string
	// answers overrides the reply for an action (default: OK).
	answers map[string]tui.IPCResponse
}

// answer makes the fake TUI reply resp to every request with action.
func (r *q3xRig) answer(action string, resp tui.IPCResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.answers == nil {
		r.answers = map[string]tui.IPCResponse{}
	}
	r.answers[action] = resp
}

// q3xSetup wires the fake bd and a fake TUI socket. panes is the list reply;
// nil answers list with no data at all (a TUI that cannot be read).
func q3xSetup(t *testing.T, beads map[string]q3xBead, listData string) *q3xRig {
	t.Helper()
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)
	assignMove = false
	t.Cleanup(func() { assignMove = false })
	t.Setenv("INITECH_AGENT", "super")

	rig := &q3xRig{}
	bdShowBeadFn = func(id string) (string, string, string, error) {
		b, ok := beads[id]
		if !ok {
			return "", "", "", fmt.Errorf("bead %s not found", id)
		}
		return b.title, b.assignee, b.status, nil
	}
	bdDispatchFn = func(id, agent, status string, record bool) error {
		rig.mu.Lock()
		defer rig.mu.Unlock()
		rig.dispatched = append(rig.dispatched, id+">"+agent+":"+status)
		return nil
	}

	n := fakeIPCCounter.Add(1)
	sock := filepath.Join("/tmp", fmt.Sprintf("initech-q3x-%d-%d.sock", os.Getpid(), n))
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sock) })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			var req tui.IPCRequest
			if json.NewDecoder(conn).Decode(&req) == nil {
				rig.mu.Lock()
				rig.reqs = append(rig.reqs, req)
				rig.mu.Unlock()
			}
			resp := tui.IPCResponse{OK: true}
			if req.Action == "send" {
				resp.Delivery = tui.DeliveryDelivered // today's TUI (ini-69fy)
			}
			if req.Action == "list" {
				resp.Data = listData
			}
			rig.mu.Lock()
			if a, ok := rig.answers[req.Action]; ok {
				resp = a
			}
			rig.mu.Unlock()
			data, _ := json.Marshal(resp)
			conn.Write(append(data, '\n'))
			conn.Close()
		}
	}()
	t.Setenv("INITECH_SOCKET", sock)
	return rig
}

// q3xList encodes a list reply from real tui.PaneInfo values, so the field's
// own encoding (its JSON tag) is what assign reads.
func q3xList(t *testing.T, panes ...tui.PaneInfo) string {
	t.Helper()
	data, err := json.Marshal(panes)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func q3xRun(t *testing.T, args ...string) (stderr string, err error) {
	t.Helper()
	var errBuf bytes.Buffer
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs(append([]string{"assign"}, args...))
	defer rootCmd.SetArgs(nil)
	err = rootCmd.Execute()
	return errBuf.String(), err
}

// of returns the recorded requests with the given action (and target, if set).
func (r *q3xRig) of(action, target string) []tui.IPCRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []tui.IPCRequest
	for _, q := range r.reqs {
		if q.Action == action && (target == "" || q.Target == target) {
			out = append(out, q)
		}
	}
	return out
}

func (r *q3xRig) writes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.dispatched...)
}

// Criterion 1: a bead already in progress with the target makes no bd write,
// its message is delivered, and the ribbon gains it while keeping the bead it
// already showed.
func TestAssign_FoldInMakesNoBdWriteAndAddsToTheRibbon(t *testing.T) {
	listData := q3xList(t, tui.PaneInfo{Name: "eng3", Beads: []string{"ini-other"}})
	rig2 := q3xSetup(t, map[string]q3xBead{"ini-x": {"Fix it", "eng3", "in_progress"}}, listData)
	if _, err := q3xRun(t, "eng3", "ini-x", "-m", "fold ini-y in"); err != nil {
		t.Fatalf("fold-in failed: %v", err)
	}
	if w := rig2.writes(); len(w) != 0 {
		t.Errorf("fold-in wrote bd: %v; a bead already with the agent is left as it is", w)
	}
	beads := rig2.of("bead", "eng3")
	if len(beads) != 1 || beads[0].Text != "ini-other,ini-x" {
		t.Errorf("ribbon writes = %+v, want one write of ini-other,ini-x (added, the other kept)", beads)
	}
	sends := rig2.of("send", "eng3")
	if len(sends) != 1 || !strings.Contains(sends[0].Text, "fold ini-y in") {
		t.Errorf("message to eng3 = %+v, want the fold-in message delivered", sends)
	}
	ev := rig2.of("emit_event", "eng3")
	if len(ev) != 1 || !strings.Contains(ev[0].Text, "bead_assigned|fold-in") {
		t.Errorf("events = %+v, want one bead_assigned saying fold-in", ev)
	}
}

// pm's mutation: a TUI that drops the beads field on an EMPTY ribbon looks
// like an older TUI, and the fold-in would leave the ribbon blank. An empty
// ribbon must be told apart and get the bead, with its title.
func TestAssign_FoldInOnAnEmptyRibbonSetsTheBeadWithItsTitle(t *testing.T) {
	listData := q3xList(t, tui.PaneInfo{Name: "eng3", Beads: []string{}})
	rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"Fix it", "eng3", "in_progress"}}, listData)
	stderr, err := q3xRun(t, "eng3", "ini-x")
	if err != nil {
		t.Fatalf("fold-in failed: %v", err)
	}
	beads := rig.of("bead", "eng3")
	if len(beads) != 1 || beads[0].Text != "ini-x\tFix it" {
		t.Errorf("ribbon writes = %+v, want ini-x with its title; stderr: %s", beads, stderr)
	}
}

func TestAssign_FoldInAlreadyOnTheRibbonWritesNothing(t *testing.T) {
	listData := q3xList(t, tui.PaneInfo{Name: "eng3", Beads: []string{"ini-x", "ini-other"}})
	rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"Fix it", "eng3", "in_progress"}}, listData)
	if _, err := q3xRun(t, "eng3", "ini-x"); err != nil {
		t.Fatalf("fold-in failed: %v", err)
	}
	if beads := rig.of("bead", ""); len(beads) != 0 {
		t.Errorf("ribbon writes = %+v, want none: the ribbon already shows the bead", beads)
	}
	if len(rig.of("send", "eng3")) != 1 {
		t.Error("the fold-in message was not delivered")
	}
}

// Older TUI (no beads field): the fold-in writes no ribbon, warns, and the
// message still goes.
func TestAssign_FoldInWithAnOlderTUILeavesTheRibbonAndWarns(t *testing.T) {
	rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"Fix it", "eng3", "in_progress"}},
		`[{"name":"eng3","activity":"idle","alive":true,"visible":true}]`)
	stderr, err := q3xRun(t, "eng3", "ini-x")
	if err != nil {
		t.Fatalf("fold-in failed: %v", err)
	}
	if beads := rig.of("bead", ""); len(beads) != 0 {
		t.Errorf("ribbon writes = %+v, want none against a TUI that does not report ribbons", beads)
	}
	if !strings.Contains(stderr, "eng3's ribbon left as it was") {
		t.Errorf("stderr = %q, want the ribbon warning", stderr)
	}
	if len(rig.of("send", "eng3")) != 1 {
		t.Error("the message was not delivered")
	}
}

// Criterion 2, refused: another agent's in-progress bead is not taken.
func TestAssign_AnotherAgentsActiveBeadIsRefusedWithoutMove(t *testing.T) {
	for _, tc := range []struct{ status, holder, target, want string }{
		{"in_progress", "eng5", "eng3", "ini-x is in progress with eng5; add --move to reassign"},
		// A QA-failed bead sent back while QA still holds it needs --move too.
		{"in_qa", "qa2", "eng3", "ini-x is in QA with qa2; add --move to reassign"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"Fix it", tc.holder, tc.status}}, "[]")
			stderr, err := q3xRun(t, tc.target, "ini-x")
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if w := rig.writes(); len(w) != 0 {
				t.Errorf("bd writes = %v, want none", w)
			}
			for _, a := range []string{"bead", "send", "emit_event"} {
				if got := rig.of(a, ""); len(got) != 0 {
					t.Errorf("%s requests = %+v, want none on a refusal", a, got)
				}
			}
			if stderr != "" {
				t.Errorf("stderr = %q; the refusal is the one line", stderr)
			}
		})
	}
}

// Criterion 2, --move: bd moves, the ribbons move, the holder is told.
func TestAssign_MoveReassignsMovesTheRibbonAndTellsTheHolder(t *testing.T) {
	listData := q3xList(t,
		tui.PaneInfo{Name: "eng3", Beads: []string{"ini-old"}},
		tui.PaneInfo{Name: "eng5", Beads: []string{"ini-x", "ini-keep"}})
	rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"Fix it", "eng5", "in_progress"}}, listData)
	if _, err := q3xRun(t, "eng3", "ini-x", "--move"); err != nil {
		t.Fatalf("move failed: %v", err)
	}
	if w := rig.writes(); len(w) != 1 || w[0] != "ini-x>eng3:in_progress" {
		t.Errorf("bd writes = %v, want ini-x to eng3 in_progress", w)
	}
	if b := rig.of("bead", "eng3"); len(b) != 1 || b[0].Text != "ini-x\tFix it" {
		t.Errorf("eng3 ribbon = %+v, want ini-x set as a dispatch sets it", b)
	}
	if b := rig.of("bead", "eng5"); len(b) != 1 || b[0].Text != "ini-keep" {
		t.Errorf("eng5 ribbon = %+v, want ini-x taken off and ini-keep kept", b)
	}
	notice := rig.of("send", "eng5")
	if len(notice) != 1 || notice[0].Text != "[from initech] ini-x moved to eng3 by super" {
		t.Errorf("notice to eng5 = %+v, want the one-line move notice", notice)
	}
	if len(rig.of("send", "eng3")) != 1 {
		t.Error("eng3 did not get the dispatch message")
	}
	ev := rig.of("emit_event", "eng3")
	if len(ev) != 1 || !strings.Contains(ev[0].Text, "moved from eng5 to eng3") {
		t.Errorf("events = %+v, want the move recorded", ev)
	}
}

func TestAssign_MoveOfTheHoldersOnlyBeadClearsItsRibbon(t *testing.T) {
	listData := q3xList(t, tui.PaneInfo{Name: "eng3", Beads: []string{}}, tui.PaneInfo{Name: "eng5", Beads: []string{"ini-x"}})
	rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"Fix it", "eng5", "in_progress"}}, listData)
	if _, err := q3xRun(t, "eng3", "ini-x", "--move"); err != nil {
		t.Fatalf("move failed: %v", err)
	}
	if b := rig.of("bead", "eng5"); len(b) != 1 || b[0].Text != "" {
		t.Errorf("eng5 ribbon = %+v, want cleared", b)
	}
}

// Older TUI and --move: the target is set as today, the holder's ribbon is
// left alone with a warning, and the holder is still told.
func TestAssign_MoveWithAnOlderTUISetsTheTargetAndWarnsAboutTheHolder(t *testing.T) {
	rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"Fix it", "eng5", "in_progress"}},
		`[{"name":"eng3"},{"name":"eng5"}]`)
	stderr, err := q3xRun(t, "eng3", "ini-x", "--move")
	if err != nil {
		t.Fatalf("move failed: %v", err)
	}
	if b := rig.of("bead", "eng3"); len(b) != 1 || b[0].Text != "ini-x\tFix it" {
		t.Errorf("eng3 ribbon = %+v, want set as today", b)
	}
	if b := rig.of("bead", "eng5"); len(b) != 0 {
		t.Errorf("eng5 ribbon = %+v, want left alone", b)
	}
	if !strings.Contains(stderr, "eng5's ribbon left as it was") {
		t.Errorf("stderr = %q, want the holder warning", stderr)
	}
	if len(rig.of("send", "eng5")) != 1 {
		t.Error("eng5 was not told")
	}
}

// --move on a bead nobody else holds is a plain dispatch with nobody to tell.
func TestAssign_MoveOnAnUnheldBeadIsAPlainDispatch(t *testing.T) {
	rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"Fix it", "", "open"}}, "[]")
	if _, err := q3xRun(t, "eng3", "ini-x", "--move"); err != nil {
		t.Fatalf("assign failed: %v", err)
	}
	if w := rig.writes(); len(w) != 1 {
		t.Errorf("bd writes = %v, want the dispatch", w)
	}
	if s := rig.of("send", ""); len(s) != 1 || s[0].Target != "eng3" {
		t.Errorf("sends = %+v, want only the dispatch to eng3", s)
	}
}

// Criterion 3.
func TestAssign_AClosedBeadIsRefused(t *testing.T) {
	rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"Fix it", "eng3", "closed"}}, "[]")
	_, err := q3xRun(t, "eng3", "ini-x", "--move")
	if err == nil || err.Error() != "ini-x is closed; follow-on work gets its own bead" {
		t.Fatalf("err = %v, want the closed refusal", err)
	}
	if w := rig.writes(); len(w) != 0 {
		t.Errorf("bd writes = %v, want none", w)
	}
	if s := rig.of("send", ""); len(s) != 0 {
		t.Errorf("sends = %+v, want none", s)
	}
}

// Criterion 4: every other state dispatches exactly as before.
func TestAssign_EveryOtherStateDispatchesAsBefore(t *testing.T) {
	for _, b := range []q3xBead{
		{"T", "", "open"},
		{"T", "eng5", "open"},
		{"T", "eng5", "ready_for_qa"},
		{"T", "eng5", "qa_passed"},
		{"T", "eng5", "ready_to_ship"},
		{"T", "", "in_progress"},
		{"T", "eng3", "ready_for_qa"},
		// with the target but not in the status the table writes for it
		{"T", "eng3", "in_qa"},
	} {
		t.Run(b.assignee+"/"+b.status, func(t *testing.T) {
			rig := q3xSetup(t, map[string]q3xBead{"ini-x": b}, "[]")
			if _, err := q3xRun(t, "eng3", "ini-x"); err != nil {
				t.Fatalf("assign failed: %v", err)
			}
			if w := rig.writes(); len(w) != 1 || w[0] != "ini-x>eng3:in_progress" {
				t.Errorf("bd writes = %v, want the dispatch write", w)
			}
			if bd := rig.of("bead", "eng3"); len(bd) != 1 || bd[0].Text != "ini-x\tT" {
				t.Errorf("ribbon = %+v, want replaced as today", bd)
			}
			if len(rig.of("list", "")) != 0 {
				t.Error("a plain dispatch read the ribbons; it has no need to")
			}
		})
	}
}

// A validator's fold-in is the in_qa status the table writes for it.
func TestAssign_ValidatorFoldInIsInQA(t *testing.T) {
	rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"T", "qa2", "in_qa"}}, q3xList(t, tui.PaneInfo{Name: "qa2", Beads: []string{"ini-x"}}))
	if _, err := q3xRun(t, "qa2", "ini-x"); err != nil {
		t.Fatalf("assign failed: %v", err)
	}
	if w := rig.writes(); len(w) != 0 {
		t.Errorf("bd writes = %v, want none for a validator fold-in", w)
	}
}

// Several beads: each gets its rule; the refused one is listed as a failure;
// a call with a dispatch in it replaces the ribbon with its beads, as today.
func TestAssign_MixedCallAppliesEachRule(t *testing.T) {
	rig := q3xSetup(t, map[string]q3xBead{
		"ini-f": {"Fold", "eng3", "in_progress"},
		"ini-r": {"Held", "eng5", "in_progress"},
		"ini-c": {"Shut", "", "closed"},
		"ini-d": {"New", "", "open"},
	}, "[]")
	stderr, err := q3xRun(t, "eng3", "ini-f", "ini-r", "ini-c", "ini-d")
	if err != nil {
		t.Fatalf("mixed call failed: %v", err)
	}
	if w := rig.writes(); len(w) != 1 || w[0] != "ini-d>eng3:in_progress" {
		t.Errorf("bd writes = %v, want only ini-d", w)
	}
	for _, want := range []string{
		"ini-r is in progress with eng5; add --move to reassign",
		"ini-c is closed; follow-on work gets its own bead",
		"assigned 2 bead(s) to eng3: ini-f, ini-d (failed: ini-r, ini-c)",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if b := rig.of("bead", "eng3"); len(b) != 1 || b[0].Text != "ini-f,ini-d" {
		t.Errorf("ribbon = %+v, want ini-f,ini-d", b)
	}
}

func TestAssign_AllRefusedExitsNonZero(t *testing.T) {
	rig := q3xSetup(t, map[string]q3xBead{
		"ini-r": {"Held", "eng5", "in_progress"},
		"ini-c": {"Shut", "", "closed"},
	}, "[]")
	stderr, err := q3xRun(t, "eng3", "ini-r", "ini-c")
	if err == nil {
		t.Fatal("all refused, want an error")
	}
	if !strings.Contains(stderr, "ini-r is in progress with eng5") || !strings.Contains(stderr, "ini-c is closed") {
		t.Errorf("stderr = %q, want both reasons", stderr)
	}
	if s := rig.of("send", ""); len(s) != 0 {
		t.Errorf("sends = %+v, want none", s)
	}
}

// Feed it nothing: bd gives no status and no assignee -> dispatch as today.
func TestAssign_NoStatusOrAssigneeDispatchesAsBefore(t *testing.T) {
	rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"T", "", ""}}, "")
	if _, err := q3xRun(t, "eng3", "ini-x"); err != nil {
		t.Fatalf("assign failed: %v", err)
	}
	if w := rig.writes(); len(w) != 1 {
		t.Errorf("bd writes = %v, want the dispatch", w)
	}
}

// Feed it nothing on the TUI side: a list reply with no panes, or no data.
func TestAssign_FoldInWithAnEmptyListReplyWritesNoRibbon(t *testing.T) {
	for name, data := range map[string]string{"no panes": "[]", "no data": ""} {
		t.Run(name, func(t *testing.T) {
			rig := q3xSetup(t, map[string]q3xBead{"ini-x": {"T", "eng3", "in_progress"}}, data)
			stderr, err := q3xRun(t, "eng3", "ini-x")
			if err != nil {
				t.Fatalf("assign failed: %v", err)
			}
			if b := rig.of("bead", ""); len(b) != 0 {
				t.Errorf("ribbon writes = %+v, want none", b)
			}
			if !strings.Contains(stderr, "eng3") || !strings.Contains(stderr, "warning") {
				t.Errorf("stderr = %q, want a ribbon warning", stderr)
			}
			if len(rig.of("send", "eng3")) != 1 {
				t.Error("the message was not delivered")
			}
		})
	}
}

func TestAssignHelp_ShowsTheDispatchFoldInAndMoveExamples(t *testing.T) {
	for _, want := range []string{
		"initech assign eng1 ini-abc ",
		`-m "fold ini-x in"`,
		"--move",
	} {
		if !strings.Contains(assignCmd.Long, want) {
			t.Errorf("assign help missing %q", want)
		}
	}
}
