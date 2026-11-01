package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// The load gate's wiring (ini-c5a.7): in ci.yml one small job, gate, starts
// the run and waits for the shared Mac host right after setup-go; the six jobs
// that do the work need it and do not wait themselves, so a waiting run holds
// one runner and its other jobs wait in GitHub's queue holding none; and no
// job that follows other jobs waits, so a run is never held back by the load
// it made itself. rigs.yml runs on GitHub-hosted macOS and has
// no gate (rigs_hosted_test.go). The gate's own rules are tested in
// scripts/loadgate.
func TestWorkflows_OneGateJobWaitsForTheHostAndTheWorkNeedsIt(t *testing.T) {
	root := repoRootForWorkflows(t)
	const gate = "./.github/actions/load-gate"
	var gated, afterGate, followers int
	for _, name := range []string{"ci.yml"} {
		src, err := os.ReadFile(filepath.Join(root, ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		var w workflow
		if err := yaml.Unmarshal(src, &w); err != nil {
			t.Fatal(err)
		}
		for key, job := range w.Jobs {
			setupGo, gateAt := -1, -1
			for i, s := range job.Steps {
				switch {
				case len(s.Uses) >= 18 && s.Uses[:18] == "actions/setup-go@v":
					setupGo = i
				case s.Uses == gate:
					gateAt = i
				}
			}
			switch {
			case key == "gate":
				gated++
				if job.Needs != nil {
					t.Errorf("%s gate: needs %v; it starts the run", name, job.Needs)
				}
				if gateAt < 0 || setupGo < 0 || gateAt != setupGo+1 || len(job.Steps) != gateAt+1 {
					t.Errorf("%s gate: the load gate is step %d of %d, want the last step, right after setup-go (step %d): the job only waits", name, gateAt, len(job.Steps), setupGo)
				}
			case gateAt >= 0:
				t.Errorf("%s %s: waits for the host itself, holding a runner while it waits; only the gate job waits", name, key)
			case job.Needs == "gate":
				afterGate++
			case job.Needs == nil:
				t.Errorf("%s %s: starts a run without needing the gate", name, key)
			default:
				followers++
			}
		}
	}
	if gated != 1 || afterGate != 6 || followers != 1 {
		t.Errorf("%d gate jobs, %d jobs after the gate and %d followers, want 1, 6 (build-and-test and five shards) and 1 (full-suite-census): this test is not reading the jobs", gated, afterGate, followers)
	}
	action, err := os.ReadFile(filepath.Join(root, ".github", "actions", "load-gate", "action.yml"))
	if err != nil {
		t.Fatalf("the load gate action is missing: %v", err)
	}
	var a struct {
		Runs struct {
			Steps []struct {
				If  string `yaml:"if"`
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(action, &a); err != nil || len(a.Runs.Steps) != 1 {
		t.Fatalf("the action is not one step: %v", err)
	}
	s := a.Runs.Steps[0]
	if s.If != "runner.environment == 'self-hosted'" || len(s.Run) < 24 || s.Run[:24] != "go run ./scripts/loadgat" {
		t.Errorf("the action runs %q if %q; want the loadgate tool, on self-hosted runners only", s.Run, s.If)
	}
}
