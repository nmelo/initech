package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	cronQuiet = false
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

// ── ini-7k3n: --quiet ───────────────────────────────────────────────────

// TestCronQuiet_AddSendsQuietAndPrintsNoWarningWhenItTook: the request carries
// quiet, and a reply naming it prints only the reply.
func TestCronQuiet_AddSendsQuietAndPrintsNoWarningWhenItTook(t *testing.T) {
	skipWindows(t)
	sock, got := recordingIPC(t, tui.IPCResponse{OK: true, Data: "added s1, owned by eng1, quiet"})
	t.Setenv("INITECH_SOCKET", sock)
	out, err := runCron(t, "add", "--to", "qa1", "--every", "15m", "--command", "bd ready", "--quiet")
	if err != nil {
		t.Fatal(err)
	}
	var add tui.CronAddRequest
	json.Unmarshal([]byte(got.Text), &add)
	if !add.Quiet || add.Command != "bd ready" {
		t.Fatalf("sent job %+v, want quiet", add)
	}
	if strings.TrimSpace(out) != "added s1, owned by eng1, quiet" {
		t.Fatalf("printed %q", out)
	}
}

// TestCronQuiet_WithAMessageIsRefusedBeforeAnyCall is AC5 at the command.
func TestCronQuiet_WithAMessageIsRefusedBeforeAnyCall(t *testing.T) {
	skipWindows(t)
	sock, got := recordingIPC(t, tui.IPCResponse{OK: true, Data: "added s1, owned by eng1"})
	t.Setenv("INITECH_SOCKET", sock)
	_, err := runCron(t, "add", "--to", "qa1", "--every", "15m", "--message", "hi", "--quiet")
	if err == nil || !strings.Contains(err.Error(), "--quiet") || !strings.Contains(err.Error(), "--message") {
		t.Fatalf("err %v, want a refusal naming --quiet and --message", err)
	}
	if got.Action != "" {
		t.Fatalf("a refused add still called window 1: %+v", got)
	}
}

// TestCronQuiet_ListMarksAQuietJobAndItsQuietRun is AC3.
func TestCronQuiet_ListMarksAQuietJobAndItsQuietRun(t *testing.T) {
	skipWindows(t)
	last := time.Date(2026, 11, 4, 9, 30, 0, 0, time.Local)
	rows := []tui.CronListing{
		{Job: schedule.Job{ID: "s1", Owner: "eng1", To: "qa1", Every: "5m", Command: "bd ready", Quiet: true, LastRun: &last, LastResult: "quiet: exit 0, no output"}},
		{Job: schedule.Job{ID: "s2", Owner: "eng1", To: "qa1", Every: "5m", Command: "bd ready"}},
	}
	data, _ := json.Marshal(rows)
	sock, _ := recordingIPC(t, tui.IPCResponse{OK: true, Data: string(data)})
	t.Setenv("INITECH_SOCKET", sock)
	out, err := runCron(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], "quiet command: bd ready") ||
		!strings.Contains(lines[1], "quiet: exit 0, no output") || strings.Contains(lines[2], "quiet") {
		t.Fatalf("listed:\n%s", out)
	}
}

// ── ini-bqqk: --quiet against a window 1 that cannot keep it ────────────

// scriptedIPC answers each action with its own response and records every
// request in order, so a test sees an add and the remove that follows it.
func scriptedIPC(t *testing.T, answers map[string]tui.IPCResponse) (string, func() []tui.IPCRequest) {
	t.Helper()
	n := fakeIPCCounter.Add(1)
	sock := filepath.Join("/tmp", fmt.Sprintf("initech-cronq-%d-%d.sock", os.Getpid(), n))
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sock) })
	var mu sync.Mutex
	var got []tui.IPCRequest
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadBytes('\n')
			var req tui.IPCRequest
			json.Unmarshal(line, &req)
			mu.Lock()
			got = append(got, req)
			mu.Unlock()
			data, _ := json.Marshal(answers[req.Action])
			conn.Write(append(data, '\n'))
			conn.Close()
		}
	}()
	return sock, func() []tui.IPCRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]tui.IPCRequest(nil), got...)
	}
}

