package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var peersCmd = &cobra.Command{
	Use:   "peers",
	Short: "Show available peers and their agents",
	Long:  `Lists all connected peers (local and remote) with their agent names. Use host:agent syntax to send messages across machines.`,
	RunE:  runPeers,
}

func init() {
	rootCmd.AddCommand(peersCmd)
}

func runPeers(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	sockPath, _, err := discoverSocket()
	if err != nil {
		return err
	}

	resp, err := ipcCallSocket(sockPath, tui.IPCRequest{Action: "peers_query"})
	if err != nil {
		return fmt.Errorf("query peers: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	var peers []tui.PeerInfo
	if err := json.Unmarshal([]byte(resp.Data), &peers); err != nil {
		return fmt.Errorf("parse peers response: %w", err)
	}

	if len(peers) == 0 {
		fmt.Fprintln(out, "No peers connected.")
		return nil
	}

	fmt.Fprintf(out, "\n  %s %s\n",
		color.Pad(color.Cyan("PEER"), 14),
		color.Cyan("AGENTS"),
	)
	for _, p := range peers {
		agents := strings.Join(p.Agents, "  ")
		fmt.Fprintf(out, "  %s %s\n",
			color.Pad(color.Blue(p.Name), 14),
			agents,
		)
	}
	fmt.Fprintln(out)

	return nil
}
