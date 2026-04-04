package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nmelo/initech/internal/color"
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
	Host     string `json:"host,omitempty"`
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

	// Merge remote peer agents not already in the pane list.
	panes = mergeRemotePeers(sockPath, panes)

	// Try to get bead assignments (skip when disabled).
	runner := &iexec.DefaultRunner{}
	var beadMap map[string]beadInfo
	if p.Beads.IsEnabled() {
		beadMap = getBeadAssignments(runner)
	} else {
		beadMap = make(map[string]beadInfo)
	}

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

	// Check if any pane is remote (has a non-empty host).
	hasRemotes := false
	for _, pi := range panes {
		if pi.Host != "" {
			hasRemotes = true
			break
		}
	}

	header := fmt.Sprintf("Session: %s (%d agents", p.Name, running)
	if stopped > 0 {
		header += fmt.Sprintf(", %d stopped", stopped)
	}
	header += ")"
	fmt.Fprintf(out, "\n%s\n\n", color.Bold(header))

	// Header row: cyan, padded before coloring so alignment holds.
	// Include HOST column only when remotes are present.
	if hasRemotes {
		fmt.Fprintf(out, "  %s %s %s %s %s\n",
			color.Pad(color.Cyan("Role"), 18),
			color.Pad(color.Cyan("Host"), 12),
			color.Pad(color.Cyan("Alive"), 8),
			color.Pad(color.Cyan("Bead"), 38),
			color.Cyan("Status"),
		)
	} else {
		fmt.Fprintf(out, "  %s %s %s %s\n",
			color.Pad(color.Cyan("Role"), 10),
			color.Pad(color.Cyan("Alive"), 8),
			color.Pad(color.Cyan("Bead"), 38),
			color.Cyan("Status"),
		)
	}

	for _, pi := range panes {
		alive := color.Green("yes")
		if !pi.Alive {
			alive = color.Red("no")
		}
		bead := "-"
		status := statusColor(pi.Activity)
		if !pi.Alive {
			status = statusColor("stopped")
		}
		if !pi.Visible {
			status += " " + color.Dim("[hidden]")
		}

		// For remote panes, use host:name as display name.
		displayName := pi.Name
		if pi.Host != "" {
			displayName = pi.Host + ":" + pi.Name
		}

		if b, ok := beadMap[pi.Name]; ok {
			title := b.Title
			if len(title) > 30 {
				title = title[:27] + "..."
			}
			bead = fmt.Sprintf("%s %s", color.Blue(b.ID), color.Dim("("+title+")"))
			if pi.Alive {
				status = statusColor(b.Status)
			}
		}

		if hasRemotes {
			host := color.Dim("local")
			if pi.Host != "" {
				host = color.Cyan(pi.Host)
			}
			fmt.Fprintf(out, "  %s %s %s %s %s\n",
				color.Pad(color.Blue(displayName), 18),
				color.Pad(host, 12),
				color.Pad(alive, 8),
				color.Pad(bead, 38),
				status,
			)
		} else {
			fmt.Fprintf(out, "  %s %s %s %s\n",
				color.Pad(color.Blue(displayName), 10),
				color.Pad(alive, 8),
				color.Pad(bead, 38),
				status,
			)
		}
	}
	fmt.Fprintln(out)

	return nil
}

// statusColor applies a color to a bead/agent status string based on its value.
func statusColor(s string) string {
	switch {
	case s == "running" || s == "in_progress" || s == "qa_passed":
		return color.Green(s)
	case s == "idle" || s == "stopped":
		return color.Dim(s)
	case strings.HasPrefix(s, "stalled"):
		return color.Yellow(s)
	case s == "dead":
		return color.Red(s)
	case s == "stuck":
		return color.RedBold(s)
	default:
		return s
	}
}

// mergeRemotePeers queries peers_query and appends any remote agents not
// already present in the pane list. Best-effort: errors are silently ignored.
func mergeRemotePeers(sockPath string, panes []paneInfo) []paneInfo {
	resp, err := ipcCallSocket(sockPath, tui.IPCRequest{Action: "peers_query"})
	if err != nil || !resp.OK {
		return panes
	}

	var peers []tui.PeerInfo
	if err := json.Unmarshal([]byte(resp.Data), &peers); err != nil {
		return panes
	}

	// Build set of existing pane names (host:name for remote, name for local).
	existing := make(map[string]bool, len(panes))
	for _, pi := range panes {
		existing[pi.Name] = true
	}

	// Skip the first peer (local). Merge agents from remote peers.
	for i, peer := range peers {
		if i == 0 {
			continue
		}
		for _, agent := range peer.Agents {
			if existing[agent] {
				continue
			}
			panes = append(panes, paneInfo{
				Name:     agent,
				Host:     peer.Name,
				Activity: "connected",
				Alive:    true,
				Visible:  true,
			})
			existing[agent] = true
		}
	}
	return panes
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
