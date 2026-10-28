package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

var gateJobs = []string{"build-and-test", "census-leg", "full-suite-1", "full-suite-2", "full-suite-3", "full-suite-4", "full-suite-5", "full-suite-census"}

func greenJobs(names ...string) []Job {
	jobs := make([]Job, len(names))
	for i, n := range names {
		jobs[i] = Job{Name: n, Conclusion: "success"}
	}
	return jobs
}

func greenRun() Run {
	return Run{ID: 1, Event: "push", HeadSHA: sha, Conclusion: "success", Jobs: greenJobs(gateJobs...)}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test")
		}
		dir = parent
	}
}

// The guard's job list is ci.yml's job keys, the list shipper's gate reads; the
// live file must give the eight-job set, full suite included.
func TestExpectedJobs_ReadsCIYamlJobKeys(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := expectedJobs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != strings.Join(gateJobs, ",") {
		t.Fatalf("ci.yml job keys %v; the gate's set is %v", got, gateJobs)
	}
}

func TestExpectedJobs_EmptyWorkflowIsAnError(t *testing.T) {
	if _, err := expectedJobs([]byte("name: CI\n")); err == nil {
		t.Fatal("a ci.yml with no jobs gave a job list; the guard would pass on any run")
	}
}

func TestVerdict_GreenPushRunWithEveryJobPasses(t *testing.T) {
	if err := verdict(sha, gateJobs, []Run{greenRun()}); err != nil {
		t.Fatalf("a green push run with the full job set was refused: %v", err)
	}
}

func TestVerdict_NoRunIsRefused(t *testing.T) { // feed-it-nothing
	if err := verdict(sha, gateJobs, nil); err == nil {
		t.Fatal("a commit with no CI run passed the guard")
	}
}

func TestVerdict_BuildJobOnlyIsRefused(t *testing.T) {
	r := greenRun()
	r.Jobs = greenJobs("build-and-test")
	if err := verdict(sha, gateJobs, []Run{r}); err == nil || !strings.Contains(err.Error(), "missing jobs") {
		t.Fatalf("a run with only the build job passed: %v", err)
	}
}

// A shard the workflow names but the run lacks is refused: a gate whose set
// lost full-suite-5 while ci.yml still runs five shards would pass a cut on
// four fifths of the suite (ini-c5a.3).
func TestVerdict_RunMissingOneShardIsRefused(t *testing.T) {
	r := greenRun()
	var jobs []Job
	for _, j := range r.Jobs {
		if j.Name != "full-suite-5" {
			jobs = append(jobs, j)
		}
	}
	if len(jobs) != len(r.Jobs)-1 {
		t.Fatal("fixture: full-suite-5 is not in the gate's set")
	}
	r.Jobs = jobs
	if err := verdict(sha, gateJobs, []Run{r}); err == nil || !strings.Contains(err.Error(), "missing jobs") {
		t.Fatalf("a run without full-suite-5 passed: %v", err)
	}
}

func TestVerdict_PullRequestRunIsRefused(t *testing.T) {
	r := greenRun()
	r.Event = "pull_request"
	if err := verdict(sha, gateJobs, []Run{r}); err == nil {
		t.Fatal("a pull_request run stood in for the push run")
	}
}

func TestVerdict_RedRunIsRefused(t *testing.T) {
	r := greenRun()
	r.Conclusion = "failure"
	if err := verdict(sha, gateJobs, []Run{r}); err == nil {
		t.Fatal("a failed run passed")
	}
}

func TestVerdict_RedJobIsRefused(t *testing.T) {
	r := greenRun()
	r.Jobs[3].Conclusion = "failure"
	if err := verdict(sha, gateJobs, []Run{r}); err == nil || !strings.Contains(err.Error(), "not green") {
		t.Fatalf("a run with a red job passed: %v", err)
	}
}

func TestVerdict_ExtraJobIsRefused(t *testing.T) {
	r := greenRun()
	r.Jobs = append(r.Jobs, Job{Name: "surprise", Conclusion: "success"})
	if err := verdict(sha, gateJobs, []Run{r}); err == nil || !strings.Contains(err.Error(), "unexpected jobs") {
		t.Fatalf("a run with a job ci.yml does not name passed: %v", err)
	}
}

func TestVerdict_OtherCommitIsRefused(t *testing.T) {
	r := greenRun()
	r.HeadSHA = strings.Repeat("f", 40)
	if err := verdict(sha, gateJobs, []Run{r}); err == nil {
		t.Fatal("a green run at another commit passed")
	}
}

func TestVerdict_OneGreenRunAmongCancelledPasses(t *testing.T) {
	cancelled := greenRun()
	cancelled.ID, cancelled.Conclusion = 2, "cancelled"
	if err := verdict(sha, gateJobs, []Run{cancelled, greenRun()}); err != nil {
		t.Fatalf("a cancelled run hid the green one beside it: %v", err)
	}
}

// release.yml runs the guard before goreleaser in the release job, has no test
// job (the guard stands in for it) and no AI-summary step (pm's notes replace
// the body every release).
func TestReleaseWorkflow_GuardRunsBeforeGoreleaser(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Needs interface{} `yaml:"needs"`
			Steps []struct {
				Name string `yaml:"name"`
				Uses string `yaml:"uses"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	if _, ok := wf.Jobs["test"]; ok {
		t.Error("release.yml still has a test job; CI green at the sha is checked by the guard")
	}
	rel, ok := wf.Jobs["release"]
	if !ok {
		t.Fatal("release.yml has no release job")
	}
	guard, gorel := -1, -1
	for i, s := range rel.Steps {
		if strings.Contains(s.Run, "./scripts/releaseguard") && guard < 0 {
			guard = i
		}
		if strings.HasPrefix(s.Uses, "goreleaser/goreleaser-action") && gorel < 0 {
			gorel = i
		}
		if strings.Contains(strings.ToLower(s.Name), "ai changelog") {
			t.Errorf("release.yml still has the %q step", s.Name)
		}
	}
	if guard < 0 || gorel < 0 || guard > gorel {
		t.Fatalf("release job: guard step at %d, goreleaser at %d; the guard must run first", guard, gorel)
	}
}
