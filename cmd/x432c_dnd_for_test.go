package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-x432.3: initech dnd on --for <duration> sends the timer in seconds,
// prints the TUI's notices on stderr once each, refuses --for 0, a negative
// value and --for with off, and warns when the running initech did not take
// the timer.

// x432cRun runs initech args against a TUI that records each request and
// answers resp.
func x432cRun(t *testing.T, resp tui.IPCResponse, args ...string) (stdout, stderr string, err error, got []tui.IPCRequest) {
	t.Helper()
	skipWindows(t)
	sockPath := filepath.Join("/tmp", fmt.Sprintf("initech-x432c-%d-%d.sock", os.Getpid(), fakeIPCCounter.Add(1)))
	os.Remove(sockPath)
	ln, lerr := net.Listen("unix", sockPath)
	if lerr != nil {
		t.Fatal(lerr)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sockPath) })
	reqs := make(chan tui.IPCRequest, 4)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadBytes('\n')
			var req tui.IPCRequest
			json.Unmarshal(line, &req)
			reqs <- req
			data, _ := json.Marshal(resp)
			conn.Write(append(data, '\n'))
			conn.Close()
		}
	}()
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng2")
	dndAgent, dndFor = "", 0
	dndCmd.Flags().Lookup("for").Changed = false
	t.Cleanup(func() { dndAgent, dndFor = "", 0; dndCmd.Flags().Lookup("for").Changed = false })
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetArgs(args)
	defer rootCmd.SetArgs(nil)
	err = rootCmd.Execute()
	close(reqs)
	for r := range reqs {
		got = append(got, r)
	}
	return out.String(), errOut.String(), err, got
}

func TestDNDFor_TheTimerTravelsInSeconds(t *testing.T) {
	for _, c := range []struct {
		arg  string
		want int
	}{{"30m", 1800}, {"10s", 10}, {"2h", 7200}, {"1500ms", 2}} {
		_, _, err, reqs := x432cRun(t, tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2 until 15:00:00 (30m): x"}, "dnd", "on", "--for", c.arg)
		if err != nil || len(reqs) != 1 || reqs[0].ForSec != c.want || reqs[0].Text != "on" {
			t.Errorf("--for %s: %v %+v, want for_sec %d", c.arg, err, reqs, c.want)
		}
	}
}

// AC3 and AC6: the TUI's notices are printed on stderr, once each.
func TestDNDFor_NoticesGoToStderrOnce(t *testing.T) {
	stdout, stderr, err, _ := x432cRun(t, tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2 until 15:00:00 (1h): x",
		Notices: []string{"do-not-disturb capped at 1h"}}, "dnd", "on", "--for", "2h")
	if err != nil || strings.Count(stderr, "do-not-disturb capped at 1h") != 1 || strings.Contains(stdout, "capped") {
		t.Errorf("stdout %q stderr %q err %v", stdout, stderr, err)
	}
	_, stderr, _, _ = x432cRun(t, tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2: x",
		Notices: []string{"pane is idle; do-not-disturb ends at turn end"}}, "dnd", "on")
	if strings.TrimSpace(stderr) != "pane is idle; do-not-disturb ends at turn end" {
		t.Errorf("idle notice: stderr %q", stderr)
	}
}

// AC6: --for 0, a negative value, and --for with off are refused before
// anything is sent.
func TestDNDFor_ZeroNegativeAndOffAreRefused(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"dnd", "on", "--for", "0"}, "--for must be more than 0"},
		{[]string{"dnd", "on", "--for=-5m"}, "--for must be more than 0"},
		{[]string{"dnd", "off", "--for", "5m"}, "--for goes with dnd on"},
	} {
		_, _, err, reqs := x432cRun(t, tui.IPCResponse{OK: true, Data: "x"}, c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) || len(reqs) != 0 {
			t.Errorf("%v: err %v, %d requests sent; want %q and nothing sent", c.args, err, len(reqs), c.want)
		}
	}
}

// An older initech ignores the timer: its reply names none, and dnd says the
// flag holds until dnd off.
func TestDNDFor_AnOlderTUIThatDropsTheTimerIsNamed(t *testing.T) {
	_, stderr, err, _ := x432cRun(t, tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2: messages from other agents wait until initech dnd off"},
		"dnd", "on", "--for", "10m")
	if err != nil || !strings.Contains(stderr, "the running initech does not know --for; do-not-disturb holds until initech dnd off") {
		t.Errorf("stderr %q err %v", stderr, err)
	}
	_, stderr, _, _ = x432cRun(t, tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2 until 15:00:00 (10m): x"}, "dnd", "on", "--for", "10m")
	if stderr != "" {
		t.Errorf("a timer the TUI took drew %q", stderr)
	}
}
