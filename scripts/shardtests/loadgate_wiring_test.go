package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// The load gate's wiring (ini-c5a.7): the gate guards the shared Mac
// (192.168.1.207); ci.yml and rigs.yml run on the workbench's Linux runners and
// never wait at it, except rigs.yml's darwin job, which runs on the Mac.
// app-tests.yml has one job, which waits right after checkout with simulators
// on. The gate's own
// rules are tested in scripts/loadgate.
func TestWorkflows_OnlyTheMacJobWaitsAtTheLoadGate(t *testing.T) {
	root := repoRootForWorkflows(t)
	const gate = "./.github/actions/load-gate"
	read := func(name string) workflow {
		src, err := os.ReadFile(filepath.Join(root, ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		var w workflow
		if err := yaml.Unmarshal(src, &w); err != nil || len(w.Jobs) == 0 {
			t.Fatalf("%s: %v (%d jobs): this test is not reading it", name, err, len(w.Jobs))
		}
		return w
	}
	for _, name := range []string{"ci.yml", "rigs.yml"} {
		for key, job := range read(name).Jobs {
			var waits bool
			for _, s := range job.Steps {
				waits = waits || s.Uses == gate
			}
			mac := name == "rigs.yml" && key == "darwin"
			if waits != mac {
				t.Errorf("%s %s waits at the Mac's load gate: %v, want %v (only a job on the Mac waits)", name, key, waits, mac)
			}
		}
	}
	var gated int
	for key, job := range read("app-tests.yml").Jobs {
		for _, s := range job.Steps {
			if s.Uses == gate {
				gated++
				if s.With["simulators"] != "true" {
					t.Errorf("app-tests.yml %s waits without simulators: it boots one", key)
				}
			}
		}
	}
	if gated != 1 {
		t.Errorf("app-tests.yml waits at the load gate %d times, want once", gated)
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
