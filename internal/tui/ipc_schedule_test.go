package tui

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/schedule"
)

// ini-6ib: initech cron add, list and remove through the real IPC handler, and
// window 1's delivery through the real send path.

func cronTUI(t *testing.T, panes ...*Pane) (*TUI, *fakeClock) {
	t.Helper()
	tui := newTestTUI(panes...)
	clk := &fakeClock{t: clockAt(4, 9, 0)}
	tui.sched = newRigScheduler(t.TempDir(), tui, clk.now)
	return tui, clk
}

func cronIPC(t *testing.T, tui *TUI, req IPCRequest) IPCResponse {
	t.Helper()
	conn := &fakeConn{}
	// The connection's identity is the request's From here: the stand-in for
	// identifyPostConnection, which needs a real pane process behind the socket.
	tui.sched.identify = func(net.Conn) (string, error) {
		if req.From == "" {
			return "", errors.New("not inside an agent's pane")
		}
		return req.From, nil
	}
	tui.handleIPCCron(conn, req)
	var resp IPCResponse
	if err := json.Unmarshal(conn.written[:conn.findNewline()], &resp); err != nil {
		t.Fatalf("unparseable reply %q: %v", conn.written, err)
	}
	return resp
}

func cronAddReq(from string, add CronAddRequest) IPCRequest {
	body, _ := json.Marshal(add)
	return IPCRequest{Action: "cron_add", From: from, Text: string(body)}
}

// TestCron_AddWritesEveryFieldAndListShowsTheOwnerAndNextRun is AC1's store
// half, red at base (no such action).
func TestCron_AddWritesEveryFieldAndListShowsTheOwnerAndNextRun(t *testing.T) {
	tui, _ := cronTUI(t)
	resp := cronIPC(t, tui, cronAddReq("eng1", CronAddRequest{To: "qa1", At: "09:30 weekdays", Message: "standup"}))
	if !resp.OK || !strings.Contains(resp.Data, "added s1, owned by eng1") {
		t.Fatalf("add answered %+v", resp)
	}
	f, err := schedule.Load(tui.sched.path)
	if err != nil {
		t.Fatal(err)
	}
	j := f.Jobs[0]
	if j.ID != "s1" || j.Owner != "eng1" || j.To != "qa1" || j.At != "09:30 weekdays" ||
		j.Message != "standup" || !j.Created.Equal(clockAt(4, 9, 0)) {
		t.Fatalf("the file holds %+v", j)
	}
	list := cronIPC(t, tui, IPCRequest{Action: "cron_list"})
	var rows []CronListing
	if err := json.Unmarshal([]byte(list.Data), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("list: %v %q", err, list.Data)
	}
	if rows[0].Owner != "eng1" || !rows[0].Next.Equal(clockAt(4, 9, 30)) {
		t.Fatalf("list row %+v, want owner eng1 and next 09:30", rows[0])
	}
}

