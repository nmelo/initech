package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run headless daemon for remote TUI connections",
	Long: `Launches agents in headless mode (no TUI) and listens on TCP for
yamux connections from remote TUI clients. Requires mode: headless and
listen: <addr> in initech.yaml.`,
	RunE: runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	cfgPath, err := config.Discover(wd)
	if err != nil {
		return fmt.Errorf("no initech.yaml found. Run 'initech init' first")
	}

	proj, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if proj.Mode != "headless" {
		return fmt.Errorf("initech serve requires mode: headless in initech.yaml")
	}

	// Build agent configs.
	agents := make([]tui.PaneConfig, 0, len(proj.Roles))
	for _, roleName := range proj.Roles {
		pcfg, err := buildServeAgentConfig(roleName, proj)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			continue
		}
		agents = append(agents, pcfg)
	}

	if len(agents) == 0 {
		return fmt.Errorf("no valid role directories found")
	}

	return tui.RunDaemon(tui.DaemonConfig{
		Project: proj,
		Agents:  agents,
		Version: Version,
		Verbose: verbose,
	})
}

// buildServeAgentConfig constructs a PaneConfig for a role in serve mode.
func buildServeAgentConfig(roleName string, proj *config.Project) (tui.PaneConfig, error) {
	var argv []string
	if mock := os.Getenv("INITECH_MOCK_AGENT"); mock != "" {
		argv = []string{mock}
	} else {
		// Per-role command override takes priority (e.g. ["codex"] for non-Claude agents).
		// When Command is set, it is the complete command; claude_args are NOT appended.
		ov, hasOverride := proj.RoleOverrides[roleName]
		if hasOverride && len(ov.Command) > 0 {
			argv = append(argv, ov.Command...)
		} else {
			if len(proj.ClaudeCommand) > 0 {
				argv = append(argv, proj.ClaudeCommand...)
			} else {
				argv = []string{"claude"}
			}
			var roleArgs []string
			if hasOverride {
				roleArgs = ov.ClaudeArgs
			}
			if resolved := roles.ResolveClaudeArgs(roleName, proj.ClaudeArgs, roleArgs); len(resolved) > 0 {
				argv = append(argv, resolved...)
			}
		}
	}

	dir := filepath.Join(proj.Root, roleName)
	if ov, ok := proj.RoleOverrides[roleName]; ok && ov.Dir != "" {
		dir = ov.Dir
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return tui.PaneConfig{}, fmt.Errorf("role %q directory does not exist: %s", roleName, dir)
	}

	var env []string
	if proj.Beads.Prefix != "" {
		env = append(env, fmt.Sprintf("BEADS_DIR=%s/.beads", proj.Root))
	}

	var submitKey string
	var noBracketedPaste bool
	if ov, ok := proj.RoleOverrides[roleName]; ok {
		submitKey = ov.SubmitKey
		noBracketedPaste = ov.NoBracketedPaste
	}

	return tui.PaneConfig{
		Name:             roleName,
		Command:          argv,
		Dir:              dir,
		Env:              env,
		SubmitKey:        submitKey,
		NoBracketedPaste: noBracketedPaste,
	}, nil
}
