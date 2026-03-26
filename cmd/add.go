package cmd

import (
	"fmt"
	"time"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var addBead string

var addCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a new agent to the running TUI session",
	Long: `Creates a new pane for the named agent. The workspace directory
(<project_root>/<name>/) must already exist. The agent is session-scoped:
initech.yaml is not modified.

Use --bead to dispatch a bead after the agent starts.`,
	Args: cobra.ExactArgs(1),
	RunE: runAdd,
}

func init() {
	addCmd.Flags().StringVar(&addBead, "bead", "", "Bead ID to dispatch after agent starts")
	rootCmd.AddCommand(addCmd)
}

func runAdd(cmd *cobra.Command, args []string) error {
	name := args[0]

	sockPath, _, err := discoverSocket()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	resp, err := ipcCallSocket(sockPath, tui.IPCRequest{
		Action: "add",
		Target: name,
	})
	if err != nil {
		return fmt.Errorf("could not add %s: %w", name, err)
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	fmt.Fprintf(out, "Added %s.\n", name)

	if addBead != "" {
		time.Sleep(3 * time.Second)
		dispatch := fmt.Sprintf("[from initech] Resume %s.", addBead)
		dresp, derr := ipcCallSocket(sockPath, tui.IPCRequest{
			Action: "send",
			Target: name,
			Text:   dispatch,
			Enter:  true,
		})
		if derr != nil {
			fmt.Fprintf(out, "Warning: could not dispatch bead to %s: %v\n", name, derr)
		} else if !dresp.OK {
			fmt.Fprintf(out, "Warning: dispatch to %s: %s\n", name, dresp.Error)
		} else {
			fmt.Fprintf(out, "Dispatched: \"%s\"\n", dispatch)
		}
	}
	return nil
}
