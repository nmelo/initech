// Additional coverage tests for cmd/ package: ipcCallSocket, discoverSocket, ipcCall.
package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
)

// ── ipcCallSocket ───────────────────────────────────────────────────

func TestIpcCallSocket_Success(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		conn.Read(buf)
		resp := tui.IPCResponse{OK: true, Data: "hello"}
		data, _ := json.Marshal(resp)
		conn.Write(data)
		conn.Write([]byte("\n"))
	}()

	resp, err := ipcCallSocket(sock, tui.IPCRequest{Action: "list"})
	if err != nil {
		t.Fatalf("ipcCallSocket error: %v", err)
	}
	if !resp.OK {
		t.Error("resp.OK = false, want true")
	}
	if resp.Data != "hello" {
		t.Errorf("resp.Data = %q, want 'hello'", resp.Data)
	}
}

func TestIpcCallSocket_ConnectionRefused(t *testing.T) {
	_, err := ipcCallSocket("/tmp/nonexistent-initech-coverage-test.sock", tui.IPCRequest{Action: "list"})
	if err == nil {
		t.Error("expected error for nonexistent socket")
	}
}

func TestIpcCallSocket_NoResponse(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, _ := ln.Accept()
		conn.Close() // close without sending response
	}()

	_, err = ipcCallSocket(sock, tui.IPCRequest{Action: "list"})
	if err == nil {
		t.Error("expected error when server closes without response")
	}
}

func TestIpcCallSocket_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, _ := ln.Accept()
		defer conn.Close()
		buf := make([]byte, 4096)
		conn.Read(buf)
		conn.Write([]byte("not json\n"))
	}()

	_, err = ipcCallSocket(sock, tui.IPCRequest{Action: "list"})
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

// ── ipcCall ─────────────────────────────────────────────────────────

func TestIpcCall_MissingSocket(t *testing.T) {
	old := os.Getenv("INITECH_SOCKET")
	os.Unsetenv("INITECH_SOCKET")
	defer func() {
		if old != "" {
			os.Setenv("INITECH_SOCKET", old)
		}
	}()

	_, err := ipcCall(tui.IPCRequest{Action: "list"})
	if err == nil {
		t.Error("expected error when INITECH_SOCKET is unset")
	}
}

// ── discoverSocket ──────────────────────────────────────────────────

func TestDiscoverSocket_NoConfig(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(old)

	_, _, err := discoverSocket()
	if err == nil {
		t.Error("expected error when no initech.yaml exists")
	}
}

func TestDiscoverSocket_WithConfig(t *testing.T) {
	// Use /tmp for short socket paths (macOS 104-byte limit).
	dir, err := os.MkdirTemp("/tmp", "initech-cov-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	cfg := fmt.Sprintf("project: test\nroot: %s\nroles:\n  - eng1\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0644)
	initechDir := filepath.Join(dir, ".initech")
	os.MkdirAll(initechDir, 0755)

	sockFile := filepath.Join(initechDir, "initech.sock")
	ln, lnErr := net.Listen("unix", sockFile)
	if lnErr != nil {
		t.Fatal(lnErr)
	}
	defer ln.Close()

	old, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(old)

	sockPath, proj, err := discoverSocket()
	if err != nil {
		t.Fatalf("discoverSocket error: %v", err)
	}
	if proj.Name != "test" {
		t.Errorf("project name = %q, want 'test'", proj.Name)
	}
	if sockPath == "" {
		t.Error("sockPath should not be empty")
	}
}

func TestBuildAgentPaneConfig_RoleCommandOverride(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "claude-agent"), 0755)
	os.MkdirAll(filepath.Join(dir, "codex-agent"), 0755)

	proj := &config.Project{
		Name:  "test",
		Root:  dir,
		Roles: []string{"claude-agent", "codex-agent"},
		RoleOverrides: map[string]config.RoleOverride{
			"codex-agent": {Command: []string{"codex", "--full-auto"}},
		},
	}

	// Claude agent uses default command.
	cfg1, err := buildAgentPaneConfig("claude-agent", proj)
	if err != nil {
		t.Fatalf("claude-agent: %v", err)
	}
	if cfg1.Command[0] != "claude" {
		t.Errorf("claude-agent argv[0] = %q, want 'claude'", cfg1.Command[0])
	}

	// Codex agent uses per-role command override.
	cfg2, err := buildAgentPaneConfig("codex-agent", proj)
	if err != nil {
		t.Fatalf("codex-agent: %v", err)
	}
	if cfg2.Command[0] != "codex" {
		t.Errorf("codex-agent argv[0] = %q, want 'codex'", cfg2.Command[0])
	}
	if !strings.Contains(strings.Join(cfg2.Command, " "), "--full-auto") {
		t.Errorf("codex-agent argv should contain --full-auto: %v", cfg2.Command)
	}
}
