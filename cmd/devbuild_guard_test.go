package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// Regression tests for ini-grg3: a locally-built dev binary run from inside
// the project tree inherits INITECH_SOCKET (the orchestrator injects it into
// every agent pane) and silently delivered against the LIVE fleet -- three
// stray keystroke deliveries into another agent's live pane in one incident.
// The discriminating signal is dev-build vs release-build (cmd.Version /
// runtime/debug build info), not project identity: the incident's cwd and the
// live socket's project resolved to the exact same path, so a project-based
// guard would not have caught it.

// listenCountingFakeIPC spins up a Unix socket like startFakeIPC, but also
// counts Accept() calls, so a test can assert the client never even dialed
// (proving the guard fires BEFORE any bytes reach the socket, not just before
// the write).
func listenCountingFakeIPC(t *testing.T, resp tui.IPCResponse) (sockPath string, accepts *atomic.Int64) {
	t.Helper()
	n := fakeIPCCounter.Add(1)
	sockPath = filepath.Join("/tmp", fmt.Sprintf("initech-test-guard-%d-%d.sock", os.Getpid(), n))
	os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sockPath) })

	accepts = &atomic.Int64{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			data, _ := json.Marshal(resp)
			conn.Write(data)
			conn.Write([]byte("\n"))
			conn.Close()
		}
	}()
	return sockPath, accepts
}

// resetDevBuildGuardState restores every package var the guard touches, so
// tests don't leak state into each other. Also forces isRunningUnderGoTest
// off: these tests exist specifically to exercise the real refusal/allow
// logic, which the go-test bypass would otherwise short-circuit entirely
// (every OTHER test in this package relies on that bypass being on and needs
// no changes at all).
func resetDevBuildGuardState(t *testing.T) {
	t.Helper()
	origVersion := Version
	origAllow := allowDevDelivery
	origUnderTest := isRunningUnderGoTest
	isRunningUnderGoTest = func() bool { return false }
	t.Cleanup(func() {
		Version = origVersion
		allowDevDelivery = origAllow
		isRunningUnderGoTest = origUnderTest
	})
}

// TestDevBuildGuard_RefusesDeliveryEffectCommand pins the corrected contract
// directly: Version=='dev' must gate the command BY ITSELF, with no other
// condition able to override it. A prior version of isDevBuild() ALSO
// required runtime/debug's build info to show "(devel)" before agreeing,
// treating any other value as a veto. That was actively wrong: verified
// empirically (a fresh, plain `git clone` -- NOT the submodule-style
// checkout every agent workspace uses for src/, which happens to suppress
// VCS stamping and was why an earlier check looked correct) that `make
// build`/`go build .` from a clean, tagged checkout on this toolchain
// (go1.26.4) embeds a REAL computed pseudo-version in build info, not
// "(devel)". That is the common case, not an exotic one, so the veto
// silently disabled the guard for ordinary local builds -- a P1 safety fix
// that did nothing in practice. This gap cannot be pinned by asserting a
// specific build-info shape in-process: a go-test binary's own build info
// never matches what a sibling `go build`/`make build` produces, so any test
// built that way would only prove go-test's shape, never a real build's.
// The bead documents the manual check instead: `make build`, then run the
// binary against a nonexistent socket path -- the guard's refusal error is
// the expected result, a raw dial error means it did not fire.
func TestDevBuildGuard_RefusesDeliveryEffectCommand(t *testing.T) {
	skipWindows(t)
	resetDevBuildGuardState(t)
	Version = "dev" // the default for every local go build / make build.

	sockPath, accepts := listenCountingFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"send", "eng1", "test"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected the dev-build guard to refuse, got nil error")
	}

	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "dev build") {
		t.Errorf("error = %q, must state this is a dev build", err.Error())
	}
	if !strings.Contains(err.Error(), "--allow-dev-delivery") {
		t.Errorf("error = %q, must name --allow-dev-delivery explicitly", err.Error())
	}
	if !strings.Contains(err.Error(), sockPath) {
		t.Errorf("error = %q, must identify which session (%s) would have been targeted", err.Error(), sockPath)
	}

	if n := accepts.Load(); n != 0 {
		t.Errorf("fake server accepted %d connection(s); the guard must refuse BEFORE dialing, not after", n)
	}
}

