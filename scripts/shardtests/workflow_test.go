package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The shape of ci.yml this tool depends on (ini-bus5), read from the file
// itself so a change to the workflow that breaks it fails here as well as in
// CI: every shard runs the tool with no -short; the census job needs every
// shard and runs even when one failed; and setup-go's cache is on for pull
// requests only.

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

const shards = 3

func readCI(t *testing.T) workflow {
	t.Helper()
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var w workflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	if len(w.Jobs) == 0 {
		t.Fatal("ci.yml parsed to no jobs: this test is not reading the workflow")
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
// only, never on for the self-hosted runner.
func TestCIWorkflow_SetupGoCachesOnlyOnPullRequests(t *testing.T) {
	w := readCI(t)
	var n int
	for key, job := range w.Jobs {
		for _, s := range job.Steps {
			if !strings.HasPrefix(s.Uses, "actions/setup-go@") {
				continue
			}
			n++
			if got := s.With["cache"]; got != "${{ github.event_name == 'pull_request' }}" {
				t.Errorf("%s: setup-go cache is %q, want on for pull_request only", key, got)
			}
		}
	}
	if n == 0 {
		t.Fatal("found no setup-go step: this test is not reading the workflow")
	}
}
