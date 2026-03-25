// Package config owns the initech.yaml schema. It reads, parses, validates,
// and exposes the Project type that all other packages consume.
//
// Config discovery order: explicit --config flag, ./initech.yaml in the
// current directory, then walk upward to find initech.yaml (like .git
// discovery). The first match wins.
//
// This package does not know about tmux, git, scaffold, or roles. It only
// knows how to turn a YAML file into a validated Go struct.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Project is the top-level config read from initech.yaml.
type Project struct {
	Name          string                  `yaml:"project"`
	Root          string                  `yaml:"root"`
	Repos         []Repo                  `yaml:"repos,omitempty"`
	Env           map[string]string       `yaml:"env,omitempty"`
	Beads         BeadsConfig             `yaml:"beads,omitempty"`
	Roles         []string                `yaml:"roles"`
	Grid          []string                `yaml:"grid,omitempty"`
	ClaudeArgs    []string                `yaml:"claude_args,omitempty"`
	RoleOverrides map[string]RoleOverride `yaml:"role_overrides,omitempty"`
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

// RoleOverride lets a project customize per-role settings beyond catalog defaults.
type RoleOverride struct {
	TechStack  string   `yaml:"tech_stack,omitempty"`
	BuildCmd   string   `yaml:"build_cmd,omitempty"`
	TestCmd    string   `yaml:"test_cmd,omitempty"`
	Dir        string   `yaml:"dir,omitempty"`
	RepoName   string   `yaml:"repo_name,omitempty"`
	ClaudeArgs []string `yaml:"claude_args,omitempty"`
}

// Load reads and parses an initech.yaml file from the given path.
// It expands ~ in the root field to the user's home directory.
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

	return &p, nil
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

	return nil
}

// Write serializes a Project to YAML and writes it to the given path.
func Write(path string, p *Project) error {
	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0644)
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
