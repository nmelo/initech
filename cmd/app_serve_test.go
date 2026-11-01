package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/appserve"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/progress"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// serveFakeTUI answers every request on sockPath with resp, counting them.
func serveFakeTUI(t *testing.T, sockPath string, resp tui.IPCResponse) *atomic.Int32 {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var n atomic.Int32
	data, _ := json.Marshal(resp)
	// One line, built before any connection: each goroutine only reads it
	// (an append per connection raced on data's spare capacity).
	line := append(append([]byte(nil), data...), '\n')
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if _, err := bufio.NewReader(c).ReadString('\n'); err != nil {
					return
				}
				n.Add(1)
				c.Write(line)
			}()
		}
	}()
	return &n
}

const appServePanes = `[
 {"name":"eng1","activity":"running","alive":true,"visible":true,"beads":["ini-x"],"banner":"none","context_percent":41},
 {"name":"super","activity":"idle","alive":true,"visible":true,"beads":[],"banner":"permission_prompt","modal_latched":true,"modal_age_sec":12},
 {"name":"qa1","activity":"suspended","alive":false,"visible":false,"banner":""}
]`

const appServeBD = `#!/bin/sh
cat <<'EOF'
[{"id":"ini-a","title":"A","status":"in_progress","assignee":"eng1"},
 {"id":"ini-b","title":"B","status":"in_qa","assignee":"super"}]
EOF
`

// ini-r0q.1 AC1: the service's status for a rig is what initech status
// --json prints on that rig, bd's bead answer included, object for object
// (the service adds only screen). Red at base: no service.
func TestAppServeStatus_MatchesStatusJSONOnTheRig(t *testing.T) {
	skipWindows(t)
	restoreColor := disableColor(t)
	defer restoreColor()
	dir := shortProjectDir(t)
	writeBasicConfig(t, dir, "demo")
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0o755)
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	// bd answers only in the rig's root, so a service that read bd anywhere
	// else would get the ribbon's beads instead.
	writeExecutable(t, filepath.Join(binDir, "bd"), "#!/bin/sh\n[ \"$(pwd -P)\" = \""+real+"\" ] || exit 1\n"+strings.TrimPrefix(appServeBD, "#!/bin/sh\n"))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INITECH_SOCKET", "")
	sock := tui.SocketPath(dir, "demo")
	serveFakeTUI(t, sock, tui.IPCResponse{OK: true, Data: appServePanes})

	restoreWD := chdirForTest(t, dir)
	statusJSON = true
	defer func() { statusJSON = false }()
	var out bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&out)
	c.SetErr(&bytes.Buffer{})
	if err := runStatus(c, nil); err != nil {
		t.Fatal(err)
	}
	var cli []map[string]any
	if err := json.Unmarshal(out.Bytes(), &cli); err != nil {
		t.Fatal(err)
	}
	// The service runs from wherever launchd starts it, not the rig.
	restoreWD()
	defer chdirForTest(t, t.TempDir())()

	b := &appBackend{dirs: []string{filepath.Dir(dir)}, runner: &iexec.DefaultRunner{}}
	var rig appserve.Rig
	for _, rg := range b.FindRigs() {
		if rg.Root == dir {
			rig = rg
		}
	}
	if rig.Socket != sock || rig.Name != "demo" {
		t.Fatalf("the scan did not find the rig at %s: %+v", dir, rig)
	}
	got, err := b.Status(rig)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, cli) {
		t.Errorf("service status differs from initech status --json.\nservice: %v\ncli:     %v", got, cli)
	}
	if beads := cli[1]["beads"]; !reflect.DeepEqual(beads, []any{"ini-b"}) {
		t.Errorf("fixture: super's beads %v, want bd's [ini-b] (the ribbon says none)", beads)
	}

	panes, err := b.Panes(rig)
	if err != nil || len(panes) != 3 || panes[1] != (appserve.PaneState{Name: "super", State: "idle", Banner: "permission_prompt"}) || panes[2].State != "suspended" {
		t.Errorf("panes = %+v %v", panes, err)
	}
}

// ini-r0q.1 AC6 feed-it-nothing: a folder with no rig, and a rig whose
// socket has no TUI, give no rig and an error, never a hang.
func TestAppServeBackend_NoRigAndNoTUI(t *testing.T) {
	skipWindows(t)
	empty := t.TempDir()
	b := &appBackend{dirs: []string{empty}, runner: &iexec.DefaultRunner{}}
	if rigs := b.FindRigs(); len(rigs) != 0 {
		t.Errorf("empty folder: %v", rigs)
	}
	dir := shortProjectDir(t)
	writeBasicConfig(t, dir, "demo")
	b.dirs = []string{filepath.Dir(dir)}
	for _, rg := range b.FindRigs() {
		if rg.Root == dir {
			if _, err := b.Panes(rg); err == nil {
				t.Error("a rig with no TUI answered")
			}
			return
		}
	}
	t.Error("the rig was not found")
}

