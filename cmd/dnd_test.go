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

// ini-x432.1: initech dnd on|off asks the TUI through the dnd action, prints
// its line, and against an older TUI exits 2 with the older-initech line;
// initech send prints the held line exit 0 and the queue-full refusal exit 1.

// x432Run runs initech with args against a TUI that answers resp, inside a
// pane named env ("" outside one).
func x432Run(t *testing.T, resp tui.IPCResponse, env string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	skipWindows(t)
	t.Setenv("INITECH_SOCKET", startFakeIPC(t, resp))
	t.Setenv("INITECH_AGENT", env)
	dndAgent = ""
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	rootCmd.SetArgs(args)
	defer rootCmd.SetArgs(nil)
	err = rootCmd.Execute()
	return out.String(), errOut.String(), err
}

func TestDND_OnAndOffPrintTheTUIsLine(t *testing.T) {
	stdout, _, err := x432Run(t, tui.IPCResponse{OK: true, Data: "do-not-disturb on for eng2: messages from other agents wait until initech dnd off"}, "eng2", "dnd", "on")
	if err != nil || strings.TrimSpace(stdout) != "do-not-disturb on for eng2: messages from other agents wait until initech dnd off" {
		t.Errorf("on: %q, %v", stdout, err)
	}
	if v69Exit(err) != 0 {
		t.Errorf("on exit %d", v69Exit(err))
	}
}

// AC5: an older TUI answers "unknown action": dnd exits 2 with the
// older-initech line, printed once.
func TestDND_AnOlderTUIExitsTwo(t *testing.T) {
	_, stderr, err := x432Run(t, tui.IPCResponse{Error: `unknown action "dnd"`}, "eng2", "dnd", "on")
	if got := v69Exit(err); got != 2 {
		t.Errorf("exit %d (%v), want 2", got, err)
	}
	if err == nil || err.Error() != "older initech running; restart it for do-not-disturb" {
		t.Errorf("err %v, want the older-initech line", err)
	}
	if stderr != "" {
		t.Errorf("cobra printed %q beside the error", stderr)
	}
}

// A refusal from the TUI is exit 1 with its words; no agent is a usage error.
func TestDND_ARefusalAndNoAgent(t *testing.T) {
	_, _, err := x432Run(t, tui.IPCResponse{Error: `pane "ghost" not found`}, "ghost", "dnd", "off")
	if v69Exit(err) != 1 || err == nil || err.Error() != `pane "ghost" not found` {
		t.Errorf("refusal: %v exit %d", err, v69Exit(err))
	}
	_, _, err = x432Run(t, tui.IPCResponse{OK: true}, "", "dnd", "on")
	if err == nil || !strings.Contains(err.Error(), "no agent specified") {
		t.Errorf("no agent: %v", err)
	}
	_, _, err = x432Run(t, tui.IPCResponse{OK: true}, "eng2", "dnd", "maybe")
	if err == nil || !strings.Contains(err.Error(), `dnd takes on or off, not "maybe"`) {
		t.Errorf("bad word: %v", err)
	}
}

// The request carries the agent (INITECH_AGENT, or --agent over it) and the
// word.
func TestDND_TheRequestNamesTheAgentAndTheWord(t *testing.T) {
	skipWindows(t)
	sockPath := filepath.Join("/tmp", fmt.Sprintf("initech-x432r-%d-%d.sock", os.Getpid(), fakeIPCCounter.Add(1)))
	os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sockPath) })
	got := make(chan tui.IPCRequest, 4)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadBytes('\n')
			var req tui.IPCRequest
			json.Unmarshal(line, &req)
			got <- req
			conn.Write([]byte(`{"ok":true,"data":"x"}` + "\n"))
			conn.Close()
		}
	}()
	t.Setenv("INITECH_SOCKET", sockPath)
	t.Setenv("INITECH_AGENT", "eng2")
	for _, c := range []struct {
		args       []string
		agent, txt string
	}{
		{[]string{"dnd", "on"}, "eng2", "on"},
		{[]string{"dnd", "--agent", "qa1", "off"}, "qa1", "off"},
	} {
		dndAgent = ""
		rootCmd.SetOut(&bytes.Buffer{})
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs(c.args)
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		req := <-got
		if req.Action != "dnd" || req.Target != c.agent || req.Text != c.txt {
			t.Errorf("%v sent %+v, want dnd %s %s", c.args, req, c.agent, c.txt)
		}
	}
	rootCmd.SetArgs(nil)
	dndAgent = ""
}

// AC1/AC3 at the sender: held prints the TUI's line exit 0; the full queue's
// refusal prints its line exit 1.
func TestDND_SendPrintsHeldAndTheFullRefusal(t *testing.T) {
	stderr, err := v69RunSend(t, tui.IPCResponse{OK: true, Delivery: tui.DeliveryHeld, Data: "held: eng2 is in do-not-disturb"}, "eng2")
	if v69Exit(err) != 0 || strings.TrimSpace(stderr) != "held: eng2 is in do-not-disturb" {
		t.Errorf("held: %q exit %d", stderr, v69Exit(err))
	}
	stderr, err = v69RunSend(t, tui.IPCResponse{OK: true, Delivery: tui.DeliveryHeld}, "workbench:eng2")
	if v69Exit(err) != 0 || strings.TrimSpace(stderr) != "held: workbench:eng2 is in do-not-disturb" {
		t.Errorf("held with no line: %q exit %d", stderr, v69Exit(err))
	}
	_, err = v69RunSend(t, tui.IPCResponse{Error: "not delivered to eng2: do-not-disturb queue is full", Delivery: tui.DeliveryNotDelivered, Data: "do-not-disturb queue is full"}, "eng2")
	if v69Exit(err) != 1 || err == nil || err.Error() != "not delivered to eng2: do-not-disturb queue is full" {
		t.Errorf("full: %v exit %d", err, v69Exit(err))
	}
}

// dnd changes a live session's state, so a dev build refuses it without
// --allow-dev-delivery, as it refuses send and bead.
func TestDND_ADevBuildIsGuarded(t *testing.T) {
	if !deliveryEffectActions["dnd"] {
		t.Fatal("dnd is not in deliveryEffectActions")
	}
	oldTest, oldVersion, oldAllow := isRunningUnderGoTest, Version, allowDevDelivery
	isRunningUnderGoTest = func() bool { return false }
	Version = "dev"
	allowDevDelivery = false
	t.Cleanup(func() { isRunningUnderGoTest = oldTest; Version = oldVersion; allowDevDelivery = oldAllow })
	_, _, err := x432Run(t, tui.IPCResponse{OK: true, Data: "x"}, "eng2", "dnd", "on")
	if err == nil || !strings.Contains(err.Error(), "allow-dev-delivery") {
		t.Errorf("dev build ran dnd: %v", err)
	}
}
