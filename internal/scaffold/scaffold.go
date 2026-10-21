// Package scaffold creates the project directory tree on disk from an initech
// config. It writes CLAUDE.md files for each role, root CLAUDE.md, AGENTS.md,
// .gitignore, and the four project documents (prd, spec, systemdesign, roadmap).
//
// All file writes are idempotent: existing files are not overwritten unless
// force is true. This lets users safely re-run initech init without losing
// their customizations.
//
// This package does not know about git or beads. It only creates directories
// and writes files.
package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
)

// Options controls scaffold behavior.
type Options struct {
	// Force overwrites existing files when true.
	Force bool
	// Progress is called with a short description for each created item.
	// Nil disables progress output.
	Progress func(msg string)
}

// Run creates the full project directory tree from the given config.
// Returns a list of paths that were created (for summary output).
func Run(p *config.Project, opts Options) ([]string, error) {
	var created []string
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {} // no-op
	}

	// Root directory
	if err := os.MkdirAll(p.Root, 0755); err != nil {
		return nil, fmt.Errorf("create root: %w", err)
	}

	// .gitignore
	if path, err := writeIfAbsent(p.Root, ".gitignore", gitignoreContent); err != nil {
		return nil, err
	} else if path != "" {
		progress("Creating .gitignore")
		created = append(created, ".gitignore")
	}

	// Root CLAUDE.md
	claudeContent := RenderRootCLAUDE(p)
	if path, err := writeFile(p.Root, "CLAUDE.md", claudeContent, opts.Force); err != nil {
		return nil, err
	} else if path != "" {
		progress("Creating CLAUDE.md")
		created = append(created, "CLAUDE.md")
	}

	// AGENTS.md
	if path, err := writeIfAbsent(p.Root, "AGENTS.md", agentsContent); err != nil {
		return nil, err
	} else if path != "" {
		progress("Creating AGENTS.md")
		created = append(created, "AGENTS.md")
	}

	// docs/ directory with four project documents
	docsDir := filepath.Join(p.Root, "docs")
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		return nil, fmt.Errorf("create docs/: %w", err)
	}
	progress("Creating docs/")

	vars := roles.RenderVars{
		ProjectName: p.Name,
		ProjectRoot: p.Root,
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
		if path, err := writeIfAbsent(docsDir, dt.filename, content); err != nil {
			return nil, err
		} else if path != "" {
			progress("Creating docs/" + dt.filename)
			created = append(created, "docs/"+dt.filename)
		}
	}

	// Fleet files (ini-vby5.13). They hold the project's own history and
	// procedures, so they are written only when missing: init --force never
	// replaces them.
	for _, ff := range []struct{ dir, name, content string }{
		{"fleet", "lessons.md", roles.FleetLessonsTemplate},
		{"fleet", "reference.md", roles.FleetReferenceTemplate},
		{"skills", "README.md", roles.SkillsReadmeTemplate},
	} {
		dir := filepath.Join(p.Root, ff.dir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create %s/: %w", ff.dir, err)
		}
		if path, err := writeIfAbsent(dir, ff.name, ff.content); err != nil {
			return nil, err
		} else if path != "" {
			rel := ff.dir + "/" + ff.name
			progress("Creating " + rel)
			created = append(created, rel)
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

		// CLAUDE.md from template. RenderVarsFor is shared with the hire path
		// and the remote push builder so all three resolve identically
		// (ini-rg12).
		content := RenderCharter(p, p.Root, roleName)

		if path, err := writeFile(roleDir, "CLAUDE.md", content, opts.Force); err != nil {
			return nil, err
		} else if path != "" {
			if def.NeedsSrc {
				progress(fmt.Sprintf("Creating %s/CLAUDE.md + .claude/", roleName))
			} else {
				progress(fmt.Sprintf("Creating %s/CLAUDE.md", roleName))
			}
			created = append(created, roleName+"/CLAUDE.md")
		}

		// NeedsSrc directories are created by git submodule add, not scaffold.
		// Creating them here would cause submodule add to fail with
		// "already exists and is not a valid git repo".

		if def.NeedsPlaybooks {
			pbDir := filepath.Join(roleDir, "playbooks")
			if err := os.MkdirAll(pbDir, 0755); err != nil {
				return nil, fmt.Errorf("create %s/playbooks/: %w", roleName, err)
			}
			progress(fmt.Sprintf("Creating %s/playbooks/", roleName))
		}
	}

	return created, nil
}

