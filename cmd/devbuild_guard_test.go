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
	origBuildInfo := buildInfoMainVersion
	origAllow := allowDevDelivery
	origUnderTest := isRunningUnderGoTest
	isRunningUnderGoTest = func() bool { return false }
	t.Cleanup(func() {
		Version = origVersion
		buildInfoMainVersion = origBuildInfo
		allowDevDelivery = origAllow
		isRunningUnderGoTest = origUnderTest
	})
}

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

// TestDevBuildGuard_RealBuildInfoVersionIsNotGated is the defense-in-depth
// branch: Version stays "dev" (as it would for `go install pkg@version`,
// which does not run our Makefile's ldflags), but runtime/debug build info
// shows a real resolved module version rather than "(devel)". Verified
// empirically that "go build ." from inside this repo embeds "(devel)" and a
// real brew install embeds a real version (see ini-grg3 bead comments); this
// test simulates the "go install" shape via the overridable var, since a real
// go install round-trip isn't reproducible in this suite (this repo's
// replace directive currently blocks `go install` outright -- see the bead).
func TestDevBuildGuard_RealBuildInfoVersionIsNotGated(t *testing.T) {
	skipWindows(t)
	resetDevBuildGuardState(t)
	Version = "dev"
	buildInfoMainVersion = func() (string, bool) { return "v1.9.1", true }

	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"send", "eng1", "test"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("a real build-info version should not be gated even with Version=dev, got error: %v", err)
	}
}

func TestDevBuildGuard_DoesNotGateReadOnlyActions(t *testing.T) {
	skipWindows(t)
	resetDevBuildGuardState(t)
	Version = "dev"

	panes := `[{"name":"eng1","activity":"idle","alive":true}]`
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true, Data: panes})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"status"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("read-only commands must not be gated by the dev-build guard, got error: %v", err)
	}
}
