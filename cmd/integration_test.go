// Integration tests: end-to-end verification of scaffold, config, IPC, doctor,
// and CLI error paths. All use t.TempDir() for filesystem isolation.
package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/scaffold"
	"github.com/nmelo/initech/internal/tui"
)

// ── Test 1: Full scaffold round-trip ────────────────────────────────

func TestInteg_ScaffoldRoundTrip(t *testing.T) {
	dir := t.TempDir()
	proj := &config.Project{
		Name:  "testproj",
		Root:  dir,
		Roles: []string{"super", "eng1", "qa1"},
		Beads: config.BeadsConfig{Prefix: "tp"},
	}

	created, err := scaffold.Run(proj, scaffold.Options{})
	if err != nil {
		t.Fatalf("scaffold.Run: %v", err)
	}
	if len(created) == 0 {
		t.Error("scaffold.Run should return created paths")
	}

	// Write initech.yaml (scaffold creates the tree but not the config file).
	cfgPath := filepath.Join(dir, "initech.yaml")
	if err := config.Write(cfgPath, proj); err != nil {
		t.Fatalf("config.Write: %v", err)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load after scaffold: %v", err)
	}
	if loaded.Name != "testproj" {
		t.Errorf("loaded name = %q, want 'testproj'", loaded.Name)
	}

	// Root CLAUDE.md exists.
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Error("root CLAUDE.md missing")
	}

	// Role CLAUDE.md files exist and contain substituted content.
	for _, role := range proj.Roles {
		claudePath := filepath.Join(dir, role, "CLAUDE.md")
		data, err := os.ReadFile(claudePath)
		if err != nil {
			t.Errorf("%s/CLAUDE.md missing: %v", role, err)
			continue
		}
		content := string(data)
		// Core variables (role_name, project_name) must be substituted.
		// Optional vars (tech_stack, build_cmd, test_cmd) may remain if empty.
		if strings.Contains(content, "{{role_name}}") {
			t.Errorf("%s/CLAUDE.md has unsubstituted {{role_name}}", role)
		}
		if strings.Contains(content, "{{project_name}}") {
			t.Errorf("%s/CLAUDE.md has unsubstituted {{project_name}}", role)
		}
	}

	// docs/ directory.
	for _, doc := range []string{"prd.md", "spec.md", "systemdesign.md", "roadmap.md"} {
		if _, err := os.Stat(filepath.Join(dir, "docs", doc)); err != nil {
			t.Errorf("docs/%s missing", doc)
		}
	}

	// .gitignore and AGENTS.md.
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err != nil {
		t.Error(".gitignore missing")
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Error("AGENTS.md missing")
	}
}

// ── Test 2: Config round-trip ───────────────────────────────────────

func TestInteg_ConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	original := &config.Project{
		Name:          "myproj",
		Root:          dir,
		Roles:         []string{"super", "eng1", "eng2"},
		Beads:         config.BeadsConfig{Prefix: "mp"},
		ClaudeCommand: []string{"claude"},
		ClaudeArgs:    []string{"--continue", "--dangerously-skip-permissions"},
		RoleOverrides: map[string]config.RoleOverride{
			"eng1": {Dir: "/custom/path", ClaudeArgs: []string{"--model", "opus"}},
		},
	}

	cfgPath := filepath.Join(dir, "initech.yaml")
	if err := config.Write(cfgPath, original); err != nil {
		t.Fatalf("config.Write: %v", err)
	}

	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	if loaded.Name != original.Name {
		t.Errorf("Name = %q, want %q", loaded.Name, original.Name)
	}
	if len(loaded.Roles) != len(original.Roles) {
		t.Errorf("Roles len = %d, want %d", len(loaded.Roles), len(original.Roles))
	}
	if loaded.Beads.Prefix != original.Beads.Prefix {
		t.Errorf("Beads.Prefix = %q, want %q", loaded.Beads.Prefix, original.Beads.Prefix)
	}
	if len(loaded.ClaudeArgs) != len(original.ClaudeArgs) {
		t.Errorf("ClaudeArgs len = %d, want %d", len(loaded.ClaudeArgs), len(original.ClaudeArgs))
	}
	if ov, ok := loaded.RoleOverrides["eng1"]; !ok {
		t.Error("RoleOverrides missing eng1")
	} else if ov.Dir != "/custom/path" {
		t.Errorf("eng1 override Dir = %q, want '/custom/path'", ov.Dir)
	}
}