var (
	reply261 = tui.IPCResponse{OK: true, Data: "added s1, owned by eng1"}
	reply262 = tui.IPCResponse{OK: true, Data: "added s1, owned by eng1, quiet"}
	removed  = tui.IPCResponse{OK: true, Data: "removed s1 (owner eng1, to qa1)"}
)

func quietAdd(t *testing.T, add tui.IPCResponse, remove tui.IPCResponse, quiet bool) (string, error, []tui.IPCRequest) {
	t.Helper()
	skipWindows(t)
	sock, reqs := scriptedIPC(t, map[string]tui.IPCResponse{"cron_add": add, "cron_remove": remove})
	t.Setenv("INITECH_SOCKET", sock)
	args := []string{"add", "--to", "qa1", "--every", "15m", "--command", "bd ready"}
	if quiet {
		args = append(args, "--quiet")
	}
	out, err := runCron(t, args...)
	return out, err, reqs()
}

func actions(reqs []tui.IPCRequest) string {
	var a []string
	for _, r := range reqs {
		a = append(a, r.Action+" "+r.Target)
	}
	return strings.TrimSpace(strings.Join(a, ","))
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ec exitCoder
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return 1
}

// TestCronQuietRefusal_AnOlderWindowOneIsRolledBack is AC2: against a 2.61.1
// reply the job is removed, nothing reaches stdout, one line, exit 1.
func TestCronQuietRefusal_AnOlderWindowOneIsRolledBack(t *testing.T) {
	out, err, reqs := quietAdd(t, reply261, removed, true)
	if got := actions(reqs); got != "cron_add ,cron_remove s1" {
		t.Fatalf("requests %q, want the add and the remove of s1", got)
	}
	if out != "" {
		t.Fatalf("printed %q, want nothing on stdout or from cobra", out)
	}
	want := "not added: the running initech is older than --quiet and would deliver every run; restart initech on " + Version + " or later and add it again"
	if err == nil || err.Error() != want || exitCode(err) != 1 || strings.Contains(err.Error(), "\n") {
		t.Fatalf("err %v (exit %d), want one line %q, exit 1", err, exitCode(err), want)
	}
}

// TestCronQuietRefusal_TheTwinsAreUnchanged is AC3: a reply naming quiet adds
// with exit 0 and no remove; a plain add against either reply is as before.
func TestCronQuietRefusal_TheTwinsAreUnchanged(t *testing.T) {
	out, err, reqs := quietAdd(t, reply262, removed, true)
	if err != nil || strings.TrimSpace(out) != "added s1, owned by eng1, quiet" || actions(reqs) != "cron_add" {
		t.Fatalf("a 2.62 reply: out %q err %v requests %q", out, err, actions(reqs))
	}
	for _, r := range []tui.IPCResponse{reply261, reply262} {
		out, err, reqs := quietAdd(t, r, removed, false)
		if err != nil || strings.TrimSpace(out) != r.Data || actions(reqs) != "cron_add" {
			t.Fatalf("a plain add against %q: out %q err %v requests %q", r.Data, out, err, actions(reqs))
		}
	}
}

// TestCronQuietRefusal_AFailedRemoveNamesTheJob is AC4: the job is left, so the
// line says so and names it; exit 1.
func TestCronQuietRefusal_AFailedRemoveNamesTheJob(t *testing.T) {
	out, err, reqs := quietAdd(t, reply261, tui.IPCResponse{Error: "no job s1"}, true)
	if actions(reqs) != "cron_add ,cron_remove s1" || out != "" {
		t.Fatalf("requests %q out %q", actions(reqs), out)
	}
	if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "initech cron remove s1") ||
		!strings.Contains(err.Error(), "no job s1") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("err %v (exit %d), want one line naming s1 and why, exit 1", err, exitCode(err))
	}
}

// TestCronQuietRefusal_AReplyWithNoIDRemovesNothing is feed-it-nothing: a reply
// with no id, or an empty one, gives the line, no remove, exit 1.
func TestCronQuietRefusal_AReplyWithNoIDRemovesNothing(t *testing.T) {
	for _, data := range []string{"added, owned by eng1", ""} {
		out, err, reqs := quietAdd(t, tui.IPCResponse{OK: true, Data: data}, removed, true)
		if actions(reqs) != "cron_add" || out != "" || err == nil || exitCode(err) != 1 ||
			!strings.Contains(err.Error(), "not added") || !strings.Contains(err.Error(), "initech cron list") {
			t.Fatalf("reply %q: requests %q out %q err %v", data, actions(reqs), out, err)
		}
	}
}

