package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/nmelo/initech/internal/progress"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// initech progress: the effort table and lines, on the fixture in
// testdata/progress, whose golden.txt is theguy's progress.py run on the same
// files (gen_golden.sh).

// progressFixtureNow is the clock gen_golden.sh fixes.
var progressFixtureNow = time.Date(2026, 10, 31, 22, 0, 0, 0, time.UTC)

// progressFake answers bd, gh and git from testdata/progress, and records
// every call. It is a Runner and a contextRunner.
type progressFake struct {
	custom    string // bd config get status.custom; "" is this rig's list
	customErr error
	mu        sync.Mutex
	calls     []string
	bdErr     error  // bd list fails
	origin    string // git remote get-url origin; "" fails
	gitLog    string // git log output
	hang      bool   // git fetch never finishes until its context ends
	fetched   bool
	data      string // absolute path of testdata/progress
}

func (f *progressFake) RunInDir(dir, name string, args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, dir+"|"+name+" "+strings.Join(args, " "))
	f.mu.Unlock()
	data := f.data
	switch {
	case name == "bd" && args[0] == "list":
		if f.bdErr != nil {
			return "", f.bdErr
		}
		return readFixture(data, "list.json")
	case name == "bd" && args[0] == "comments":
		return readFixture(data, "comments", args[1]+".json")
	case name == "gh":
		return readFixture(data, "commits.txt")
	case name == "git" && args[0] == "remote":
		if f.origin == "" {
			return "", errors.New("no origin")
		}
		return f.origin, nil
	case name == "git" && args[0] == "log":
		return f.gitLog, nil
	case name == "bd" && args[0] == "config":
		if f.customErr != nil {
			return "", f.customErr
		}
		if f.custom != "" {
			return f.custom + "\n", nil
		}
		return "ready_for_qa,in_qa,qa_passed,waiting_on_nelson,waiting\n", nil
	case name == "git" && args[0] == "fetch":
		f.mu.Lock()
		f.fetched = true
		f.mu.Unlock()
		return "", nil
	}
	return "", errors.New("unexpected call: " + name + " " + strings.Join(args, " "))
}

func (f *progressFake) Run(name string, args ...string) (string, error) {
	return f.RunInDir("", name, args...)
}

func (f *progressFake) RunContext(ctx context.Context, name string, args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "ctx|"+name+" "+strings.Join(args, " "))
	f.fetched = true
	f.mu.Unlock()
	if f.hang {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return "", nil
}

// progressTestdata is testdata/progress, found from this file so that a
// test that changes directory still reads it.
var progressTestdata = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", "progress")
}()

func readFixture(parts ...string) (string, error) {
	b, err := os.ReadFile(filepath.Join(parts...))
	return string(b), err
}

// runProgressFixture runs initech progress <args> in a project named
// initech (repos as given) with fake reading the fixture; it returns stdout
// and the error.
func runProgressFixture(t *testing.T, fake *progressFake, repos string, args ...string) (string, error) {
	t.Helper()
	fake.data = progressTestdata
	dir := t.TempDir()
	cfg := "project: initech\nroot: " + dir + "\nroles:\n  - eng1\n" + repos
	if err := os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(chdirForTest(t, dir))
	oldR, oldNow, oldHours := progressRunner, progressNow, progressHours
	t.Cleanup(func() { progressRunner, progressNow, progressHours = oldR, oldNow, oldHours })
	progressRunner = fake
	progressNow = func() time.Time { return progressFixtureNow }
	progressHours = 6
	if os.Getenv("INITECH_SOCKET") == "" {
		t.Setenv("INITECH_SOCKET", filepath.Join(dir, "none.sock"))
	}
	var out bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&out)
	err := runProgress(c, args)
	return out.String(), err
}

const progressRepos = "repos:\n  - url: github.com:nmelo/initech.git\n    name: initech\n"