// templatesByName maps a template name to its CLAUDE.md template. The name is
// what a charter's generated header cites (ini-vby5.1).
var templatesByName = map[string]string{
	"super":   roles.SuperTemplate,
	"pm":      roles.PMTemplate,
	"arch":    roles.ArchTemplate,
	"sec":     roles.SecTemplate,
	"shipper": roles.ShipperTemplate,
	"pmm":     roles.PMMTemplate,
	"writer":  roles.WriterTemplate,
	"ops":     roles.OpsTemplate,
	"growth":  roles.GrowthTemplate,
	"intern":  roles.InternTemplate,
	"eng":     roles.EngTemplate,
	"qa":      roles.QATemplate,
}

// TemplateNameForRole returns the name of the template a role renders from.
// Numbered variants share one template (eng1, eng2 -> eng; qa1 -> qa);
// unknown roles fall back to eng.
func TemplateNameForRole(name string) string {
	if _, ok := templatesByName[name]; ok && name != "eng" && name != "qa" {
		return name
	}
	if strings.HasPrefix(name, "qa") {
		return "qa"
	}
	return "eng"
}

// TemplateForRole returns the appropriate CLAUDE.md template for a role.
// Matches against known role prefixes (eng1, eng2 both match eng).
// Falls back to EngTemplate for unknown roles.
func TemplateForRole(name string) string {
	return templatesByName[TemplateNameForRole(name)]
}

// RenderCharter renders a role's CLAUDE.md: the generated header (template
// name and body hash) followed by the role template rendered for this
// project. Scaffold, add-agent --regen and the remote push builder all render
// through here, so the three cannot drift (ini-vby5.1, ini-rg12).
func RenderCharter(p *config.Project, root, roleName string) string {
	body := roles.Render(TemplateForRole(roleName), RenderVarsFor(p, root, roleName))
	body = roles.RenderString(body, "role_name", roleName)
	body = roles.ApplyRoleConditions(body, p.Roles)
	return roles.CharterHeader(TemplateNameForRole(roleName), roleName, body) + body
}

// WriteCharter renders and writes roleName's CLAUDE.md, replacing any file
// already there. The caller decides whether replacing is allowed (add-agent
// --regen checks the header hash first). Goes through writeFile so the
// unrendered-placeholder refusal applies here too.
func WriteCharter(p *config.Project, roleName string) (string, error) {
	roleDir := filepath.Join(p.Root, roleName)
	if err := os.MkdirAll(roleDir, 0755); err != nil {
		return "", fmt.Errorf("create %s/: %w", roleName, err)
	}
	return writeFile(roleDir, "CLAUDE.md", RenderCharter(p, p.Root, roleName), true)
}

// writeIfAbsent writes a file only when it does not exist yet, force or not.
// It is for every file the project owns once created: the product docs,
// AGENTS.md, .gitignore and the fleet and skills files (ini-0h7u). init
// --force rewrites exactly the charters the operator guide names, the root
// CLAUDE.md and each agent's CLAUDE.md; those are the only callers of
// writeFile with opts.Force. Routing the rest through this helper keeps a
// new file from inheriting --force by copying the nearest writeFile call.
func writeIfAbsent(dir, name, content string) (string, error) {
	return writeFile(dir, name, content, false)
}

