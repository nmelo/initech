// Package scaffold creates the project directory tree on disk from an initech
// config. It writes CLAUDE.md files for each role, root CLAUDE.md, AGENTS.md,
// .gitignore, and the four project documents (prd, spec, systemdesign, roadmap).
//
// All file writes are idempotent: existing files are not overwritten unless
// force is true. This lets users safely re-run initech init without losing
// their customizations.
//
// This package does not know about git, tmux, beads, or tmuxinator. It only
// creates directories and writes files.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
)

// Options controls scaffold behavior.
type Options struct {
	// Force overwrites existing files when true.
	Force bool
}

// Run creates the full project directory tree from the given config.
// Returns a list of paths that were created (for summary output).
func Run(p *config.Project, opts Options) ([]string, error) {
	var created []string

	// Root directory
	if err := os.MkdirAll(p.Root, 0755); err != nil {
		return nil, fmt.Errorf("create root: %w", err)
	}

	// .gitignore
	if path, err := writeFile(p.Root, ".gitignore", gitignoreContent, opts.Force); err != nil {
		return nil, err
	} else if path != "" {
		created = append(created, ".gitignore")
	}

	// Root CLAUDE.md
	claudeContent := renderRootCLAUDE(p)
	if path, err := writeFile(p.Root, "CLAUDE.md", claudeContent, opts.Force); err != nil {
		return nil, err
	} else if path != "" {
		created = append(created, "CLAUDE.md")
	}

	// AGENTS.md
	if path, err := writeFile(p.Root, "AGENTS.md", agentsContent, opts.Force); err != nil {
		return nil, err
	} else if path != "" {
		created = append(created, "AGENTS.md")
	}

	// docs/ directory with four project documents
	docsDir := filepath.Join(p.Root, "docs")
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		return nil, fmt.Errorf("create docs/: %w", err)
	}

	vars := roles.RenderVars{
		ProjectName: p.Name,
		ProjectRoot: p.Root,
		BeadsPrefix: p.Beads.Prefix,
	}

	docTemplates := []struct {
		filename string
		template string
	}{
		{"prd.md", roles.PRDTemplate},
		{"spec.md", roles.SpecTemplate},
		{"systemdesign.md", roles.SystemDesignTemplate},
		{"roadmap.md", roles.RoadmapTemplate},
	}

	for _, dt := range docTemplates {
		content := roles.Render(dt.template, vars)
		if path, err := writeFile(docsDir, dt.filename, content, opts.Force); err != nil {
			return nil, err
		} else if path != "" {
			created = append(created, "docs/"+dt.filename)
		}
	}

	// Role directories
	for _, roleName := range p.Roles {
		def := roles.LookupRole(roleName)
		roleDir := filepath.Join(p.Root, roleName)

		if err := os.MkdirAll(roleDir, 0755); err != nil {
			return nil, fmt.Errorf("create %s/: %w", roleName, err)
		}

		// .claude/ directory for agent-specific config
		claudeDir := filepath.Join(roleDir, ".claude")
		if err := os.MkdirAll(claudeDir, 0755); err != nil {
			return nil, fmt.Errorf("create %s/.claude/: %w", roleName, err)
		}

		// CLAUDE.md from template
		roleVars := vars
		if ov, ok := p.RoleOverrides[roleName]; ok {
			if ov.TechStack != "" {
				roleVars.TechStack = ov.TechStack
			}
			if ov.BuildCmd != "" {
				roleVars.BuildCmd = ov.BuildCmd
			}
			if ov.TestCmd != "" {
				roleVars.TestCmd = ov.TestCmd
			}
		}
		if len(p.Repos) > 0 {
			roleVars.RepoURL = p.Repos[0].URL
		}

		tmpl := templateForRole(roleName)
		content := roles.Render(tmpl, roleVars)
		// Substitute role_name (not in RenderVars, role-specific)
		content = roles.RenderString(content, "role_name", roleName)

		if path, err := writeFile(roleDir, "CLAUDE.md", content, opts.Force); err != nil {
			return nil, err
		} else if path != "" {
			created = append(created, roleName+"/CLAUDE.md")
		}

		// Optional directories based on role catalog
		if def.NeedsSrc {
			srcDir := filepath.Join(roleDir, "src")
			if err := os.MkdirAll(srcDir, 0755); err != nil {
				return nil, fmt.Errorf("create %s/src/: %w", roleName, err)
			}
		}

		if def.NeedsPlaybooks {
			pbDir := filepath.Join(roleDir, "playbooks")
			if err := os.MkdirAll(pbDir, 0755); err != nil {
				return nil, fmt.Errorf("create %s/playbooks/: %w", roleName, err)
			}
		}
	}

	return created, nil
}