func TestDevBuildGuard_AllowDevDeliveryFlagBypassesRefusal(t *testing.T) {
	skipWindows(t)
	resetDevBuildGuardState(t)
	Version = "dev"

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"send", "eng1", "test", "--allow-dev-delivery"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("--allow-dev-delivery should bypass the guard, got error: %v", err)
	}
}

func TestDevBuildGuard_ReleaseVersionIsNotGated(t *testing.T) {
	skipWindows(t)
	resetDevBuildGuardState(t)
	Version = "v1.9.1" // what a real goreleaser+brew install carries.

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"send", "eng1", "test"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("a release-versioned build should never be gated, got error: %v", err)
	}
}

// TestDevBuildGuard_DoesNotGateReadOnlyActions previously set INITECH_SOCKET
// and expected "status" to use it. That was wrong: status.go's runStatus
// calls discoverSocket() directly and never reads INITECH_SOCKET at all
// (unlike send/interrupt/bead/etc., which go through the shared ipcCall
// wrapper) -- so the env var was silently ignored and discoverSocket() walked
// up from the test binary's REAL working directory instead. In this repo
// that reaches the actual shared workspace's real initech.yaml and, if a
// live TUI happens to be running, dials its REAL socket -- an uncontrolled
// test dependency on live infrastructure, structurally the same class of
// hazard as ini-grg3 itself, just triggered by `go test` instead of a manual
// CLI invocation. When no live TUI is reachable there, the dial fails as
// stale and ini-0fvf's classifier (correctly) deletes that socket file,
// which is what qa2 observed as "session 'qa-approve' is not running (stale
// socket removed)" -- a real, uncontrolled project this test had no business
// touching, discovered via an untethered cwd walk, not a fixture.
//
// Fixed by properly isolating status's ACTUAL resolution path: chdir into a
// dedicated temp project with its own initech.yaml and a real listening
// socket, so discoverSocket() finds THIS controlled fixture rather than
// whatever the real cwd happens to reach. The temp dir is created under
// /tmp directly (not t.TempDir(), which uses the long $TMPDIR on macOS) to
// keep the resulting socket path under the ~104-byte unix-socket sun_path
// limit -- exceeding it yields EINVAL, which ini-0fvf correctly classifies
// as stale and removes, an unrelated failure mode this test must not
// reintroduce by using a long path (mirrors startFakeIPC's own documented
// reason for using /tmp).
//
// The ini-0fvf REMOVAL behavior itself is not changed here, deliberately:
// discoverSocket's stale-socket cleanup is a property of discovery, not of
// which command triggered it, and it is already gated to fire only on a
// definite no-listener dial error (ECONNREFUSED/ENOENT/EINVAL), never on a
// timeout that could indicate a live-but-busy session. A read-only command
// finding and cleaning up a genuinely dead socket matches ini-db1's original
// intent; the defect was this test's isolation, not that behavior.
func TestDevBuildGuard_DoesNotGateReadOnlyActions(t *testing.T) {
	skipWindows(t)
	resetDevBuildGuardState(t)
	Version = "dev"

	dir, err := os.MkdirTemp("/tmp", "initech-grg3-ro-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	cfg := fmt.Sprintf("project: test\nroot: %s\nroles:\n  - eng1\n", dir)
	if err := os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".initech"), 0755); err != nil {
		t.Fatal(err)
	}
	sockFile := filepath.Join(dir, ".initech", "initech.sock")
	ln, err := net.Listen("unix", sockFile)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	panes := `[{"name":"eng1","activity":"idle","alive":true}]`
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			data, _ := json.Marshal(tui.IPCResponse{OK: true, Data: panes})
			conn.Write(data)
			conn.Write([]byte("\n"))
			conn.Close()
		}
	}()

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"status"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("read-only commands must not be gated by the dev-build guard, got error: %v", err)
	}
}
