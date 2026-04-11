package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var beadAgent string
var beadClear bool

var beadCmd = &cobra.Command{
	Use:   "bead [id...]",
	Short: "Set or clear the current bead(s) for an agent pane",
	Long: `Reports the current bead assignment to the TUI so it appears in the
ribbon badge and overlay panel. Supports multiple bead IDs.

Inside a TUI pane (INITECH_AGENT set automatically):
  initech bead ini-bhk.3                      Set single bead
  initech bead ini-abc ini-def ini-ghi         Set multiple beads
  initech bead --clear                         Clear all beads

Outside the TUI (must specify agent):
  initech bead --agent eng1 ini-bhk.3
  initech bead --agent eng1 ini-abc ini-def
  initech bead --agent eng1 --clear`,
	RunE: runBead,
}

func init() {
	beadCmd.Flags().StringVar(&beadAgent, "agent", "", "Target agent name (auto-detected inside TUI via INITECH_AGENT)")
	beadCmd.Flags().BoolVar(&beadClear, "clear", false, "Clear the current bead display")
	rootCmd.AddCommand(beadCmd)
}

func runBead(cmd *cobra.Command, args []string) error {
	// Determine the target agent.
	agent := beadAgent
	if agent == "" {
		agent = os.Getenv("INITECH_AGENT")
	}
	if agent == "" {
		return fmt.Errorf("no agent specified (set --agent or run inside a TUI pane where INITECH_AGENT is set)")
	}

	// Determine the bead ID(s). Comma-separated for multi-bead IPC.
	beadText := ""
	if !beadClear {
		if len(args) < 1 {
			return fmt.Errorf("bead ID required (or use --clear)")
		}
		beadText = strings.Join(args, ",")
	}

	req := tui.IPCRequest{
		Action: "bead",
		Target: agent,
		Text:   beadText,
	}

	resp, err := ipcCall(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}
