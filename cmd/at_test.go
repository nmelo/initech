package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

func TestParseAtTime_24Hour(t *testing.T) {
	got, err := parseAtTime("14:30")
	if err != nil {
		t.Fatalf("parseAtTime(14:30): %v", err)
	}
	now := time.Now()
	if got.Hour() != 14 || got.Minute() != 30 {
		t.Errorf("got %v, want 14:30 today", got)
	}
	if got.Year() != now.Year() || got.Month() != now.Month() || got.Day() != now.Day() {
		t.Errorf("date should be today, got %v", got)
	}
}

func TestParseAtTime_12Hour(t *testing.T) {
	got, err := parseAtTime("2:30pm")
	if err != nil {
		t.Fatalf("parseAtTime(2:30pm): %v", err)
	}
	if got.Hour() != 14 || got.Minute() != 30 {
		t.Errorf("got %v, want 14:30", got)
	}
}

func TestParseAtTime_FullDate(t *testing.T) {
	got, err := parseAtTime("2026-04-01 09:00")
	if err != nil {
		t.Fatalf("parseAtTime(full date): %v", err)
	}
	if got.Year() != 2026 || got.Month() != 4 || got.Day() != 1 {
		t.Errorf("date = %v, want 2026-04-01", got)
	}
	if got.Hour() != 9 || got.Minute() != 0 {
		t.Errorf("time = %v, want 09:00", got)
	}
}

func TestParseAtTime_Invalid(t *testing.T) {
	_, err := parseAtTime("not-a-time")
	if err == nil {
		t.Error("expected error for invalid time")
	}
}

func TestRunAt_MutuallyExclusive(t *testing.T) {
	atIn = "5m"
	atAt = "14:00"
	defer func() { atIn = ""; atAt = "" }()

	err := runAt(atCmd, []string{"eng1", "test"})
	if err == nil || err.Error() != "cannot use both --in and --at" {
		t.Errorf("expected mutual exclusion error, got: %v", err)
	}
}

func TestRunAt_RequiresTimeFlag(t *testing.T) {
	atIn = ""
	atAt = ""
	err := runAt(atCmd, []string{"eng1", "test"})
	if err == nil || err.Error() != "must specify --in or --at" {
		t.Errorf("expected missing time error, got: %v", err)
	}
}

func TestRunAt_RequiresArgs(t *testing.T) {
	atIn = "5m"
	defer func() { atIn = "" }()
	err := runAt(atCmd, []string{"eng1"})
	if err == nil {
		t.Error("expected error with only 1 arg")
	}
}

