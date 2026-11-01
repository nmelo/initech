package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Rigs run on the workbench beside main CI (ini-c5a.7, the operator via
// theguy): every rigs.yml job but darwin runs on the guarded Linux selector, and every
// step runs at the lowest CPU priority (the workflow's default shell is
// nice -n 19 bash, and no step names another shell), so main CI's jobs and the
// other projects' work come first. No job waits at the Mac's
// load gate, setup-go's cache is on for pull requests only (the runners share
// one Go build cache), and every job keeps a timeout. The darwin job runs the
// tests that need macOS on the shared Mac, behind its load gate, and every Go
// test it names exists (a stale name would select nothing and pass).
func TestRigsWorkflow_EveryJobRunsOnTheWorkbenchAtLowPriority(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRootForWorkflows(t), ".github", "workflows", "rigs.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Defaults struct {
			Run struct {
				Shell string `yaml:"shell"`
			} `yaml:"run"`
		} `yaml:"defaults"`
		Jobs map[string]struct {
			RunsOn  interface{} `yaml:"runs-on"`
			Timeout int         `yaml:"timeout-minutes"`
			Steps   []struct {
				Uses  string            `yaml:"uses"`
				Run   string            `yaml:"run"`
				Shell string            `yaml:"shell"`
				With  map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(src, &w); err != nil {
		t.Fatal(err)
	}
	if len(w.Jobs) == 0 {
		t.Fatal("rigs.yml parsed to no jobs: this test is not reading it")
	}
	if w.Defaults.Run.Shell != "nice -n 19 bash -e {0}" {
		t.Errorf("rigs.yml's default shell is %q, want nice -n 19 bash -e {0}: rigs yield to main CI", w.Defaults.Run.Shell)
	}
	var tests int
	for key, job := range w.Jobs {
		if key == "darwin" {
			continue // TestRigsWorkflow_TheDarwinJobRunsOnTheMacBehindItsGate
		}
		if job.RunsOn != linuxGuard {
			t.Errorf("%s runs on %v, want the workbench's guarded selector %s", key, job.RunsOn, linuxGuard)
		}
		if job.Timeout <= 0 {
			t.Errorf("%s has no timeout-minutes", key)
		}
		for _, s := range job.Steps {
			if s.Uses == "./.github/actions/load-gate" {
				t.Errorf("%s waits at the Mac's load gate: it runs on the workbench", key)
			}
			// The test steps: go test with -count (a -list witness runs
			// nothing) or make test-race.
			runsTests := (strings.Contains(s.Run, "go test") && strings.Contains(s.Run, "-count")) || strings.Contains(s.Run, "make test")
			if runsTests {
				tests++
			}
			if s.Run != "" && s.Shell != "" {
				t.Errorf("%s runs %q in shell %q, not the workflow's nice'd default", key, s.Run, s.Shell)
			}
		}
	}
	if tests < len(w.Jobs)-1 {
		t.Errorf("found %d test steps in %d jobs: this test is not reading them", tests, len(w.Jobs))
	}
}

func TestRigsWorkflow_TheDarwinJobRunsOnTheMacBehindItsGate(t *testing.T) {
	root := repoRootForWorkflows(t)
	src, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "rigs.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Jobs map[string]struct {
			RunsOn   string `yaml:"runs-on"`
			Defaults struct {
				Run struct {
					Shell string `yaml:"shell"`
				} `yaml:"run"`
			} `yaml:"defaults"`
			Steps []struct {
				Uses string `yaml:"uses"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(src, &w); err != nil {
		t.Fatal(err)
	}
	job, ok := w.Jobs["darwin"]
	if !ok {
		t.Fatal("rigs.yml has no darwin job: the tests that need macOS run nowhere")
	}
	if job.RunsOn != macGuard {
		t.Errorf("darwin runs on %q, want the Mac's guarded selector", job.RunsOn)
	}
	if job.Defaults.Run.Shell != "bash -e {0}" {
		t.Errorf("darwin's shell is %q, want bash -e {0}: at nice 19 on the shared Mac its bounded tests starve", job.Defaults.Run.Shell)
	}
	var gate, sign, install bool
	var names []string
	for _, s := range job.Steps {
		gate = gate || s.Uses == "./.github/actions/load-gate"
		sign = sign || s.Run == "make release-sign-test"
		install = install || s.Run == "make release-install-test"
		if i := strings.Index(s.Run, "-run '^("); i >= 0 && strings.Contains(s.Run, "go test") {
			sel := s.Run[i+len("-run '^("):]
			names = append(names, strings.Split(sel[:strings.Index(sel, ")")], "|")...)
		}
	}
	if !gate || !sign || !install || len(names) == 0 {
		t.Fatalf("darwin: gate %v, signing suite %v, install suite %v, Go tests %v: want all four", gate, sign, install, names)
	}
	files, _ := filepath.Glob(filepath.Join(root, "internal", "*", "*_test.go"))
	for _, n := range names {
		var found bool
		for _, f := range files {
			b, _ := os.ReadFile(f)
			if strings.Contains(string(b), "func "+n+"(t *testing.T)") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("darwin runs %s, which no test file defines: it would select nothing", n)
		}
	}
	// Every test a workbench rigs job skips runs here: a skip moves a test to
	// the Mac, it never drops one.
	runHere := map[string]bool{}
	for _, n := range names {
		runHere[n] = true
	}
	var skipped int
	for key, j := range w.Jobs {
		if key == "darwin" {
			continue
		}
		for _, s := range j.Steps {
			i := strings.Index(s.Run, "-skip '")
			if i < 0 {
				continue
			}
			sel := s.Run[i+len("-skip '"):]
			sel = strings.TrimSuffix(strings.TrimPrefix(sel[:strings.Index(sel, "'")], "^"), "$")
			var names []string
			if p := strings.Index(sel, "("); p >= 0 {
				prefix, alts := sel[:p], strings.TrimSuffix(sel[p+1:], ")")
				for _, a := range strings.Split(alts, "|") {
					names = append(names, prefix+a)
				}
			} else {
				names = []string{sel}
			}
			for _, n := range names {
				skipped++
				if !runHere[n] {
					t.Errorf("%s skips %s, which the darwin job does not run: the test would run nowhere", key, n)
				}
			}
		}
	}
	if skipped == 0 {
		t.Error("found no -skip in the workbench rigs jobs: this test is not reading them")
	}
}
