package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/tui"
)

// TestMain clears INITECH_SOCKET before any test in this package runs.
// Several commands now treat INITECH_SOCKET as authoritative for socket
// resolution (ini-raup); every real agent pane has it exported (pointing at
// a live TUI socket — confirmed present in this repo's own dev shells), and
// none of this package's ~20+ discoverSocket-reaching tests set or clear it
// themselves, since they isolate purely via os.Chdir into a fixture. Without
// this, running `go test` in a shell that inherits a real INITECH_SOCKET
// would silently redirect those tests' dials away from their own fixtures
// and into live infrastructure — the same incident this bug fixes, in the
// opposite direction. Tests that need their own fixture socket already
// t.Setenv it explicitly; that scoping is unaffected since t.Setenv restores
// per-test regardless of what the process-wide baseline was.
func TestMain(m *testing.M) {
	os.Unsetenv("INITECH_SOCKET")
	os.Exit(m.Run())
}

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix-specific features (sockets, /tmp, /bin/sh)")
	}
}

func disableColor(t *testing.T) func() {
	t.Helper()
	prev := color.Enabled()
	color.SetEnabled(false)
	return func() {
		color.SetEnabled(prev)
	}
}

// writeBasicConfig writes a minimal single-role initech.yaml for tests that
// only need config.Discover/Load to succeed.
func writeBasicConfig(t *testing.T, dir, project string) {
	t.Helper()
	cfg := "project: " + project + "\nroot: " + dir + "\nroles:\n  - eng1\n"
	if err := os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("WriteFile initech.yaml: %v", err)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func chdirForTest(t *testing.T, dir string) func() {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	return func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore Chdir(%s): %v", wd, err)
		}
	}
}

// shortProjectDir returns a short-named temp dir, for tests whose project
// path feeds into a Unix socket path (subject to the ~108-byte sun_path
// limit) rather than t.TempDir()'s longer, test-name-derived path.
func shortProjectDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "iat-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// responseMode configures how startATIPCServer replies to the single request
// it captures: a parsed IPCResponse (resp), a raw payload bypassing that
// encoding (raw), or no reply at all (closeWithoutReply).
type responseMode struct {
	resp              string
	raw               string
	closeWithoutReply bool
}

// startATIPCServer listens on sockPath and replies to the first request per
// mode, capturing the raw request bytes onto the returned channel. Named for
// its original caller (the at-command tests); reused by other commands that
// need the same single-request IPC fake.
func startATIPCServer(t *testing.T, sockPath string, mode responseMode) (<-chan []byte, func()) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		t.Fatalf("MkdirAll socket dir: %v", err)
	}
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}

	reqCh := make(chan []byte, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			scanner := bufio.NewScanner(conn)
			if !scanner.Scan() {
				_ = conn.Close()
				continue // discoverSocket probe connection
			}
			req := append([]byte(nil), scanner.Bytes()...)
			select {
			case reqCh <- req:
			default:
			}

			if !mode.closeWithoutReply {
				payload := mode.raw
				if payload == "" {
					payload = mode.resp + "\n"
				}
				_, _ = conn.Write([]byte(payload))
			}
			_ = conn.Close()
		}
	}()

	cleanup := func() {
		_ = ln.Close()
		_ = os.Remove(sockPath)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for at IPC server shutdown")
		}
	}
	return reqCh, cleanup
}

// waitATRequest reads one request captured by startATIPCServer and unmarshals
// it into dst, failing the test if none arrives within 2s.
func waitATRequest(t *testing.T, reqCh <-chan []byte, dst any) {
	t.Helper()
	select {
	case req := <-reqCh:
		if err := json.Unmarshal(req, dst); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for at IPC request")
	}
}

// fakeProjectWithIPC creates a temp dir with initech.yaml, starts a fake IPC
// server at the socket path discoverSocket will find, and chdir into it.
// This isolates tests from the real TUI — discoverSocket never escapes the
// temp dir.
func fakeProjectWithIPC(t *testing.T, resp tui.IPCResponse) string {
	t.Helper()

	// Use /tmp base to keep socket paths short (macOS 104-byte limit).
	dir, err := os.MkdirTemp("", fmt.Sprintf("initech-test-%d-", os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	cfg := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - eng1\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0644)

	initechDir := filepath.Join(dir, ".initech")
	os.MkdirAll(initechDir, 0755)
	sockPath := filepath.Join(initechDir, "initech.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sockPath) })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			data, _ := json.Marshal(resp)
			conn.Write(data)
			conn.Write([]byte("\n"))
			conn.Close()
		}
	}()

	orig, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(orig) })
	return sockPath
}
