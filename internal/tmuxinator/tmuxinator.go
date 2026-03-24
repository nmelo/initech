// Package tmuxinator generates tmuxinator YAML configs from initech project
// configs. It produces both main session YAML and grid (monitoring) session
// YAML as byte slices.
//
// This package does not write files to disk (scaffold handles that) and does
// not interact with tmux at runtime. It only generates YAML content.
package tmuxinator

import (
	"fmt"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
	"gopkg.in/yaml.v3"
)

// session is the tmuxinator YAML structure.
type session struct {
	Name      string            `yaml:"name"`
	Root      string            `yaml:"root"`
	PreWindow string            `yaml:"pre_window,omitempty"`
	Windows   []map[string]any  `yaml:"windows"`
}

// Generate produces the main tmuxinator session YAML.
// Each role gets a window running Claude with the appropriate permission level.
func Generate(p *config.Project) ([]byte, error) {
	s := session{
		Name: p.Name,
		Root: p.Root,
	}

	// Set BEADS_DIR if beads prefix is configured
	if p.Beads.Prefix != "" {
		s.PreWindow = fmt.Sprintf("export BEADS_DIR=%s/.beads", p.Root)
	}

	for _, roleName := range p.Roles {
		def := roles.LookupRole(roleName)
		cmd := claudeCommand(def)
		window := map[string]any{
			roleName: cmd,
		}
		s.Windows = append(s.Windows, window)
	}

	return yaml.Marshal(s)
}

// GenerateGrid produces a grid (monitoring) session YAML.
// Uses tmux's "tiled" layout so all windows are visible simultaneously.
// Only includes roles listed in the project's Grid config.
func GenerateGrid(p *config.Project) ([]byte, error) {
	if len(p.Grid) == 0 {
		return nil, nil
	}

	s := session{
		Name: p.Name + "-grid",
		Root: p.Root,
	}

	if p.Beads.Prefix != "" {
		s.PreWindow = fmt.Sprintf("export BEADS_DIR=%s/.beads", p.Root)
	}

	for _, roleName := range p.Grid {
		def := roles.LookupRole(roleName)
		cmd := claudeCommand(def)
		window := map[string]any{
			roleName: cmd,
		}
		s.Windows = append(s.Windows, window)
	}

	return yaml.Marshal(s)
}

func claudeCommand(def roles.RoleDef) string {
	if def.Permission == roles.Autonomous {
		return "claude --continue --dangerously-skip-permissions"
	}
	return "claude --continue"
}
