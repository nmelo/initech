package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// The load gate's wiring (ini-c5a.7): in ci.yml every job that starts a run
// (no needs) waits for the shared Mac host right after setup-go,
// and every job that only follows other jobs does not, so a run is never held
// back by the load it made itself. rigs.yml runs on GitHub-hosted macOS and has
// no gate (rigs_hosted_test.go). The gate's own rules are tested in
// scripts/loadgate.
func TestWorkflows_EveryFirstWaveJobWaitsForTheHost(t *testing.T) {
	root := repoRootForWorkflows(t)
	const gate = "./.github/actions/load-gate"
	var gated, followers int
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
			if job.Needs != nil {
				followers++
				if gateAt >= 0 {
					t.Errorf("%s %s: follows other jobs but waits for the host, held by its own run's load", name, key)
				}
				continue
			}
			gated++
			if gateAt < 0 {
				t.Errorf("%s %s: starts a run without the load gate", name, key)
			} else if setupGo < 0 || gateAt != setupGo+1 {
				t.Errorf("%s %s: the load gate is step %d, want right after setup-go (step %d): it runs go", name, key, gateAt, setupGo)
			}
		}
	}
	if gated != 6 || followers != 1 {
		t.Errorf("%d first-wave jobs and %d followers, want 6 (build-and-test and five shards) and 1 (full-suite-census): this test is not reading the jobs", gated, followers)
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