// ini-r0q.1 AC9 (token, text half): initech app-serve token prints the
// pairing link once, with the address the service binds, the port, a new
// token and the certificate pin; it stores the token's digest only, and a
// second run revokes the first token. With no tailnet address it refuses
// and names --listen local-network.
func TestAppServeToken_PrintsThePairingLinkOnce(t *testing.T) {
	state := filepath.Join(t.TempDir(), "app-serve")
	oldDir, oldAddrs, oldListen, oldPort := appServeStateDir, appServeAddrs, appServeListen, appServePort
	t.Cleanup(func() {
		appServeStateDir, appServeAddrs, appServeListen, appServePort = oldDir, oldAddrs, oldListen, oldPort
	})
	appServeStateDir = func() string { return state }
	appServeAddrs = func() ([]appserve.IfaceAddr, error) {
		return []appserve.IfaceAddr{
			{Iface: "en0", IP: net.ParseIP("192.168.1.50"), Up: true},
			{Iface: "utun4", IP: net.ParseIP("100.101.1.2"), Up: true},
			{Iface: "utun4", IP: net.ParseIP("fd7a:115c:a1e0::1"), Up: true},
		}, nil
	}
	appServeListen, appServePort = appserve.ListenTailnet, appserve.DefaultPort

	run := func() string {
		var out bytes.Buffer
		c := &cobra.Command{}
		c.SetOut(&out)
		if err := runAppServeToken(c, nil); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	out := run()
	_, fp, err := appserve.LoadCert(state)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`initech://pair\?host=100\.101\.1\.2&port=7392&token=([A-Za-z0-9_-]{43})&fp=` + regexp.QuoteMeta(fp) + `\n`)
	m := re.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no pairing link with the tailnet address, port, token and pin in:\n%s", out)
	}
	if strings.Count(out, m[1]) != 1 {
		t.Errorf("the token is printed %d times", strings.Count(out, m[1]))
	}
	if !strings.Contains(out, "█") {
		t.Errorf("no QR code in the output:\n%s", out)
	}
	stored, _ := os.ReadFile(filepath.Join(state, appserve.TokenFile))
	if strings.Contains(string(stored), m[1]) {
		t.Error("the token itself is stored")
	}
	second := re.FindStringSubmatch(run())
	if second == nil || second[1] == m[1] {
		t.Fatalf("second run: %v, want a new token", second)
	}
	d, _ := appserve.LoadDigest(state)
	h := appserve.New(appserve.Config{Backend: &appBackend{}, Digest: d, HasToken: true}).Handler()
	for tok, want := range map[string]int{m[1]: 401, second[1]: 200} {
		req := httptest.NewRequest("GET", "/v1/rigs", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("token %s...: %d, want %d", tok[:4], rec.Code, want)
		}
	}

	appServeAddrs = func() ([]appserve.IfaceAddr, error) {
		return []appserve.IfaceAddr{{Iface: "en0", IP: net.ParseIP("192.168.1.50"), Up: true}}, nil
	}
	c := &cobra.Command{}
	c.SetOut(&bytes.Buffer{})
	if err := runAppServeToken(c, nil); err == nil || !strings.Contains(err.Error(), "--listen local-network") {
		t.Errorf("no tailnet: %v, want the error naming --listen local-network", err)
	}
}

// countingRunner records every command run through it and runs none.
type countingRunner struct {
	mu    sync.Mutex
	calls []string
}

func (r *countingRunner) Run(name string, args ...string) (string, error) {
	return r.RunInDir("", name, args...)
}

func (r *countingRunner) RunInDir(dir, name string, args ...string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	r.mu.Unlock()
	return "", errors.New("not run")
}

