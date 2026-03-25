package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show agent status, bead assignments, and memory usage",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

// beadInfo holds bead data matched to an agent from bd list --json.
type beadInfo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Assignee string `json:"assignee"`
}

// paneInfo matches the JSON returned by the IPC "list" action.
type paneInfo struct {
	Name     string `json:"name"`
	Activity string `json:"activity"`
	Alive    bool   `json:"alive"`
	Visible  bool   `json:"visible"`
}

func runStatus(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	sockPath, p, err := discoverSocket()
	if err != nil {
		return err
	}

	// Query pane list from TUI.
	resp, err := ipcCallSocket(sockPath, tui.IPCRequest{Action: "list"})
	if err != nil {
		return fmt.Errorf("query TUI: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	var panes []paneInfo
	if err := json.Unmarshal([]byte(resp.Data), &panes); err != nil {
		return fmt.Errorf("parse pane list: %w", err)
	}

	// Try to get bead assignments.
	runner := &iexec.DefaultRunner{}
	beadMap := getBeadAssignments(runner)

	// Build table.
	running := 0
	stopped := 0
	for _, pi := range panes {
		if pi.Alive {
			running++
		} else {
			stopped++
		}
	}

	header := fmt.Sprintf("Session: %s (%d agents", p.Name, running)
	if stopped > 0 {
		header += fmt.Sprintf(", %d stopped", stopped)
	}
	header += ")"
	fmt.Fprintf(out, "\n%s\n\n", header)

	fmt.Fprintf(out, "  %-10s %-8s %-38s %-16s\n", "Role", "Alive", "Bead", "Status")
	for _, pi := range panes {
		alive := "yes"
		if !pi.Alive {
			alive = "no"
		}
		bead := "-"
		status := pi.Activity
		if !pi.Alive {
			status = "stopped"
		}
		if !pi.Visible {
			status += " [hidden]"
		}

		if b, ok := beadMap[pi.Name]; ok {
			title := b.Title
			if len(title) > 30 {
				title = title[:27] + "..."
			}
			bead = fmt.Sprintf("%s (%s)", b.ID, title)
			if pi.Alive {
				status = b.Status
			}
		}

		fmt.Fprintf(out, "  %-10s %-8s %-38s %-16s\n", pi.Name, alive, bead, status)
	}
	fmt.Fprintln(out)

	return nil
}

func getBeadAssignments(runner iexec.Runner) map[string]beadInfo {
	result := make(map[string]beadInfo)

	if _, err := runner.Run("which", "bd"); err != nil {
		return result
	}

	out, err := runner.Run("bd", "list", "--status", "in_progress", "--json")
	if err != nil {
		out, err = runner.Run("bd", "list", "--json")
		if err != nil {
			return result
		}
	}

	var beads []beadInfo
	if err := json.Unmarshal([]byte(out), &beads); err != nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var b beadInfo
			if err := json.Unmarshal([]byte(line), &b); err == nil {
				beads = append(beads, b)
			}
		}
	}

	for _, b := range beads {
		if b.Assignee != "" && (b.Status == "in_progress" || b.Status == "in_qa") {
			result[b.Assignee] = b
		}
	}

	return result
}