func TestRunAt_ScheduleModeSendsCustomIPC(t *testing.T) {
	restoreFlags := setAtFlags(t, "5m", "", false, "")
	defer restoreFlags()

	projectDir := shortProjectDir(t)
	writeStandupConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	sockPath := tui.SocketPath(projectDir, "demo")
	reqCh, cleanup := startATIPCServer(t, sockPath, responseMode{resp: `{"ok":true,"data":"at-1"}`})
	defer cleanup()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	if err := runAt(cmd, []string{"workbench:eng1", "run", "tests"}); err != nil {
		t.Fatalf("runAt schedule: %v", err)
	}

	var req map[string]any
	waitATRequest(t, reqCh, &req)

	if req["action"] != "schedule" {
		t.Fatalf("action = %v, want schedule", req["action"])
	}
	if req["target"] != "eng1" {
		t.Fatalf("target = %v, want eng1", req["target"])
	}
	if req["host"] != "workbench" {
		t.Fatalf("host = %v, want workbench", req["host"])
	}
	if req["text"] != "run tests" {
		t.Fatalf("text = %v, want 'run tests'", req["text"])
	}
	if req["enter"] != true {
		t.Fatalf("enter = %v, want true", req["enter"])
	}
	fireAt, ok := req["fire_at"].(string)
	if !ok || fireAt == "" {
		t.Fatalf("fire_at = %#v, want RFC3339 string", req["fire_at"])
	}
	if _, err := time.Parse(time.RFC3339, fireAt); err != nil {
		t.Fatalf("fire_at parse: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Scheduled: workbench:eng1 in 5m0s") {
		t.Fatalf("schedule output missing target/duration:\n%s", got)
	}
	if !strings.Contains(got, "[id: at-1]") {
		t.Fatalf("schedule output missing timer id:\n%s", got)
	}
}

func TestRunAtList_PrintsTimersAndTruncatesMessage(t *testing.T) {
	restoreFlags := setAtFlags(t, "", "", true, "")
	defer restoreFlags()

	projectDir := shortProjectDir(t)
	writeStandupConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	fireAt := time.Now().Add(10 * time.Minute).UTC()
	timersJSON, err := json.Marshal([]tui.Timer{
		{ID: "at-1", Target: "eng1", Text: "short msg", Enter: true, FireAt: fireAt, CreatedAt: fireAt},
		{ID: "at-2", Target: "eng2", Host: "workbench", Text: "this message is definitely longer than twenty two chars", Enter: true, FireAt: fireAt, CreatedAt: fireAt},
	})
	if err != nil {
		t.Fatalf("Marshal timers: %v", err)
	}
	respBytes, err := json.Marshal(tui.IPCResponse{OK: true, Data: string(timersJSON)})
	if err != nil {
		t.Fatalf("Marshal response: %v", err)
	}
	sockPath := tui.SocketPath(projectDir, "demo")
	reqCh, cleanup := startATIPCServer(t, sockPath, responseMode{raw: string(respBytes) + "\n"})
	defer cleanup()

	var out bytes.Buffer
	if err := runAtList(&out); err != nil {
		t.Fatalf("runAtList: %v", err)
	}

	var req map[string]any
	waitATRequest(t, reqCh, &req)
	if req["action"] != "list_timers" {
		t.Fatalf("action = %v, want list_timers", req["action"])
	}

	got := out.String()
	for _, want := range []string{
		"ID     TARGET     MESSAGE                  FIRES AT           REMAINING",
		"at-1   eng1       short msg",
		"at-2   workbench:eng2 this message is def...",
		"No pending timers.",
	} {
		_ = want
	}
	if !strings.Contains(got, "ID     TARGET     MESSAGE") {
		t.Fatalf("list output missing header:\n%s", got)
	}
	if !strings.Contains(got, "at-1   eng1       short msg") {
		t.Fatalf("list output missing first timer:\n%s", got)
	}
	if !strings.Contains(got, "workbench:eng2") || !strings.Contains(got, "this message is def...") {
		t.Fatalf("list output missing truncated remote timer:\n%s", got)
	}
}

func TestRunAtList_PrintsEmptyState(t *testing.T) {
	restoreFlags := setAtFlags(t, "", "", true, "")
	defer restoreFlags()

	projectDir := shortProjectDir(t)
	writeStandupConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	sockPath := tui.SocketPath(projectDir, "demo")
	reqCh, cleanup := startATIPCServer(t, sockPath, responseMode{resp: `{"ok":true,"data":"[]"}`})
	defer cleanup()

	var out bytes.Buffer
	if err := runAtList(&out); err != nil {
		t.Fatalf("runAtList empty: %v", err)
	}
	var req map[string]any
	waitATRequest(t, reqCh, &req)
	if req["action"] != "list_timers" {
		t.Fatalf("action = %v, want list_timers", req["action"])
	}
	if !strings.Contains(out.String(), "No pending timers.") {
		t.Fatalf("empty list output = %q", out.String())
	}
}

func TestRunAtCancel_PrintsCanceledID(t *testing.T) {
	restoreFlags := setAtFlags(t, "", "", false, "at-7")
	defer restoreFlags()

	projectDir := shortProjectDir(t)
	writeStandupConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	sockPath := tui.SocketPath(projectDir, "demo")
	reqCh, cleanup := startATIPCServer(t, sockPath, responseMode{resp: `{"ok":true,"data":"at-7"}`})
	defer cleanup()

	var out bytes.Buffer
	if err := runAtCancel(&out, "at-7"); err != nil {
		t.Fatalf("runAtCancel: %v", err)
	}
	var req map[string]any
	waitATRequest(t, reqCh, &req)
	if req["action"] != "cancel_timer" || req["text"] != "at-7" {
		t.Fatalf("cancel request = %#v, want cancel_timer/at-7", req)
	}
	if got := out.String(); !strings.Contains(got, "Canceled: at-7") {
		t.Fatalf("cancel output = %q", got)
	}
}

func TestIPCCallCustom_InvalidResponse(t *testing.T) {
	projectDir := shortProjectDir(t)
	writeStandupConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	sockPath := tui.SocketPath(projectDir, "demo")
	reqCh, cleanup := startATIPCServer(t, sockPath, responseMode{raw: "not-json\n"})
	defer cleanup()

	_, err := ipcCallCustom(map[string]string{"action": "list_timers"})
	if err == nil || !strings.Contains(err.Error(), "invalid response") {
		t.Fatalf("ipcCallCustom invalid response error = %v", err)
	}
	var req map[string]any
	waitATRequest(t, reqCh, &req)
}

func TestIPCCallCustom_NoResponse(t *testing.T) {
	projectDir := shortProjectDir(t)
	writeStandupConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	sockPath := tui.SocketPath(projectDir, "demo")
	reqCh, cleanup := startATIPCServer(t, sockPath, responseMode{closeWithoutReply: true})
	defer cleanup()

	_, err := ipcCallCustom(map[string]string{"action": "list_timers"})
	if err == nil || err.Error() != "no response from TUI" {
		t.Fatalf("ipcCallCustom no-response error = %v", err)
	}
	var req map[string]any
	waitATRequest(t, reqCh, &req)
}

type responseMode struct {
	resp              string
	raw               string
	closeWithoutReply bool
}

func setAtFlags(t *testing.T, in, at string, list bool, cancel string) func() {
	t.Helper()
	oldIn, oldAt, oldList, oldCancel := atIn, atAt, atList, atCancel
	atIn, atAt, atList, atCancel = in, at, list, cancel
	return func() {
		atIn, atAt, atList, atCancel = oldIn, oldAt, oldList, oldCancel
	}
}

func shortProjectDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "iat-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func startATIPCServer(t *testing.T, sockPath string, mode responseMode) (<-chan []byte, func()) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		t.Fatalf("MkdirAll socket dir: %v", err)
	}
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}

	reqCh := make(chan []byte, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			scanner := bufio.NewScanner(conn)
			if !scanner.Scan() {
				_ = conn.Close()
				continue // discoverSocket probe connection
			}
			req := append([]byte(nil), scanner.Bytes()...)
			select {
			case reqCh <- req:
			default:
			}

			if !mode.closeWithoutReply {
				payload := mode.raw
				if payload == "" {
					payload = mode.resp + "\n"
				}
				_, _ = conn.Write([]byte(payload))
			}
			_ = conn.Close()
		}
	}()

	cleanup := func() {
		_ = ln.Close()
		_ = os.Remove(sockPath)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for at IPC server shutdown")
		}
	}
	return reqCh, cleanup
}

func waitATRequest(t *testing.T, reqCh <-chan []byte, dst any) {
	t.Helper()
	select {
	case req := <-reqCh:
		if err := json.Unmarshal(req, dst); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for at IPC request")
	}
}