// ini-r0q.1 AC5: over a minute of idle the service polls the running rig
// through its TUI only: the runner every bd and git call goes through is
// called zero times. A status request is what reads bd, and it does (the
// positive twin: the counter can see a bd call).
func TestAppServe_AMinuteOfIdleMakesNoBdOrGitCall(t *testing.T) {
	skipWindows(t)
	projects, err := os.MkdirTemp("", "iap-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(projects) })
	dir := filepath.Join(projects, "r")
	os.MkdirAll(dir, 0o755)
	writeBasicConfig(t, dir, "demo")
	tuiCalls := serveFakeTUI(t, tui.SocketPath(dir, "demo"), tui.IPCResponse{OK: true, Data: appServePanes})

	runner := &countingRunner{}
	now := time.Unix(1_800_000_000, 0)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	ctx, cancel := context.WithCancel(context.Background())
	var waited time.Duration
	after := func(d time.Duration) <-chan time.Time {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
		waited += d
		ch := make(chan time.Time, 1)
		if waited >= time.Minute {
			cancel()
			return make(chan time.Time)
		}
		ch <- now
		return ch
	}
	srv := appserve.New(appserve.Config{Backend: &appBackend{dirs: []string{projects}, runner: runner},
		HasToken: true, Now: clock, After: after})
	srv.Run(ctx)
	if n := len(runner.calls); n != 0 {
		t.Errorf("a minute of idle ran %d commands: %v", n, runner.calls)
	}
	if tuiCalls.Load() == 0 {
		t.Fatal("the poll never asked the TUI: the minute did not reach the rig")
	}

	(&appBackend{dirs: []string{projects}, runner: runner}).Status(appserve.Rig{Name: "demo", Root: dir, Socket: tui.SocketPath(dir, "demo")})
	if len(runner.calls) == 0 {
		t.Error("a status read ran no command: the counter cannot see bd")
	}
}

// ini-r0q.1 AC8: the service's report for a rig and prefix is what initech
// progress --json prints for them, and the active efforts come from one bd
// list of the beads past open.
func TestAppServeProgress_IsProgressJSONForTheEffort(t *testing.T) {
	cli, err := runProgressJSON(t, &progressFake{}, progressRepos, "fx-")
	if err != nil {
		t.Fatal(err)
	}
	var want progress.Report
	if err := json.Unmarshal([]byte(cli), &want); err != nil {
		t.Fatal(err)
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	progressNow = func() time.Time { return progressFixtureNow }
	fake := &progressFake{data: progressTestdata}
	got, err := (&appBackend{runner: fake}).Progress(appserve.Rig{Name: "initech", Root: dir}, "fx-")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Errorf("service report differs from progress --json.\nservice: %s\ncli:     %s", a, b)
	}

	lister := &effortLister{out: `[{"id":"ini-a.1"},{"id":"ini-a.2"},{"id":"ini-b"}]`}
	efforts, err := (&appBackend{runner: lister}).ActiveEfforts(appserve.Rig{Root: dir})
	if err != nil || !reflect.DeepEqual(efforts, []string{"ini-a.", "ini-b"}) {
		t.Errorf("efforts = %v %v", efforts, err)
	}
	if len(lister.calls) != 1 || !strings.Contains(lister.calls[0], "bd list --status in_progress,ready_for_qa,in_qa --json") {
		t.Errorf("calls = %v, want one bd list of the beads past open", lister.calls)
	}
}

// effortLister answers every command with out, recording them.
type effortLister struct {
	out   string
	calls []string
}

func (l *effortLister) Run(name string, args ...string) (string, error) {
	return l.RunInDir("", name, args...)
}

func (l *effortLister) RunInDir(dir, name string, args ...string) (string, error) {
	l.calls = append(l.calls, name+" "+strings.Join(args, " "))
	return l.out, nil
}

// A TUI older than session_file names no transcript: the service finds the
// pane's newest one itself, as the TUI would, or every pane of a rig not yet
// restarted has an empty conversation and its super nothing waiting. A TUI
// that names one is believed; a pane with none has none; a remote pane is not
// looked for here.
func TestAppServePanes_FindsTheTranscriptForAnOlderTUI(t *testing.T) {
	skipWindows(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dir := shortProjectDir(t)
	writeBasicConfig(t, dir, "demo")
	session := filepath.Join(home, ".claude", "projects", strings.ReplaceAll(filepath.Join(dir, "eng1"), string(filepath.Separator), "-"))
	os.MkdirAll(session, 0o700)
	found := filepath.Join(session, "s1.jsonl")
	os.WriteFile(found, []byte("{}\n"), 0o600)
	panes := `[{"name":"eng1","activity":"idle","alive":true},
	 {"name":"eng2","activity":"idle","alive":true},
	 {"name":"qa1","activity":"idle","alive":true,"session_file":"/named/by/the/tui.jsonl"},
	 {"name":"eng1","host":"far","activity":"idle","alive":true}]`
	sock := tui.SocketPath(dir, "demo")
	serveFakeTUI(t, sock, tui.IPCResponse{OK: true, Data: panes})
	got, err := (&appBackend{runner: &iexec.DefaultRunner{}}).Panes(appserve.Rig{Name: "demo", Root: dir, Socket: sock})
	if err != nil || len(got) != 4 {
		t.Fatalf("panes = %+v %v", got, err)
	}
	for i, want := range []string{found, "", "/named/by/the/tui.jsonl", ""} {
		if got[i].SessionFile != want {
			t.Errorf("pane %d (%s): session file %q, want %q", i, got[i].Name, got[i].SessionFile, want)
		}
	}
}

// A service started from a shell inside a pane drops that pane's identity:
// its reads are not aimed at that rig and its sends not signed as that agent.
func TestAppServeLeavePane_DropsThePanesIdentity(t *testing.T) {
	t.Setenv("INITECH_SOCKET", "/some/rig.sock")
	t.Setenv("INITECH_AGENT", "eng9")
	appServeLeavePane()
	if os.Getenv("INITECH_SOCKET") != "" || os.Getenv("INITECH_AGENT") != "" {
		t.Errorf("still set: %q %q", os.Getenv("INITECH_SOCKET"), os.Getenv("INITECH_AGENT"))
	}
	if got := sendFrom(); got != tui.OperatorSender {
		t.Errorf("a send is from %q, want the operator", got)
	}
}
