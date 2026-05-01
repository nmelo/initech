package cmd

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

func TestRunClear_NoArgs(t *testing.T) {
	err := runClear(clearCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "specify agent") {
		t.Errorf("expected error about specifying agents, got: %v", err)
	}
}

func TestRunClear_SingleAgent(t *testing.T) {
	skipWindows(t)
	srv := startClearIPC(t, func(req tui.IPCRequest) tui.IPCResponse {
		if req.Action != "send" || req.Target != "eng1" || req.Text != "/clear" || !req.Enter {
			t.Errorf("unexpected request: %+v", req)
		}
		return tui.IPCResponse{OK: true}
	})
	t.Setenv("INITECH_SOCKET", srv)

	var buf bytes.Buffer
	clearCmd.SetOut(&buf)
	t.Cleanup(func() { clearCmd.SetOut(nil) })

	if err := runClear(clearCmd, []string{"eng1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Cleared eng1") {
		t.Errorf("output = %q, want 'Cleared eng1'", buf.String())
	}
}

func TestRunClear_MultipleAgents(t *testing.T) {
	skipWindows(t)
	var received []string
	srv := startClearIPC(t, func(req tui.IPCRequest) tui.IPCResponse {
		received = append(received, req.Target)
		return tui.IPCResponse{OK: true}
	})
	t.Setenv("INITECH_SOCKET", srv)

	var buf bytes.Buffer
	clearCmd.SetOut(&buf)
	t.Cleanup(func() { clearCmd.SetOut(nil) })

	if err := runClear(clearCmd, []string{"eng1", "eng2", "qa1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(received) != 3 {
		t.Errorf("expected 3 IPC calls, got %d", len(received))
	}
	for _, name := range []string{"eng1", "eng2", "qa1"} {
		if !strings.Contains(buf.String(), "Cleared "+name) {
			t.Errorf("output missing 'Cleared %s'", name)
		}
	}
}

func TestRunClear_All_SkipsSuper(t *testing.T) {
	skipWindows(t)
	panes := []tui.PaneInfo{
		{Name: "super", Alive: true},
		{Name: "eng1", Alive: true},
		{Name: "eng2", Alive: true},
	}
	var cleared []string
	srv := startClearIPC(t, func(req tui.IPCRequest) tui.IPCResponse {
		if req.Action == "list" {
			data, _ := json.Marshal(panes)
			return tui.IPCResponse{OK: true, Data: string(data)}
		}
		cleared = append(cleared, req.Target)
		return tui.IPCResponse{OK: true}
	})
	t.Setenv("INITECH_SOCKET", srv)

	clearAll = true
	t.Cleanup(func() { clearAll = false })

	var buf bytes.Buffer
	clearCmd.SetOut(&buf)
	t.Cleanup(func() { clearCmd.SetOut(nil) })

	if err := runClear(clearCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, name := range cleared {
		if name == "super" {
			t.Error("--all should skip super")
		}
	}
	if len(cleared) != 2 {
		t.Errorf("expected 2 clears (eng1, eng2), got %d: %v", len(cleared), cleared)
	}
}

func TestRunClear_AgentNotFound(t *testing.T) {
	skipWindows(t)
	srv := startClearIPC(t, func(req tui.IPCRequest) tui.IPCResponse {
		return tui.IPCResponse{OK: false, Error: "pane not found: ghost"}
	})
	t.Setenv("INITECH_SOCKET", srv)

	var buf bytes.Buffer
	clearCmd.SetOut(&buf)
	clearCmd.SetErr(&buf)
	t.Cleanup(func() { clearCmd.SetOut(nil); clearCmd.SetErr(nil) })

	err := runClear(clearCmd, []string{"ghost"})
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
	if !strings.Contains(buf.String(), "pane not found") {
		t.Errorf("output = %q, want mention of pane not found", buf.String())
	}
}

func startClearIPC(t *testing.T, handler func(tui.IPCRequest) tui.IPCResponse) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "clr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "test.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			scanner := tui.NewIPCScanner(conn)
			if scanner.Scan() {
				var req tui.IPCRequest
				json.Unmarshal(scanner.Bytes(), &req)
				resp := handler(req)
				data, _ := json.Marshal(resp)
				conn.Write(data)
				conn.Write([]byte("\n"))
			}
			conn.Close()
		}
	}()
	return sockPath
}
