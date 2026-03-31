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
	Env           map[string]string       `yaml:"env,omitempty"`
	Beads         BeadsConfig             `yaml:"beads,omitempty"`
	Resource      ResourceConfig          `yaml:"resource,omitempty"`
	Roles         []string                `yaml:"roles"`
	Grid          []string                `yaml:"grid,omitempty"`
	ClaudeCommand []string                `yaml:"claude_command,omitempty"`
	ClaudeArgs    []string                `yaml:"claude_args,omitempty"`
	RoleOverrides map[string]RoleOverride `yaml:"role_overrides,omitempty"`

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
	Prefix string `yaml:"prefix,omitempty"`
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

// EffectivePressureThreshold returns the pressure threshold to use, applying
// the default when the configured value is zero.
func (rc ResourceConfig) EffectivePressureThreshold() int {
	if rc.PressureThreshold > 0 {
		return rc.PressureThreshold
	}
	return DefaultPressureThreshold
}

// RoleOverride lets a project customize per-role settings beyond catalog defaults.
type RoleOverride struct {
	TechStack  string   `yaml:"tech_stack,omitempty"`
	BuildCmd   string   `yaml:"build_cmd,omitempty"`
	TestCmd    string   `yaml:"test_cmd,omitempty"`
	Dir        string   `yaml:"dir,omitempty"`
	RepoName   string   `yaml:"repo_name,omitempty"`
	ClaudeArgs []string `yaml:"claude_args,omitempty"`
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
	if p.Token != "" {
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

	for _, g := range p.Grid {
		if !roleSet[g] {
			return fmt.Errorf("grid role %q is not in roles list", g)
		}
	}

	for name := range p.RoleOverrides {
		if !roleSet[name] {
			return fmt.Errorf("role_override %q is not in roles list", name)
		}
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
