package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ini-jxjl.1: the identity check that cron, watch and strip share answers in
// words that name no command, never initech post, which is going away. Each
// command here goes over a real socket to the real handler and the real
// identity check, from this test process, which runs in no agent's pane.

// refusalOverSocket sends req to tu over a real unix socket and returns the
// reply.
func refusalOverSocket(t *testing.T, tu *TUI, req IPCRequest) IPCResponse {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("identity over a unix socket: Windows refuses before reading the peer")
	}
	dir, err := os.MkdirTemp("", "cid") // short: a socket path has a length limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	cleanup, err := tu.startIPC(filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	conn, err := net.Dial("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// A hang guard, not a timing: the reply comes as soon as the handler answers.
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("no reply: %v", err)
	}
	var resp IPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("reply %q: %v", line, err)
	}
	return resp
}

// refusedWithout asserts resp is a refusal (the CLI exits 1 on it, as
// before) carrying want and never "post".
func refusedWithout(t *testing.T, what string, resp IPCResponse, want string) {
	t.Helper()
	if resp.OK {
		t.Fatalf("%s outside a pane was accepted: %+v", what, resp)
	}
	if strings.Contains(strings.ToLower(resp.Error), "post") {
		t.Errorf("%s outside a pane names post: %q", what, resp.Error)
	}
	if !strings.Contains(resp.Error, want) {
		t.Errorf("%s outside a pane: %q, want it to say %q", what, resp.Error, want)
	}
}

// AC1, red first: cron add passes the identity check's own words through.
func TestConnIdentity_CronAddOutsideAPaneNamesNoPost(t *testing.T) {
	tu, _ := cronTUI(t) // the schedule's identity is the real check
	resp := refusalOverSocket(t, tu, cronAddReq("", CronAddRequest{To: "qa1", At: "09:30 weekdays", Message: "standup"}))
	refusedWithout(t, "cron add", resp, "run it from inside that agent's pane; could not identify your pane")
}

// AC1, red first: cron remove passes them through too.
func TestConnIdentity_CronRemoveOutsideAPaneNamesNoPost(t *testing.T) {
	tu, _ := cronTUI(t)
	resp := refusalOverSocket(t, tu, IPCRequest{Action: "cron_remove", Target: "s1"})
	refusedWithout(t, "cron remove", resp, "removing a job needs to know which agent asks: this command acts for the agent whose pane runs it")
}

// AC1, a guard: watch refuses in its own words.
func TestConnIdentity_WatchSetOutsideAPaneNamesNoPost(t *testing.T) {
	tu := watchTUI(t) // watchIdentify stays the real check
	resp := refusalOverSocket(t, tu, setIPC("", beadReq("ini-a", "read it")))
	refusedWithout(t, "watch set", resp, "run initech watch from inside your agent's pane")
}

// AC1, a guard: strip refuses in its own words.
func TestConnIdentity_StripOutsideAPaneNamesNoPost(t *testing.T) {
	tu := newTestTUI(newEmuPane("eng1", 80, 24))
	tu.strips = loadStripBook(filepath.Join(t.TempDir(), "strip.yaml"), time.Now())
	resp := refusalOverSocket(t, tu, IPCRequest{Action: "strip", Target: "eng1", Items: []string{"a"}})
	refusedWithout(t, "strip", resp, "initech strip sets only the strip of the pane it runs in")
}

// The platform refusals name no command either.
func TestConnIdentity_PlatformRefusalsNameNoPost(t *testing.T) {
	for platform, want := range map[string]string{
		"windows": "this command is not available on Windows in this release: initech cannot tell which agent's pane a call comes from over this transport",
		"plan9":   "this command is not available on plan9: initech cannot tell which agent pane a call comes from there",
	} {
		err := connPlatformError(platform)
		if err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %q", platform, err, want)
		}
	}
	for _, platform := range []string{"darwin", "linux"} { // the twin: these identify
		if err := connPlatformError(platform); err != nil {
			t.Errorf("%s refused: %v", platform, err)
		}
	}
}
