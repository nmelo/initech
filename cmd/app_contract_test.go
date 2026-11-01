package cmd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/nmelo/initech/internal/tui"
)

// The app's contract fixtures: what the service's own handlers write for
// every route and feed event the app reads, from recorded state, committed
// under the app's tests, which decode each with the app's models. A change to
// either side's shape goes red in CI before a build reaches a device.

var updateContract = flag.Bool("update-contract", false, "write the app's contract fixtures instead of comparing them")

// contractDir is the app's contract fixtures folder, found from this file so
// a test that changes directory still writes there.
var contractDir = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "app", "InitechAppTests", "Contract")
}()

// contractTestdata is the recorded transcript and pane list.
var contractTestdata = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", "appcontract")
}()

// contractRoot stands for the temp rig folder in the fixtures, so they do
// not change from run to run.
const contractRoot = "/Users/operator/Desktop/Projects/demo"

const contractToken = "contract-device-token"

// contractTUI answers a rig socket by action, as a TUI does: the pane list,
// a peek, and a delivered send or key.
type contractTUI struct {
	mu    sync.Mutex
	panes string
}

func (c *contractTUI) setPanes(p string) { c.mu.Lock(); c.panes = p; c.mu.Unlock() }

func (c *contractTUI) answer(req tui.IPCRequest) tui.IPCResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch req.Action {
	case "list":
		return tui.IPCResponse{OK: true, Data: c.panes}
	case "peek":
		return tui.IPCResponse{OK: true, Data: "Bash command\n\n  make check-fast\n\nDo you want to proceed?\n❯ 1. Yes\n  2. No"}
	case "send", "keystroke":
		return tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered}
	}
	return tui.IPCResponse{Error: "unknown action " + req.Action}
}

func (c *contractTUI) serve(t *testing.T, sock string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var req tui.IPCRequest
				json.Unmarshal(line, &req)
				out, _ := json.Marshal(c.answer(req))
				conn.Write(append(out, '\n'))
			}()
		}
	}()
}

// contractRunner is the progress fixture's bd, gh and git, with the
// waiting_on_nelson read answered from waiting.
type contractRunner struct {
	*progressFake
	mu      sync.Mutex
	waiting string
}

func (r *contractRunner) RunInDir(dir, name string, args ...string) (string, error) {
	if name == "bd" && strings.Contains(strings.Join(args, " "), "waiting_on_nelson") {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.waiting, nil
	}
	return r.progressFake.RunInDir(dir, name, args...)
}

func (r *contractRunner) Run(name string, args ...string) (string, error) {
	return r.RunInDir("", name, args...)
}

// stepper runs one round of a service loop: the loop does its work, then asks
// for its next wait, which ends the round.
type stepper struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

func (s *stepper) after(time.Duration) <-chan time.Time {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	return make(chan time.Time)
}

