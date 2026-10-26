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
// requests only, in ci.yml and rigs.yml alike, since both land on the same
// self-hosted runner. And the gate is exactly the seven jobs the release gate
// expects (ini-p2ve.2), with the suite run once, in the shards.

type workflow struct {
	Jobs map[string]struct {
		Name  string      `yaml:"name"`
		Needs interface{} `yaml:"needs"`
		If    string      `yaml:"if"`
		Steps []struct {
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

const shards = 4

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
	var census, gate bool
	for _, s := range job.Steps {
		census = census || strings.Contains(s.Run, fmt.Sprintf("shardtests census -shards %d ", shards))
		gate = gate || strings.Contains(s.Run, `test "$S1" = success`)
	}
	if !census || !gate {
		t.Errorf("full-suite-census lacks the census step (%v) or the every-shard-passed step (%v)", census, gate)
	}
}

// AC 2, the cache lever: every setup-go has its cache on for pull requests
// only, never on for the self-hosted runner. rigs.yml too: its jobs share the
// runner's one Go build cache with CI's, and a restore there unlinks object
// files under a compile running in another slot (the v2.34.0 rigs race job).
func TestWorkflows_SetupGoCachesOnlyOnPullRequests(t *testing.T) {
	for _, file := range []string{"ci.yml", "rigs.yml"} {
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

// The job set the release gate expects, exactly (ini-p2ve.2): six start
// together on the Mac's six runner slots and the census follows. And
// build-and-test runs no tests: its only go test is the witness -list, and it
// runs the short census, which is what makes dropping its -short run safe.
func TestCIWorkflow_TheGateIsTheSevenJobs(t *testing.T) {
	w := readCI(t)
	want := []string{"build-and-test", "census-leg", "full-suite-1", "full-suite-2", "full-suite-3", "full-suite-4", "full-suite-census"}
	var got []string
	for key := range w.Jobs {
		got = append(got, key)
	}
	sort.Strings(got)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("ci.yml jobs are %v, want %v: the release gate reads this set (fleet/reference.md)", got, want)
	}
	var started int
	for _, key := range got {
		if w.Jobs[key].Needs == nil {
			started++
		}
	}
	if started != 6 {
		t.Errorf("%d jobs start without needs, want 6, one per runner slot", started)
	}
	job := w.Jobs["build-and-test"]
	var census bool
	for _, s := range job.Steps {
		if strings.Contains(s.Run, "go test") && !strings.Contains(s.Run, " -list ") {
			t.Errorf("build-and-test runs %q: the suite runs once, in the full-suite shards", s.Run)
		}
		census = census || strings.HasPrefix(s.Run, "go run ./scripts/shortcensus")
	}
	if !census {
		t.Error("build-and-test does not run the short census: nothing proves -short only skips tests")
	}
	if len(job.Steps) == 0 {
		t.Error("build-and-test has no steps: this test is not reading it")
	}
}
