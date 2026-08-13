package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

// notifWiringProject builds a two-role project: one plain Claude agent and one
// codex agent driven by a per-role Command override.
func notifWiringProject(t *testing.T) *config.Project {
	t.Helper()
	// HOME is redirected so the developer's own ~/.claude/settings.json cannot
	// decide these assertions (ini-2fd: an operator-set channel legitimately
	// suppresses the injection, which would look like a wiring failure here).
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	for _, role := range []string{"claude-agent", "codex-agent"} {
		if err := os.MkdirAll(filepath.Join(dir, role), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", role, err)
		}
	}
	return &config.Project{
		Name:  "test",
		Root:  dir,
		Roles: []string{"claude-agent", "codex-agent"},
		RoleOverrides: map[string]config.RoleOverride{
			"codex-agent": {
				AgentType: config.AgentTypeCodex,
				Command:   []string{"codex", "--full-auto"},
			},
		},
	}
}

// TestBuildAgentPaneConfig_InjectsNotifChannelForClaude pins the wiring: the
// ini-2fd injection has to reach the argv initech actually runs. The unit
// tests around NotifChannelArgs prove the decision logic; this proves the
// product calls it.
func TestBuildAgentPaneConfig_InjectsNotifChannelForClaude(t *testing.T) {
	proj := notifWiringProject(t)

	cfg, err := buildAgentPaneConfig("claude-agent", proj)
	if err != nil {
		t.Fatalf("claude-agent: %v", err)
	}
	joined := strings.Join(cfg.Command, " ")
	if !strings.Contains(joined, "--settings") ||
		!strings.Contains(joined, `"preferredNotifChannel":"ghostty"`) {
		t.Errorf("claude argv carries no notification-channel injection: %q", cfg.Command)
	}
}

// TestBuildAgentPaneConfig_NoNotifChannelForNonClaudeAgent: --settings is
// Claude's flag. Appending it to codex would be a syntax error handed to a
// program that never had the bug.
func TestBuildAgentPaneConfig_NoNotifChannelForNonClaudeAgent(t *testing.T) {
	proj := notifWiringProject(t)

	cfg, err := buildAgentPaneConfig("codex-agent", proj)
	if err != nil {
		t.Fatalf("codex-agent: %v", err)
	}
	if joined := strings.Join(cfg.Command, " "); strings.Contains(joined, "--settings") {
		t.Errorf("codex argv carries a Claude --settings flag: %q", cfg.Command)
	}
}

// TestBuildAgentPaneConfig_NoNotifChannelForMockAgent keeps the test-harness
// agent free of the flag too: INITECH_MOCK_AGENT is usually a shell script.
func TestBuildAgentPaneConfig_NoNotifChannelForMockAgent(t *testing.T) {
	proj := notifWiringProject(t)
	t.Setenv("INITECH_MOCK_AGENT", "/bin/cat")

	cfg, err := buildAgentPaneConfig("claude-agent", proj)
	if err != nil {
		t.Fatalf("claude-agent: %v", err)
	}
	if joined := strings.Join(cfg.Command, " "); strings.Contains(joined, "--settings") {
		t.Errorf("mock agent argv carries a --settings flag: %q", cfg.Command)
	}
}

// TestBuildAgentPaneConfig_NoNotifChannelWhenOperatorPassesSettings is the
// end-to-end form of the measured composition rule: a second --settings would
// replace the operator's blob wholesale rather than merging with it, so
// initech must stand down when their claude_args already carry one.
func TestBuildAgentPaneConfig_NoNotifChannelWhenOperatorPassesSettings(t *testing.T) {
	proj := notifWiringProject(t)
	proj.ClaudeArgs = []string{"--settings", `{"permissions":{"allow":["Bash"]}}`}

	cfg, err := buildAgentPaneConfig("claude-agent", proj)
	if err != nil {
		t.Fatalf("claude-agent: %v", err)
	}
	if n := strings.Count(strings.Join(cfg.Command, " "), "--settings"); n != 1 {
		t.Errorf("expected the operator's single --settings to survive alone, found %d: %q",
			n, cfg.Command)
	}
}
