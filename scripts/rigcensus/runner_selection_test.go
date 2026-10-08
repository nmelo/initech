package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// macRunnerSelector is the ONLY way a workflow job may reach the operator's
// self-hosted Mac (ini-8k1o). The pull_request guard comes first, so a PR --
// including one from a fork of this public repo -- always resolves to
// GitHub-hosted macos-latest; an unset MAC_RUNNER also falls back to it.
const macRunnerSelector = "${{ github.event_name != 'pull_request' && vars.MAC_RUNNER || 'macos-latest' }}"

// TestWorkflows_SelfHostedOnlyThroughTheGuardedSelector fences the one real
// risk of a self-hosted runner on a public repo: a job reachable by
// pull_request naming the Mac directly would run a stranger's code on the
// operator's machine. Every job in every workflow must run on macos-latest, on
// the guarded selector, or -- for release.yml's goreleaser job only -- on
// ubuntu-latest. A bare self-hosted label, or a selector missing the PR guard,
// fails here.
//
// Parsed with YAML, not a regex: the fence must see EVERY job, and a pattern
// that recognises job blocks by indentation is a selector a new layout can
// slip past.
func TestWorkflows_SelfHostedOnlyThroughTheGuardedSelector(t *testing.T) {
	root := repoRoot(t)
	total := 0
	for _, name := range []string{"ci.yml", "rigs.yml", "release.yml"} {
		raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		var wf struct {
			Jobs map[string]struct {
				RunsOn interface{} `yaml:"runs-on"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(raw, &wf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(wf.Jobs) == 0 {
			t.Fatalf("%s: no jobs parsed -- the fence is reading nothing", name)
		}
		jobs := make([]string, 0, len(wf.Jobs))
		for j := range wf.Jobs {
			jobs = append(jobs, j)
		}
		sort.Strings(jobs)
		for _, job := range jobs {
			total++
			got, ok := wf.Jobs[job].RunsOn.(string)
			switch {
			case !ok:
				t.Errorf("%s job %q: runs-on is %#v; only a plain string is allowed (a label list could name the Mac directly)", name, job, wf.Jobs[job].RunsOn)
			case got == "macos-latest", got == macRunnerSelector:
			case got == "ubuntu-latest" && name == "release.yml" && job == "release":
			default:
				t.Errorf("%s job %q runs on %q: a job may reach the self-hosted Mac only through the guarded selector %s (ini-8k1o)", name, job, got, macRunnerSelector)
			}
		}
	}
	if total < 6 {
		t.Fatalf("fenced only %d jobs across the three workflows; expected at least 6 -- the parse is reading less than the files", total)
	}
}
