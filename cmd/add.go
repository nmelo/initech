package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nmelo/initech/internal/roles"
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

	sockPath, proj, err := discoverSocket()
	if err != nil {
		if hint := addAgentHint(name, ""); hint != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), hint)
		}
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
		if hint := addAgentHint(name, proj.Root); hint != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), hint)
		}
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

// addAgentHint returns a suggestion to use 'add-agent' when the named role is
// recognized (catalog member or numbered family like qa10/eng7) but its
// workspace directory does not exist. projRoot may be empty (e.g. when
// discoverSocket failed); in that case only role validity is checked.
func addAgentHint(name, projRoot string) string {
	if !roles.IsValidRoleName(name) {
		return ""
	}
	if projRoot != "" {
		if _, err := os.Stat(filepath.Join(projRoot, name)); err == nil {
			return "" // dir exists, not a missing-agent situation
		}
	}
	return fmt.Sprintf("Hint: to create the %s workspace first, run: initech add-agent %s", name, name)
}