// AC1: the table and the effort lines equal progress.py's output on the
// same fixture, field for field. Red at base: no progress command. One line
// departs from the script by ruling: the closed line ends with the stuck and
// blocked counts (ini-s3b), here 0 and 0 (the panes are not read, and the
// fixture has no blocked bead).
func TestProgress_FixtureMatchesTheGuysScript(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join(progressTestdata, "golden.txt"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := runProgressFixture(t, &progressFake{}, progressRepos, "fx-")
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if !strings.HasPrefix(out, string(golden)) {
		t.Errorf("the table and effort lines differ from progress.py's.\n--- got:\n%s\n--- progress.py:\n%s", out, golden)
	}
	rest := strings.TrimPrefix(out, string(golden))
	for _, want := range []string{
		"quiet 1h or more:",
		`  fx-3 eng2: waiting on "the shared test cluster"`,
		"  fx-8 eng2: stalled",
		"note: owner panes not read",
	} {
		if !strings.Contains(rest, want) {
			t.Errorf("after the effort lines, want %q; got:\n%s", want, rest)
		}
	}
	if strings.Contains(rest, "fx-1 ") || strings.Contains(rest, "fx-10") || strings.Contains(rest, "fx-2 ") {
		t.Errorf("a bead that moved within the hour has a wait line:\n%s", rest)
	}
}

// AC6: nothing about estimates, actuals or a finish time is printed, though
// the fixture's bead carries an estimate and an actual.
func TestProgress_PrintsNoEstimateActualOrFinishTime(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join(progressTestdata, "list.json"))
	if !bytes.Contains(raw, []byte("actual_min")) || !bytes.Contains(raw, []byte("estimated_minutes")) {
		t.Fatal("fixture: the bead does not carry an estimate and an actual")
	}
	out, err := runProgressFixture(t, &progressFake{}, progressRepos, "fx-")
	if err != nil {
		t.Fatal(err)
	}
	low := strings.ToLower(out)
	for _, bad := range []string{"estimate", "actual", "finish", "eta", "remaining", "120", "33 min", "~3 min"} {
		if strings.Contains(low, bad) {
			t.Errorf("output names %q:\n%s", bad, out)
		}
	}
}

// AC5 feed-it-nothing: no beads with the prefix, bd unreachable, no repos.
func TestProgress_NoBeadsBdDownOrNoRepos(t *testing.T) {
	out, err := runProgressFixture(t, &progressFake{}, progressRepos, "zz-")
	if err != nil || out != "no beads with prefix zz- in initech\n" {
		t.Errorf("no beads with the prefix: %q %v, want the one line and no error", out, err)
	}

	out, err = runProgressFixture(t, &progressFake{bdErr: errors.New("dolt server down\nmore")}, progressRepos, "fx-")
	var ec exitCoder
	if err == nil || !errors.As(err, &ec) || ec.ExitCode() != 1 || out != "" {
		t.Fatalf("bd unreachable: out %q err %v, want no output and an exit-1 error", out, err)
	}
	if msg := err.Error(); msg != "bd unreachable: dolt server down" {
		t.Errorf("bd unreachable message %q, want one line", msg)
	}

	out, err = runProgressFixture(t, &progressFake{}, "", "fx-")
	if err != nil {
		t.Fatal(err)
	}
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "fx-1 ") {
			if f := strings.Fields(ln); len(f) < 5 || f[4] != "-" {
				t.Errorf("no repos: fx-1 row %q, want the commits column \"-\"", ln)
			}
			return
		}
	}
	t.Errorf("no fx-1 row in:\n%s", out)
}

