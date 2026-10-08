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
	t.Cleanup(func() { ln.Close(); os.Remove(sockPath) })
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