// writeFile writes content to a file, respecting idempotency.
// Returns the full path if written, empty string if skipped.
//
// Content that still holds a "{{" placeholder is REFUSED, not warned about
// (ini-rg12). Every file the scaffold writes is instructions an agent will read
// and act on, so a surviving placeholder is not cosmetic: an agent told its
// tech stack is "{{tech_stack}}" has been handed a false fact with no way to
// notice. The check sits here, at the single write chokepoint, rather than in
// each caller — that is what makes it total. A template variable added later
// and wired up nowhere fails the scaffold loudly instead of shipping holes into
// every agent in the fleet, which is how this bug survived long enough to be
// found by hand-auditing agent files.
func writeFile(dir, name, content string, force bool) (string, error) {
	path := filepath.Join(dir, name)
	if left := roles.UnrenderedPlaceholders(content); len(left) > 0 {
		return "", fmt.Errorf("refusing to write %s with unrendered template variables:\n  %s",
			name, strings.Join(left, "\n  "))
	}
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

// RenderRootCLAUDE renders the project-root CLAUDE.md content from the
// project config. Exported (ini-om0) so the remote-push builder can render
// the same content sent to a zero-config remote daemon as configure_agent's
// RootClaudeMD field, without duplicating this project-summary logic.
//
// The root is the fleet's shared rulebook (ini-vby5.1): every rule that
// applies to all roles lives here once, and each role's CLAUDE.md carries
// only its delta. Sections, in order: Project documents, Roles, then
// roles.RootSections(), one per shared rule.
func RenderRootCLAUDE(p *config.Project) string {
	var b strings.Builder
	b.WriteString("# " + p.Name + "\n\n")
	b.WriteString("This file applies to every agent in the fleet. Each agent's own CLAUDE.md adds only what that role does differently.\n\n")
	b.WriteString("## Project Documents\n\n")
	b.WriteString("| Document | Question | Contains |\n")
	b.WriteString("|----------|----------|----------|\n")
	b.WriteString("| `docs/prd.md` | **Why** does this exist? | Problem statement, users, success criteria, journeys |\n")
	b.WriteString("| `docs/spec.md` | **What** does this do? | Requirements, behaviors, acceptance criteria |\n")
	b.WriteString("| `docs/systemdesign.md` | **How** does this work? | Architecture, packages, interfaces, build order |\n")
	b.WriteString("| `docs/roadmap.md` | **When/Who** does what get built? | Phases, milestones, gates, agent allocation |\n")
	b.WriteString("| `fleet/lessons.md` | **Why** is a rule there? | The incident behind each rule |\n")
	b.WriteString("| `fleet/reference.md` | **Where** is it? | Commands, QA pairs, who owns which area |\n")
	b.WriteString("| `skills/` | **How** is a repeated step done? | One skill per repeated procedure |\n")
	b.WriteString("\n")
	b.WriteString("## Roles\n\n")
	b.WriteString(roles.RenderRoleBlock("roles-list", p.Roles) + "\n\n")
	for _, sec := range roles.RootSections(p.Roles) {
		b.WriteString(roles.RenderString(sec, "qa_pairs_table", roles.RenderQAPairsBlock(p.Roles)))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// ErrNoRootRoleBlock means the root CLAUDE.md lacks at least one well-formed
// role block (one begin marker, then one end marker): it was written by hand
// or before the block existed. Blocks that are well formed are still
// refreshed; the rest of the file is left as it is.
var ErrNoRootRoleBlock = errors.New("root CLAUDE.md has no generated roles block")

// RefreshRootRoleBlocks re-renders every named role block of
// <p.Root>/CLAUDE.md for p's current roles: the sentences that name sec and
// arch only when the fleet has them (ini-vby5.3). Only the text between each
// block's markers changes; everything outside them is the project's own and
// is kept byte for byte. A missing root is not an error (nothing to refresh);
// a block that is missing or malformed is skipped and reported as
// ErrNoRootRoleBlock. Reports whether it rewrote the file. add-agent and
// delete-agent call it after the roles list changes.
func RefreshRootRoleBlocks(p *config.Project) (bool, error) {
	path := filepath.Join(p.Root, "CLAUDE.md")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	old := string(data)
	updated := old
	var missing error
	for _, name := range roles.RoleBlockNames {
		begin, end := roles.RoleBlockBegin(name), roles.RoleBlockEnd(name)
		i, j := strings.Index(updated, begin), strings.Index(updated, end)
		if i < 0 || j < i || strings.Count(updated, begin) != 1 || strings.Count(updated, end) != 1 {
			missing = ErrNoRootRoleBlock
			continue
		}
		updated = updated[:i] + roles.RenderRoleBlock(name, p.Roles) + updated[j+len(end):]
	}
	if updated == old {
		return false, missing
	}
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, missing
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

# Initech runtime state (machine-specific layout, not shared)
.initech/

# Local agent config
*/.mcp.json

# PTY recordings (initech --record): raw agent output, never committed
*.irec

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
initech send <agent> 'message'    # Send message to an agent
initech peek <agent>              # Read agent terminal output
` + "```" + `
`
