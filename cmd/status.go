package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

	ModalLatched bool `json:"modal_latched,omitempty"`
	ModalAgeSec  int  `json:"modal_age_sec,omitempty"`
	QueuedCount  int  `json:"queued_count,omitempty"`

	// Beads is the pane's ribbon; nil when the TUI sent no beads field (an
	// older TUI), which turns the ribbon-versus-bd check off for the pane.
	Beads *[]string `json:"beads"`

	// NoBead is the no-bead marker's words for an agent dispatched work with
	// no bead (initech assign -m with no bead id); empty otherwise.
	NoBead string `json:"no_bead,omitempty"`
}

func runStatus(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	sockPath, p, err := resolveSocket()
	if err != nil {
		return err
	}
	// p is nil when INITECH_SOCKET is set but no initech.yaml is
	// discoverable from cwd (e.g. an isolated test fixture) — resolveSocket
	// deliberately decouples socket resolution from project metadata
	// (ini-raup), so display and bead-lookup gracefully degrade instead of
	// requiring a project.
	projName := "(unknown project)"
	beadsEnabled := false
	if p != nil {
		projName = p.Name
		beadsEnabled = p.Beads.IsEnabled()
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
	var beadMap map[string][]beadInfo
	if beadsEnabled {
		beadMap = getBeadAssignments(runner)
	} else {
		beadMap = make(map[string][]beadInfo)
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

	header := fmt.Sprintf("Session: %s (%d agents", projName, running)
	if stopped > 0 {
		header += fmt.Sprintf(", %d stopped", stopped)
	}
	header += ")"
	fmt.Fprintf(out, "\n%s\n\n", color.Bold(header))

	if resp.WindowPort != nil {
		fmt.Fprintln(out, resp.WindowPort.Summary(time.Now()))
		fmt.Fprintln(out)
	} else if p != nil && p.WindowListen != "" {
		fmt.Fprintln(out, "Window port: state unavailable (server does not report it)")
		fmt.Fprintln(out)
	}

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
		// The latch is why mail stops moving, so it shows up where someone
		// looks when mail stops moving (ini-gbqc). Age and depth, because
		// "modal" alone cannot separate a dialog opened a second ago from one
		// that has held four messages for hours -- which is the case the live
		// report was, and it took a human peeking at the pane to see it.
		if pi.ModalLatched {
			status += " " + color.Yellow(fmt.Sprintf("[modal %s, %d queued]",
				shortDur(pi.ModalAgeSec), pi.QueuedCount))
		}

		// For remote panes, use host:name as display name.
		displayName := pi.Name
		if pi.Host != "" {
			displayName = pi.Host + ":" + pi.Name
		}

		held := beadMap[pi.Name]
		if len(held) > 0 && pi.NoBead == "" {
			b := held[0]
			title := b.Title
			if len(title) > 30 {
				title = title[:27] + "..."
			}
			bead = fmt.Sprintf("%s %s", color.Blue(b.ID), color.Dim("("+title+")"))
			if pi.Alive {
				status = statusColor(b.Status)
			}
		}
		// Work with no bead shows in the bead's place. It wins over a bd
		// assignment (that bead's id and status are not shown): it is the
		// newer dispatch, and it is what the ribbon shows.
		if pi.NoBead != "" {
			words := pi.NoBead
			if r := []rune(words); len(r) > 30 {
				words = string(r[:27]) + "..."
			}
			bead = fmt.Sprintf("%s %s", color.Yellow("no bead"), color.Dim("("+words+")"))
		}
		// bd is the truth for who holds a bead and the ribbon is a display
		// of it: a ribbon that says something else is named next to bd's
		// answer, so the stale pointer is visible instead of silently
		// different (ini-jvrs).
		if pi.Beads != nil && pi.NoBead == "" && !sameBeadSet(*pi.Beads, held) {
			shown := "-"
			if len(*pi.Beads) > 0 {
				shown = strings.Join(*pi.Beads, ",")
			}
			bead += " " + color.Yellow("ribbon: "+shown)
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

// sameBeadSet reports whether a ribbon shows exactly the beads bd says the
// agent holds, in any order.
func sameBeadSet(ribbon []string, held []beadInfo) bool {
	if len(ribbon) != len(held) {
		return false
	}
	for _, b := range held {
		if !containsString(ribbon, b.ID) {
			return false
		}
	}
	return true
}

// getBeadAssignments maps each agent to the beads bd says it holds: an
// assignee set and a shown status (in_progress, ready_for_qa, in_qa), in bd's
// order. qa_passed is left out: nobody is working the bead.
func getBeadAssignments(runner iexec.Runner) map[string][]beadInfo {
	result := make(map[string][]beadInfo)

	if _, err := runner.Run("which", "bd"); err != nil {
		return result
	}

	out, err := runner.Run("bd", "list", "--status", strings.Join(shownStatuses, ","), "--json")
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
		if b.Assignee != "" && isShownStatus(b.Status) {
			result[b.Assignee] = append(result[b.Assignee], b)
		}
	}

	return result
}

// shortDur renders a latch age compactly: 47m, 2h, 8s.
func shortDur(sec int) string {
	switch {
	case sec >= 3600:
		return fmt.Sprintf("%dh", sec/3600)
	case sec >= 60:
		return fmt.Sprintf("%dm", sec/60)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}
