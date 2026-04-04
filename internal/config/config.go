// Package config owns the initech.yaml schema. It reads, parses, validates,
// and exposes the Project type that all other packages consume.
//
// Config discovery order: explicit --config flag, ./initech.yaml in the
// current directory, then walk upward to find initech.yaml (like .git
// discovery). The first match wins.
//
// This package does not know about git, scaffold, or roles. It only knows
// how to turn a YAML file into a validated Go struct.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// roleNameRe restricts role names to letters, digits, hyphens, and underscores.
// Spaces, slashes, dots, and all other characters break IPC target parsing,
// filesystem paths, and CLI argument splitting.
var roleNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// peerNameRe restricts peer names to letters, digits, and hyphens. No colons
// (colon is the host:agent separator in cross-machine addressing), no
// underscores (distinguish from role names at a glance).
var peerNameRe = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

// Project is the top-level config read from initech.yaml.
type Project struct {
	Name          string                  `yaml:"project"`
	Root          string                  `yaml:"root"`
	Repos         []Repo                  `yaml:"repos,omitempty"`
	Beads         BeadsConfig             `yaml:"beads,omitempty"`
	Resource      ResourceConfig          `yaml:"resource,omitempty"`
	Roles         []string                `yaml:"roles"`
	ClaudeCommand []string                `yaml:"claude_command,omitempty"`
	ClaudeArgs    []string                `yaml:"claude_args,omitempty"`
	RoleOverrides map[string]RoleOverride `yaml:"role_overrides,omitempty"`

	// Web companion server fields.
	WebPort    *int   `yaml:"web_port,omitempty"`    // Web companion port. nil/0 = disabled, >0 = enabled.
	WebhookURL string `yaml:"webhook_url,omitempty"` // HTTP endpoint for agent event POSTs. Empty = disabled.

	// MCP server fields.
	McpPort  *int   `yaml:"mcp_port,omitempty"`  // MCP server port. Default 9200, nil uses default, 0 disables.
	McpToken string `yaml:"mcp_token,omitempty"` // Bearer token. Auto-generated if empty. INITECH_MCP_TOKEN env var overrides.
	McpBind  string `yaml:"mcp_bind,omitempty"`  // Bind address. Default "0.0.0.0".

	// Cross-machine coordination fields.
	PeerName string            `yaml:"peer_name,omitempty"` // This instance's identity (e.g., "workbench").
	Mode     string            `yaml:"mode,omitempty"`      // "" (default TUI) or "headless" (daemon).
	Listen   string            `yaml:"listen,omitempty"`    // TCP listen addr for headless mode. Defaults to 127.0.0.1 if only port given (e.g., ":7391" becomes "127.0.0.1:7391"). Use "0.0.0.0:port" to bind all interfaces.
	Token    string            `yaml:"token,omitempty"`     // Shared auth token.
	Remotes  map[string]Remote `yaml:"remotes,omitempty"`   // Named remote peers.
}

// Remote describes a remote initech peer for cross-machine coordination.
type Remote struct {
	Addr  string `yaml:"addr"`            // host:port of the remote peer.
	Token string `yaml:"token,omitempty"` // Auth token for this remote (overrides project-level token).
}

// Repo is a code repository that agents get as a git submodule.
type Repo struct {
	URL  string `yaml:"url"`
	Name string `yaml:"name"`
}

// BeadsConfig holds beads issue tracker settings.
type BeadsConfig struct {
	Enabled *bool  `yaml:"enabled,omitempty"` // nil = legacy (treat as enabled), false = disabled
	Prefix  string `yaml:"prefix,omitempty"`
}

// IsEnabled returns whether beads integration is active. Nil (legacy configs
// without the field) is treated as enabled for backwards compatibility.
func (b BeadsConfig) IsEnabled() bool {
	return b.Enabled == nil || *b.Enabled
}

// ResourceConfig holds resource management settings. When AutoSuspend is true,
// the TUI runs a memory monitor and can auto-suspend/resume agents under
// memory pressure. When absent or false, all resource management is dormant.
type ResourceConfig struct {
	AutoSuspend       bool `yaml:"auto_suspend,omitempty"`
	PressureThreshold int  `yaml:"pressure_threshold,omitempty"` // RSS percentage (0-100). Default: 85.
}

// DefaultPressureThreshold is the RSS percentage above which agents may be
// auto-suspended. Used when PressureThreshold is zero (unset).
const DefaultPressureThreshold = 85

// EffectiveWebPort returns the web companion port from config. Returns 0
// (disabled) when web_port is not set, or the explicit value when set.
func (p *Project) EffectiveWebPort() int {
	if p.WebPort == nil {
		return 0
	}
	return *p.WebPort
}

// DefaultMcpBind is the default bind address for the MCP server.
const DefaultMcpBind = "0.0.0.0"

