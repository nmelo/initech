package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var clearAll bool

var clearCmd = &cobra.Command{
	Use:   "clear <agent> [agent2 ...]",
	Short: "Send /clear to reset an agent's conversation context",
	Long: `Sends /clear to the specified agent(s) to reset their Claude Code context.
Use --all to clear every agent except super.

Requires a running initech TUI session.`,
	Args: cobra.ArbitraryArgs,
	RunE: runClear,
}

func init() {
	clearCmd.Flags().BoolVar(&clearAll, "all", false, "Clear all agents except super")
	rootCmd.AddCommand(clearCmd)
}

func runClear(cmd *cobra.Command, args []string) error {
	if !clearAll && len(args) == 0 {
		return fmt.Errorf("specify agent name(s) or use --all")
	}

	out := cmd.OutOrStdout()
	targets := args

	if clearAll {
		panes, err := listPanes()
		if err != nil {
			return err
		}
		targets = nil
		for _, p := range panes {
			if p.Name != "super" {
				targets = append(targets, p.Name)
			}
		}
		if len(targets) == 0 {
			fmt.Fprintln(out, "No agents to clear.")
			return nil
		}
	}

	var errs int
	for _, name := range targets {
		resp, err := ipcCall(tui.IPCRequest{
			Action: "send",
			Target: name,
			Text:   "/clear",
			Enter:  true,
		})
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", name, err)
			errs++
			continue
		}
		if !resp.OK {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", name, resp.Error)
			errs++
			continue
		}
		fmt.Fprintf(out, "Cleared %s.\n", name)
	}
	if errs > 0 {
		return fmt.Errorf("%d agent(s) could not be cleared", errs)
	}
	return nil
}

func listPanes() ([]tui.PaneInfo, error) {
	resp, err := ipcCall(tui.IPCRequest{Action: "list"})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var panes []tui.PaneInfo
	if err := json.Unmarshal([]byte(resp.Data), &panes); err != nil {
		return nil, fmt.Errorf("parse pane list: %w", err)
	}
	return panes, nil
}
