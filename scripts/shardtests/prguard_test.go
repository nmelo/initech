package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The repo is public, so a pull request can come from anyone. No workflow may
// send a pull_request or pull_request_target job to the operator's Mac runners
// (ini-c5a.7, the operator's condition): a fork's pull request must never run
// on that machine. A job a pull request can start must run on a GitHub-hosted
// image, or on the one guarded expression ci.yml uses, which falls back to
// macos-latest for a pull_request. That expression does not protect a
// pull_request_target run (its event name differs, so it would pick the Mac),
// and a reusable workflow cannot be checked from here, so both fail.

// macGuard is the runs-on expression that sends a pull_request run to
// GitHub-hosted macos-latest and every other event to the Mac runners.
const macGuard = "${{ github.event_name != 'pull_request' && vars.MAC_RUNNER || 'macos-latest' }}"

// linuxGuard is the same guard for the workbench runners (initech-ci on
// 192.168.1.100): a pull_request run goes to GitHub-hosted ubuntu-latest.
const linuxGuard = "${{ github.event_name != 'pull_request' && 'initech-wb' || 'ubuntu-latest' }}"

// hostedLabel is a GitHub-hosted image name: ubuntu-, macos- or windows-
// followed by a version or latest.
var hostedLabel = regexp.MustCompile(`^(ubuntu|macos|windows)-[0-9a-z.-]+$`)

type prWorkflow struct {
	On   yaml.Node `yaml:"on"`
	Jobs map[string]struct {
		RunsOn yaml.Node `yaml:"runs-on"`
		Uses   string    `yaml:"uses"`
	} `yaml:"jobs"`
}

// prTriggers is which of pull_request and pull_request_target start w.
func prTriggers(on yaml.Node) (pr, prTarget bool) {
	var names []string
	switch on.Kind {
	case yaml.ScalarNode:
		names = []string{on.Value}
	case yaml.SequenceNode:
		for _, n := range on.Content {
			names = append(names, n.Value)
		}
	case yaml.MappingNode:
		for i := 0; i < len(on.Content); i += 2 {
			names = append(names, on.Content[i].Value)
		}
	}
	for _, n := range names {
		pr = pr || n == "pull_request"
		prTarget = prTarget || n == "pull_request_target"
	}
	return pr, prTarget
}

// runsOnLabels is a job's runs-on as its labels: a string, a list, or
// {group, labels}; a group is a runner group, so it is reported as one.
func runsOnLabels(n yaml.Node) []string {
	switch n.Kind {
	case yaml.ScalarNode:
		return []string{n.Value}
	case yaml.SequenceNode:
		var out []string
		for _, c := range n.Content {
			out = append(out, c.Value)
		}
		return out
	case yaml.MappingNode:
		var out []string
		for i := 0; i < len(n.Content); i += 2 {
			switch n.Content[i].Value {
			case "group":
				out = append(out, "group:"+n.Content[i+1].Value)
			case "labels":
				out = append(out, runsOnLabels(*n.Content[i+1])...)
			}
		}
		return out
	}
	return nil
}

// prMacJobs is every job in a workflow that a pull request could send off
// GitHub-hosted runners, with why; none when no pull request starts it.
func prMacJobs(src []byte) ([]string, error) {
	var w prWorkflow
	if err := yaml.Unmarshal(src, &w); err != nil {
		return nil, err
	}
	pr, prTarget := prTriggers(w.On)
	if !pr && !prTarget {
		return nil, nil
	}
	var bad []string
	for name, job := range w.Jobs {
		if job.Uses != "" {
			bad = append(bad, fmt.Sprintf("%s: a reusable workflow (%s) whose runners cannot be checked here", name, job.Uses))
			continue
		}
		labels := runsOnLabels(job.RunsOn)
		if len(labels) == 0 {
			bad = append(bad, name+": no runs-on")
			continue
		}
		for _, l := range labels {
			switch {
			case hostedLabel.MatchString(l):
			case (l == macGuard || l == linuxGuard) && !prTarget:
			case l == macGuard || l == linuxGuard:
				bad = append(bad, name+": the guarded expression under pull_request_target, where it picks the Mac")
			default:
				bad = append(bad, fmt.Sprintf("%s: runs-on %q is not a GitHub-hosted image", name, l))
			}
		}
	}
	sort.Strings(bad)
	return bad, nil
}

