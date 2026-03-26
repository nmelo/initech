package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var (
	patrolLines  int
	patrolActive bool
	patrolAgents []string
)

var patrolCmd = &cobra.Command{
	Use:   "patrol",
	Short: "Bulk peek: show recent output from all agents",
	Long: `Gathers the last N lines of terminal output from every agent pane
and prints them with headers. Replaces multiple initech peek calls.

Examples:
  initech patrol                   All agents, 20 lines each
  initech patrol -n 5              All agents, 5 lines each
  initech patrol --active          Skip idle agents with no output
  initech patrol --agent eng1      Only show eng1 and eng2
    --agent eng2`,
	RunE: runPatrol,
}

func init() {
	patrolCmd.Flags().IntVarP(&patrolLines, "lines", "n", 20, "Lines per agent")
	patrolCmd.Flags().BoolVar(&patrolActive, "active", false, "Skip idle agents with no content")
	patrolCmd.Flags().StringArrayVar(&patrolAgents, "agent", nil, "Filter to specific agent(s)")
	rootCmd.AddCommand(patrolCmd)
}

func runPatrol(cmd *cobra.Command, args []string) error {
	req := tui.IPCRequest{
		Action: "patrol",
		Lines:  patrolLines,
	}

	resp, err := ipcCall(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	// Parse the JSON array of patrol entries.
	type patrolEntry struct {
		Name     string `json:"name"`
		Activity string `json:"activity"`
		Bead     string `json:"bead,omitempty"`
		Alive    bool   `json:"alive"`
		Content  string `json:"content"`
	}
	var entries []patrolEntry
	if err := json.Unmarshal([]byte(resp.Data), &entries); err != nil {
		return fmt.Errorf("parse patrol response: %w", err)
	}

	// Build agent filter set.
	agentFilter := make(map[string]bool, len(patrolAgents))
	for _, a := range patrolAgents {
		agentFilter[a] = true
	}

	for _, e := range entries {
		// Apply agent filter.
		if len(agentFilter) > 0 && !agentFilter[e.Name] {
			continue
		}

		content := strings.TrimRight(e.Content, "\n")

		// Apply active filter.
		if patrolActive && e.Activity == "idle" && content == "" {
			continue
		}

		// Header line.
		header := e.Name + " (" + e.Activity
		if e.Bead != "" {
			header += " | " + e.Bead
		}
		if !e.Alive {
			header += " | dead"
		}
		header += ")"
		fmt.Printf("=== %s ===\n", header)

		// Content.
		if content == "" {
			fmt.Println("[no recent output]")
		} else {
			fmt.Println(content)
		}
		fmt.Println()
	}

	return nil
}
