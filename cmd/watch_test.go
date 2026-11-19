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

	"github.com/nmelo/initech/internal/tui"
	"github.com/nmelo/initech/internal/watch"
	"github.com/spf13/pflag"
)

// watchFake is a fake TUI for initech watch: answer decides each reply, and
// every request is recorded in order.
type watchFake struct {
	mu     sync.Mutex
	reqs   []tui.IPCRequest
	answer func(tui.IPCRequest) tui.IPCResponse
}

func (f *watchFake) requests() []tui.IPCRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tui.IPCRequest(nil), f.reqs...)
}

func startWatchFake(t *testing.T, answer func(tui.IPCRequest) tui.IPCResponse) *watchFake {
	t.Helper()
	skipWindows(t)
	f := &watchFake{answer: answer}
	sock := filepath.Join("/tmp", fmt.Sprintf("initech-watch-%d-%d.sock", os.Getpid(), fakeIPCCounter.Add(1)))
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sock) })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadBytes('\n')
				if err != nil {
					return
				}
				var req tui.IPCRequest
				if json.Unmarshal(line, &req) != nil {
					return
				}
				f.mu.Lock()
				f.reqs = append(f.reqs, req)
				f.mu.Unlock()
				out, _ := json.Marshal(f.answer(req))
				c.Write(append(out, '\n'))
			}()
		}
	}()
	t.Setenv("INITECH_SOCKET", sock)
	return f
}

// watchClock is 10:00 local on a fixed day: no test reads the real clock.
var watchClock = time.Date(2026, 11, 19, 10, 0, 0, 0, time.Local)

// runWatch runs initech watch with args at watchClock, and returns stdout,
// the error and its exit code (0, 1, or the error's own).
func runWatch(t *testing.T, args ...string) (string, error, int) {
	t.Helper()
	watchBead, watchStatus, watchCommentBy = "", "", ""
	watchTo, watchReceipt, watchHeldFor = "", "", ""
	watchWhy, watchUntil = "", ""
	watchDelivered, watchHoldIfAsleep, watchAll = false, false, false
	for _, c := range watchCmd.Commands() {
		c.Flags().VisitAll(func(fl *pflag.Flag) { fl.Changed = false })
	}
	orig := watchNow
	watchNow = func() time.Time { return watchClock }
	t.Cleanup(func() { watchNow = orig })
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs(append([]string{"watch"}, args...))
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	code := 0
	if err != nil {
		code = 1
		var ec exitCoder
		if errors.As(err, &ec) {
			code = ec.ExitCode()
		}
	}
	return out.String(), err, code
}

func setOK(id string) func(tui.IPCRequest) tui.IPCResponse {
	return func(tui.IPCRequest) tui.IPCResponse { return tui.IPCResponse{OK: true, Data: id} }
}

// setRequest is the one watch_set request the fake saw, decoded.
func setRequest(t *testing.T, f *watchFake) tui.WatchSetRequest {
	t.Helper()
	reqs := f.requests()
	if len(reqs) != 1 || reqs[0].Action != "watch_set" {
		t.Fatalf("requests = %+v, want one watch_set", reqs)
	}
	var r tui.WatchSetRequest
	if err := json.Unmarshal([]byte(reqs[0].Text), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// Each condition reaches initech as its kind with eng4's param keys, and set
// prints the id initech gave it.
func TestWatchSet_EachCondition_IsItsKindAndParams_AndPrintsTheID(t *testing.T) {
	cases := []struct {
		args   []string
		kind   watch.Kind
		params map[string]string
	}{
		{[]string{"--bead", "ini-abc", "--status", "ready_for_qa"}, watch.KindBeadStatus, map[string]string{"bead": "ini-abc", "status": "ready_for_qa"}},
		{[]string{"--bead", "ini-abc", "--comment-by", "lead1"}, watch.KindBeadComment, map[string]string{"bead": "ini-abc", "author": "lead1"}},
		{nil, watch.KindDeadline, nil},
		{[]string{"--to", "eng1", "--receipt", "r7", "--delivered"}, watch.KindSendDelivered, map[string]string{"pane": "eng1", "receipt": "r7"}},
		{[]string{"--to", "eng1", "--receipt", "r7", "--held-for", "15m"}, watch.KindSendHeld, map[string]string{"pane": "eng1", "receipt": "r7", "for": "15m0s"}},
	}
	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			f := startWatchFake(t, setOK("w12"))
			args := append([]string{"set", "--why", "read eng8's change", "--until", "2h", "--hold-if-asleep"}, c.args...)
			out, err, _ := runWatch(t, args...)
			if err != nil {
				t.Fatalf("set: %v", err)
			}
			if out != "w12\n" {
				t.Errorf("stdout = %q, want the id", out)
			}
			r := setRequest(t, f)
			if r.Kind != c.kind || fmt.Sprint(r.Params) != fmt.Sprint(c.params) {
				t.Errorf("request = %s %v, want %s %v", r.Kind, r.Params, c.kind, c.params)
			}
			if r.Why != "read eng8's change" || !r.HoldIfAsleep || !r.Deadline.Equal(watchClock.Add(2*time.Hour)) {
				t.Errorf("request = %+v", r)
			}
		})
	}
}