func TestCron_AddRefusesWhatIsNotAJob(t *testing.T) {
	tui, _ := cronTUI(t)
	for name, add := range map[string]CronAddRequest{
		"no pane":    {At: "09:30", Message: "x"},
		"too often":  {To: "qa1", Every: "10s", Message: "x"},
		"two lines":  {To: "qa1", Every: "5m", Command: "ls\nrm -rf x"},
		"both kinds": {To: "qa1", Every: "5m", Message: "x", Command: "ls"},
		"bad time":   {To: "qa1", At: "9:30", Message: "x"},
	} {
		if resp := cronIPC(t, tui, cronAddReq("eng1", add)); resp.OK {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(tui.sched.snapshot()) != 0 {
		t.Fatal("a refused job was kept")
	}
}

// TestCron_RemoveIsTheOwnersOrSupers: another agent is refused with the owner
// named.
func TestCron_RemoveIsTheOwnersOrSupers(t *testing.T) {
	tui, _ := cronTUI(t)
	cronIPC(t, tui, cronAddReq("eng1", CronAddRequest{To: "qa1", Every: "5m", Message: "x"}))
	cronIPC(t, tui, cronAddReq("eng1", CronAddRequest{To: "qa1", Every: "5m", Message: "y"}))
	if resp := cronIPC(t, tui, IPCRequest{Action: "cron_remove", From: "eng2", Target: "s1"}); resp.OK ||
		!strings.Contains(resp.Error, "belongs to eng1") {
		t.Fatalf("eng2 removing eng1's job: %+v", resp)
	}
	if resp := cronIPC(t, tui, IPCRequest{Action: "cron_remove", From: "eng1", Target: "s1"}); !resp.OK {
		t.Fatalf("the owner: %+v", resp)
	}
	if resp := cronIPC(t, tui, IPCRequest{Action: "cron_remove", From: "super", Target: "s2"}); !resp.OK {
		t.Fatalf("super: %+v", resp)
	}
	if n := len(tui.sched.snapshot()); n != 0 {
		t.Fatalf("%d jobs left", n)
	}
}

// TestCron_AWindowWithoutASchedule: a viewer has no scheduler and says so.
func TestCron_AWindowWithoutASchedule(t *testing.T) {
	tui := newTestTUI()
	conn := &fakeConn{}
	tui.handleIPCCron(conn, IPCRequest{Action: "cron_list"})
	if !strings.Contains(string(conn.written), "window 1") {
		t.Fatalf("a window with no schedule answered %q", conn.written)
	}
}

// TestCron_DeliveryNamesWhyItCouldNot: no such pane, or a pane not running.
func TestCron_DeliveryNamesWhyItCouldNot(t *testing.T) {
	dead := newResourceTestPane("eng9", false, StateDead, "", time.Now())
	tui, _ := cronTUI(t, dead)
	if ok, why := tui.deliver("ghost", "hi", "eng1"); ok || !strings.Contains(why, `no pane named "ghost"`) {
		t.Fatalf("no pane: ok %v why %q", ok, why)
	}
	if ok, why := tui.deliver("eng9", "hi", "eng1"); ok || !strings.Contains(why, "not running") {
		t.Fatalf("a dead pane: ok %v why %q", ok, why)
	}
}

// TestCron_AnOwnerMustBeAnAgentOfThisRig.
func TestCron_AnOwnerMustBeAnAgentOfThisRig(t *testing.T) {
	tui, _ := cronTUI(t)
	tui.sched.isAgent = func(n string) bool { return n == "eng1" }
	if resp := cronIPC(t, tui, cronAddReq("mallory", CronAddRequest{To: "qa1", Every: "5m", Message: "x"})); resp.OK ||
		!strings.Contains(resp.Error, "not an agent of this rig") {
		t.Fatalf("a non-agent added a job: %+v", resp)
	}
}

// TestCron_TheOwnerIsTheConnectionsAgentNotWhatTheRequestSays: a request that
// claims to be super from eng2's pane is eng2.
func TestCron_TheOwnerIsTheConnectionsAgentNotWhatTheRequestSays(t *testing.T) {
	tui, _ := cronTUI(t)
	cronIPC(t, tui, cronAddReq("eng1", CronAddRequest{To: "qa1", Every: "5m", Message: "x"}))
	conn := &fakeConn{}
	tui.sched.identify = func(net.Conn) (string, error) { return "eng2", nil }
	tui.handleIPCCron(conn, IPCRequest{Action: "cron_remove", From: "super", Target: "s1"})
	if !strings.Contains(string(conn.written), "belongs to eng1") {
		t.Fatalf("a request saying super from eng2's pane: %q", conn.written)
	}
	conn = &fakeConn{}
	body, _ := json.Marshal(CronAddRequest{To: "qa1", Every: "5m", Message: "y"})
	tui.handleIPCCron(conn, IPCRequest{Action: "cron_add", From: "super", Text: string(body)})
	if !strings.Contains(string(conn.written), "owned by eng2") {
		t.Fatalf("an add saying super from eng2's pane: %q", conn.written)
	}
}

// TestCron_ARequestFromOutsideAnyPaneIsRefused.
func TestCron_ARequestFromOutsideAnyPaneIsRefused(t *testing.T) {
	tui, _ := cronTUI(t)
	resp := cronIPC(t, tui, cronAddReq("", CronAddRequest{To: "qa1", Every: "5m", Message: "x"}))
	if resp.OK || !strings.Contains(resp.Error, "owned by the agent that adds it") {
		t.Fatalf("an add from outside any pane: %+v", resp)
	}
}

// ── ini-7k3n: a quiet command job ──────────────────────────────────────

// TestQuietCron_AQuietRunWakesNoSuspendedPane is AC1 through window 1's real
// delivery: a suspended target is neither queued for nor woken by a quiet run,
// while the same empty run of a job without quiet queues and wakes it.
func TestQuietCron_AQuietRunWakesNoSuspendedPane(t *testing.T) {
	for _, c := range []struct {
		quiet         bool
		queued, wakes int
	}{{true, 0, 0}, {false, 1, 1}} {
		wakes := 0
		p := &Pane{name: "eng2", suspended: true, onSuspendedMessage: func(*Pane) { wakes++ }}
		tui, clk := cronTUI(t, p)
		f := &schedule.File{}
		if _, err := f.Add(schedule.Job{Owner: "qa1", To: "eng2", Every: "1m", Command: "true", Quiet: c.quiet, Created: clk.now()}); err != nil {
			t.Fatal(err)
		}
		if err := schedule.Save(tui.sched.path, f); err != nil {
			t.Fatal(err)
		}
		tui.sched.step(clk, clockAt(4, 9, 1))
		if n := p.QueuedMessageCount(); n != c.queued || wakes != c.wakes {
			t.Fatalf("quiet %v: queued %d and woke %d times, want %d and %d", c.quiet, n, wakes, c.queued, c.wakes)
		}
	}
}

// TestQuietCron_AddStoresQuietAndSaysSo: the stored job is quiet and the reply
// names it, which is what initech cron add checks (rule 7).
func TestQuietCron_AddStoresQuietAndSaysSo(t *testing.T) {
	tui, _ := cronTUI(t)
	resp := cronIPC(t, tui, cronAddReq("eng1", CronAddRequest{To: "qa1", Every: "5m", Command: "bd ready", Quiet: true}))
	if !resp.OK || resp.Data != "added s1, owned by eng1, quiet" {
		t.Fatalf("add answered %+v", resp)
	}
	f, err := schedule.Load(tui.sched.path)
	if err != nil || len(f.Jobs) != 1 || !f.Jobs[0].Quiet {
		t.Fatalf("stored %+v %v, want a quiet job", f, err)
	}
	plain := cronIPC(t, tui, cronAddReq("eng1", CronAddRequest{To: "qa1", Every: "5m", Command: "bd ready"}))
	if plain.Data != "added s2, owned by eng1" {
		t.Fatalf("a plain add answered %q", plain.Data)
	}
}

// TestQuietCron_QuietOnAMessageIsRefused is AC5 at window 1.
func TestQuietCron_QuietOnAMessageIsRefused(t *testing.T) {
	tui, _ := cronTUI(t)
	resp := cronIPC(t, tui, cronAddReq("eng1", CronAddRequest{To: "qa1", Every: "5m", Message: "hi", Quiet: true}))
	if resp.OK || !strings.Contains(resp.Error, "--quiet") || !strings.Contains(resp.Error, "--message") {
		t.Fatalf("answered %+v, want a refusal naming --quiet and --message", resp)
	}
}
