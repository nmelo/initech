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
	"sync"
	"testing"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/nmelo/initech/internal/tui"
)

// ini-47vy: every command that types a message hands the TUI's send action
// the body as given, so the send path's one cleaning sees it (the TUI side is
// internal/tui/send_clean_path_test.go), and prints the count it answers with.

const hostile47vy = "hello\x1b[201~ls -la\r"

// recordingIPC47vy answers every send with reply and records each request.
func recordingIPC47vy(t *testing.T, reply tui.IPCResponse) (string, func() []tui.IPCRequest) {
	t.Helper()
	n := fakeIPCCounter.Add(1)
	sock := filepath.Join("/tmp", fmt.Sprintf("initech-47vy-%d-%d.sock", os.Getpid(), n))
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sock) })
	var mu sync.Mutex
	var reqs []tui.IPCRequest
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
			mu.Lock()
			reqs = append(reqs, req)
			mu.Unlock()
			resp := tui.IPCResponse{OK: true}
			if req.Action == "send" {
				resp = reply
			}
			b, _ := json.Marshal(resp)
			conn.Write(append(b, '\n'))
			conn.Close()
		}
	}()
	return sock, func() []tui.IPCRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]tui.IPCRequest(nil), reqs...)
	}
}

func sends47vy(reqs []tui.IPCRequest) []tui.IPCRequest {
	var out []tui.IPCRequest
	for _, r := range reqs {
		if r.Action == "send" {
			out = append(out, r)
		}
	}
	return out
}

var delivered47vy = tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered, Removed: 2}

func TestSend47vy_InitechSendHandsTheBodyToTheSendActionAndPrintsTheCount(t *testing.T) {
	skipWindows(t)
	sock, got := recordingIPC47vy(t, delivered47vy)
	t.Setenv("INITECH_SOCKET", sock)
	out := &bytes.Buffer{}
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(out) // initech send prints its line on stderr
	rootCmd.SetArgs([]string{"send", "--allow-dev-delivery", "eng1", hostile47vy})
	defer rootCmd.SetArgs(nil)
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	s := sends47vy(got())
	if len(s) != 1 || s[0].Text != hostile47vy {
		t.Fatalf("send requests %+v, want one carrying the body as given", s)
	}
	if want := "delivered to eng1; 2 control characters removed\n"; out.String() != want {
		t.Fatalf("printed %q, want %q", out.String(), want)
	}
}

func TestSend47vy_AssignsMessageHandsTheBodyToTheSendAction(t *testing.T) {
	skipWindows(t)
	sock, got := recordingIPC47vy(t, delivered47vy)
	out := &bytes.Buffer{}
	if _, err := deliverTextVia(out, func(req tui.IPCRequest) (*tui.IPCResponse, error) {
		return ipcCallSocket(sock, req)
	}, "eng1", "", hostile47vy, true, false, ""); err != nil {
		t.Fatal(err)
	}
	if s := sends47vy(got()); len(s) != 1 || s[0].Text != hostile47vy {
		t.Fatalf("send requests %+v, want one carrying the body as given", s)
	}
	if !strings.Contains(out.String(), "; 2 control characters removed") {
		t.Fatalf("printed %q, want the count", out.String())
	}
}

func TestSend47vy_DeliversReportGoesThroughTheSendAction(t *testing.T) {
	skipWindows(t)
	sock, got := recordingIPC47vy(t, delivered47vy)
	stubBdFns(t)
	resetDeliverFlags(t)
	bdShowBeadFn = func(id string) (string, string, string, error) {
		return "Title " + hostile47vy, "eng2", "in_progress", nil
	}
	bdUpdateBeadFn = func(id string, w beadWrite) error { return nil }
	t.Setenv("INITECH_SOCKET", sock)
	t.Setenv("INITECH_AGENT", "eng2")
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"deliver", "--allow-dev-delivery", "ini-47vy", "--to", "qa2"})
	defer rootCmd.SetArgs(nil)
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	s := sends47vy(got())
	if len(s) != 1 || s[0].Target != "qa2" || !strings.Contains(s[0].Text, "\x1b[201~") {
		t.Fatalf("send requests %+v, want the report to qa2 through the send action, body as given", s)
	}
}

func TestSend47vy_TheAppsSendHandsTheBodyToTheSendAction(t *testing.T) {
	skipWindows(t)
	sock, got := recordingIPC47vy(t, delivered47vy)
	typed := (&appBackend{}).Send(appserve.Rig{Socket: sock}, "eng1", hostile47vy, true)
	if s := sends47vy(got()); len(s) != 1 || s[0].Text != hostile47vy {
		t.Fatalf("send requests %+v, want one carrying the body as given", s)
	}
	if typed.Verdict != tui.DeliveryDelivered || typed.Words != "delivered to eng1; 2 control characters removed" {
		t.Fatalf("app answered %+v, want delivered with the count", typed)
	}
}

// AC4: each answer line gains the count; a clean answer is unchanged.
func TestSend47vy_EveryLineNamesTheCountAndACleanLineIsUnchanged(t *testing.T) {
	cases := []struct {
		resp tui.IPCResponse
		want string
	}{
		{tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered}, "delivered to eng1\n"},
		{tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered, Removed: 3}, "delivered to eng1; 3 control characters removed\n"},
		{tui.IPCResponse{OK: true, Delivery: tui.DeliveryUnchecked, Data: "no delivery check for this agent type", Removed: 1},
			"sent to eng1, unconfirmed: no delivery check for this agent type; 1 control characters removed\n"},
		{tui.IPCResponse{OK: true, Delivery: tui.DeliveryHeld, Data: "held: eng1 is in do-not-disturb", Removed: 1},
			"held: eng1 is in do-not-disturb; 1 control characters removed\n"},
		{tui.IPCResponse{OK: true, Delivery: tui.DeliveryDeferred, Removed: 1},
			"deferred to eng1 — target has a modal open; will deliver when it closes; 1 control characters removed\n"},
	}
	for _, c := range cases {
		resp := c.resp
		out := &bytes.Buffer{}
		_, err := deliverTextVia(out, func(tui.IPCRequest) (*tui.IPCResponse, error) { return &resp, nil }, "eng1", "", "x", true, false, "")
		if err != nil || out.String() != c.want {
			t.Errorf("%+v: printed %q err %v, want %q", c.resp, out.String(), err, c.want)
		}
	}
	refused := tui.IPCResponse{Error: "not delivered to eng1: the message held only control characters (3 removed)", Delivery: tui.DeliveryNotDelivered, Removed: 3}
	_, err := deliverTextVia(&bytes.Buffer{}, func(tui.IPCRequest) (*tui.IPCResponse, error) { return &refused, nil }, "eng1", "", "x", true, false, "")
	if err == nil || err.Error() != refused.Error {
		t.Fatalf("refusal err %v, want exactly %q", err, refused.Error)
	}
}