// --until in local time: a duration, a clock time today, RFC 3339.
func TestWatchSet_Until_DurationClockTodayAndRFC3339(t *testing.T) {
	rfc := watchClock.Add(26 * time.Hour)
	for until, want := range map[string]time.Time{
		"45m":                    watchClock.Add(45 * time.Minute),
		"14:30":                  time.Date(2026, 11, 19, 14, 30, 0, 0, time.Local),
		rfc.Format(time.RFC3339): rfc,
	} {
		f := startWatchFake(t, setOK("w1"))
		if _, err, _ := runWatch(t, "set", "--why", "w", "--until", until); err != nil {
			t.Fatalf("--until %s: %v", until, err)
		}
		if got := setRequest(t, f).Deadline; !got.Equal(want) {
			t.Errorf("--until %s gave %s, want %s", until, got, want)
		}
	}
}

// Every refusal the command can judge alone exits 1 with one line, and
// initech is never asked: nothing is set.
func TestWatchSet_Refusals_ExitOne_OneLine_NothingSent(t *testing.T) {
	long := strings.Repeat("x", watch.MaxWhy+1)
	cases := map[string][]string{
		"no reason":                     {"--until", "1h"},
		"a blank reason":                {"--why", "  ", "--until", "1h"},
		"a reason on two lines":         {"--why", "a\nb", "--until", "1h"},
		"a reason over 200":             {"--why", long, "--until", "1h"},
		"no deadline":                   {"--why", "w"},
		"a past clock time":             {"--why", "w", "--until", "09:59"},
		"now":                           {"--why", "w", "--until", "10:00"},
		"a deadline that is not one":    {"--why", "w", "--until", "soon"},
		"two conditions":                {"--why", "w", "--until", "1h", "--bead", "ini-a", "--status", "closed", "--to", "eng1", "--receipt", "r1", "--delivered"},
		"--status without --bead":       {"--why", "w", "--until", "1h", "--status", "closed"},
		"--bead alone":                  {"--why", "w", "--until", "1h", "--bead", "ini-a"},
		"--bead with both":              {"--why", "w", "--until", "1h", "--bead", "ini-a", "--status", "closed", "--comment-by", "lead1"},
		"--to alone":                    {"--why", "w", "--until", "1h", "--to", "eng1", "--delivered"},
		"--receipt alone":               {"--why", "w", "--until", "1h", "--receipt", "r1", "--delivered"},
		"a send with no ending":         {"--why", "w", "--until", "1h", "--to", "eng1", "--receipt", "r1"},
		"--delivered and --held-for":    {"--why", "w", "--until", "1h", "--to", "eng1", "--receipt", "r1", "--delivered", "--held-for", "5m"},
		"--held-for as long as --until": {"--why", "w", "--until", "1h", "--to", "eng1", "--receipt", "r1", "--held-for", "1h"},
		"--held-for not a duration":     {"--why", "w", "--until", "1h", "--to", "eng1", "--receipt", "r1", "--held-for", "a while"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			f := startWatchFake(t, setOK("w1"))
			out, err, code := runWatch(t, append([]string{"set"}, args...)...)
			if err == nil || code != 1 {
				t.Fatalf("err = %v, exit %d, want a refusal with exit 1", err, code)
			}
			if strings.Contains(err.Error(), "\n") {
				t.Errorf("refusal is not one line: %q", err.Error())
			}
			if n := len(f.requests()); n != 0 {
				t.Errorf("initech was asked %d times; a refusal sets nothing", n)
			}
			if out != "" {
				t.Errorf("stdout = %q on a refusal", out)
			}
		})
	}
}