// Every workflow in the repo: no pull request reaches the Mac runners.
func TestWorkflows_NoPullRequestReachesTheMacRunners(t *testing.T) {
	root := repoRootForWorkflows(t)
	files, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.y*ml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("found no workflows under %s: this test is not reading them (%v)", root, err)
	}
	var sawPR bool
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		bad, err := prMacJobs(src)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, b := range bad {
			t.Errorf("%s: %s", filepath.Base(f), b)
		}
		var w prWorkflow
		_ = yaml.Unmarshal(src, &w)
		if pr, prt := prTriggers(w.On); pr || prt {
			sawPR = true
		}
	}
	if !sawPR {
		t.Error("no workflow triggers on pull_request: ci.yml does, so this test is not reading triggers")
	}
}

// The check itself, red first: each unsafe shape fails, and each safe shape
// passes.
func TestWorkflows_PullRequestCheckCatchesEachUnsafeShape(t *testing.T) {
	job := func(on, runsOn string) string {
		return "on: " + on + "\njobs:\n  j:\n    runs-on: " + runsOn + "\n    steps: [{run: x}]\n"
	}
	for name, src := range map[string]string{
		"the Mac variable on pull_request":        job("[push, pull_request]", "${{ vars.MAC_RUNNER }}"),
		"the Mac variable with a hosted fallback": job("[pull_request]", "${{ vars.MAC_RUNNER || 'macos-latest' }}"),
		"the guard under pull_request_target":     job("[pull_request_target]", "\""+macGuard+"\""),
		"the Linux guard under a PR target":       job("[pull_request_target]", "\""+linuxGuard+"\""),
		"the Linux variable on pull_request":      job("[pull_request]", "${{ vars.LINUX_RUNNER }}"),
		"the runner label":                        job("{pull_request: {branches: [main]}}", "nmbp14"),
		"self-hosted in a list":                   job("pull_request", "[self-hosted, macOS]"),
		"a runner group":                          job("[pull_request]", "{group: mac, labels: [macos-latest]}"),
		"a reusable workflow":                     "on: [pull_request]\njobs:\n  j:\n    uses: ./.github/workflows/x.yml\n",
		"no runs-on":                              "on: [pull_request]\njobs:\n  j:\n    steps: [{run: x}]\n",
	} {
		bad, err := prMacJobs([]byte(src))
		if err != nil || len(bad) == 0 {
			t.Errorf("%s: passed (%v), want it refused", name, err)
		}
	}
	for name, src := range map[string]string{
		"the guard on pull_request":    job("[push, pull_request, workflow_dispatch]", "\""+macGuard+"\""),
		"the Linux guard on a PR":      job("[push, pull_request]", "\""+linuxGuard+"\""),
		"a hosted image on a PR":       job("[pull_request]", "macos-latest"),
		"the Mac with no PR trigger":   job("[push, workflow_dispatch]", "${{ vars.MAC_RUNNER }}"),
		"the label on schedule":        job("{schedule: [{cron: '0 7 * * *'}]}", "nmbp14"),
		"a hosted list on a PR target": job("[pull_request_target]", "[ubuntu-24.04]"),
	} {
		bad, err := prMacJobs([]byte(src))
		if err != nil || len(bad) != 0 {
			t.Errorf("%s: refused %v (%v), want it passed", name, bad, err)
		}
	}
}

// repoRootForWorkflows is the module root, two levels above this package.
func repoRootForWorkflows(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(wd))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s is not the repo root: %v", root, err)
	}
	return strings.TrimSuffix(root, string(filepath.Separator))
}
