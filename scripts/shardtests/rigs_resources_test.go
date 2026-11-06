package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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
	hold := os.Getenv("FAKE_WORKER_HOLD")
	if hold == "" {
		hold = "4"
	}
	later := exec.Command("sleep", hold)
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

// buildFakeWorker builds the fake Runner.Worker into dir.
func buildFakeWorker(t *testing.T, dir string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(fakeWorker), 0o600); err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(dir, "Runner.Worker")
	build := exec.Command("go", "build", "-o", worker, "main.go")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the fake worker: %v\n%s", err, out)
	}
	return worker
}

// alive reports whether pid is a running process, by ps: a process that has
// exited but is not yet reaped (a zombie) is not running.
func alive(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	st := strings.TrimSpace(string(out))
	return err == nil && st != "" && !strings.HasPrefix(st, "Z")
}

// ini-r0d8 AC6: the sampler stops within one sample once its scope is gone,
// as when its job ends: it no longer samples a Runner.Worker that has exited
// for the rest of its two hours. The test waits on the sampler's own exit;
// the context is only the outer bound (red at base: it keeps sampling).
func TestRigsResources_TheSamplerStopsWhenItsScopeIsGone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash and ps")
	}
	root := repoRootForWorkflows(t)
	dir := t.TempDir()
	scope := exec.Command("sleep", "30")
	if err := scope.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := exec.CommandContext(ctx, "bash", filepath.Join(root, "scripts", "rigs-resources.sh"), "sample", "", strconv.Itoa(scope.Process.Pid))
	s.Env = append(os.Environ(), "RUNNER_TEMP="+dir, "RIGS_SAMPLES=1000", "RIGS_EVERY=0.1")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	_ = scope.Process.Kill()
	_, _ = scope.Process.Wait()
	err := s.Wait()
	if ctx.Err() != nil {
		t.Fatal("the sampler kept sampling after its scope was gone")
	}
	if err != nil {
		t.Fatalf("the sampler failed: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "rigs-resources"))
	if n := len(strings.Fields(strings.TrimSpace(string(b)))) / 3; n > 2 {
		t.Errorf("%d samples after the scope was gone, want at most one more:\n%s", n, b)
	}
}

// ini-r0d8 AC6: report stops the loop start left, by the pid start recorded,
// while the job's worker is still alive (red at base: the loop survives).
func TestRigsResources_ReportStopsTheLoopItsStartLeft(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the fake Runner.Worker scope is the macOS path")
	}
	root := repoRootForWorkflows(t)
	dir := t.TempDir()
	worker := buildFakeWorker(t, dir)
	step := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(step, []byte(sampleStep(t, root)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := exec.Command(worker, step, root)
	w.Env = append(os.Environ(), "RUNNER_TEMP="+dir, "RIGS_SAMPLES=1000", "RIGS_EVERY=0.2", "FAKE_WORKER_HOLD=30")
	stdout, err := w.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Process.Kill(); _, _ = w.Process.Wait() })
	// The worker prints its line when the step (start) has returned: an
	// event, not a clock.
	if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatalf("the worker said nothing: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "rigs-resources.pid"))
	if err != nil {
		t.Fatalf("start wrote no pid file for its loop: %v", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0]))
	if pid == 0 {
		t.Fatalf("pid file %q", b)
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	})
	if !alive(pid) {
		t.Fatalf("the loop %d is not running while its worker lives", pid)
	}
	r := exec.Command("bash", filepath.Join(root, "scripts", "rigs-resources.sh"), "report")
	r.Env = append(os.Environ(), "RUNNER_TEMP="+dir)
	out, err := r.CombinedOutput()
	if err != nil {
		t.Fatalf("report: %v\n%s", err, out)
	}
	if alive(pid) {
		t.Errorf("the loop %d survived report:\n%s", pid, out)
	}
}

// ini-r0d8 AC6: a pid file whose pid is now another process (its start time
// differs) is left alone; report kills only the loop it recorded.
func TestRigsResources_ReportLeavesAProcessThatIsNotTheLoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash and ps")
	}
	root := repoRootForWorkflows(t)
	dir := t.TempDir()
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _, _ = other.Process.Wait() })
	if err := os.WriteFile(filepath.Join(dir, "rigs-resources"), []byte("scope none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pidFile := strconv.Itoa(other.Process.Pid) + "\nMon Jan  1 00:00:00 2001\n"
	if err := os.WriteFile(filepath.Join(dir, "rigs-resources.pid"), []byte(pidFile), 0o600); err != nil {
		t.Fatal(err)
	}
	r := exec.Command("bash", filepath.Join(root, "scripts", "rigs-resources.sh"), "report")
	r.Env = append(os.Environ(), "RUNNER_TEMP="+dir)
	if out, err := r.CombinedOutput(); err != nil {
		t.Fatalf("report: %v\n%s", err, out)
	}
	// other is this test's child: killed, it would linger as a zombie, which
	// alive does not count as running.
	if !alive(other.Process.Pid) {
		t.Error("report killed a process that was not its loop")
	}
}
