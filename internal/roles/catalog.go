// Package roles owns role definitions, templates, and template rendering for
// initech projects. It provides the catalog of well-known roles, the template
// rendering engine, and the inline template constants.
//
// The catalog is an open set: unknown role names are valid and receive sensible
// defaults. This lets users define custom roles (e.g., "designer", "dba")
// without modifying initech's source code.
//
// This package does not know about files on disk, config parsing, or tmux.
package roles

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

// RoleDef describes a role's metadata that drives scaffold and tmuxinator generation.
type RoleDef struct {
	Name           string
	Permission     PermissionTier
	NeedsSrc       bool
	NeedsPlaybooks bool
	NeedsMakefile  bool
}

// Catalog maps well-known role names to their definitions.
// This is not a closed set; roles not in the catalog get defaults via LookupRole.
var Catalog = map[string]RoleDef{
	"super":   {Name: "super", Permission: Supervised},
	"eng1":    {Name: "eng1", Permission: Autonomous, NeedsSrc: true, NeedsMakefile: true},
	"eng2":    {Name: "eng2", Permission: Autonomous, NeedsSrc: true, NeedsMakefile: true},
	"qa1":     {Name: "qa1", Permission: Autonomous, NeedsSrc: true, NeedsPlaybooks: true},
	"qa2":     {Name: "qa2", Permission: Autonomous, NeedsSrc: true, NeedsPlaybooks: true},
	"shipper": {Name: "shipper", Permission: Supervised, NeedsSrc: true, NeedsPlaybooks: true},
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