func (s *stepper) once(run func(context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	run(ctx)
	cancel()
}

// ini-470 AC2: every route and feed event the app reads, as the service's
// own handlers write it from a recorded transcript and recorded rig state, is
// the file committed in the app's Contract folder; a difference, a missing
// file or a file no route writes fails, naming how to regenerate.
func TestAppContract_FixturesAreWhatTheServiceWrites(t *testing.T) {
	skipWindows(t)
	t.Setenv("INITECH_SOCKET", "")
	t.Setenv("HOME", t.TempDir())
	projects, dir := ownRigFolder(t)
	cfg := "project: demo\nroot: " + dir + "\nroles:\n  - eng1\n  - super\n  - qa1\n" + progressRepos
	if err := os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	recorded, err := os.ReadFile(filepath.Join(contractTestdata, "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(dir, "eng1", "sess-1.jsonl")
	os.MkdirAll(filepath.Dir(session), 0o700)
	if err := os.WriteFile(session, recorded, 0o600); err != nil {
		t.Fatal(err)
	}
	panesFor := func(file string) string {
		raw, err := os.ReadFile(filepath.Join(contractTestdata, file))
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(raw), "SESSION", session)
	}
	tu := &contractTUI{panes: panesFor("panes-before.json")}
	tu.serve(t, tui.SocketPath(dir, "demo"))

	oldNow := progressNow
	t.Cleanup(func() { progressNow = oldNow })
	progressNow = func() time.Time { return progressFixtureNow }
	runner := &contractRunner{progressFake: &progressFake{data: progressTestdata},
		waiting: `[{"id":"fx-1","title":"Approve the v2.55 release","assignee":"super"}]`}

	now := progressFixtureNow
	var clockMu sync.Mutex
	clock := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now }
	step := &stepper{}
	srv := appserve.New(appserve.Config{
		Backend:  &appBackend{dirs: []string{projects}, runner: runner},
		Digest:   sha256.Sum256([]byte(contractToken)),
		HasToken: true,
		StateDir: t.TempDir(),
		Now:      clock,
		After:    step.after,
	})
	// One round of every loop: the baseline poll, bd read, progress and tail.
	step.once(srv.Run)
	step.once(srv.RunWaiting)
	step.once(srv.RunProgress)
	step.once(srv.RunTail)

	h := srv.Handler()
	call := func(method, path, body string) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+contractToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body)
		}
		return rec.Body.Bytes()
	}

	got := map[string][]byte{}
	got["rigs"] = call("GET", "/v1/rigs", "")
	got["status"] = call("GET", "/v1/status", "")
	got["status-rig"] = call("GET", "/v1/status/demo", "")
	// The conversation as the app pages it: the newest page (page 1), then
	// each page's before passed back as given, to the page that reaches the
	// start. Every kind and a skipped line must be on some page, or the
	// recorded transcript no longer spans what the app decodes.
	var page struct {
		Messages     []struct{ Kind string } `json:"messages"`
		Before       int64                   `json:"before"`
		StartReached bool                    `json:"start_reached"`
		Skipped      int                     `json:"skipped_lines"`
	}
	kinds, skipped := map[string]bool{}, 0
	path := "/v1/conversation/demo/eng1?limit=2"
	for n := 1; ; n++ {
		if n > 50 {
			t.Fatal("paging back never reached the start")
		}
		name := "conversation-page-" + jsonInt(int64(n))
		got[name] = call("GET", path, "")
		page.Messages = nil
		if err := json.Unmarshal(got[name], &page); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, m := range page.Messages {
			kinds[m.Kind] = true
		}
		skipped += page.Skipped
		if page.StartReached {
			if n < 3 {
				t.Fatalf("the recorded transcript reached its start on page %d: it needs more entries", n)
			}
			break
		}
		path = "/v1/conversation/demo/eng1?limit=2&before=" + jsonInt(page.Before)
	}
	for _, k := range appserve.Kinds {
		if !kinds[k] {
			t.Errorf("no page of the recorded transcript carries kind %q", k)
		}
	}
	if skipped == 0 {
		t.Error("no page counts a skipped line: the recorded transcript lost its malformed line")
	}
	got["conversation-no-transcript"] = call("GET", "/v1/conversation/demo/qa1", "")
	got["blockers"] = call("GET", "/v1/blockers", "")
	got["progress"] = call("GET", "/v1/progress", "")
	got["progress-rig"] = call("GET", "/v1/progress/demo", "")
	got["pins-put"] = call("PUT", "/v1/pins", `[{"rig":"demo","pane":"eng1","name":"Builder","order":0},{"rig":"demo","pane":"super","name":"","order":1}]`)
	got["pins"] = call("GET", "/v1/pins", "")
	got["send"] = call("POST", "/v1/send/demo/eng1", `{"text":"ship it","enter":true}`)
	got["keys"] = call("POST", "/v1/keys/demo/super", `{"key":"enter"}`)
	got["devices-put"] = call("PUT", "/v1/devices", `{"token":"`+strings.Repeat("ab", 32)+`"}`)
	// The two pushes as Sender.Push puts them on the wire (json.Marshal of the
	// payload): the app routes a tap, and decides the foreground banner, from
	// these. The bead is the one the blockers fixture lists.
	got["push-pane"], _ = json.Marshal(appserve.NewPayload("demo", "eng1", "stuck"))
	got["push-bead"], _ = json.Marshal(appserve.NewBeadPayload("demo", "fx-1"))

	// The changes every feed event announces: eng1 stops at a dialog, a bead
	// enters waiting_on_nelson (and so the blockers list changes), and eng1's
	// transcript gains a reply.
	tu.setPanes(panesFor("panes-after.json"))
	runner.mu.Lock()
	runner.waiting = `[{"id":"fx-1","title":"Approve the v2.55 release","assignee":"super"},{"id":"fx-2","title":"Pick the app icon","assignee":"super"}]`
	runner.mu.Unlock()
	f, err := os.OpenFile(session, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"assistant","uuid":"a9","timestamp":"2026-10-31T21:59:30Z","message":{"role":"assistant","content":[{"type":"text","text":"Waiting on the permission dialog."}]}}` + "\n")
	f.Close()
	step.once(srv.Run)
	step.once(srv.RunWaiting)
	step.once(srv.RunTail)

	hs := httptest.NewServer(h)
	defer hs.Close()
	events := readFeed(t, hs.URL+"/v1/feed?since=0", []string{"pane_state", "stuck", "waiting_item", "transcript", "blockers"})
	for typ, data := range events {
		got["feed-"+typ] = data
	}
	// What a subscriber gets when its id is older than what is kept.
	clockMu.Lock()
	now = now.Add(appserve.FeedKeep + time.Minute)
	clockMu.Unlock()
	tu.setPanes(panesFor("panes-before.json"))
	step.once(srv.Run)
	reset := readFeed(t, hs.URL+"/v1/feed?since=1", []string{"reset"})
	got["feed-reset"] = reset["reset"]

	real, _ := filepath.EvalSymlinks(dir)
	for name, raw := range got {
		raw = bytes.ReplaceAll(raw, []byte(real), []byte(contractRoot))
		raw = bytes.ReplaceAll(raw, []byte(dir), []byte(contractRoot))
		var out bytes.Buffer
		if err := json.Indent(&out, raw, "", "  "); err != nil {
			t.Fatalf("%s is not JSON: %v\n%s", name, err, raw)
		}
		out.WriteByte('\n')
		got[name] = out.Bytes()
	}
	checkContract(t, got)
}

// jsonInt is n as the app sends it back.
func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

// readFeed reads the feed at url until it has seen one event of each type in
// want, and returns each one's data line.
func readFeed(t *testing.T, url string, want []string) map[string][]byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+contractToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string][]byte{}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	typ := ""
	for len(out) < len(want) && sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			typ = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if _, seen := out[typ]; !seen {
				for _, w := range want {
					if w == typ {
						out[typ] = []byte(strings.TrimPrefix(line, "data: "))
					}
				}
			}
		}
	}
	for _, w := range want {
		if out[w] == nil {
			t.Fatalf("the feed at %s sent no %s event (got %v)", url, w, keys(out))
		}
	}
	return out
}

func keys(m map[string][]byte) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}

// checkContract writes got as the fixtures with -update-contract; otherwise
// it fails on every file that differs, is missing, or no route writes.
func checkContract(t *testing.T, got map[string][]byte) {
	t.Helper()
	const regen = "go test ./cmd -run TestAppContract -update-contract, then commit app/InitechAppTests/Contract"
	if *updateContract {
		os.MkdirAll(contractDir, 0o755)
		for name, b := range got {
			if err := os.WriteFile(filepath.Join(contractDir, name+".json"), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, name := range keys(got) {
		have, err := os.ReadFile(filepath.Join(contractDir, name+".json"))
		if err != nil {
			t.Errorf("contract fixture %s.json is missing; run %s", name, regen)
			continue
		}
		if !bytes.Equal(have, got[name]) {
			t.Errorf("contract fixture %s.json differs from what the service writes now; run %s\n--- committed:\n%s\n--- service:\n%s", name, regen, have, got[name])
		}
	}
	entries, _ := os.ReadDir(contractDir)
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".json")
		if strings.HasSuffix(e.Name(), ".json") && got[name] == nil {
			t.Errorf("contract fixture %s is written by no route; remove it or add its route here", e.Name())
		}
	}
}