// A refusal initech makes (an unknown bead, the cap, another's watch, outside
// a pane) is its one line, exit 1; an initech without watches is exit 2.
func TestWatch_InitechsRefusal_IsExit1_AnOlderInitech_IsExit2(t *testing.T) {
	startWatchFake(t, func(tui.IPCRequest) tui.IPCResponse {
		return tui.IPCResponse{OK: false, Error: "ini-zzz is not a bead bd knows; nothing set"}
	})
	if _, err, code := runWatch(t, "set", "--why", "w", "--until", "1h", "--bead", "ini-zzz", "--status", "closed"); code != 1 || err == nil || err.Error() != "ini-zzz is not a bead bd knows; nothing set" {
		t.Errorf("err = %v, exit %d, want initech's line with exit 1", err, code)
	}
	startWatchFake(t, func(tui.IPCRequest) tui.IPCResponse {
		return tui.IPCResponse{OK: false, Error: "unknown action: watch_list"}
	})
	for _, args := range [][]string{{"list"}, {"cancel", "w1"}, {"set", "--why", "w", "--until", "1h"}} {
		if _, err, code := runWatch(t, args...); code != 2 || err == nil || err.Error() != olderTUIWatchLine {
			t.Errorf("%v: err = %v, exit %d, want %q with exit 2", args, err, code, olderTUIWatchLine)
		}
	}
}

func TestWatchCancel_SendsTheID_AndPrintsInitechsLine(t *testing.T) {
	f := startWatchFake(t, func(tui.IPCRequest) tui.IPCResponse { return tui.IPCResponse{OK: true, Data: "cancelled w12"} })
	out, err, _ := runWatch(t, "cancel", "w12")
	if err != nil || out != "cancelled w12\n" {
		t.Fatalf("out %q, err %v", out, err)
	}
	if r := f.requests(); len(r) != 1 || r[0].Action != "watch_cancel" || r[0].Target != "w12" {
		t.Errorf("requests = %+v", r)
	}
}

func TestWatchList_ShowsEachWatch_AndAllAddsTheOwner(t *testing.T) {
	rows := []tui.WatchListing{
		{Watch: watch.Watch{ID: "w12", Owner: "eng6", Kind: watch.KindSendDelivered, Params: map[string]string{"pane": "eng1", "receipt": "r7"},
			Why: "tell me eng1 has it", Deadline: watchClock.Add(30 * time.Minute), State: watch.Waiting}, Wakes: true},
		{Watch: watch.Watch{ID: "w13", Owner: "eng6", Kind: watch.KindBeadStatus, Params: map[string]string{"bead": "ini-abc", "status": "ready_for_qa"},
			Why: "read the change", Deadline: watchClock.Add(26 * time.Hour), State: watch.Waiting}, Sharers: []string{"eng4"}},
	}
	data, _ := json.Marshal(rows)
	f := startWatchFake(t, func(tui.IPCRequest) tui.IPCResponse { return tui.IPCResponse{OK: true, Data: string(data)} })
	out, err, _ := runWatch(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"w12", "my send to eng1 (r7) delivered", "until 10:30", "(wakes)",
		"w13", "ini-abc reaches ready_for_qa", "until Nov 20 12:00", "(shared with eng4)", "read the change"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "eng6") {
		t.Errorf("own list shows the owner column:\n%s", out)
	}
	var req tui.WatchListRequest
	json.Unmarshal([]byte(f.requests()[0].Text), &req)
	if req.All {
		t.Error("list without --all asked for all")
	}

	f = startWatchFake(t, func(tui.IPCRequest) tui.IPCResponse { return tui.IPCResponse{OK: true, Data: string(data)} })
	out, _, _ = runWatch(t, "list", "--all")
	json.Unmarshal([]byte(f.requests()[0].Text), &req)
	if !req.All || !strings.Contains(out, "eng6") {
		t.Errorf("--all: request all=%v, out:\n%s", req.All, out)
	}

	startWatchFake(t, func(tui.IPCRequest) tui.IPCResponse { return tui.IPCResponse{OK: true, Data: "[]"} })
	if out, _, _ := runWatch(t, "list"); out != "no watches\n" {
		t.Errorf("empty list = %q", out)
	}
}

