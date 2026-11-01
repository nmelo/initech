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
	Short: "Override or clear an agent pane's bead (it comes from bd)",
	Long: `A pane's bead comes from bd: the bead whose assignee is the pane's role
and whose status is in_progress, read every 10 seconds. With two such beads,
the one this command last named wins, else the newest updated.

initech bead <id> is a manual override: the pane shows that bead until
initech bead --clear, which returns it to the bead bd says at once. Supports
multiple bead IDs.

Inside a TUI pane (INITECH_AGENT set automatically):
  initech bead ini-bhk.3                      Override with one bead
  initech bead ini-abc ini-def ini-ghi         Override with several beads
  initech bead --clear                         Clear the override (back to bd)

Outside the TUI (must specify agent):
  initech bead --agent eng1 ini-bhk.3
  initech bead --agent eng1 ini-abc ini-def
  initech bead --agent eng1 --clear`,
	RunE: runBead,
}

func init() {
	beadCmd.Flags().StringVar(&beadAgent, "agent", "", "Target agent name (auto-detected inside TUI via INITECH_AGENT)")
	beadCmd.Flags().BoolVar(&beadClear, "clear", false, "Clear the override: the pane shows the bead bd says again")
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

	// Determine the bead ID(s).
	beadText := ""
	if !beadClear {
		if len(args) < 1 {
			return fmt.Errorf("bead ID required (or use --clear)")
		}
		if len(args) == 1 {
			// Single bead: try to fetch title from bd for ribbon display.
			id := args[0]
			title, err := bdShowTitleFn(id)
			if err == nil && title != "" {
				beadText = id + "\t" + truncateTitle(title, 80)
			} else {
				beadText = id
			}
		} else {
			// Multiple beads: comma-separated, no titles.
			beadText = strings.Join(args, ",")
		}
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

	// A silent success (exit 0, no output) is indistinguishable from doing
	// nothing at all -- confirm what changed so a caller outside the TUI
	// (no visible ribbon to check) has direct evidence the IPC call actually
	// landed, and so a LATER failure (e.g. an unrelated auto-detect clear
	// racing in and reverting this) is visibly a change from this baseline
	// rather than an unexplained absence (ini-cs7).
	out := cmd.OutOrStdout()
	if beadClear {
		fmt.Fprintf(out, "Cleared bead for %s.\n", agent)
	} else {
		fmt.Fprintf(out, "Set %s for %s.\n", strings.Join(args, ", "), agent)
	}
	return nil
}
