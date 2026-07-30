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
	"github.com/spf13/cobra"
)

// Regression tests for ini-raup: discoverSocket() was called directly by 10
// commands (status, down, add, add-agent, delete-agent, remove, restart,
// peers, start, stop), silently ignoring INITECH_SOCKET. A caller that set
// the env var to isolate itself (the documented way to point a command at
// an isolated session) could still have its socket resolution escape to
// whatever real project cwd's upward walk found — in one environment, a go
// test run reached the operator's live fleet and deleted a real socket
// file. resolveSocket() fixes this by making INITECH_SOCKET authoritative:
// when set, discoverSocket() — and therefore its internal config.Discover
// walk, dial probe, and stale-delete — is never invoked at all.

// setupDecoyAncestorProject creates <tmp>/decoy with its own initech.yaml
// (project "decoy") and a live listener at its socket path, plus
// <tmp>/decoy/child — a subdirectory with no config of its own. This
// reproduces the qa2-shadowing shape from the bead's evidence: a real,
// unrelated project one level above a caller's cwd. acceptCount is
// incremented every time the decoy's listener accepts a connection, proving
// whether it was ever dialed at all.
func setupDecoyAncestorProject(t *testing.T) (childDir, decoySockPath string, acceptCount *atomic.Int64) {
	t.Helper()
	decoyRoot, err := os.MkdirTemp("", "initech-raup-decoy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(decoyRoot) })

	// beads.enabled: false keeps runStatus from shelling out to a real bd
	// binary against this fixture's cwd (unrelated to what this test proves).
	cfg := fmt.Sprintf("project: decoy\nroot: %s\nroles:\n  - eng1\nbeads:\n  enabled: false\n", decoyRoot)
	if err := os.WriteFile(filepath.Join(decoyRoot, "initech.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	sockPath := tui.SocketPath(decoyRoot, "decoy")
	if err := os.MkdirAll(filepath.Dir(sockPath), 0755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	acceptCount = &atomic.Int64{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			acceptCount.Add(1)
			data, _ := json.Marshal(tui.IPCResponse{OK: true, Data: "[]"})
			conn.Write(data)
			conn.Write([]byte("\n"))
			conn.Close()
		}
	}()

	child := filepath.Join(decoyRoot, "child")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	return child, sockPath, acceptCount
}

// TestResolveSocket_INITECH_SOCKET_NeverInvokesDiscoverSocket proves the
// negative at the mechanism level: with INITECH_SOCKET set, discoverSocket's
// internal dial (discoverSocketDial) is never invoked at all, even with a
// real, live ancestor project sitting one directory above cwd. The metadata
// lookup (proj) is deliberately independent — it DOES find the decoy
// project via its own cwd walk, proving resolveSocket decouples "which
// socket gets dialed" from "which project's config is used for display,"
// rather than happening to skip the decoy by accident.
func TestResolveSocket_INITECH_SOCKET_NeverInvokesDiscoverSocket(t *testing.T) {
	skipWindows(t)
	childDir, decoySockPath, acceptCount := setupDecoyAncestorProject(t)
	restore := chdirForTest(t, childDir)
	defer restore()

	fixtureSock := filepath.Join("/tmp", fmt.Sprintf("initech-raup-fixture-%d.sock", os.Getpid()))
	t.Setenv("INITECH_SOCKET", fixtureSock)

	var dialCount int
	stubDiscoverSocketDial(t, func(string) (net.Conn, error) {
		dialCount++
		return nil, fmt.Errorf("discoverSocketDial must not be called when INITECH_SOCKET is set")
	})

	sockPath, proj, err := resolveSocket()
	if err != nil {
		t.Fatalf("resolveSocket error: %v", err)
	}
	if sockPath != fixtureSock {
		t.Errorf("sockPath = %q, want the INITECH_SOCKET fixture value %q", sockPath, fixtureSock)
	}
	if dialCount != 0 {
		t.Errorf("discoverSocketDial was called %d times; want 0 — the ancestor decoy project must never be dialed", dialCount)
	}
	if proj == nil || proj.Name != "decoy" {
		t.Errorf("proj = %+v, want the decoy project (metadata lookup is independent of socket resolution)", proj)
	}
	if acceptCount.Load() != 0 {
		t.Errorf("decoy listener accepted %d connections; want 0 — it must never be dialed", acceptCount.Load())
	}
	if _, statErr := os.Stat(decoySockPath); statErr != nil {
		t.Errorf("decoy socket file was removed (should survive untouched): %v", statErr)
	}
}

// TestRunStatus_INITECH_SOCKET_IsolatesFromAncestorProject is the end-to-end
// counterpart: with INITECH_SOCKET pointing at a fixture IPC server, running
// the actual status command must use that fixture's response, never dial
// the decoy ancestor project's live socket, and never delete it.
func TestRunStatus_INITECH_SOCKET_IsolatesFromAncestorProject(t *testing.T) {
	skipWindows(t)
	childDir, decoySockPath, decoyAcceptCount := setupDecoyAncestorProject(t)
	restore := chdirForTest(t, childDir)
	defer restore()

	marker := `[{"name":"fixture-marker-agent","activity":"idle","alive":true,"visible":true}]`
	fixtureSock := startFakeIPC(t, tui.IPCResponse{OK: true, Data: marker})
	t.Setenv("INITECH_SOCKET", fixtureSock)

	var stdout bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)

	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus error: %v", err)
	}
	if !strings.Contains(stdout.String(), "fixture-marker-agent") {
		t.Errorf("stdout = %q, want the fixture's response (fixture-marker-agent), not the decoy's", stdout.String())
	}
	if decoyAcceptCount.Load() != 0 {
		t.Errorf("decoy listener accepted %d connections; want 0 — status must never reach the ancestor project", decoyAcceptCount.Load())
	}
	if _, statErr := os.Stat(decoySockPath); statErr != nil {
		t.Errorf("decoy socket file was removed (should survive untouched): %v", statErr)
	}
}

// TestResolveSocket_FallsThroughToDiscoverSocketWhenUnset confirms
// resolveSocket preserves discoverSocket's existing cwd-walk behavior
// unchanged when INITECH_SOCKET is unset (the ordinary, non-fixture case
// every real agent pane relies on for project metadata).
func TestResolveSocket_FallsThroughToDiscoverSocketWhenUnset(t *testing.T) {
	skipWindows(t)
	dir := setupDiscoverableProject(t)
	sockFile := filepath.Join(dir, ".initech", "initech.sock")
	ln, err := net.Listen("unix", sockFile)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	restore := chdirForTest(t, dir)
	defer restore()

	sockPath, proj, err := resolveSocket()
	if err != nil {
		t.Fatalf("resolveSocket error: %v", err)
	}
	if sockPath != sockFile {
		t.Errorf("sockPath = %q, want %q", sockPath, sockFile)
	}
	if proj == nil || proj.Name != "test" {
		t.Errorf("proj = %+v, want project 'test'", proj)
	}
}