// ── Test 3: Config validation rejects bad inputs ────────────────────

func TestInteg_ConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		proj   config.Project
		errSub string
	}{
		{"empty name", config.Project{Root: "/x", Roles: []string{"a"}}, "project name"},
		{"empty roles", config.Project{Name: "x", Root: "/x"}, "at least one role"},
		{"duplicate role", config.Project{Name: "x", Root: "/x", Roles: []string{"a", "a"}}, "duplicate role"},
		{"space in role", config.Project{Name: "x", Root: "/x", Roles: []string{"bad name"}}, "invalid role name"},
		{"slash in role", config.Project{Name: "x", Root: "/x", Roles: []string{"bad/name"}}, "invalid role name"},
		{"dots in role", config.Project{Name: "x", Root: "/x", Roles: []string{".."}}, "invalid role name"},
		{"override for missing role", config.Project{
			Name: "x", Root: "/x", Roles: []string{"a"},
			RoleOverrides: map[string]config.RoleOverride{"z": {}},
		}, "not in roles list"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := config.Validate(&tc.proj)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.errSub)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("error = %q, want contains %q", err.Error(), tc.errSub)
			}
		})
	}
}

// ── Test 4: IPC socket round-trip ───────────────────────────────────

func TestInteg_IPCRoundTrip(t *testing.T) {
	skipWindows(t)
	// Use /tmp for short socket paths (macOS 104-byte limit).
	sockDir, err := os.MkdirTemp("", "initech-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	sockPath := filepath.Join(sockDir, "test.sock")

	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()

	quitCh := make(chan struct{})
	tuiInst := &tui.TUI{}
	// Use exported SetTestFields to configure the TUI for IPC testing.
	// Since we can't access unexported fields from cmd package, we'll
	// use the IPC socket directly with a mock server instead.

	// Start a mock IPC server that handles list, peek, bead actions.
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_ = tuiInst
	_ = quitCh

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				n, _ := c.Read(buf)
				var req tui.IPCRequest
				json.Unmarshal(buf[:n], &req)

				var resp tui.IPCResponse
				switch req.Action {
				case "list":
					panes := []paneInfo{{Name: "eng1", Activity: "idle", Alive: true, Visible: true}}
					data, _ := json.Marshal(panes)
					resp = tui.IPCResponse{OK: true, Data: string(data)}
				case "peek":
					resp = tui.IPCResponse{OK: true, Data: "hello from eng1\n"}
				case "bead":
					resp = tui.IPCResponse{OK: true}
				default:
					resp = tui.IPCResponse{Error: "unknown action"}
				}
				data, _ := json.Marshal(resp)
				c.Write(data)
				c.Write([]byte("\n"))
			}(conn)
		}
	}()

	// List: verify pane name.
	resp, err := ipcCallSocket(sockPath, tui.IPCRequest{Action: "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !resp.OK {
		t.Errorf("list: resp not OK: %s", resp.Error)
	}
	if !strings.Contains(resp.Data, "eng1") {
		t.Errorf("list data should contain 'eng1': %s", resp.Data)
	}

	// Peek: verify content.
	resp, err = ipcCallSocket(sockPath, tui.IPCRequest{Action: "peek", Target: "eng1"})
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if !strings.Contains(resp.Data, "hello") {
		t.Errorf("peek data should contain 'hello': %s", resp.Data)
	}

	// Bead: set and verify.
	resp, err = ipcCallSocket(sockPath, tui.IPCRequest{Action: "bead", Target: "eng1", Text: "ini-test"})
	if err != nil {
		t.Fatalf("bead: %v", err)
	}
	if !resp.OK {
		t.Errorf("bead: resp not OK: %s", resp.Error)
	}
}

// ── Test 5: Socket liveness check ───────────────────────────────────

