package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/tmux"
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

func runStatus(cmd *cobra.Command, args []string) error {
	runner := &iexec.DefaultRunner{}
	out := cmd.OutOrStdout()

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	cfgPath, err := config.Discover(wd)
	if err != nil {
		return fmt.Errorf("no initech.yaml found. Run 'initech init' first")
	}

	p, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	if !tmux.SessionExists(runner, p.Name) {
		fmt.Fprintf(out, "Session '%s' is not running. Use 'initech' to start.\n", p.Name)
		return nil
	}

	windows, err := tmux.ListWindows(runner, p.Name)
	if err != nil {
		return err
	}

	// Build lookup of windows by name
	windowMap := make(map[string]tmux.Window, len(windows))
	for _, w := range windows {
		windowMap[w.Name] = w
	}

	// Try to get bead assignments
	beadMap := getBeadAssignments(runner)

	// Collect rows and total memory
	type row struct {
		role    string
		claude  string
		bead    string
		status  string
		memStr  string
		memBytes uint64
	}

	var rows []row
	var totalMem uint64
	running := 0
	stopped := 0

	for _, roleName := range p.Roles {
		w, exists := windowMap[roleName]
		r := row{role: roleName}

		if !exists {
			r.claude = "-"
			r.bead = "-"
			r.status = "stopped"
			r.memStr = "-"
			stopped++
		} else if tmux.IsClaudeRunning(runner, w) {
			r.claude = "yes"
			mem := tmux.GetProcessMemory(runner, w)
			r.memBytes = mem
			totalMem += mem

			if b, ok := beadMap[roleName]; ok {
				title := b.Title
				if len(title) > 30 {
					title = title[:27] + "..."
				}
				r.bead = fmt.Sprintf("%s (%s)", b.ID, title)
				r.status = b.Status
			} else {
				r.bead = "-"
				r.status = "idle"
			}

			if mem > 0 {
				r.memStr = formatMemory(mem)
			} else {
				r.memStr = "-"
			}
			running++
		} else {
			r.claude = "no"
			r.bead = "-"
			r.status = "agent down"
			r.memStr = "-"
			running++ // window exists, just no Claude
		}

		rows = append(rows, r)
	}

	// Header
	header := fmt.Sprintf("Session: %s (running, %d agents", p.Name, running)
	if stopped > 0 {
		header += fmt.Sprintf(", %d stopped", stopped)
	}
	if totalMem > 0 {
		header += fmt.Sprintf(", %s total", formatMemory(totalMem))
	}
	header += ")"
	fmt.Fprintf(out, "\n%s\n\n", header)

	// Table
	fmt.Fprintf(out, "  %-10s %-8s %-38s %-16s %8s\n", "Role", "Claude", "Bead", "Status", "Mem")
	for _, r := range rows {
		fmt.Fprintf(out, "  %-10s %-8s %-38s %-16s %8s\n", r.role, r.claude, r.bead, r.status, r.memStr)
	}
	fmt.Fprintln(out)

	return nil
}

func formatMemory(bytes uint64) string {
	gb := float64(bytes) / (1024 * 1024 * 1024)
	if gb >= 1.0 {
		return fmt.Sprintf("%.1f GB", gb)
	}
	mb := float64(bytes) / (1024 * 1024)
	return fmt.Sprintf("%.0f MB", mb)
}

func getBeadAssignments(runner iexec.Runner) map[string]beadInfo {
	result := make(map[string]beadInfo)

	// Check if bd is available
	if _, err := runner.Run("which", "bd"); err != nil {
		return result
	}

	out, err := runner.Run("bd", "list", "--status", "in_progress", "--json")
	if err != nil {
		// Also try in_qa
		out, err = runner.Run("bd", "list", "--json")
		if err != nil {
			return result
		}
	}

	var beads []beadInfo
	if err := json.Unmarshal([]byte(out), &beads); err != nil {
		// Try parsing as JSON lines
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
