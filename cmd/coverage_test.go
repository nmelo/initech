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

	// Run from a temp dir with no initech.yaml so discoverSocket() fails.
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	_, err := ipcCall(tui.IPCRequest{Action: "list"})
	if err == nil {
		t.Error("expected error when INITECH_SOCKET is unset and no initech.yaml exists")
	}
}

func TestIpcCall_FallbackToDiscoverSocket(t *testing.T) {
	old := os.Getenv("INITECH_SOCKET")
	os.Unsetenv("INITECH_SOCKET")
	defer func() {
		if old != "" {
			os.Setenv("INITECH_SOCKET", old)
		}
	}()

	// Create a project dir with initech.yaml so discoverSocket finds it.
	dir := t.TempDir()
	cfgContent := "project: testproj\nroot: " + dir + "\nroles:\n  - eng1\n"
	os.WriteFile(dir+"/initech.yaml", []byte(cfgContent), 0600)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// ipcCall should attempt to connect to the discovered socket (and fail
	// because no TUI is running), NOT fail with "INITECH_SOCKET not set".
	_, err := ipcCall(tui.IPCRequest{Action: "list"})
	if err == nil {
		t.Error("expected connection error (no TUI running)")
	}
	// The error should be about the session not running, not about env var.
	if strings.Contains(err.Error(), "INITECH_SOCKET") {
		t.Errorf("error mentions INITECH_SOCKET (should fall back to discover): %v", err)
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
	skipWindows(t)
	// Use /tmp for short socket paths (macOS 104-byte limit).
	dir, err := os.MkdirTemp("", "initech-cov-")
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
		Name:       "test",
		Root:       dir,
		Roles:      []string{"claude-agent", "codex-agent"},
		ClaudeArgs: []string{"--continue", "--dangerously-skip-permissions"},
		RoleOverrides: map[string]config.RoleOverride{
			"codex-agent": {
				AgentType: config.AgentTypeCodex,
				Command:   []string{"codex", "--full-auto"},
				SubmitKey: "ctrl+enter",
			},
		},
	}

	// Claude agent uses default command + global claude_args.
	cfg1, err := buildAgentPaneConfig("claude-agent", proj)
	if err != nil {
		t.Fatalf("claude-agent: %v", err)
	}
	if cfg1.Command[0] != "claude" {
		t.Errorf("claude-agent argv[0] = %q, want 'claude'", cfg1.Command[0])
	}
	joined1 := strings.Join(cfg1.Command, " ")
	if !strings.Contains(joined1, "--continue") {
		t.Errorf("claude-agent should have --continue: %v", cfg1.Command)
	}
	if cfg1.AgentType != config.AgentTypeClaudeCode {
		t.Errorf("claude-agent AgentType = %q, want %q", cfg1.AgentType, config.AgentTypeClaudeCode)
	}
	if cfg1.AutoApprove {
		t.Error("claude-agent AutoApprove = true, want false")
	}
	if cfg1.NoBracketedPaste {
		t.Error("claude-agent NoBracketedPaste = true, want false")
	}
	if cfg1.SubmitKey != "" {
		t.Errorf("claude-agent SubmitKey = %q, want empty default", cfg1.SubmitKey)
	}

	// Codex agent uses per-role command override; claude_args must NOT be appended.
	cfg2, err := buildAgentPaneConfig("codex-agent", proj)
	if err != nil {
		t.Fatalf("codex-agent: %v", err)
	}
	if cfg2.Command[0] != "codex" {
		t.Errorf("codex-agent argv[0] = %q, want 'codex'", cfg2.Command[0])
	}
	joined2 := strings.Join(cfg2.Command, " ")
	if !strings.Contains(joined2, "--full-auto") {
		t.Errorf("codex-agent should contain --full-auto: %v", cfg2.Command)
	}
	if strings.Contains(joined2, "--continue") || strings.Contains(joined2, "--dangerously-skip-permissions") {
		t.Errorf("codex-agent should NOT have claude_args appended: %v", cfg2.Command)
	}
	if cfg2.AgentType != config.AgentTypeCodex {
		t.Errorf("codex-agent AgentType = %q, want %q", cfg2.AgentType, config.AgentTypeCodex)
	}
	if !cfg2.AutoApprove {
		t.Error("codex-agent AutoApprove = false, want codex default true")
	}
	if !cfg2.NoBracketedPaste {
		t.Error("codex-agent NoBracketedPaste = false, want codex default true")
	}
	if cfg2.SubmitKey != "ctrl+enter" {
		t.Errorf("codex-agent SubmitKey = %q, want ctrl+enter override", cfg2.SubmitKey)
	}
}