// EffectiveMcpPort returns the MCP server port from config. Returns 0 (disabled)
// when mcp_port is not set, or the explicit value when set.
func (p *Project) EffectiveMcpPort() int {
	if p.McpPort == nil {
		return 0
	}
	return *p.McpPort
}

// EffectiveMcpToken returns the MCP bearer token, checking the
// INITECH_MCP_TOKEN environment variable first, then the config value.
// Returns empty string if neither is set (caller should auto-generate).
func (p *Project) EffectiveMcpToken() string {
	if env := os.Getenv("INITECH_MCP_TOKEN"); env != "" {
		return env
	}
	return p.McpToken
}

// EffectiveMcpBind returns the MCP bind address, defaulting to "0.0.0.0".
func (p *Project) EffectiveMcpBind() string {
	if p.McpBind != "" {
		return p.McpBind
	}
	return DefaultMcpBind
}

const (
	// AgentTypeClaudeCode is the default agent type. Claude Code supports
	// bracketed paste, so it keeps the existing paste-based injection path.
	AgentTypeClaudeCode = "claude-code"
	// AgentTypeCodex uses typed injection and Enter submit by default.
	AgentTypeCodex = "codex"
	// AgentTypeOpenCode uses the same raw-input defaults and readiness handling
	// as Codex.
	AgentTypeOpenCode = "opencode"
	// AgentTypeGeneric is a non-Claude agent with conservative typed-input defaults.
	AgentTypeGeneric = "generic"
)

// RoleOverride lets a project customize per-role settings beyond catalog defaults.
type RoleOverride struct {
	TechStack        string   `yaml:"tech_stack,omitempty"`
	BuildCmd         string   `yaml:"build_cmd,omitempty"`
	TestCmd          string   `yaml:"test_cmd,omitempty"`
	Dir              string   `yaml:"dir,omitempty"`
	RepoName         string   `yaml:"repo_name,omitempty"`
	AgentType        string   `yaml:"agent_type,omitempty"` // "claude-code" (default), "codex", "opencode", or "generic".
	Command          []string `yaml:"command,omitempty"`    // Override the agent command entirely (e.g. ["codex"]).
	ClaudeArgs       []string `yaml:"claude_args,omitempty"`
	AutoApprove      *bool    `yaml:"auto_approve,omitempty"`       // When true, auto-approve matching permission prompts.
	NoBracketedPaste bool     `yaml:"no_bracketed_paste,omitempty"` // When true, use the non-bracketed injection path.
	SubmitKey        string   `yaml:"submit_key,omitempty"`         // "enter" (default) or "ctrl+enter".
}

// NormalizeAgentType returns the effective agent type, defaulting to
// claude-code when the config omits it.
func NormalizeAgentType(agentType string) string {
	if agentType == "" {
		return AgentTypeClaudeCode
	}
	return agentType
}

// ValidAgentType reports whether agentType is one of the supported config values.
func ValidAgentType(agentType string) bool {
	switch NormalizeAgentType(agentType) {
	case AgentTypeClaudeCode, AgentTypeCodex, AgentTypeOpenCode, AgentTypeGeneric:
		return true
	default:
		return false
	}
}

// IsCodexLikeAgentType reports whether agentType should use the Codex/OpenCode
// readiness and raw-send flow.
func IsCodexLikeAgentType(agentType string) bool {
	switch NormalizeAgentType(agentType) {
	case AgentTypeCodex, AgentTypeOpenCode:
		return true
	default:
		return false
	}
}

// DefaultNoBracketedPaste returns the agent-type default for text injection.
// Only Claude Code keeps bracketed paste enabled by default.
func DefaultNoBracketedPaste(agentType string) bool {
	switch NormalizeAgentType(agentType) {
	case AgentTypeClaudeCode:
		return false
	default:
		return true
	}
}

// DefaultSubmitKey returns the submit key implied by the agent type.
func DefaultSubmitKey(agentType string) string {
	switch NormalizeAgentType(agentType) {
	case AgentTypeCodex, AgentTypeOpenCode, AgentTypeGeneric:
		return "enter"
	default:
		return ""
	}
}

// DefaultAutoApprove returns the agent-type default for permission prompt
// auto-approval. Codex and OpenCode default on; all other agent types default
// off.
func DefaultAutoApprove(agentType string) bool {
	switch NormalizeAgentType(agentType) {
	case AgentTypeCodex, AgentTypeOpenCode:
		return true
	default:
		return false
	}
}

// Load reads, parses, and validates an initech.yaml file from the given path.
// It expands ~ in the root field to the user's home directory.
// If the config contains auth tokens and the file is group/world readable,
// a warning is printed to stderr.
func Load(path string) (*Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var p Project
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	p.Root = expandHome(p.Root)

	if err := Validate(&p); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Warn if config contains tokens and file is group/world readable.
	if hasTokens(&p) {
		if info, err := os.Stat(path); err == nil {
			if perm := info.Mode().Perm(); perm&0077 != 0 {
				fmt.Fprintf(os.Stderr, "Warning: %s contains auth tokens but has permissions %o (should be 0600). Fix with: chmod 600 %s\n", path, perm, path)
			}
		}
	}

	return &p, nil
}

