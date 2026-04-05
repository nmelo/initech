package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
	"github.com/spf13/cobra"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show this agent's identity and working directory",
	RunE:  runWhoami,
}

func init() {
	rootCmd.AddCommand(whoamiCmd)
}

func runWhoami(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	// Load config roles for directory detection (best-effort).
	var configRoles []string
	peerName := "(not set)"
	if cfgPath, err := config.Discover(wd); err == nil {
		if p, err := config.Load(cfgPath); err == nil {
			if p.PeerName != "" {
				peerName = p.PeerName
			}
			configRoles = p.Roles
		}
	}

	role, source := detectRole(wd, configRoles)
	claudeMD := findClaudeMD(wd)

	fmt.Fprintf(out, "role:      %s\n", role)
	fmt.Fprintf(out, "peer:      %s\n", peerName)
	fmt.Fprintf(out, "directory: %s\n", wd)
	fmt.Fprintf(out, "claude.md: %s\n", claudeMD)
	_ = source // source is included in role string
	return nil
}

// detectRole resolves the agent role using a priority chain:
//  1. INITECH_ROLE env var
//  2. INITECH_AGENT env var
//  3. Workspace directory name (walk up cwd, match against catalog + config roles)
//
// Returns the display string (role name with source annotation) and the raw role name.
func detectRole(wd string, configRoles []string) (display, raw string) {
	if v := os.Getenv("INITECH_ROLE"); v != "" {
		return fmt.Sprintf("%s (from INITECH_ROLE)", v), v
	}
	if v := os.Getenv("INITECH_AGENT"); v != "" {
		return fmt.Sprintf("%s (from INITECH_AGENT)", v), v
	}

	if name := detectRoleFromDir(wd, configRoles); name != "" {
		return fmt.Sprintf("%s (from directory name)", name), name
	}

	return "(not set)", ""
}

// detectRoleFromDir walks up from dir checking each directory name against the
// roles catalog and the config roles list. Returns the first match or "".
func detectRoleFromDir(dir string, configRoles []string) string {
	configSet := make(map[string]bool, len(configRoles))
	for _, r := range configRoles {
		configSet[r] = true
	}

	for {
		name := filepath.Base(dir)
		if _, ok := roles.Catalog[name]; ok {
			return name
		}
		if configSet[name] {
			return name
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// findClaudeMD walks up from dir looking for CLAUDE.md. Returns the first
// match or "(none)" if not found.
func findClaudeMD(dir string) string {
	for {
		candidate := filepath.Join(dir, "CLAUDE.md")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "(none)"
}