// A cleared or restarted agent keeps its watches, and the command says how
// many; none, or an initech that cannot say, prints nothing and changes
// nothing.
func TestClearAndRestart_SayHowManyWatchesTheyKept(t *testing.T) {
	answer := func(count string, known bool) func(tui.IPCRequest) tui.IPCResponse {
		return func(r tui.IPCRequest) tui.IPCResponse {
			if r.Action == "watch_count" {
				if !known {
					return tui.IPCResponse{OK: false, Error: "unknown action: watch_count"}
				}
				return tui.IPCResponse{OK: true, Data: count}
			}
			return tui.IPCResponse{OK: true}
		}
	}
	cases := []struct {
		count string
		known bool
		want  string
	}{{"2", true, "kept 2 watches for eng1\n"}, {"1", true, "kept 1 watch for eng1\n"}, {"0", true, ""}, {"", false, ""}}
	for _, c := range cases {
		for _, args := range [][]string{{"clear", "eng1"}, {"restart", "eng1"}} {
			startWatchFake(t, answer(c.count, c.known))
			var out bytes.Buffer
			rootCmd.SetOut(&out)
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs(args)
			err := rootCmd.Execute()
			rootCmd.SetArgs(nil)
			if err != nil {
				t.Fatalf("%v with count %q: %v", args, c.count, err)
			}
			got := ""
			if i := strings.Index(out.String(), "kept "); i >= 0 {
				got = out.String()[i:]
			}
			if got != c.want {
				t.Errorf("%v with count %q (known %v): kept line %q, want %q; out %q", args, c.count, c.known, got, c.want, out.String())
			}
		}
	}
}

// Under initech serve the daemon answers the no-store line: exit 1 with that
// line, never the older-initech exit 2; and clear stays silent about watches.
func TestWatch_UnderServe_IsExit1_AndClearStaysSilent(t *testing.T) {
	const line = "this initech keeps no watches: window 1 of a rig does (not a second window or initech serve)"
	f := func(r tui.IPCRequest) tui.IPCResponse {
		if strings.HasPrefix(r.Action, "watch_") {
			return tui.IPCResponse{OK: false, Error: line}
		}
		return tui.IPCResponse{OK: true}
	}
	startWatchFake(t, f)
	for _, args := range [][]string{{"set", "--why", "w", "--until", "1h"}, {"list"}, {"cancel", "w1"}} {
		if _, err, code := runWatch(t, args...); code != 1 || err == nil || err.Error() != line {
			t.Errorf("%v: err = %v, exit %d, want the no-store line with exit 1", args, err, code)
		}
	}
	startWatchFake(t, f)
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"clear", "eng1"})
	err := rootCmd.Execute()
	rootCmd.SetArgs(nil)
	if err != nil || strings.Contains(out.String(), "kept") {
		t.Errorf("clear under serve: err %v, out %q", err, out.String())
	}
}

// The release gate (ini-n0to): window 1 of a release build refuses every watch
// action; the command prints its one line and exits 1, the code a script sees,
// and clear and restart print no kept line. 2C's delivery commit deletes this.
func TestWatch_AReleaseBuildsRefusalExitsOne(t *testing.T) {
	refuse := func(tui.IPCRequest) tui.IPCResponse {
		return tui.IPCResponse{Error: "watches are not in this release"}
	}
	startWatchFake(t, refuse)
	for _, args := range [][]string{
		{"set", "--bead", "ini-a", "--status", "closed", "--why", "read it", "--until", "1h"},
		{"list"},
		{"cancel", "w1"},
	} {
		_, err, code := runWatch(t, args...)
		if code != 1 || err == nil || err.Error() != "watches are not in this release" {
			t.Errorf("watch %s: exit %d, %v; want exit 1 and the line", args[0], code, err)
		}
	}
	var out bytes.Buffer
	printKeptWatches(&out, "eng6")
	if out.Len() != 0 {
		t.Errorf("a refused count printed %q", out.String())
	}
}
