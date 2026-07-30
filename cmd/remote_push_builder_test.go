package cmd

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
)

// TestConfigureAgentBuilder_RegisteredAfterNormalStartup is THE regression
// test for ini-om0: defaultConfigureAgentBuilder was never registered by any
// cmd/ code, so pushRolesToPeer always used the placeholder builder (which
// always errors), so no client could ever establish ownership of a remote
// agent. Deliberately does NOT call tui.SetConfigureAgentBuilder itself --
// that would only prove the setter works, which is exactly what the two
// pre-existing tests in remote_push_test.go already did and precisely why
// this bug survived three months unnoticed. This asserts the builder is
// registered as a side effect of this package's own init() (remote_push_
// builder.go) having run, which happens automatically for every cmd/ binary
// and every test binary in this package -- i.e. "normal startup wiring."
func TestConfigureAgentBuilder_RegisteredAfterNormalStartup(t *testing.T) {
	if !tui.ConfigureAgentBuilderRegistered() {
		t.Fatal("no real configure_agent builder registered -- pushRolesToPeer would still be using the placeholder, so no remote agent could ever establish ownership")
	}
}

// TestBuildRemoteConfigureAgentCmd_UsesRemoteRootNotLocalRoot confirms the
// payload targets the remote's workspace root, not the local project root --
// the two are frequently different paths (e.g. local dev checkout vs
// /opt/initech/<project> on the remote host).
func TestBuildRemoteConfigureAgentCmd_UsesRemoteRootNotLocalRoot(t *testing.T) {
	proj := &config.Project{
		Name:  "test",
		Root:  "/Users/dev/local-checkout", // never used by the remote builder
		Roles: []string{"eng9"},
	}
	remote := config.Remote{Addr: "workbench:9000", Root: "/opt/initech/test"}

	cmd, err := buildRemoteConfigureAgentCmd("eng9", proj, remote)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Dir != "/opt/initech/test/eng9" {
		t.Errorf("Dir = %q, want /opt/initech/test/eng9 (remote root, not local project root)", cmd.Dir)
	}
}

// TestBuildRemoteConfigureAgentCmd_DefaultsRemoteRoot confirms EffectiveRoot's
// default ("/opt/initech/<project>") is used when remote.Root is unset.
func TestBuildRemoteConfigureAgentCmd_DefaultsRemoteRoot(t *testing.T) {
	proj := &config.Project{Name: "demo", Roles: []string{"eng9"}}
	cmd, err := buildRemoteConfigureAgentCmd("eng9", proj, config.Remote{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Dir != "/opt/initech/demo/eng9" {
		t.Errorf("Dir = %q, want /opt/initech/demo/eng9", cmd.Dir)
	}
}

// TestBuildRemoteConfigureAgentCmd_DoesNotRequireLocalDirectoryToExist
// confirms the remote builder does NOT stat any local path (unlike
// buildAgentPaneConfig, which errors if the local role directory is
// missing) -- the remote host's directory is created by the daemon itself
// from ClaudeMD/RootClaudeMD (writeWorkspace), and may not exist locally at
// all, or exist under a completely different path.
func TestBuildRemoteConfigureAgentCmd_DoesNotRequireLocalDirectoryToExist(t *testing.T) {
	proj := &config.Project{
		Name:  "test",
		Root:  t.TempDir(), // empty -- role subdirectory deliberately not created
		Roles: []string{"eng9"},
	}
	if _, err := buildRemoteConfigureAgentCmd("eng9", proj, config.Remote{}); err != nil {
		t.Errorf("unexpected error for a nonexistent local directory: %v", err)
	}
}

// TestBuildRemoteConfigureAgentCmd_RendersClaudeMDContent confirms the
// payload carries real, non-empty rendered CLAUDE.md content for both the
// role and the project root -- writeWorkspace (daemon side) writes these
// verbatim, so an empty or placeholder value here means the remote agent
// starts with no CLAUDE.md at all.
func TestBuildRemoteConfigureAgentCmd_RendersClaudeMDContent(t *testing.T) {
	proj := &config.Project{Name: "test", Roles: []string{"eng9"}}
	cmd, err := buildRemoteConfigureAgentCmd("eng9", proj, config.Remote{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.ClaudeMD == "" {
		t.Error("ClaudeMD is empty, want rendered role template content")
	}
	if !strings.Contains(cmd.ClaudeMD, "eng9") {
		t.Errorf("ClaudeMD = %q, want it to reference the role name", cmd.ClaudeMD)
	}
	if cmd.RootClaudeMD == "" {
		t.Error("RootClaudeMD is empty, want rendered project-root content")
	}
	if !strings.Contains(cmd.RootClaudeMD, "test") {
		t.Errorf("RootClaudeMD = %q, want it to reference the project name", cmd.RootClaudeMD)
	}
}

// TestBuildRemoteConfigureAgentCmd_RoleCommandOverride mirrors
// TestBuildAgentPaneConfig_RoleCommandOverride's local-builder coverage, to
// prove the remote builder resolves command/agent-type overrides the same
// way -- a remote-pushed codex agent must not silently become a claude one.
func TestBuildRemoteConfigureAgentCmd_RoleCommandOverride(t *testing.T) {
	proj := &config.Project{
		Name:       "test",
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

	cfg1, err := buildRemoteConfigureAgentCmd("claude-agent", proj, config.Remote{})
	if err != nil {
		t.Fatalf("claude-agent: %v", err)
	}
	if cfg1.Command[0] != "claude" {
		t.Errorf("claude-agent argv[0] = %q, want claude", cfg1.Command[0])
	}
	if !strings.Contains(strings.Join(cfg1.Command, " "), "--continue") {
		t.Errorf("claude-agent should have --continue: %v", cfg1.Command)
	}

	cfg2, err := buildRemoteConfigureAgentCmd("codex-agent", proj, config.Remote{})
	if err != nil {
		t.Fatalf("codex-agent: %v", err)
	}
	if cfg2.Command[0] != "codex" {
		t.Errorf("codex-agent argv[0] = %q, want codex", cfg2.Command[0])
	}
	if cfg2.AgentType != config.AgentTypeCodex {
		t.Errorf("codex-agent AgentType = %q, want %q", cfg2.AgentType, config.AgentTypeCodex)
	}
	if strings.Contains(strings.Join(cfg2.Command, " "), "--continue") {
		t.Errorf("codex-agent should NOT have claude_args appended: %v", cfg2.Command)
	}
}
