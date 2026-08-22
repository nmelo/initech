package tui

// Liveness must be answerable on every platform we ship (ini-uop2).
//
// DELIBERATELY UNTAGGED. The defect this pins was invisible for a release
// because every test that exercised the probe was unix-only: vet
// cross-compiles, but cross-compiled tests cannot RUN, so a probe that is not
// a probe on Windows passed every commit-time guard and shipped in v2.9.0.
//
// STALE PREMISE CORRECTED (ini-ibsm, 2026-08-22). This comment used to say
// these cells run on the Windows CI leg, "the only place the question can
// actually be asked". THERE IS NO WINDOWS LEG ANY MORE -- macOS is the only
// tested platform; linux and windows still SHIP, untested and
// community-maintained. So nothing currently executes the Windows branch of
// childProcessAlive, and the backstop that caught the v2.9.0 defect no longer
// exists. These cells stay untagged and stay honest: they are ready for a leg
// that may return, and until one does, the Windows probe is unverified by
// anything except review. Do not read their presence as coverage.
//
// They spawn the test binary itself as a helper, which is the stdlib's own
// pattern for "a real process, on any OS" -- no sleep(1), no timeout.exe, no
// shell.

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestLivenessHelperProcess is not a test: it is the child the cells below
// spawn. It exits immediately unless the parent asks for it.
//
// lint:test-name-allow helper process for the liveness cells; asserts nothing
// by design and is skipped in every ordinary run.
func TestLivenessHelperProcess(t *testing.T) {
	if os.Getenv("INITECH_LIVENESS_HELPER") != "1" {
		t.Skip("helper process for TestChildProcessAlive_*; not run directly")
	}
	time.Sleep(30 * time.Second)
}

// startLivenessHelper spawns a real, live child and returns it.
func startLivenessHelper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLivenessHelperProcess$")
	cmd.Env = append(os.Environ(), "INITECH_LIVENESS_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

// TestChildProcessAlive_SaysAliveForALiveChild is the cell that was red on
// Windows and green everywhere else.
//
// On Windows the old probe returned EWINDOWS for every signal but Kill,
// without consulting the process, so this answered "dead" for a child that was
// plainly running -- and resumePane turned that into "process died during
// startup", re-queued the messages and re-suspended the pane. Suspend/resume
// was a one-way door on that platform.
func TestChildProcessAlive_SaysAliveForALiveChild(t *testing.T) {
	cmd := startLivenessHelper(t)

	if !childProcessAlive(cmd.Process) {
		t.Fatalf("a child that is running was reported dead.\n\nEvery wake on this platform "+
			"fails this way: resumePane reads it as \"process died during startup\", puts the "+
			"messages back on the queue and marks the pane suspended again, so the agent "+
			"never comes back. (pid %d)", cmd.Process.Pid)
	}
	if !pidExists(cmd.Process.Pid) {
		t.Errorf("pidExists says pid %d is gone while it is running; the PID-file guard "+
			"reads a live previous instance as an unclean exit and deletes its file",
			cmd.Process.Pid)
	}
}

// TestChildProcessAlive_SaysDeadForAnExitedChild is the direction that must not
// widen. ini-g7fl's probe exists because an exec-failed child still produces
// OUTPUT, so a liveness answer of "alive" for a corpse sends queued messages
// into a pane whose process is gone.
func TestChildProcessAlive_SaysDeadForAnExitedChild(t *testing.T) {
	cmd := startLivenessHelper(t)
	if !childProcessAlive(cmd.Process) {
		t.Fatal("precondition: the helper was not alive to begin with, so the assertion " +
			"below would pass without measuring anything")
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	if _, err := cmd.Process.Wait(); err != nil {
		t.Fatalf("wait helper: %v", err)
	}

	if childProcessAlive(cmd.Process) {
		t.Errorf("an exited child (pid %d) is reported alive -- queued messages would be "+
			"delivered into a corpse instead of the wake being retried", cmd.Process.Pid)
	}
}

// TestPidExists_SaysDeadForAnImpossiblePid keeps the PID-file guard honest at
// the other end: a pid nothing can hold must read as gone on every platform.
func TestPidExists_SaysDeadForAnImpossiblePid(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if pidExists(pid) {
			t.Errorf("pidExists(%d) is true; a non-pid must never read as a live instance", pid)
		}
	}
}
