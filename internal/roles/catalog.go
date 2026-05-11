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

import (
	"regexp"
	"strings"
)

// numberedRoleRe matches role names like eng1, eng7, qa1, qa10. Anchored on
// both ends to reject typos (qaa1, enginer), separators (qa-1, qa_1), and
// fractional/extra characters (qa1.5, eng99extra). Used by both IsValidRoleName
// (CLI gate) and LookupRole (default selection for unlisted family members).
var numberedRoleRe = regexp.MustCompile(`^(qa|eng)\d+$`)

// PermissionTier controls whether an agent runs with --dangerously-skip-permissions.
type PermissionTier int

const (
	// Supervised agents require manual approval for tool use.
	// Available for opt-in via per-role config; no catalog role uses it today.
	Supervised PermissionTier = iota

	// Autonomous agents run with --dangerously-skip-permissions.
	// Used for every catalog role today (super, eng, qa, pm, arch, sec, shipper,
	// pmm, writer, ops, growth, intern). Operators can override per role via
	// claude_args in initech.yaml.
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
	"intern":  {Name: "intern", Permission: Autonomous, NeedsSrc: true},
}

// LookupRole returns the RoleDef for a role name. Resolution order:
//  1. Exact catalog match -> the catalog entry verbatim.
//  2. Numbered family match (qa\d+, eng\d+) -> a synthesized RoleDef with the
//     same NeedsSrc/NeedsPlaybooks defaults the catalog uses for the explicit
//     family members (qa1/qa2 and eng1/eng2/eng3). This lets operators spin up
//     qa10 or eng7 without modifying the catalog.
//  3. Anything else (custom roles like "designer", "dba") -> a bare default
//     RoleDef: Autonomous, no src, no playbooks. Preserves the open-set design.
func LookupRole(name string) RoleDef {
	if def, ok := Catalog[name]; ok {
		return def
	}
	if numberedRoleRe.MatchString(name) {
		if strings.HasPrefix(name, "qa") {
			return RoleDef{
				Name:           name,
				Permission:     Autonomous,
				NeedsSrc:       true,
				NeedsPlaybooks: true,
			}
		}
		// Must be eng\d+ — numberedRoleRe alternation has only two branches.
		return RoleDef{
			Name:       name,
			Permission: Autonomous,
			NeedsSrc:   true,
		}
	}
	return RoleDef{
		Name:       name,
		Permission: Autonomous,
	}
}

// IsValidRoleName reports whether name is acceptable as a role name for CLI
// commands like 'initech hire' / 'initech add-agent'. Two paths to acceptance:
//  - exact match against the Catalog (covers the curated role set), or
//  - match against the numbered family pattern qa\d+ / eng\d+ (covers
//    arbitrary scaling like qa10, eng7, qa007).
//
// Custom non-numbered names (e.g. "designer", "dba") are deliberately rejected
// at the CLI to preserve typo protection. Operators wanting truly custom roles
// must add them to the Catalog or design a separate opt-in (out of scope here).
func IsValidRoleName(name string) bool {
	if _, ok := Catalog[name]; ok {
		return true
	}
	return numberedRoleRe.MatchString(name)
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

// RoleFamilyOf maps an agent name to its RoleFamily using built-in
// classification only (prefix + catalog). Equivalent to RoleFamilyOfWithRoster
// with a nil roster; see that function for the full resolution order.
//
// Callers that have access to the project roster (e.g. the deliver command)
// should call RoleFamilyOfWithRoster directly so custom roles defined in
// initech.yaml are accepted instead of being misclassified as FamilyUnknown
// (ini-98n).
func RoleFamilyOf(name string) RoleFamily {
	return RoleFamilyOfWithRoster(name, nil)
}

// RoleFamilyOfWithRoster maps an agent name to its RoleFamily, consulting an
// optional project roster as the final non-error tier. Resolution order:
//
//  1. Empty name -> FamilyUnknown.
//  2. qa* prefix -> FamilyQA (covers qa1, qa10, qaWhatever).
//  3. eng* prefix -> FamilyEng (covers eng1, eng7, engineer).
//  4. Catalog exact match (super, shipper, pm, pmm, etc.) -> FamilyOther.
//  5. Roster exact match -> FamilyOther (the ini-98n tier: custom roles
//     defined in initech.yaml are accepted with the generic announce
//     template).
//  6. Otherwise -> FamilyUnknown so callers can reject with a clear error
//     rather than silently defaulting to the engineer template.
//
// The roster is passed in as data (not loaded from disk inside this function)
// to preserve the package's pure-function promise. Prefix and catalog tiers
// intentionally win first so a roster entry like "engineer" doesn't reclassify
// it from FamilyEng to FamilyOther.
func RoleFamilyOfWithRoster(name string, roster []string) RoleFamily {
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
	for _, r := range roster {
		if r == name {
			return FamilyOther
		}
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
