package cmd

import (
	"fmt"
	"strings"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var interruptCmd = &cobra.Command{
	Use:   "interrupt [host:]<agent>",
	Short: "Send Escape or Ctrl+C to an agent's terminal",
	Long: `Sends a raw control character to the specified agent's PTY to interrupt
the current operation.

By default sends Escape (0x1B), which stops Claude Code's current tool
execution and returns to the prompt. Use --hard to send Ctrl+C (0x03),
which kills a running shell command.

For cross-machine addressing, use host:agent format:

  initech interrupt workbench:shipper --hard

Requires a running initech TUI (connects via INITECH_SOCKET).`,
	Args: cobra.ExactArgs(1),
	RunE: runInterrupt,
}

var interruptHard bool

func init() {
	interruptCmd.Flags().BoolVar(&interruptHard, "hard", false, "Send Ctrl+C instead of Escape")
	rootCmd.AddCommand(interruptCmd)
}

func runInterrupt(cmd *cobra.Command, args []string) error {
	target := args[0]

	// Parse host:agent format for cross-machine routing.
	var host string
	if idx := strings.Index(target, ":"); idx >= 0 {
		host = target[:idx]
		target = target[idx+1:]
	}

	text := ""
	if interruptHard {
		text = "hard"
	}

	req := tui.IPCRequest{
		Action: "interrupt",
		Target: target,
		Host:   host,
		Text:   text,
	}

	resp, err := ipcCall(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	label := "interrupted"
	if interruptHard {
		label = "interrupted (Ctrl+C)"
	}
	if host != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s %s:%s\n", label, host, target)
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", label, target)
	}
	return nil
}