func TestBuildServeAgentConfig_AgentTypeDefaults(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "generic-agent"), 0755)

	proj := &config.Project{
		Name:  "test",
		Root:  dir,
		Roles: []string{"generic-agent"},
		RoleOverrides: map[string]config.RoleOverride{
			"generic-agent": {AgentType: config.AgentTypeGeneric},
		},
	}

	cfg, err := buildAgentPaneConfig("generic-agent", proj)
	if err != nil {
		t.Fatalf("buildAgentPaneConfig: %v", err)
	}
	if cfg.AgentType != config.AgentTypeGeneric {
		t.Errorf("AgentType = %q, want %q", cfg.AgentType, config.AgentTypeGeneric)
	}
	if cfg.AutoApprove {
		t.Error("AutoApprove = true, want false for generic agent")
	}
	if !cfg.NoBracketedPaste {
		t.Error("NoBracketedPaste = false, want true for generic agent")
	}
	if cfg.SubmitKey != "enter" {
		t.Errorf("SubmitKey = %q, want enter", cfg.SubmitKey)
	}
}

func TestResolvePaneBehavior_AutoApproveOverride(t *testing.T) {
	disabled := false
	enabled := true

	tests := []struct {
		name            string
		override        config.RoleOverride
		wantAgentType   string
		wantAutoApprove bool
		wantNoBracketed bool
		wantSubmitKey   string
	}{
		{
			name:            "codex default",
			override:        config.RoleOverride{AgentType: config.AgentTypeCodex},
			wantAgentType:   config.AgentTypeCodex,
			wantAutoApprove: true,
			wantNoBracketed: true,
			wantSubmitKey:   "enter",
		},
		{
			name:            "opencode default",
			override:        config.RoleOverride{AgentType: config.AgentTypeOpenCode},
			wantAgentType:   config.AgentTypeOpenCode,
			wantAutoApprove: true,
			wantNoBracketed: true,
			wantSubmitKey:   "enter",
		},
		{
			name:            "opencode explicit false",
			override:        config.RoleOverride{AgentType: config.AgentTypeOpenCode, AutoApprove: &disabled},
			wantAgentType:   config.AgentTypeOpenCode,
			wantAutoApprove: false,
			wantNoBracketed: true,
			wantSubmitKey:   "enter",
		},
		{
			name:            "claude explicit true",
			override:        config.RoleOverride{AgentType: config.AgentTypeClaudeCode, AutoApprove: &enabled},
			wantAgentType:   config.AgentTypeClaudeCode,
			wantAutoApprove: true,
			wantNoBracketed: false,
			wantSubmitKey:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentType, autoApprove, noBracketedPaste, submitKey := resolvePaneBehavior(tt.override)
			if agentType != tt.wantAgentType {
				t.Fatalf("agentType = %q, want %q", agentType, tt.wantAgentType)
			}
			if autoApprove != tt.wantAutoApprove {
				t.Fatalf("autoApprove = %v, want %v", autoApprove, tt.wantAutoApprove)
			}
			if noBracketedPaste != tt.wantNoBracketed {
				t.Fatalf("noBracketedPaste = %v, want %v", noBracketedPaste, tt.wantNoBracketed)
			}
			if submitKey != tt.wantSubmitKey {
				t.Fatalf("submitKey = %q, want %q", submitKey, tt.wantSubmitKey)
			}
		})
	}
}