// templateForRole returns the appropriate CLAUDE.md template for a role.
// Falls back to EngTemplate for roles without a specific template.
func templateForRole(name string) string {
	switch {
	case name == "super":
		return roles.SuperTemplate
	case name == "qa1" || name == "qa2":
		return roles.QATemplate
	default:
		return roles.EngTemplate
	}
}

// writeFile writes content to a file, respecting idempotency.
// Returns the full path if written, empty string if skipped.
func writeFile(dir, name, content string, force bool) (string, error) {
	path := filepath.Join(dir, name)
	if !force {
		if _, err := os.Stat(path); err == nil {
			return "", nil // file exists, skip
		}
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write %s: %w", name, err)
	}
	return path, nil
}

func renderRootCLAUDE(p *config.Project) string {
	var content string
	content += "# " + p.Name + "\n\n"
	content += "## Project Documents\n\n"
	content += "| Document | Question | Contains |\n"
	content += "|----------|----------|----------|\n"
	content += "| `docs/prd.md` | **Why** does this exist? | Problem statement, users, success criteria, journeys |\n"
	content += "| `docs/spec.md` | **What** does this do? | Requirements, behaviors, acceptance criteria |\n"
	content += "| `docs/systemdesign.md` | **How** does this work? | Architecture, packages, interfaces, build order |\n"
	content += "| `docs/roadmap.md` | **When/Who** does what get built? | Phases, milestones, gates, agent allocation |\n"
	content += "\n"
	content += "## Roles\n\n"
	for _, r := range p.Roles {
		content += "- `" + r + "/` - " + r + " agent directory\n"
	}
	content += "\n"
	content += "## Issue Tracking\n\n"
	content += "Uses beads (`bd` CLI). All work is tracked as beads.\n\n"
	content += "```bash\n"
	content += "bd ready            # See unblocked work\n"
	content += "bd list             # See all beads\n"
	content += "bd show <id>        # Bead details\n"
	content += "bd update <id> --status <status>  # Transition bead\n"
	content += "```\n\n"
	content += "## Communication\n\n"
	content += "```bash\n"
	content += "gn -w <agent> \"message\"   # Nudge an agent\n"
	content += "gp <agent>                # Peek at agent output\n"
	content += "```\n"
	return content
}

const gitignoreContent = `# Source code lives in submodules
node_modules/
.next/
target/
bin/

# Beads runtime (JSONL is tracked, DB is not)
.beads/beads.db
.beads/beads.db-wal
.beads/beads.db-shm
.beads/daemon*.log*

# Local agent config
*/.mcp.json

# OS artifacts
.DS_Store
`

const agentsContent = `# Agents Quick Reference

## Bead Commands

` + "```" + `bash
bd ready                              # See unblocked work
bd show <id>                          # Bead details
bd update <id> --status in_progress   # Claim a bead
bd update <id> --status ready_for_qa  # Submit for QA
bd comments add <id> "message"        # Add a comment
bd list                               # See all beads
` + "```" + `

## Landing the Plane (End of Session)

1. Commit all work in progress
2. Comment current state on any in-progress beads
3. Push all branches
4. Report status to super

## Communication

` + "```" + `bash
gn -w <agent> "message"    # Nudge an agent
gp <agent>                 # Peek at agent output
` + "```" + `
`