// AC5: the agent's own clone is read after a bounded fetch; a fetch that
// times out reads the clone as it stands, with a note, and gh is not asked.
func TestProgress_CloneFetchTimesOutReadsTheCloneWithANote(t *testing.T) {
	old := progressFetchTimeout
	progressFetchTimeout = 50 * time.Millisecond
	t.Cleanup(func() { progressFetchTimeout = old })
	t.Setenv("INITECH_AGENT", "eng1")
	fake := &progressFake{
		origin: "git@github.com:nmelo/initech.git",
		gitLog: "2026-10-31T17:40:00-04:00\tfeat(bead): the ribbon reads bd (fx-1)\n2026-10-31T16:15:00-04:00\tfix: fx-1 follow-up\n",
		hang:   true,
	}
	// The clone the command looks for: <root>/eng1/src.
	makeClone := func(dir string) { os.MkdirAll(filepath.Join(dir, "eng1", "src"), 0o755) }
	fake.data = progressTestdata
	dir := t.TempDir()
	makeClone(dir)
	cfg := "project: initech\nroot: " + dir + "\nroles:\n  - eng1\n" + progressRepos
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0o644)
	t.Cleanup(chdirForTest(t, dir))
	oldR, oldNow, oldHours := progressRunner, progressNow, progressHours
	t.Cleanup(func() { progressRunner, progressNow, progressHours = oldR, oldNow, oldHours })
	progressRunner = fake
	progressNow = func() time.Time { return progressFixtureNow }
	progressHours = 6
	t.Setenv("INITECH_SOCKET", filepath.Join(dir, "none.sock"))
	var out bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&out)
	start := time.Now()
	if err := runProgress(c, []string{"fx-"}); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("progress took %v with a hung fetch; the bound did not hold", took)
	}
	s := out.String()
	if !strings.Contains(s, "note: fetch of origin/main timed out; commits read from the clone as it stands") {
		t.Errorf("no timeout note:\n%s", s)
	}
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(ln, "fx-1 ") {
			if f := strings.Fields(ln); len(f) < 5 || f[4] != "2" {
				t.Errorf("fx-1 row %q: want 2 commits from the clone's log", ln)
			}
		}
	}
	for _, call := range fake.calls {
		if strings.Contains(call, "|gh ") {
			t.Errorf("gh was asked though the clone answered: %s", call)
		}
	}
	if !fake.fetched {
		t.Error("the clone was not fetched")
	}
}

// AC7 reachability: owner pane states come from the running initech's list
// reply; a busy owner's quiet bead reads "working, unrecorded".
func TestProgress_BusyOwnerPaneReadsWorkingUnrecorded(t *testing.T) {
	skipWindows(t)
	dir := shortProjectDir(t)
	panes, _ := json.Marshal([]tui.PaneInfo{{Name: "eng2", Activity: "running", Alive: true}, {Name: "eng1", Activity: "idle", Alive: true}})
	resp, _ := json.Marshal(tui.IPCResponse{OK: true, Data: string(panes)})
	sock := filepath.Join(dir, "p.sock")
	_, cleanup := startATIPCServer(t, sock, responseMode{raw: string(resp) + "\n"})
	defer cleanup()
	t.Setenv("INITECH_SOCKET", sock)
	out, err := runProgressFixture(t, &progressFake{}, progressRepos, "fx-")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  fx-3 eng2: working, unrecorded", "  fx-8 eng2: working, unrecorded"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q (eng2's pane is running); got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "owner panes not read") {
		t.Errorf("the panes were read but the note says not:\n%s", out)
	}
}

// runProgressJSON is runProgressFixture with --json set.
func runProgressJSON(t *testing.T, fake *progressFake, repos string, args ...string) (string, error) {
	t.Helper()
	old := progressJSON
	t.Cleanup(func() { progressJSON = old })
	progressJSON = true
	return runProgressFixture(t, fake, repos, args...)
}

