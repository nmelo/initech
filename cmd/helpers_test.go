package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/tui"
)

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
