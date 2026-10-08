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
	"time"

	"github.com/nmelo/initech/internal/tui"
)

// i35wHang as a response's Error makes the fake hold the connection and never
// answer, the way a TUI still waiting on a booting agent looks to the client.
const i35wHang = "__hang__"

// i35wActionIPC is a fake TUI socket that answers by action, so assign's
// preflight and its dispatch send can be told apart. Unlisted actions get OK.
func i35wActionIPC(t *testing.T, byAction map[string]tui.IPCResponse) string {
	t.Helper()
	n := fakeIPCCounter.Add(1)
	sockPath := filepath.Join("/tmp", fmt.Sprintf("initech-i35w-%d-%d.sock", os.Getpid(), n))
	os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done); ln.Close(); os.Remove(sockPath) })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			var req tui.IPCRequest
			sc := bufio.NewScanner(conn)
			if sc.Scan() {
				_ = json.Unmarshal(sc.Bytes(), &req)
			}
			resp, ok := byAction[req.Action]
			if !ok {
				resp = tui.IPCResponse{OK: true}
			}
			if resp.Error == i35wHang {
				<-done // a TUI that never answers in time
				conn.Close()
				continue
			}
			data, _ := json.Marshal(resp)
			conn.Write(append(data, '\n'))
			conn.Close()
		}
	}()
	return sockPath
}

// ini-i35w: an agent still booting gets NOTHING claimed -- no bd write, so the
// board can never say someone is working on a bead whose message never landed.
func TestRunAssign_AnAgentStillStartingGetsNothingClaimed(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)
	bdShowTitleFn = func(id string) (string, error) { return "Fix the bug", nil }
	dispatched := 0
	bdDispatchFn = func(id, agent, status string, recordImplementer bool) error {
		dispatched++
		return nil
	}
	starting := tui.ErrPaneStillStarting.Error() + ": eng4 started 6s ago and has not gone quiet for 600ms; resend when it is ready"
	sock := i35wActionIPC(t, map[string]tui.IPCResponse{"send_ready": {Error: starting}})
	t.Setenv("INITECH_SOCKET", sock)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"assign", "eng4", "ini-77ys"})
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing assigned") || !strings.Contains(err.Error(), "still starting") {
		t.Fatalf("err = %v, want a nothing-assigned refusal naming the cause", err)
	}
	if dispatched != 0 {
		t.Fatalf("bd was written %d time(s) for an agent that could not receive the dispatch", dispatched)
	}
}

// Version skew: a TUI that predates send_ready answers "unknown action", and
// assign must behave exactly as it always has rather than refuse to work.
func TestRunAssign_AnOlderTUIWithoutSendReadyStillAssigns(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)
	bdShowTitleFn = func(id string) (string, error) { return "Fix the bug", nil }
	dispatched := 0
	bdDispatchFn = func(id, agent, status string, recordImplementer bool) error {
		dispatched++
		return nil
	}
	sock := i35wActionIPC(t, map[string]tui.IPCResponse{"send_ready": {Error: `unknown action "send_ready"`}})
	t.Setenv("INITECH_SOCKET", sock)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"assign", "eng4", "ini-77ys"})
	defer rootCmd.SetArgs(nil)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("assign against an older TUI: %v", err)
	}
	if dispatched != 1 {
		t.Fatalf("dispatched %d, want 1", dispatched)
	}
}

// qa2's FAIL on AC 3: the client gave up on send_ready before the server
// finished waiting, read the silence as "go ahead", and claimed the bead. A
// preflight that gets NO ANSWER in time claims nothing.
func TestRunAssign_APreflightTimeoutClaimsNothing(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)
	old := sendReadyActionTimeout
	sendReadyActionTimeout = 200 * time.Millisecond
	t.Cleanup(func() { sendReadyActionTimeout = old })
	bdShowTitleFn = func(id string) (string, error) { return "Fix the bug", nil }
	dispatched := 0
	bdDispatchFn = func(id, agent, status string, recordImplementer bool) error {
		dispatched++
		return nil
	}
	sock := i35wActionIPC(t, map[string]tui.IPCResponse{"send_ready": {Error: i35wHang}})
	t.Setenv("INITECH_SOCKET", sock)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"assign", "eng4", "ini-77ys"})
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing assigned") {
		t.Fatalf("err = %v, want nothing assigned after a preflight timeout", err)
	}
	if dispatched != 0 {
		t.Fatalf("bd was written %d time(s) after the preflight got no answer", dispatched)
	}
}

// The client must outwait the server's settle bound, or it cannot tell "still
// starting" from "no answer". Pinned to the exported bound, so changing the
// server's wait moves the client with it or fails here.
func TestIPCDeadline_SendReadyOutwaitsTheServerSettleBound(t *testing.T) {
	if got := ipcDeadlineFor("send_ready"); got <= tui.StartupSettleBound {
		t.Fatalf("send_ready deadline %s does not exceed the server's settle bound %s", got, tui.StartupSettleBound)
	}
	if got := ipcDeadlineFor("send"); got != sendActionTimeout {
		t.Fatalf("send deadline changed to %s", got)
	}
	if got := ipcDeadlineFor("peek"); got != ipcCallTimeout {
		t.Fatalf("an ordinary action's deadline changed to %s", got)
	}
}