// ini-r0q.1 AC8: --json prints the data the table shows. The JSON, decoded
// and rendered, is the table byte for byte, so no row, count, close, path
// bead, engineer, wait or note is in one and not the other. Red at base: no
// --json flag.
func TestProgressJSON_RendersToTheSameTable(t *testing.T) {
	table, err := runProgressFixture(t, &progressFake{}, progressRepos, "fx-")
	if err != nil {
		t.Fatal(err)
	}
	out, err := runProgressJSON(t, &progressFake{}, progressRepos, "fx-")
	if err != nil {
		t.Fatal(err)
	}
	var rep progress.Report
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rep); err != nil {
		t.Fatalf("--json is not one report object: %v\n%s", err, out)
	}
	if got := progress.RenderReport(rep); got != table {
		t.Errorf("the JSON renders to a different table.\n--- from JSON:\n%s\n--- the table:\n%s", got, table)
	}
	if len(rep.Beads) == 0 || len(rep.Quiet) == 0 || rep.Total == 0 {
		t.Fatalf("fixture report is empty: %+v", rep)
	}
	for _, key := range []string{`"beads"`, `"ac_proven"`, `"commits"`, `"closed_recent"`, `"critical_path"`, `"quiet"`, `"wait"`, `"notes"`} {
		if !strings.Contains(out, key) {
			t.Errorf("--json has no %s key:\n%s", key, out)
		}
	}
	low := strings.ToLower(out)
	for _, bad := range []string{"estimate", "actual", "finish"} {
		if strings.Contains(low, bad) {
			t.Errorf("--json names %q", bad)
		}
	}
}

// ini-r0q.1 AC8 feed-it-nothing: no bead with the prefix is a report with
// total 0 and empty lists, never null, and no table line.
func TestProgressJSON_NoBeadsIsAnEmptyReport(t *testing.T) {
	out, err := runProgressJSON(t, &progressFake{}, progressRepos, "zz-")
	if err != nil {
		t.Fatal(err)
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if rep["total"] != float64(0) || rep["prefix"] != "zz-" || rep["rig"] != "initech" {
		t.Errorf("empty report: %v", rep)
	}
	for _, k := range []string{"beads", "closed_recent", "critical_path", "engineers", "quiet", "notes"} {
		if v, ok := rep[k].([]any); !ok || len(v) != 0 {
			t.Errorf("%s = %v, want []", k, rep[k])
		}
	}
}

// e4cProgress runs initech progress with eng1 and eng2 idle, in a rig whose
// yaml carries yamlExtra, with --stuck-after flag ("" for none).
func e4cProgress(t *testing.T, fake *progressFake, yamlExtra, flag string, asJSON bool) (string, error) {
	t.Helper()
	skipWindows(t)
	dir := shortProjectDir(t)
	panes, _ := json.Marshal([]tui.PaneInfo{{Name: "eng1", Activity: "idle", Alive: true}, {Name: "eng2", Activity: "idle", Alive: true}, {Name: "eng3", Activity: "idle", Alive: true}, {Name: "qa1", Activity: "idle", Alive: true}})
	resp, _ := json.Marshal(tui.IPCResponse{OK: true, Data: string(panes)})
	sock := filepath.Join(dir, "p.sock")
	_, cleanup := startATIPCServer(t, sock, responseMode{raw: string(resp) + "\n"})
	t.Cleanup(cleanup)
	t.Setenv("INITECH_SOCKET", sock)
	old, oldJSON := progressStuckAfter, progressJSON
	t.Cleanup(func() { progressStuckAfter, progressJSON = old, oldJSON })
	progressStuckAfter, progressJSON = flag, asJSON
	return runProgressFixture(t, fake, progressRepos+yamlExtra, "fx-")
}

// AC2: the flag over the yaml over one hour; the header and the JSON name the
// value in effect; two values return different named beads. In the fixture
// fx-1's last move is 20 minutes old, fx-3's 2h15m and fx-8's 4h30m, owners idle.
func TestProgress_StuckAfterFlagOverYamlOverOneHour(t *testing.T) {
	yaml15 := "beads:\n  stuck_after: 15m\n"
	for _, c := range []struct {
		name, yaml, flag, header string
		want                     []string
		notWant                  []string
	}{
		{"neither", "", "", "stuck after 1h", []string{"fx-3", "fx-8"}, []string{"fx-1 "}},
		{"yaml", yaml15, "", "stuck after 15m", []string{"fx-1", "fx-3", "fx-8"}, nil},
		{"flag over yaml", yaml15, "3h", "stuck after 3h", []string{"fx-8"}, []string{"fx-1 ", "fx-3"}},
		{"flag alone", "", "15m", "stuck after 15m", []string{"fx-1", "fx-3", "fx-8"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := e4cProgress(t, &progressFake{}, c.yaml, c.flag, false)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, c.header) {
				t.Errorf("no %q in the header:\n%s", c.header, out)
			}
			var line string
			for _, l := range strings.Split(out, "\n") {
				if strings.HasPrefix(l, "stuck: ") {
					line = l + " "
				}
			}
			for _, id := range c.want {
				if !strings.Contains(line, id) {
					t.Errorf("stuck line %q lacks %s", line, id)
				}
			}
			for _, id := range c.notWant {
				if strings.Contains(line, id) {
					t.Errorf("stuck line %q has %s", line, id)
				}
			}
			js, err := e4cProgress(t, &progressFake{}, c.yaml, c.flag, true)
			if err != nil {
				t.Fatal(err)
			}
			var rep progress.Report
			if err := json.Unmarshal([]byte(js), &rep); err != nil {
				t.Fatal(err)
			}
			if "stuck after "+rep.StuckAfter != c.header {
				t.Errorf("JSON stuck_after %q, header %q", rep.StuckAfter, c.header)
			}
			if rep.Stuck != len(rep.StuckIDs) || rep.Stuck != len(strings.Fields(strings.TrimPrefix(line, "stuck: "))) {
				t.Errorf("JSON stuck %d, ids %v, table line %q", rep.Stuck, rep.StuckIDs, line)
			}
		})
	}
}

