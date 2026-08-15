package cmd

import (
	"fmt"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// reloadCmd tells a remote peer's daemon to re-read its initech.yaml and spawn
// any newly-configured roles — the bounce-free path for roster growth
// (ini-ap3i). Spawn-only by design: deconfigured-but-running agents are
// reported, never killed.
var reloadCmd = &cobra.Command{
	Use:   "reload <peer>",
	Short: "Ask a remote peer's daemon to re-read its config and spawn new agents",
	Long: `Forwards reload_agents to the named peer's daemon. The daemon re-reads its
initech.yaml, spawns any roles that are configured but not running, and
announces them to every connected window — no daemon restart, no lost
sessions. Roles removed from config are reported but left running; use
'initech stop <peer>:<agent>' to stop them deliberately.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sockPath, _, err := resolveSocket()
		if err != nil {
			return err
		}
		resp, err := ipcCallSocket(sockPath, tui.IPCRequest{Action: "reload", Target: args[0]})
		if err != nil {
			return fmt.Errorf("reload %s: %w", args[0], err)
		}
		if !resp.OK {
			return fmt.Errorf("%s", resp.Error)
		}
		fmt.Fprintln(cmd.OutOrStdout(), resp.Data)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(reloadCmd)
}
