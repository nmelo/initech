package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// Rigs run on GitHub-hosted macOS (ini-c5a.7, the operator via theguy): every
// rigs.yml job runs on macos-latest, never on the shared Mac host, so a rigs
// run adds no load there and never holds a CI push run at its load gate. With
// no shared host there is no gate to wait at, and each hosted job is a fresh
// machine, so setup-go's cache is on. Every job keeps a timeout.
func TestRigsWorkflow_EveryJobRunsOnGitHubHostedMacOS(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRootForWorkflows(t), ".github", "workflows", "rigs.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Jobs map[string]struct {
			RunsOn  interface{} `yaml:"runs-on"`
			Timeout int         `yaml:"timeout-minutes"`
			Steps   []struct {
				Uses string            `yaml:"uses"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(src, &w); err != nil {
		t.Fatal(err)
	}
	if len(w.Jobs) != 5 {
		t.Fatalf("rigs.yml has %d jobs, want 5 (race and four rig shards): this test is not reading them", len(w.Jobs))
	}
	for key, job := range w.Jobs {
		if job.RunsOn != "macos-latest" {
			t.Errorf("%s runs on %v, want macos-latest: a rigs run on the shared Mac host loads it and holds CI pushes at the gate", key, job.RunsOn)
		}
		if job.Timeout <= 0 {
			t.Errorf("%s has no timeout-minutes", key)
		}
		var setupGo int
		for _, s := range job.Steps {
			switch {
			case s.Uses == "./.github/actions/load-gate":
				t.Errorf("%s waits at the load gate: a hosted job has no shared host to wait for", key)
			case len(s.Uses) > 17 && s.Uses[:17] == "actions/setup-go@":
				setupGo++
				if s.With["cache"] != "true" {
					t.Errorf("%s: setup-go cache is %q, want true: a hosted job starts with nothing", key, s.With["cache"])
				}
			}
		}
		if setupGo != 1 {
			t.Errorf("%s has %d setup-go steps, want 1", key, setupGo)
		}
	}
}