func TestProgress_ABadStuckAfterFlagIsRefused(t *testing.T) {
	_, err := e4cProgress(t, &progressFake{}, "", "soon", false)
	if err == nil || !strings.Contains(err.Error(), "--stuck-after") {
		t.Fatalf("err = %v, want one naming --stuck-after", err)
	}
}

// AC3: the chain is the rig's status.custom, read in the rig's own folder, and
// a rig whose bd config cannot be read says so and counts only in_progress.
func TestProgress_TheChainIsTheRigsStatusCustom(t *testing.T) {
	// fx-2 (in_qa) and fx-4 (ready_for_qa) are in this fixture, owners idle.
	out, err := e4cProgress(t, &progressFake{custom: "ready_for_qa,qa_passed"}, "", "15m", false)
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "stuck: ") {
			line = l + " "
		}
	}
	if !strings.Contains(line, "fx-4 ") || strings.Contains(line, "fx-2 ") {
		t.Errorf("with ready_for_qa in the chain and in_qa out, stuck line %q", line)
	}
	out, err = e4cProgress(t, &progressFake{customErr: errors.New("bd down")}, "", "15m", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "note: bd status.custom not read") {
		t.Errorf("no note that the chain was not read:\n%s", out)
	}
	if strings.Contains(out, "stuck: ") && strings.Contains(strings.SplitN(out, "stuck: ", 2)[1], "fx-4 ") {
		t.Errorf("a ready_for_qa bead read stuck with the chain unread:\n%s", out)
	}
}

// ini-e4c: an effort holding only waiting beads still has a report for the
// app, so the waiting count reaches it.
func TestAppServe_ActiveEffortsAsksForWaitingToo(t *testing.T) {
	dir := t.TempDir()
	cfg := "project: demo\nroot: " + dir + "\nroles:\n  - eng1\n"
	if err := os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &progressFake{data: progressTestdata}
	b := &appBackend{runner: fake}
	if _, err := b.ActiveEfforts(appserve.Rig{Name: "demo", Root: dir}); err != nil {
		t.Fatal(err)
	}
	var asked string
	for _, c := range fake.calls {
		if strings.Contains(c, "bd list --status") {
			asked = c
		}
	}
	if !strings.Contains(asked, "waiting") || !strings.Contains(asked, "in_progress") {
		t.Errorf("bd list asked %q, want waiting beside the in-flight statuses", asked)
	}
	if containsString(shownStatuses, "waiting") {
		t.Error("the ribbon's shownStatuses gained waiting; only the effort list should")
	}
}
