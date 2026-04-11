package cmd

import (
	"fmt"
	"os"

	"github.com/nmelo/initech/internal/config"
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
		pcfg, err := buildAgentPaneConfig(roleName, proj)
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
		WebPort: proj.EffectiveWebPort(),
	})
}

