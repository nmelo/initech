// Command releaseguard refuses to let a release build publish a tag whose
// commit has no green CI: the release workflow runs no tests of its own, so
// "CI green at the exact sha" is checked here, before goreleaser, for every v*
// tag, not only for the ones shipper's gate pushes.
//
// The required jobs are the job keys of .github/workflows/ci.yml at the tagged
// commit, the one list shipper's gate reads; the guard keeps no list of its
// own. It passes when one push-event ci.yml run at that commit concluded
// success and its jobs are exactly that set, every one green. A commit with
// only a build job, a pull-request run, a red or missing job, or no run at all
// is refused.
//
// Usage (in release.yml): go run ./scripts/releaseguard -sha "$GITHUB_SHA"
// with GH_TOKEN and GITHUB_REPOSITORY set. Exit 0 pass, 1 refused.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Job is one job of a workflow run as the GitHub API reports it.
type Job struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

// Run is one ci.yml run at the tagged commit, with its jobs.
type Run struct {
	ID         int64  `json:"id"`
	Event      string `json:"event"`
	HeadSHA    string `json:"head_sha"`
	Conclusion string `json:"conclusion"`
	Jobs       []Job  `json:"-"`
}

// expectedJobs returns the job keys of a ci.yml, sorted: the gate's job list.
func expectedJobs(ciYAML []byte) ([]string, error) {
	var wf struct {
		Jobs map[string]yaml.Node `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(ciYAML, &wf); err != nil {
		return nil, err
	}
	if len(wf.Jobs) == 0 {
		return nil, fmt.Errorf("ci.yml names no jobs")
	}
	keys := make([]string, 0, len(wf.Jobs))
	for k := range wf.Jobs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// verdict returns nil when one run proves the commit green: a push run at sha
// that concluded success with exactly the expected jobs, each a success.
// Otherwise it returns why each run fell short.
func verdict(sha string, expected []string, runs []Run) error {
	if len(runs) == 0 {
		return fmt.Errorf("no ci.yml run at %s", sha)
	}
	var why []string
	for _, r := range runs {
		switch {
		case r.HeadSHA != sha:
			why = append(why, fmt.Sprintf("run %d is at %s, not %s", r.ID, r.HeadSHA, sha))
		case r.Event != "push":
			why = append(why, fmt.Sprintf("run %d is a %s run, not push", r.ID, r.Event))
		case r.Conclusion != "success":
			why = append(why, fmt.Sprintf("run %d concluded %q", r.ID, r.Conclusion))
		default:
			if problem := jobProblem(expected, r.Jobs); problem != "" {
				why = append(why, fmt.Sprintf("run %d: %s", r.ID, problem))
				continue
			}
			return nil
		}
	}
	return fmt.Errorf("no green push run at %s: %s", sha, strings.Join(why, "; "))
}

// jobProblem compares a run's jobs with the expected set; "" when they are
// exactly that set and every one succeeded.
func jobProblem(expected []string, jobs []Job) string {
	want := map[string]bool{}
	for _, e := range expected {
		want[e] = true
	}
	got := map[string]bool{}
	var red []string
	for _, j := range jobs {
		got[j.Name] = true
		if j.Conclusion != "success" {
			red = append(red, j.Name+"="+j.Conclusion)
		}
	}
	var missing, extra []string
	for e := range want {
		if !got[e] {
			missing = append(missing, e)
		}
	}
	for g := range got {
		if !want[g] {
			extra = append(extra, g)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(red)
	switch {
	case len(missing) > 0:
		return "missing jobs " + strings.Join(missing, ",")
	case len(extra) > 0:
		return "unexpected jobs " + strings.Join(extra, ",")
	case len(red) > 0:
		return "not green " + strings.Join(red, ",")
	}
	return ""
}

func gh(args ...string) ([]byte, error) {
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// fetchRuns lists the ci.yml runs at sha with their jobs.
func fetchRuns(repo, sha string) ([]Run, error) {
	raw, err := gh("api", fmt.Sprintf("repos/%s/actions/workflows/ci.yml/runs?head_sha=%s&per_page=50", repo, sha))
	if err != nil {
		return nil, err
	}
	var page struct {
		Runs []Run `json:"workflow_runs"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, err
	}
	for i := range page.Runs {
		raw, err := gh("api", fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", repo, page.Runs[i].ID))
		if err != nil {
			return nil, err
		}
		var jobs struct {
			Jobs []Job `json:"jobs"`
		}
		if err := json.Unmarshal(raw, &jobs); err != nil {
			return nil, err
		}
		page.Runs[i].Jobs = jobs.Jobs
	}
	return page.Runs, nil
}

func main() {
	sha := flag.String("sha", "", "the tagged commit")
	ci := flag.String("ci", ".github/workflows/ci.yml", "ci.yml at the tagged commit")
	flag.Parse()
	repo := os.Getenv("GITHUB_REPOSITORY")
	if *sha == "" || repo == "" {
		fmt.Fprintln(os.Stderr, "releaseguard: -sha and GITHUB_REPOSITORY are required")
		os.Exit(1)
	}
	raw, err := os.ReadFile(*ci)
	if err != nil {
		fmt.Fprintln(os.Stderr, "releaseguard:", err)
		os.Exit(1)
	}
	expected, err := expectedJobs(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "releaseguard:", err)
		os.Exit(1)
	}
	runs, err := fetchRuns(repo, *sha)
	if err != nil {
		fmt.Fprintln(os.Stderr, "releaseguard:", err)
		os.Exit(1)
	}
	if err := verdict(*sha, expected, runs); err != nil {
		fmt.Fprintln(os.Stderr, "releaseguard: REFUSED:", err)
		os.Exit(1)
	}
	fmt.Printf("releaseguard: %s has a green push CI run with all %d jobs: %s\n", *sha, len(expected), strings.Join(expected, ", "))
}
