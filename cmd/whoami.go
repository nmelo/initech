package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nmelo/initech/internal/config"
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

	role := os.Getenv("INITECH_ROLE")
	if role == "" {
		role = "(not set)"
	}

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	peerName := "(not set)"
	if cfgPath, err := config.Discover(wd); err == nil {
		if p, err := config.Load(cfgPath); err == nil && p.PeerName != "" {
			peerName = p.PeerName
		}
	}

	claudeMD := findClaudeMD(wd)

	fmt.Fprintf(out, "role:      %s\n", role)
	fmt.Fprintf(out, "peer:      %s\n", peerName)
	fmt.Fprintf(out, "directory: %s\n", wd)
	fmt.Fprintf(out, "claude.md: %s\n", claudeMD)
	return nil
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
