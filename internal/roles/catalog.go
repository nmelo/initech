// Package roles owns role definitions, templates, and template rendering for
// initech projects. It provides the catalog of well-known roles, the template
// rendering engine, and the inline template constants.
//
// The catalog is an open set: unknown role names are valid and receive sensible
// defaults. This lets users define custom roles (e.g., "designer", "dba")
// without modifying initech's source code.
//
// This package does not know about files on disk, config parsing, or the TUI.
package roles

import "strings"

// PermissionTier controls whether an agent runs with --dangerously-skip-permissions.
type PermissionTier int

const (
	// Supervised agents require manual approval for tool use.
	// Used for super (coordinator) and shipper (release).
	Supervised PermissionTier = iota

	// Autonomous agents run with --dangerously-skip-permissions.
	// Used for eng, qa, pm, arch, and most other roles.
	Autonomous
)

// RoleDef describes a role's metadata that drives scaffold and TUI configuration.
type RoleDef struct {
	Name           string
	Permission     PermissionTier
	NeedsSrc       bool
	NeedsPlaybooks bool
}

// Catalog maps well-known role names to their definitions.
// This is not a closed set; roles not in the catalog get defaults via LookupRole.
var Catalog = map[string]RoleDef{
	"super":   {Name: "super", Permission: Autonomous},
	"eng1":    {Name: "eng1", Permission: Autonomous, NeedsSrc: true},
	"eng2":    {Name: "eng2", Permission: Autonomous, NeedsSrc: true},
	"eng3":    {Name: "eng3", Permission: Autonomous, NeedsSrc: true},
	"qa1":     {Name: "qa1", Permission: Autonomous, NeedsSrc: true, NeedsPlaybooks: true},
	"qa2":     {Name: "qa2", Permission: Autonomous, NeedsSrc: true, NeedsPlaybooks: true},
	"shipper": {Name: "shipper", Permission: Autonomous, NeedsSrc: true, NeedsPlaybooks: true},
	"pm":      {Name: "pm", Permission: Autonomous},
	"pmm":     {Name: "pmm", Permission: Autonomous},
	"arch":    {Name: "arch", Permission: Autonomous},
	"sec":     {Name: "sec", Permission: Autonomous},
	"writer":  {Name: "writer", Permission: Autonomous},
	"ops":     {Name: "ops", Permission: Autonomous, NeedsPlaybooks: true},
	"growth":  {Name: "growth", Permission: Autonomous, NeedsSrc: true},
}

// LookupRole returns the RoleDef for a role name. Known roles return their
// catalog entry. Unknown roles return a default: Autonomous, no src, no
// playbooks, no makefile. This keeps the catalog open for custom roles.
func LookupRole(name string) RoleDef {
	if def, ok := Catalog[name]; ok {
		return def
	}
	return RoleDef{
		Name:       name,
		Permission: Autonomous,
	}
}

// RoleFamily groups roles that share notification and lifecycle semantics.
// Used by initech deliver to pick the right announce template per caller, and
// by status-transition logic to decide which lifecycle move (if any) to make.
//
// The set is intentionally small: Eng and QA are the two roles whose deliver
// behavior diverges materially today. Everything else collapses into Other,
// which gets a generic "delivered" message and no status transition. Unknown
// captures the empty/unrecognized agent case so callers can fail loudly
// instead of guessing.
type RoleFamily string

const (
	FamilyEng     RoleFamily = "eng"
	FamilyQA      RoleFamily = "qa"
	FamilyOther   RoleFamily = "other"
	FamilyUnknown RoleFamily = "unknown"
)

// RoleFamilyOf maps an agent name to its RoleFamily. Detection prefers prefix
// matching for the eng/qa families (so eng1, eng2, qa3 all resolve correctly
// without catalog updates), then falls back to exact-match against the open
// catalog of known role names. Names that match neither pattern return
// FamilyUnknown so callers can reject them rather than silently defaulting to
// the engineer template.
func RoleFamilyOf(name string) RoleFamily {
	if name == "" {
		return FamilyUnknown
	}
	switch {
	case strings.HasPrefix(name, "qa"):
		return FamilyQA
	case strings.HasPrefix(name, "eng"):
		return FamilyEng
	}
	switch name {
	case "super", "shipper", "pm", "pmm", "arch", "sec", "writer", "ops", "growth", "intern":
		return FamilyOther
	}
	return FamilyUnknown
}

// ResolveClaudeArgs returns the claude flags for a role using the priority
// chain: per-role override > global > catalog default. When no config
// overrides are set, Autonomous roles get ["--dangerously-skip-permissions"]
// and Supervised roles get an empty slice.
func ResolveClaudeArgs(roleName string, globalArgs []string, roleArgs []string) []string {
	if roleArgs != nil {
		return roleArgs
	}
	if len(globalArgs) > 0 {
		return globalArgs
	}
	def := LookupRole(roleName)
	if def.Permission == Autonomous {
		return []string{"--dangerously-skip-permissions"}
	}
	return nil
}
