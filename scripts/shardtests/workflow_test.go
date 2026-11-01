package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The shape of ci.yml this tool depends on (ini-bus5), read from the file
// itself so a change to the workflow that breaks it fails here as well as in
// CI: every shard runs the tool with no -short; the census job needs every
// shard and runs even when one failed; and setup-go's cache is on for pull
// requests only in ci.yml, which lands on the self-hosted runner. And the gate
// is exactly the jobs the release gate expects (ini-p2ve.2), with the suite
// run once, in the shards.

type workflow struct {
	Jobs map[string]struct {
		Name  string      `yaml:"name"`
		Needs interface{} `yaml:"needs"`
		If    string      `yaml:"if"`
		Steps []struct {
			Name string            `yaml:"name"`
			If   string            `yaml:"if"`
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
			Env  map[string]string `yaml:"env"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

const shards = 6

func readCI(t *testing.T) workflow { return readWorkflow(t, "ci.yml") }

func readWorkflow(t *testing.T, file string) workflow {
	t.Helper()
	data, err := os.ReadFile("../../.github/workflows/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var w workflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	if len(w.Jobs) == 0 {
		t.Fatalf("%s parsed to no jobs: this test is not reading the workflow", file)
	}
	return w
}

// AC 4: each shard job runs exactly its own shard, through the tool, with no
// -short, and the job's name is its key (the release gate's set-equality).
func TestCIWorkflow_EachShardRunsTheToolWithoutShort(t *testing.T) {
	w := readCI(t)
	for k := 1; k <= shards; k++ {
		key := fmt.Sprintf("full-suite-%d", k)
		job, ok := w.Jobs[key]
		if !ok {
			t.Fatalf("ci.yml has no %s job", key)
		}
		if job.Name != "" && job.Name != key {
			t.Errorf("%s is named %q: the gate compares job names to keys", key, job.Name)
		}
		want := fmt.Sprintf("go run ./scripts/shardtests run -shards %d -shard %d ", shards, k)
		var runs int
		for _, s := range job.Steps {
			if strings.Contains(s.Run, "shardtests run") {
				runs++
				if !strings.HasPrefix(s.Run, want) {
					t.Errorf("%s runs %q, want it to start %q", key, s.Run, want)
				}
				if strings.Contains(s.Run, "short") {
					t.Errorf("%s runs -short: %q", key, s.Run)
				}
			}
		}
		if runs != 1 {
			t.Errorf("%s has %d shard run steps, want 1", key, runs)
		}
	}
	if _, ok := w.Jobs[fmt.Sprintf("full-suite-%d", shards+1)]; ok {
		t.Errorf("ci.yml has a shard beyond the %d this test and the census expect", shards)
	}
}

// AC 6: the census job needs every shard and runs even when one is red, so a
// red shard can never be hidden behind a skipped census.
func TestCIWorkflow_TheCensusNeedsEveryShardAndAlwaysRuns(t *testing.T) {
	w := readCI(t)
	job, ok := w.Jobs["full-suite-census"]
	if !ok {
		t.Fatal("ci.yml has no full-suite-census job: nothing proves the shards ran every test")
	}
	needs := map[string]bool{}
	switch n := job.Needs.(type) {
	case []interface{}:
		for _, v := range n {
			needs[fmt.Sprint(v)] = true
		}
	case string:
		needs[n] = true
	}
	for k := 1; k <= shards; k++ {
		if !needs[fmt.Sprintf("full-suite-%d", k)] {
			t.Errorf("full-suite-census does not need full-suite-%d", k)
		}
	}
	if job.If != "always()" {
		t.Errorf("full-suite-census runs if %q, want always(): a red shard would skip it", job.If)
	}
	var census, gate, noShort bool
	for _, s := range job.Steps {
		census = census || strings.Contains(s.Run, fmt.Sprintf("shardtests census -shards %d ", shards))
		if strings.Contains(s.Run, `test "$S1" = success`) {
			gate = true
			// Every shard's result is read and required, not only the first
			// few: a shard left out of this line could go red under a green
			// census job.
			for k := 1; k <= shards; k++ {
				if !strings.Contains(s.Run, fmt.Sprintf(`test "$S%d" = success`, k)) {
					t.Errorf("the every-shard-passed step does not require shard %d", k)
				}
				if s.Env[fmt.Sprintf("S%d", k)] != fmt.Sprintf("${{ needs.full-suite-%d.result }}", k) {
					t.Errorf("S%d is %q, want full-suite-%d's result", k, s.Env[fmt.Sprintf("S%d", k)], k)
				}
			}
		}
		// The no-short step counts the shard command lines it read; that
		// count must be the number of shards, or it passes on a missing one.
		if strings.Contains(s.Run, "shardtests run ") && strings.Contains(s.Run, "-eq ") {
			noShort = true
			if !strings.Contains(s.Run, fmt.Sprintf("-eq %d ", shards)) {
				t.Errorf("the no-short step does not expect %d shard command lines", shards)
			}
		}
	}
	if !census || !gate || !noShort {
		t.Errorf("full-suite-census lacks the census step (%v), the every-shard-passed step (%v) or the no-short step (%v)", census, gate, noShort)
	}
}

// AC 2, the cache lever: every setup-go in ci.yml has its cache on for pull
// requests only, never on for the self-hosted runner, whose slots share one Go
// build cache: a restore there unlinks object files under a compile running in
// another slot (the v2.34.0 rigs race job). rigs.yml runs on GitHub-hosted
// machines with the cache on (rigs_hosted_test.go).
func TestWorkflows_SetupGoCachesOnlyOnPullRequests(t *testing.T) {
	for _, file := range []string{"ci.yml"} {
		w := readWorkflow(t, file)
		var n int
		for key, job := range w.Jobs {
			for _, s := range job.Steps {
				if !strings.HasPrefix(s.Uses, "actions/setup-go@") {
					continue
				}
				n++
				if got := s.With["cache"]; got != "${{ github.event_name == 'pull_request' }}" {
					t.Errorf("%s %s: setup-go cache is %q, want on for pull_request only", file, key, got)
				}
			}
		}
		if n == 0 {
			t.Errorf("%s: found no setup-go step: this test is not reading the workflow", file)
		}
	}
}

// The job set the release gate expects, exactly (ini-p2ve.2, ini-c5a.3,
// ini-c5a.7): the gate starts the run alone, then seven start together, on seven of
// the Mac's eight runners, so none waits for a second wave, and the census
// follows. build-and-test runs
// go test in two steps only: the witness -list of the whole tui package, and
// the side-by-side step, whose only go test lines are the -list of the census
// selector and that one env-gated selector (TestCensus_, TestRolesLessBoot_,
// TestWindowCLI_ under INITECH_CENSUS), which no shard runs; any other go test
// there would run part of the suite twice. The step itself has no if: the
// release suites in it run on a pull request too, and the census inside it is
// push-only, which sidebyside_test.go proves by running it. It runs the short
// census, which is what makes dropping its -short run safe.
func TestCIWorkflow_TheGateIsTheSevenJobs(t *testing.T) {
	w := readCI(t)
	want := []string{"build-and-test", "full-suite-1", "full-suite-2", "full-suite-3", "full-suite-4", "full-suite-5", "full-suite-6", "full-suite-census", "gate"}
	var got []string
	for key := range w.Jobs {
		got = append(got, key)
	}
	sort.Strings(got)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("ci.yml jobs are %v, want %v: the release gate reads this set (fleet/reference.md)", got, want)
	}
	var started, afterGate int
	for _, key := range got {
		switch w.Jobs[key].Needs {
		case nil:
			started++
		case "gate":
			afterGate++
		}
	}
	if started != 1 || w.Jobs["gate"].Needs != nil || afterGate != 7 {
		t.Errorf("%d jobs start without needs and %d need the gate; want only the gate to start, then 7: the six shards and build-and-test", started, afterGate)
	}
	job := w.Jobs["build-and-test"]
	const (
		witness   = "go test ./internal/tui/ -list '.*' -count=1"
		censusSel = "'TestCensus_|TestRolesLessBoot_|TestWindowCLI_'"
		censusRun = "go test ./internal/tui/ -count=1 -timeout 900s -run " + censusSel
	)
	var census, sideBySide bool
	for _, s := range job.Steps {
		switch {
		case !strings.Contains(s.Run, "go test"):
		case s.Run == witness:
		case s.Name == sideBySideStep:
			var lines []string
			for _, l := range strings.Split(s.Run, "\n") {
				if strings.Contains(l, "go test") {
					lines = append(lines, strings.TrimSpace(l))
				}
			}
			want := []string{"go test ./internal/tui/ -list " + censusSel + " &&", censusRun}
			if strings.Join(lines, "\n") != strings.Join(want, "\n") || s.Env["INITECH_CENSUS"] != "1" || s.If != "" {
				t.Errorf("%q runs go test %q with INITECH_CENSUS=%q and if %q; want exactly %q under INITECH_CENSUS=1 and no if", s.Name, lines, s.Env["INITECH_CENSUS"], s.If, want)
			} else {
				sideBySide = true
			}
		default:
			t.Errorf("build-and-test runs %q: its only go test runs are the witness list and the census selector under INITECH_CENSUS in the side-by-side step; the suite runs once, in the full-suite shards", s.Run)
		}
		census = census || strings.HasPrefix(s.Run, "go run ./scripts/shortcensus")
	}
	if !sideBySide {
		t.Errorf("build-and-test lacks the %q step running the census under INITECH_CENSUS: nothing would run the census tests", sideBySideStep)
	}
	if !census {
		t.Error("build-and-test does not run the short census: nothing proves -short only skips tests")
	}
	if len(job.Steps) == 0 {
		t.Error("build-and-test has no steps: this test is not reading it")
	}
}