// hasTokens returns true if the project config contains any auth tokens.
func hasTokens(p *Project) bool {
	if p.Token != "" || p.McpToken != "" {
		return true
	}
	for _, r := range p.Remotes {
		if r.Token != "" {
			return true
		}
	}
	return false
}

// Discover finds an initech.yaml file by walking upward from dir.
// Returns the absolute path to the config file, or an error if none is found.
func Discover(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	for {
		candidate := filepath.Join(dir, "initech.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("initech.yaml not found (searched upward from %s)", dir)
		}
		dir = parent
	}
}

// Validate checks that the project config is internally consistent.
// Returns nil if valid, or an error describing the first problem found.
func Validate(p *Project) error {
	if p.Name == "" {
		return fmt.Errorf("project name is required")
	}
	if p.Root == "" {
		return fmt.Errorf("root path is required")
	}
	if len(p.Roles) == 0 {
		return fmt.Errorf("at least one role is required")
	}

	roleSet := make(map[string]bool, len(p.Roles))
	for _, r := range p.Roles {
		if r == "" {
			return fmt.Errorf("role name must not be empty")
		}
		if !roleNameRe.MatchString(r) {
			return fmt.Errorf("invalid role name %q: must contain only letters, digits, hyphens, or underscores", r)
		}
		if roleSet[r] {
			return fmt.Errorf("duplicate role: %s", r)
		}
		roleSet[r] = true
	}

	for name, ov := range p.RoleOverrides {
		if !roleSet[name] {
			return fmt.Errorf("role_override %q is not in roles list", name)
		}
		if !ValidAgentType(ov.AgentType) {
			return fmt.Errorf("role_override %q has invalid agent_type %q: must be %q, %q, %q, or %q", name, ov.AgentType, AgentTypeClaudeCode, AgentTypeCodex, AgentTypeOpenCode, AgentTypeGeneric)
		}
		if ov.SubmitKey != "" && ov.SubmitKey != "enter" && ov.SubmitKey != "ctrl+enter" {
			return fmt.Errorf("role_override %q has invalid submit_key %q: must be \"enter\" or \"ctrl+enter\"", name, ov.SubmitKey)
		}
	}

	// MCP server validation.
	if p.WebPort != nil && (*p.WebPort < 0 || *p.WebPort > 65535) {
		return fmt.Errorf("web_port %d out of range (0-65535)", *p.WebPort)
	}
	if p.McpPort != nil && (*p.McpPort < 0 || *p.McpPort > 65535) {
		return fmt.Errorf("mcp_port %d out of range (0-65535)", *p.McpPort)
	}

	// Cross-machine coordination validation.
	if p.Mode != "" && p.Mode != "headless" {
		return fmt.Errorf("invalid mode %q: must be \"\" or \"headless\"", p.Mode)
	}
	if p.Mode == "headless" {
		if p.Listen == "" {
			return fmt.Errorf("listen address is required in headless mode")
		}
		if p.PeerName == "" {
			return fmt.Errorf("peer_name is required in headless mode")
		}
		if p.Token == "" {
			return fmt.Errorf("token is required in headless mode (unauthenticated daemon exposes all agent PTYs)")
		}
	}
	// Normalize listen address: ":port" binds all interfaces which is a
	// security risk. Default to loopback (127.0.0.1) when only a port is given.
	if p.Listen != "" && p.Listen[0] == ':' {
		p.Listen = "127.0.0.1" + p.Listen
	}
	if p.PeerName != "" && !peerNameRe.MatchString(p.PeerName) {
		return fmt.Errorf("invalid peer_name %q: must contain only letters, digits, or hyphens (no colons)", p.PeerName)
	}
	for name, remote := range p.Remotes {
		if remote.Addr == "" {
			return fmt.Errorf("remote %q has empty addr", name)
		}
	}

	return nil
}

// Write serializes a Project to YAML and writes it to the given path.
func Write(path string, p *Project) error {
	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	out := addYAMLComments(string(data))
	return os.WriteFile(path, []byte(out), 0600)
}

// addYAMLComments injects helpful comments after specific fields in the
// marshaled YAML. Go's yaml.Marshal doesn't support comments natively.
func addYAMLComments(yamlStr string) string {
	lines := strings.Split(yamlStr, "\n")
	var result []string
	for _, line := range lines {
		result = append(result, line)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "listen:") {
			result = append(result, "# use 0.0.0.0:PORT to accept remote connections (default: localhost only)")
		}
	}
	return strings.Join(result, "\n")
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
