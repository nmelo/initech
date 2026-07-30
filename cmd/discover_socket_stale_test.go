package cmd

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Regression tests for ini-0fvf: discoverSocket used to delete the socket
// file on ANY dial failure. That is correct for a genuinely stale socket (no
// listener at all — the ini-db1 scenario a crashed TUI leaves behind), but a
// live TUI with a momentarily full/busy accept queue also fails to dial, and
// deleting a LIVE session's socket over that ambiguous signal orphans it
// until manual restart. The fix classifies the dial error: only a definite
// "no listener" shape (ECONNREFUSED/ENOENT) is treated as stale; a timeout
// (or anything else) leaves the socket in place.
//
// The classification is tested with SYNTHETIC errors, not a live OS backlog
// exhaustion: reproducing a full unix-socket backlog deterministically is
// platform-timing-dependent (verified empirically that a dead listener gives
// ENOENT rather than ECONNREFUSED on this platform — see the bead comments —
// which is exactly the kind of assumption not to make about the OTHER branch
// either). What matters here is that the classification function does the
// right thing given each error SHAPE, which a synthetic error proves cleanly.

// fakeTimeoutError implements net.Error with Timeout() == true, simulating
// what net.DialTimeout returns when a dial genuinely times out (e.g. a full
// accept backlog on a live-but-busy listener) rather than being refused.
type fakeTimeoutError struct{}

func (fakeTimeoutError) Error() string   { return "i/o timeout (simulated)" }
func (fakeTimeoutError) Timeout() bool   { return true }
func (fakeTimeoutError) Temporary() bool { return true }

func setupDiscoverableProject(t *testing.T) (dir string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "initech-0fvf-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	cfg := fmt.Sprintf("project: test\nroot: %s\nroles:\n  - eng1\n", dir)
	if err := os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	initechDir := filepath.Join(dir, ".initech")
	if err := os.MkdirAll(initechDir, 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// stubDiscoverSocketDial overrides the dial call discoverSocket uses,
// restoring the original on cleanup.
func stubDiscoverSocketDial(t *testing.T, fn func(string) (net.Conn, error)) {
	t.Helper()
	orig := discoverSocketDial
	discoverSocketDial = fn
	t.Cleanup(func() { discoverSocketDial = orig })
}

func TestDiscoverSocket_TimeoutDialError_LeavesSocketInPlace(t *testing.T) {
	dir := setupDiscoverableProject(t)
	defer chdirForTest(t, dir)()
	sockFile := filepath.Join(dir, ".initech", "initech.sock")
	if err := os.WriteFile(sockFile, []byte("not a real socket, just a marker file"), 0644); err != nil {
		t.Fatal(err)
	}

	stubDiscoverSocketDial(t, func(string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "unix", Err: fakeTimeoutError{}}
	})

	_, _, err := discoverSocket()
	if err == nil {
		t.Fatal("expected an error for a timed-out dial")
	}
	if strings.Contains(err.Error(), "stale socket removed") {
		t.Errorf("error = %q; a timeout must NOT be reported as a stale-socket removal", err.Error())
	}
	if !strings.Contains(strings.ToLower(err.Error()), "busy") && !strings.Contains(strings.ToLower(err.Error()), "unresponsive") {
		t.Errorf("error = %q, want language indicating the session may be busy/unresponsive, not gone", err.Error())
	}
	if _, statErr := os.Stat(sockFile); statErr != nil {
		t.Errorf("socket file was removed on a timeout dial error (should survive): %v", statErr)
	}
}

func TestDiscoverSocket_ConnectionRefused_RemovesStaleSocket(t *testing.T) {
	dir := setupDiscoverableProject(t)
	defer chdirForTest(t, dir)()
	sockFile := filepath.Join(dir, ".initech", "initech.sock")
	if err := os.WriteFile(sockFile, []byte("stale marker"), 0644); err != nil {
		t.Fatal(err)
	}

	stubDiscoverSocketDial(t, func(string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "unix", Err: syscall.ECONNREFUSED}
	})

	_, _, err := discoverSocket()
	if err == nil {
		t.Fatal("expected an error for connection refused")
	}
	if !strings.Contains(err.Error(), "stale socket removed") {
		t.Errorf("error = %q, want the existing stale-socket-removed message preserved", err.Error())
	}
	if _, statErr := os.Stat(sockFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("expected the stale socket file to be removed, stat err = %v", statErr)
	}
}

