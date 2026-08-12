//go:build !windows

// rolesless_boot_test.go is ini-9ka.1's empirical half: proof that a REAL TUI
// process boots from a roles-less, remotes-only config, reaches a stable idle
// state, and owns zero local PTYs. The validator change is necessary but not
// sufficient -- Validate() accepting the config says nothing about whether the
// process that consumes it actually runs.
//
// Gated behind INITECH_CENSUS=1 (same gate as the census check) because it
// builds and runs the real binary under a PTY.
// Run: INITECH_CENSUS=1 go test ./internal/tui/ -run TestRolesLessBoot -v -count=1 -timeout 120s
package tui

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestRolesLessBoot_StartsIdleWithZeroLocalPanes boots the real binary with no
// roles and one remote, and asserts the three things the AC asks for:
// it started, it reached a stable idle state, and it created no local panes.
//
// The remote deliberately points at a dead address. A viewer window must be
// able to start and idle BEFORE it has anything to attach to -- if boot
// depended on a reachable peer, window N could never start first, and the
// startup order would be a hidden constraint on the whole feature.
func TestRolesLessBoot_StartsIdleWithZeroLocalPanes(t *testing.T) {
	if os.Getenv("INITECH_CENSUS") != "1" {
		t.Skip("set INITECH_CENSUS=1 to run the roles-less boot proof (builds and runs the real binary)")
	}

	// Short root, NOT t.TempDir(): this project gets a .initech/initech.sock,
	// and t.TempDir() derives its path from the test name, which pushes the
	// socket path past macOS's ~104-char sockaddr_un limit. The IPC bind then
	// fails and startup aborts -- which in THIS test would read as
	// "roles-less boot does not work", the exact conclusion the test exists
	// to rule on. Same trap documented in census_zerochange_test.go.
	root, err := os.MkdirTemp("", "ini-rl-")
	if err != nil {
		t.Fatalf("mkdir temp root: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	// No roles key at all, one remote. This is the pure-viewer shape.
	cfg := "project: viewerproj\nroot: " + root + `
peer_name: window2
remotes:
    window1:
        addr: 127.0.0.1:59999
`
	if err := os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	bin := buildInitechBinary(t)
	cmd := exec.Command(bin)
	cmd.Dir = root
	var env []string
	for _, e := range append(os.Environ(), "TERM=xterm-256color") {
		if strings.HasPrefix(e, "INITECH_SOCKET=") || strings.HasPrefix(e, "INITECH_AGENT=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = env

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("start under pty: %v", err)
	}
	var out strings.Builder
	done := make(chan struct{})
	go func() {
		io.Copy(&out, ptmx)
		close(done)
	}()
	defer func() {
		_ = cmd.Process.Kill()
		ptmx.Close()
		<-done
	}()

	time.Sleep(4 * time.Second)

	// (1) Started: a validation rejection would have exited immediately with
	// the error on stdout, before the alt-screen switch.
	if plain := preAltScreenOutput(out.String(), root); len(plain) > 0 && plain[0] != "(no plain-text output before alt-screen entry)" {
		t.Errorf("unexpected pre-alt-screen output (a roles-less config should start cleanly, not print an error): %v", plain)
	}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		t.Fatalf("process exited during startup; a roles-less remotes-only config must boot. Output:\n%s", out.String())
	}

	logPath := filepath.Join(root, ".initech", "initech.log")
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read %s: %v (no log means the process never got far enough to start one)", logPath, err)
	}
	logText := string(logBytes)

	// (2) Reached a stable idle state, and (3) with zero local panes. The
	// event-loop marker carries the pane count, so this is a positive
	// assertion about how many panes exist rather than only an absence.
	if !strings.Contains(logText, "entering event loop") {
		t.Errorf("log has no event-loop marker; the TUI never reached idle. Log:\n%s", logText)
	}
	if !strings.Contains(logText, "entering event loop") || !strings.Contains(logText, "panes=0") {
		t.Errorf("expected the event loop to be entered with panes=0 (zero local PTYs). Log:\n%s", logText)
	}

	// Belt-and-braces on the same claim from the other direction: no pane was
	// ever created, not merely none surviving at idle.
	if strings.Contains(logText, "create-pane") {
		t.Errorf("a local pane was created for a roles-less config; a viewer window must own no agents. Log:\n%s", logText)
	}
}

// TestRolesLessBoot_BrokenRoleDirsStillFailWithRemotes guards the far side of
// the startup relaxation. Zero local agents is legal only for a config with NO
// roles at all; if the operator configured roles and every one failed to build
// (missing workspace directories), that is still the error it has always been,
// even when remotes are present.
//
// Without this distinction the relaxation would convert a real "your
// workspace is broken, run initech init" diagnostic into a session that
// silently starts with nothing -- the operator's agents would simply be
// absent, with no error to explain why.
func TestRolesLessBoot_BrokenRoleDirsStillFailWithRemotes(t *testing.T) {
	if os.Getenv("INITECH_CENSUS") != "1" {
		t.Skip("set INITECH_CENSUS=1 to run the roles-less boot proof (builds and runs the real binary)")
	}

	root, err := os.MkdirTemp("", "ini-rlbad-")
	if err != nil {
		t.Fatalf("mkdir temp root: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	// Roles ARE configured, but no matching workspace directories exist.
	// Remotes are present too, so only the len(proj.Roles) check can tell
	// this apart from the legitimate viewer case.
	cfg := "project: brokenproj\nroot: " + root + `
peer_name: window2
roles:
    - eng1
    - eng2
remotes:
    window1:
        addr: 127.0.0.1:59999
`
	if err := os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	bin := buildInitechBinary(t)
	cmd := exec.Command(bin)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("start under pty: %v", err)
	}
	var out strings.Builder
	done := make(chan struct{})
	go func() {
		io.Copy(&out, ptmx)
		close(done)
	}()
	defer func() {
		_ = cmd.Process.Kill()
		ptmx.Close()
		<-done
	}()

	time.Sleep(3 * time.Second)
	if !strings.Contains(out.String(), "no valid role directories found") {
		t.Errorf("expected the unchanged 'no valid role directories found' error when configured roles fail to build, even with remotes set. Output:\n%s", out.String())
	}
}
