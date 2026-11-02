package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// fakeWorker is a parent named Runner.Worker: it runs one step the way the
// runner does, prints how long the step held it, and stays alive after the
// step's shell has exited with a child of its own, as the real worker does
// with the job's later steps.
const fakeWorker = `package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

func main() {
	later := exec.Command("sleep", "4")
	if err := later.Start(); err != nil {
		panic(err)
	}
	c := exec.Command("bash", "-e", os.Args[1])
	c.Dir = os.Args[2]
	begin := time.Now()
	err := c.Run()
	fmt.Println(os.Getpid(), time.Since(begin).Milliseconds(), err == nil)
	later.Wait()
}
`

// sampleStep returns the workflow's own text for the darwin job's sampler
// step, so the proof runs what the job runs.
func sampleStep(t *testing.T, root string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "rigs.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(src, &w); err != nil {
		t.Fatal(err)
	}
	for _, s := range w.Jobs["darwin"].Steps {
		if strings.Contains(s.Run, "rigs-resources.sh start") {
			return s.Run
		}
	}
	t.Fatal("rigs.yml darwin job has no sampler start step")
	return ""
}

// TestRigsResources_TheMacJobIsMeasuredUnderItsWorker runs the darwin job's
// sampler step under a parent named Runner.Worker. The step must return at
// once, and the sampler must still have found that parent and sampled its
// process tree. A sampler started in the background loses the chain when the
// step's shell exits, and reports scope=[none]. Linux reads the runner's
// cgroup instead, so this path exists only on the Mac.
func TestRigsResources_TheMacJobIsMeasuredUnderItsWorker(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the process-tree scope is the macOS path; Linux reads the cgroup")
	}
	root := repoRootForWorkflows(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(fakeWorker), 0o600); err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(dir, "Runner.Worker")
	build := exec.Command("go", "build", "-o", worker, "main.go")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the fake worker: %v\n%s", err, out)
	}
	step := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(step, []byte(sampleStep(t, root)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	run := exec.Command(worker, step, root)
	run.Env = append(os.Environ(), "RUNNER_TEMP="+dir, "RIGS_SAMPLES=1")
	out, err := run.Output()
	if err != nil {
		t.Fatalf("fake worker: %v", err)
	}
	var pid, heldMS int
	var ok bool
	if _, err := fmt.Sscan(string(out), &pid, &heldMS, &ok); err != nil {
		t.Fatalf("fake worker output %q: %v", out, err)
	}
	if !ok {
		t.Fatal("the sampler step failed")
	}
	if heldMS > 3000 {
		t.Errorf("the step held the job %dms, want it to return at once and leave the sampling in the background", heldMS)
	}

	log, err := os.ReadFile(filepath.Join(dir, "rigs-resources"))
	if err != nil {
		t.Fatal(err)
	}
	want := "scope process-tree Runner.Worker pid " + strconv.Itoa(pid)
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if lines[0] != want {
		t.Fatalf("first line %q, want %q (whole log %q)", lines[0], want, log)
	}
	if len(lines) != 2 {
		t.Fatalf("log %q, want the scope and one sample", log)
	}
	f := strings.Fields(lines[1])
	if len(f) != 3 {
		t.Fatalf("sample %q, want time, kilobytes, processes", lines[1])
	}
	if kb, _ := strconv.Atoi(f[1]); kb <= 0 {
		t.Errorf("sample %q measured no memory under the worker", lines[1])
	}
	if n, _ := strconv.Atoi(f[2]); n < 2 {
		t.Errorf("sample %q counted %d processes, want the worker and its child", lines[1], n)
	}
}