// ── ini-fzz5: --run-late ────────────────────────────────────────────────

func runLateAdd(t *testing.T, add tui.IPCResponse) (string, error, []tui.IPCRequest) {
	t.Helper()
	skipWindows(t)
	sock, reqs := scriptedIPC(t, map[string]tui.IPCResponse{"cron_add": add, "cron_remove": removed})
	t.Setenv("INITECH_SOCKET", sock)
	out, err := runCron(t, "add", "--to", "qa1", "--at", "06:00", "--message", "standup", "--run-late")
	return out, err, reqs()
}

// The request carries run_late, and a reply naming it prints only the reply.
func TestCronRunLate_AddSendsItAndPrintsTheReply(t *testing.T) {
	out, err, reqs := runLateAdd(t, tui.IPCResponse{OK: true, Data: "added s1, owned by eng1, run late"})
	if err != nil || strings.TrimSpace(out) != "added s1, owned by eng1, run late" || actions(reqs) != "cron_add" {
		t.Fatalf("out %q err %v reqs %q", out, err, actions(reqs))
	}
	var add tui.CronAddRequest
	json.Unmarshal([]byte(reqs[0].Text), &add)
	if !add.RunLate {
		t.Fatalf("sent %+v, want run_late", add)
	}
}

// An initech older than --run-late stores the job without it: the add is
// rolled back, one line, exit 1 (a command that cannot do all it was asked
// fails).
func TestCronRunLate_AnOlderInitechIsRolledBack(t *testing.T) {
	out, err, reqs := runLateAdd(t, reply261)
	if got := actions(reqs); got != "cron_add ,cron_remove s1" {
		t.Fatalf("requests %q, want the add and the remove of s1", got)
	}
	want := "not added: the running initech is older than --run-late and would store the job without it; restart initech on " + Version + " or later and add it again"
	if out != "" || err == nil || err.Error() != want || exitCode(err) != 1 {
		t.Fatalf("out %q err %v (exit %d), want %q", out, err, exitCode(err), want)
	}
}

// Both flags at once: each is checked by its own word in the reply.
func TestCronRunLate_WithQuietBothAreNamed(t *testing.T) {
	skipWindows(t)
	sock, reqs := scriptedIPC(t, map[string]tui.IPCResponse{"cron_add": {OK: true, Data: "added s1, owned by eng1, quiet, run late"}, "cron_remove": removed})
	t.Setenv("INITECH_SOCKET", sock)
	out, err := runCron(t, "add", "--to", "qa1", "--every", "15m", "--command", "bd ready", "--quiet", "--run-late")
	if err != nil || actions(reqs()) != "cron_add" || !strings.Contains(out, "run late") {
		t.Fatalf("out %q err %v", out, err)
	}
}

// cron list names a run-late job, and a skip reads as pm's line: the slot's
// day and time, then the skip.
func TestCronRunLate_ListShowsTheJobAndASkip(t *testing.T) {
	skipWindows(t)
	slot := time.Date(2026, 11, 5, 6, 0, 0, 0, time.Local)
	rows := []tui.CronListing{
		{Job: schedule.Job{ID: "s1", Owner: "super", To: "eng1", At: "06:00", Message: "standup", LastRun: &slot,
			LastResult: schedule.SkipResult(9*time.Hour, 3)}, Next: slot.AddDate(0, 0, 1)},
		{Job: schedule.Job{ID: "s2", Owner: "super", To: "eng2", At: "06:00", Message: "report", RunLate: true}, Next: slot.AddDate(0, 0, 1)},
	}
	data, _ := json.Marshal(rows)
	sock, _ := recordingIPC(t, tui.IPCResponse{OK: true, Data: string(data)})
	t.Setenv("INITECH_SOCKET", sock)
	out, err := runCron(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Thu 06:00 skipped: 9h late, allowed 15m (3 runs skipped)") {
		t.Fatalf("the skip is not shown:\n%s", out)
	}
	if !strings.Contains(out, "run late message: report") {
		t.Fatalf("the run-late job is not named:\n%s", out)
	}
}
