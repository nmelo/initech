package cmd

import (
	"fmt"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop the TUI session",
	Long:  `Sends a quit signal to the running TUI via IPC. The TUI shuts down and all agent panes are closed.`,
	RunE:  runDown,
}

func init() {
	rootCmd.AddCommand(downCmd)
}

func runDown(cmd *cobra.Command, args []string) error {
	sockPath, p, err := resolveSocket()
	if err != nil {
		return err
	}

	name := stoppedSessionName(sockPath, p)

	resp, err := ipcCallSocket(sockPath, tui.IPCRequest{Action: "quit"})
	if err != nil {
		return fmt.Errorf("send quit: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Session '%s' stopped.\n", name)
	return nil
}

// stoppedSessionName names the session behind sockPath, asked of the socket
// before it is told to quit: the project its list reply names. An older
// server names none; then the folder's project stands only when sockPath is
// that project's own socket, and otherwise the socket path is printed rather
// than a guess. The list is only for the label: when it fails the quit is
// still sent.
func stoppedSessionName(sockPath string, p *config.Project) string {
	if resp, err := ipcCallSocket(sockPath, tui.IPCRequest{Action: "list"}); err == nil && resp.OK &&
		resp.Project != nil && resp.Project.Name != "" {
		return resp.Project.Name
	}
	if p != nil && !socketIsAnotherProject(sockPath, p) {
		return p.Name
	}
	return sockPath
}
