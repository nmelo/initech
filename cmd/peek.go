package cmd

import (
	"fmt"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var peekLines int

var peekCmd = &cobra.Command{
	Use:   "peek <role>",
	Short: "Read an agent's terminal content",
	Long: `Captures the visible content of the specified agent's terminal pane.

Requires a running initech TUI (connects via INITECH_SOCKET).`,
	Args: cobra.ExactArgs(1),
	RunE: runPeek,
}

func init() {
	peekCmd.Flags().IntVarP(&peekLines, "lines", "n", 0, "Number of lines to capture (0 = all)")
	rootCmd.AddCommand(peekCmd)
}

func runPeek(cmd *cobra.Command, args []string) error {
	req := tui.IPCRequest{
		Action: "peek",
		Target: args[0],
		Lines:  peekLines,
	}

	resp, err := ipcCall(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	fmt.Print(resp.Data)
	return nil
}
