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

	"github.com/nmelo/initech/internal/schedule"
	"github.com/nmelo/initech/internal/tui"
)

// ini-6ib: initech cron is a thin client of window 1.

// recordingIPC answers every request with resp and keeps the last request.
func recordingIPC(t *testing.T, resp tui.IPCResponse) (string, *tui.IPCRequest) {
	t.Helper()
	n := fakeIPCCounter.Add(1)
	sock := filepath.Join("/tmp", fmt.Sprintf("initech-cron-%d-%d.sock", os.Getpid(), n))
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sock) })
	got := &tui.IPCRequest{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadBytes('\n')
			json.Unmarshal(line, got)
			data, _ := json.Marshal(resp)
			conn.Write(append(data, '\n'))
			conn.Close()
		}
	}()
	return sock, got
}

func runCron(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cronTo, cronAt, cronEvery, cronMessage, cronCommand = "", "", "", "", ""
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(append([]string{"cron"}, args...))
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	return out.String(), err
}

func TestCronCommand_AddSendsTheJobAsTheAgent(t *testing.T) {
	skipWindows(t)
	sock, got := recordingIPC(t, tui.IPCResponse{OK: true, Data: "added s1, owned by eng1"})
	t.Setenv("INITECH_SOCKET", sock)
	t.Setenv("INITECH_AGENT", "eng1")
	out, err := runCron(t, "add", "--to", "qa1", "--every", "15m", "--command", "bd ready")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "added s1, owned by eng1") {
		t.Fatalf("printed %q", out)
	}
	var add tui.CronAddRequest
	json.Unmarshal([]byte(got.Text), &add)
	// No From: window 1 works out the owner from the connection.
	if got.Action != "cron_add" || got.From != "" || add.To != "qa1" || add.Every != "15m" || add.Command != "bd ready" {
		t.Fatalf("sent %+v with job %+v", got, add)
	}
}

func TestCronCommand_ListShowsTheOwnerFirst(t *testing.T) {
	skipWindows(t)
	last := time.Date(2026, 11, 4, 9, 30, 0, 0, time.Local)
	rows := []tui.CronListing{{
		Job:  schedule.Job{ID: "s1", Owner: "eng1", To: "qa1", At: "09:30 weekdays", Message: "standup", LastRun: &last, LastResult: "delivered"},
		Next: time.Date(2026, 11, 5, 9, 30, 0, 0, time.Local),
	}}
	data, _ := json.Marshal(rows)
	sock, _ := recordingIPC(t, tui.IPCResponse{OK: true, Data: string(data)})
	t.Setenv("INITECH_SOCKET", sock)
	out, err := runCron(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "OWNER") || !strings.HasPrefix(lines[1], "eng1") ||
		!strings.Contains(lines[1], "Thu 09:30") || !strings.Contains(lines[1], "message: standup") {
		t.Fatalf("listed:\n%s", out)
	}
}

func TestCronCommand_AnOlderInitechSaysSo(t *testing.T) {
	skipWindows(t)
	sock, _ := recordingIPC(t, tui.IPCResponse{Error: `unknown action "cron_list"`})
	t.Setenv("INITECH_SOCKET", sock)
	if _, err := runCron(t, "list"); err == nil || !strings.Contains(err.Error(), "does not keep a schedule") {
		t.Fatalf("err %v", err)
	}
}