func TestDiscoverSocket_NoSuchFile_RemovesStaleSocket(t *testing.T) {
	dir := setupDiscoverableProject(t)
	defer chdirForTest(t, dir)()
	sockFile := filepath.Join(dir, ".initech", "initech.sock")
	if err := os.WriteFile(sockFile, []byte("stale marker"), 0644); err != nil {
		t.Fatal(err)
	}

	// ENOENT is what this platform's net.DialTimeout actually returns for a
	// dead listener (verified empirically, not assumed — see bead comments),
	// so this is the realistic case here, not just a defensive extra.
	stubDiscoverSocketDial(t, func(string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "unix", Err: syscall.ENOENT}
	})

	_, _, err := discoverSocket()
	if err == nil {
		t.Fatal("expected an error for no-such-file")
	}
	if !strings.Contains(err.Error(), "stale socket removed") {
		t.Errorf("error = %q, want the existing stale-socket-removed message preserved", err.Error())
	}
	if _, statErr := os.Stat(sockFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("expected the stale socket file to be removed, stat err = %v", statErr)
	}
}

// TestDiscoverSocket_InvalidArgument_RemovesStaleSocket covers a THIRD dial
// error shape found while fixing this bug, not anticipated by the bead's own
// "ECONNREFUSED/ENOENT" framing: a unix socket path exceeding the sun_path
// length limit (~104 bytes on macOS) fails with EINVAL. Verified empirically
// (a real net.DialTimeout against an over-long path) rather than assumed —
// this is exactly what an existing test (TestInteg_CLIErrorPaths, whose
// t.TempDir()-derived path plus "/.initech/initech.sock" exceeds the limit)
// already exercised, and it broke when this fix first landed with only
// ECONNREFUSED/ENOENT recognized. A path too long to ever be bound by any
// listener is just as unambiguously "no listener" as the other two shapes.
func TestDiscoverSocket_InvalidArgument_RemovesStaleSocket(t *testing.T) {
	dir := setupDiscoverableProject(t)
	defer chdirForTest(t, dir)()
	sockFile := filepath.Join(dir, ".initech", "initech.sock")
	if err := os.WriteFile(sockFile, []byte("stale marker"), 0644); err != nil {
		t.Fatal(err)
	}

	stubDiscoverSocketDial(t, func(string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "unix", Err: syscall.EINVAL}
	})

	_, _, err := discoverSocket()
	if err == nil {
		t.Fatal("expected an error for invalid-argument (over-long path)")
	}
	if !strings.Contains(err.Error(), "stale socket removed") {
		t.Errorf("error = %q, want the existing stale-socket-removed message preserved", err.Error())
	}
	if _, statErr := os.Stat(sockFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("expected the stale socket file to be removed, stat err = %v", statErr)
	}
}

func TestDiscoverSocket_LiveSocket_StillWorksUnchanged(t *testing.T) {
	skipWindows(t)
	dir := setupDiscoverableProject(t)
	sockFile := filepath.Join(dir, ".initech", "initech.sock")
	ln, err := net.Listen("unix", sockFile)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	defer chdirForTest(t, dir)()

	sockPath, proj, err := discoverSocket()
	if err != nil {
		t.Fatalf("discoverSocket error against a live listener: %v", err)
	}
	if proj.Name != "test" {
		t.Errorf("project name = %q, want 'test'", proj.Name)
	}
	if sockPath == "" {
		t.Error("sockPath should not be empty")
	}
	if _, statErr := os.Stat(sockFile); statErr != nil {
		t.Errorf("live socket file should still exist: %v", statErr)
	}
}