func TestInteg_SocketLiveness(t *testing.T) {
	skipWindows(t)
	sockDir, err := os.MkdirTemp("", "initech-live-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	sockPath := filepath.Join(sockDir, "test.sock")

	// Active listener blocks new connections on the same path.
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	// Verify the socket file exists.
	if _, err := os.Stat(sockPath); err != nil {
		t.Fatal("socket file should exist")
	}
	// Dialing the active socket should succeed (liveness check).
	conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("dial active socket should succeed: %v", err)
	}
	conn.Close()

	// Close the listener (simulates crashed instance leaving stale socket).
	ln.Close()
	time.Sleep(50 * time.Millisecond)

	// On macOS, closing a Unix listener removes the socket file. Re-create a
	// stale socket file manually to simulate a crashed instance that left a
	// socket behind without removing it.
	staleFile, err := os.Create(sockPath)
	if err != nil {
		t.Fatalf("create stale socket file: %v", err)
	}
	staleFile.Close()

	// Dialing a stale socket file should fail (it's a regular file, not a listener).
	_, err = net.DialTimeout("unix", sockPath, 200*time.Millisecond)
	if err == nil {
		t.Error("dialing stale socket should fail")
	}

	// A new listener can bind after removing the stale socket (same as startIPC).
	os.Remove(sockPath)
	ln2, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("re-listen after stale cleanup: %v", err)
	}
	ln2.Close()
}

// ── Test 6: Doctor prereq check ─────────────────────────────────────

func TestInteg_DoctorPrereqs(t *testing.T) {
	env := defaultDoctorEnv()
	results := runPrereqChecks(env)

	// Results should cover all 3 tools.
	labels := make(map[string]bool)
	for _, r := range results {
		labels[r.Label] = true
	}
	for _, tool := range []string{"claude", "git", "bd"} {
		if !labels[tool] {
			t.Errorf("prereq results missing tool %q", tool)
		}
	}
}

// ── Test 7: Doctor project health ───────────────────────────────────

func TestInteg_DoctorProjectHealth(t *testing.T) {
	dir := t.TempDir()
	proj := &config.Project{
		Name:  "healthtest",
		Root:  dir,
		Roles: []string{"eng1", "qa1"},
		Beads: config.BeadsConfig{Prefix: "ht"},
	}

	// Scaffold the project and write config.
	if _, err := scaffold.Run(proj, scaffold.Options{}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	cfgPath := filepath.Join(dir, "initech.yaml")
	if err := config.Write(cfgPath, proj); err != nil {
		t.Fatalf("config.Write: %v", err)
	}

	// First check: everything should be "ok" for workspaces.
	checks1, name1, _ := runProjectChecks(cfgPath)
	if name1 != "healthtest" {
		t.Errorf("project name = %q, want healthtest", name1)
	}
	hasOK := false
	for _, c := range checks1 {
		if c.Status == "OK" {
			hasOK = true
			break
		}
	}
	if !hasOK {
		t.Error("healthy project should have at least one OK check")
	}

	// Delete one CLAUDE.md.
	os.Remove(filepath.Join(dir, "qa1", "CLAUDE.md"))

	// Second check: should show WARNING with the missing role.
	checks2, _, _ := runProjectChecks(cfgPath)
	hasWarn := false
	mentionsQA1 := false
	for _, c := range checks2 {
		if c.Status == "WARN" {
			hasWarn = true
		}
		if strings.Contains(c.Detail, "qa1") {
			mentionsQA1 = true
		}
	}
	if !hasWarn {
		t.Error("missing CLAUDE.md should trigger WARN check result")
	}
	if !mentionsQA1 {
		t.Error("warning detail should mention 'qa1'")
	}
}

// ── Test 8: CLI error paths ─────────────────────────────────────────

func TestInteg_CLIErrorPaths(t *testing.T) {
	// ipcCallSocket with nonexistent socket.
	_, err := ipcCallSocket("/tmp/initech-inttest-nonexistent.sock", tui.IPCRequest{Action: "list"})
	if err == nil {
		t.Error("expected error for nonexistent socket")
	}

	// discoverSocket from a directory with no initech.yaml.
	dir := t.TempDir()
	old, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(old)

	_, _, err = discoverSocket()
	if err == nil {
		t.Error("expected error when no initech.yaml")
	}

	// discoverSocket with config but no running session.
	cfg := fmt.Sprintf("project: errtest\nroot: %s\nroles:\n  - a\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0644)
	_, _, err = discoverSocket()
	if err == nil {
		t.Error("expected error when session not running")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("error = %q, want contains 'not running'", err.Error())
	}
}
