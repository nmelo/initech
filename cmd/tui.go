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

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the TUI terminal multiplexer (spike)",
	Long: `Starts an interactive TUI that replaces tmux for managing agent terminals.
Each agent gets its own PTY-backed terminal pane running Claude with the
appropriate permission level as defined in initech.yaml.

Requires initech.yaml in the current directory or a parent directory.

Keybindings:
  ` + "`" + `                Open command modal
  Alt+Left/Right   Navigate between panes
  Alt+1            Focus mode (single pane)
  Alt+2            2x2 grid
  Alt+3            3x3 grid
  Alt+4            Main + stacked layout
  Alt+z            Zoom/unzoom focused pane
  Alt+s            Toggle agent status overlay
  Alt+q            Quit

Commands (via ` + "`" + ` modal):
  grid CxR         Set grid layout (e.g. grid 3x3)
  focus [name]     Focus mode, optionally on a named agent
  zoom             Toggle zoom
  panel            Toggle agent overlay
  main             Main + stacked layout
  layout reset     Reset layout to auto-calculated defaults
  quit             Exit`,
	RunE: runTUI,
}

var resetLayout bool

func init() {
	tuiCmd.Flags().BoolVar(&resetLayout, "reset-layout", false, "Ignore saved layout and start with auto-calculated defaults")
	rootCmd.AddCommand(tuiCmd)
}

func runTUI(cmd *cobra.Command, args []string) error {
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

	agents := make([]tui.PaneConfig, 0, len(proj.Roles))
	for _, roleName := range proj.Roles {
		// Build agent command. INITECH_MOCK_AGENT overrides for testing.
		var argv []string
		if mock := os.Getenv("INITECH_MOCK_AGENT"); mock != "" {
			argv = []string{mock}
		} else {
			// Base command: claude_command or default ["claude"].
			if len(proj.ClaudeCommand) > 0 {
				argv = append(argv, proj.ClaudeCommand...)
			} else {
				argv = []string{"claude"}
			}
			// Args: per-role override > global > catalog default.
			var roleArgs []string
			if ov, ok := proj.RoleOverrides[roleName]; ok {
				roleArgs = ov.ClaudeArgs
			}
			if args := roles.ResolveClaudeArgs(roleName, proj.ClaudeArgs, roleArgs); len(args) > 0 {
				argv = append(argv, args...)
			}
		}

		// Working directory: <root>/<role>/
		dir := filepath.Join(proj.Root, roleName)
		if ov, ok := proj.RoleOverrides[roleName]; ok && ov.Dir != "" {
			dir = ov.Dir
		}

		// Environment.
		var env []string
		if proj.Beads.Prefix != "" {
			env = append(env, fmt.Sprintf("BEADS_DIR=%s/.beads", proj.Root))
		}

		agents = append(agents, tui.PaneConfig{
			Name:    roleName,
			Command: argv,
			Dir:     dir,
			Env:     env,
		})
	}

	return tui.Run(tui.Config{
		Agents:      agents,
		ProjectName: proj.Name,
		ProjectRoot: proj.Root,
		ResetLayout: resetLayout,
	})
}
