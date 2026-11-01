package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// CodeQL runs weekly and on demand, never on push (ini-7h5, the operator via
// theguy): codeql.yml is the repo's only code scan (default setup is off), it
// starts only on its weekly schedule and on workflow_dispatch, it scans the
// four languages default setup scanned with the categories default setup
// used, and every job runs on a GitHub-hosted image (ubuntu-latest, and
// macos-latest for swift), never on the shared Mac host.

// codeqlLanguages is each scanned language with the runner and build mode it
// takes: the ones default setup used.
var codeqlLanguages = map[string]struct{ os, buildMode string }{
	"actions": {"ubuntu-latest", "none"},
	"go":      {"ubuntu-latest", "autobuild"},
	"python":  {"ubuntu-latest", "none"},
	"swift":   {"macos-latest", "autobuild"},
}

type codeqlWorkflow struct {
	On   yaml.Node `yaml:"on"`
	Jobs map[string]struct {
		RunsOn   string `yaml:"runs-on"`
		Strategy struct {
			Matrix struct {
				Include []map[string]string `yaml:"include"`
			} `yaml:"matrix"`
		} `yaml:"strategy"`
		Steps []struct {
			Uses string            `yaml:"uses"`
			With map[string]string `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// codeqlProblems is everything wrong with src as the CodeQL workflow, one line
// each; none for a workflow that starts only on a weekly schedule and on
// workflow_dispatch and scans the four languages on hosted runners.
func codeqlProblems(src []byte) []string {
	var bad []string
	for _, word := range []string{"self-hosted", "MAC_RUNNER", "pull_request", "push"} {
		if strings.Contains(string(src), word) {
			bad = append(bad, fmt.Sprintf("the file says %q", word))
		}
	}
	var w codeqlWorkflow
	if err := yaml.Unmarshal(src, &w); err != nil {
		return append(bad, "does not parse: "+err.Error())
	}
	if w.On.Kind != yaml.MappingNode {
		return append(bad, "on: is not a map of triggers")
	}
	var triggers []string
	for i := 0; i < len(w.On.Content); i += 2 {
		name, val := w.On.Content[i].Value, w.On.Content[i+1]
		triggers = append(triggers, name)
		if name == "schedule" {
			bad = append(bad, weeklyProblems(val)...)
		}
		if name == "workflow_dispatch" && !(val.Kind == yaml.ScalarNode && val.Value == "") {
			bad = append(bad, "workflow_dispatch takes inputs; a dispatch run is the scheduled scan, unchanged")
		}
	}
	sort.Strings(triggers)
	if strings.Join(triggers, ",") != "schedule,workflow_dispatch" {
		bad = append(bad, fmt.Sprintf("triggers are %v, want only schedule and workflow_dispatch", triggers))
	}
	if len(w.Jobs) != 1 {
		return append(bad, fmt.Sprintf("%d jobs, want 1 (analyze, one matrix entry per language)", len(w.Jobs)))
	}
	for key, job := range w.Jobs {
		if job.RunsOn != "${{ matrix.os }}" {
			bad = append(bad, fmt.Sprintf("%s runs on %q, want ${{ matrix.os }}", key, job.RunsOn))
		}
		seen := map[string]bool{}
		for _, e := range job.Strategy.Matrix.Include {
			lang := e["language"]
			want, ok := codeqlLanguages[lang]
			switch {
			case !ok:
				bad = append(bad, fmt.Sprintf("scans %q, not one of the four languages", lang))
			case seen[lang]:
				bad = append(bad, fmt.Sprintf("scans %s twice", lang))
			case e["os"] != want.os:
				bad = append(bad, fmt.Sprintf("%s runs on %q, want %s", lang, e["os"], want.os))
			case e["build-mode"] != want.buildMode:
				bad = append(bad, fmt.Sprintf("%s build mode %q, want %s", lang, e["build-mode"], want.buildMode))
			}
			seen[lang] = true
		}
		for lang := range codeqlLanguages {
			if !seen[lang] {
				bad = append(bad, lang+" is not scanned")
			}
		}
		var category string
		for _, s := range job.Steps {
			if strings.HasPrefix(s.Uses, "github/codeql-action/analyze@") {
				category = s.With["category"]
			}
		}
		if category != "/language:${{ matrix.language }}" {
			bad = append(bad, fmt.Sprintf("analyze category %q, want /language:${{ matrix.language }} (default setup's, so alerts carry over)", category))
		}
	}
	return bad
}

// weeklyProblems is why a schedule trigger is not one weekly cron: exactly one
// entry whose day-of-month and month are "*" and whose day of week is one day.
func weeklyProblems(n *yaml.Node) []string {
	var entries []struct {
		Cron string `yaml:"cron"`
	}
	if err := n.Decode(&entries); err != nil || len(entries) != 1 {
		return []string{"schedule is not exactly one cron entry"}
	}
	f := strings.Fields(entries[0].Cron)
	if len(f) != 5 || f[2] != "*" || f[3] != "*" || len(f[4]) != 1 || f[4] < "0" || f[4] > "6" {
		return []string{fmt.Sprintf("cron %q is not once a week", entries[0].Cron)}
	}
	return nil
}

// AC1: the repo's codeql.yml has no problems.
func TestCodeQLWorkflow_WeeklyAndOnDemandOnHostedRunners(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRootForWorkflows(t), ".github", "workflows", "codeql.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range codeqlProblems(src) {
		t.Errorf("codeql.yml: %s", p)
	}
}

// The check itself: each unsafe shape is caught, and the shape it guards
// passes (the positive twin; the real file is the other).
func TestCodeQLWorkflow_TheCheckCatchesEachUnsafeShape(t *testing.T) {
	const good = `on:
  schedule:
    - cron: '17 6 * * 1'
  workflow_dispatch:
jobs:
  analyze:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        include:
          - {language: actions, build-mode: none, os: ubuntu-latest}
          - {language: go, build-mode: autobuild, os: ubuntu-latest}
          - {language: python, build-mode: none, os: ubuntu-latest}
          - {language: swift, build-mode: autobuild, os: macos-latest}
    steps:
      - uses: github/codeql-action/analyze@v4
        with:
          category: /language:${{ matrix.language }}
`
	if p := codeqlProblems([]byte(good)); len(p) != 0 {
		t.Fatalf("the good shape has problems: %v", p)
	}
	for name, edit := range map[string][2]string{
		"a push trigger":       {"  workflow_dispatch:\n", "  workflow_dispatch:\n  push:\n"},
		"a pull_request":       {"  workflow_dispatch:\n", "  workflow_dispatch:\n  pull_request:\n"},
		"no dispatch":          {"  workflow_dispatch:\n", ""},
		"a dispatch input":     {"  workflow_dispatch:\n", "  workflow_dispatch:\n    inputs:\n      upload: {type: boolean}\n"},
		"a daily schedule":     {"'17 6 * * 1'", "'17 6 * * *'"},
		"two schedules":        {"    - cron: '17 6 * * 1'\n", "    - cron: '17 6 * * 1'\n    - cron: '17 6 * * 4'\n"},
		"a self-hosted runner": {"runs-on: ${{ matrix.os }}", "runs-on: [self-hosted, nmbp14]"},
		"the Mac runner":       {"os: macos-latest}", "os: '${{ vars.MAC_RUNNER }}'}"},
		"swift on ubuntu":      {"build-mode: autobuild, os: macos-latest", "build-mode: autobuild, os: ubuntu-latest"},
		"go on macos":          {"go, build-mode: autobuild, os: ubuntu-latest", "go, build-mode: autobuild, os: macos-latest"},
		"python missing":       {"          - {language: python, build-mode: none, os: ubuntu-latest}\n", ""},
		"a fifth language":     {"    steps:\n", "          - {language: ruby, build-mode: none, os: ubuntu-latest}\n    steps:\n"},
		"swift with no build":  {"swift, build-mode: autobuild", "swift, build-mode: none"},
		"another category":     {"category: /language:${{ matrix.language }}", "category: codeql"},
		"no analyze step":      {"github/codeql-action/analyze@v4", "github/codeql-action/upload-sarif@v4"},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(good, edit[0]) {
				t.Fatalf("the edit's anchor %q is not in the good shape: this case tests nothing", edit[0])
			}
			if p := codeqlProblems([]byte(strings.Replace(good, edit[0], edit[1], 1))); len(p) == 0 {
				t.Errorf("not caught")
			}
		})
	}
}
