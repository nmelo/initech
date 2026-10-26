package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show agent status, bead assignments, and memory usage",
	RunE:  runStatus,
}

// statusJSON is --json: one JSON object per agent instead of the table.
var statusJSON bool

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "print one JSON object per agent (state, beads, context, background work, dialog, account); a value not read is null")
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

	// Read from the screen for --json only; nil or empty when not read
	// (ini-l6om).
	ContextPercent *int                `json:"context_percent"`
	Background     *tui.PaneBackground `json:"background"`
	Banner         string              `json:"banner"`
	Account        *tui.PaneAccount    `json:"account"`

	// Busy is the background work the TUI's tick last saw (ini-b4l0).
	Busy *tui.PaneBusy `json:"busy"`
}

// busyText is background work as status and patrol print it after the state
// word: "1 background shell 4m", "2 background shells, 1 monitor 30s". Empty
// for none.
func busyText(b *tui.PaneBusy) string {
	if b == nil {
		return ""
	}
	var kinds []string
	for _, k := range []struct {
		n          int
		one, other string
	}{
		{b.Shells, "background shell", "background shells"},
		{b.Monitors, "monitor", "monitors"},
		{b.Tasks, "background agent", "background agents"},
	} {
		switch {
		case k.n == 1:
			kinds = append(kinds, "1 "+k.one)
		case k.n > 1:
			kinds = append(kinds, fmt.Sprintf("%d %s", k.n, k.other))
		}
	}
	if len(kinds) == 0 {
		return ""
	}
	return strings.Join(kinds, ", ") + " " + shortDur(b.SinceSec)
}

// agentJSON is one agent in initech status --json. Its field names are a
// public contract: every key is always printed, and a value initech did not
// read is null, never a guess.
type agentJSON struct {
	Role           string         `json:"role"`
	Host           string         `json:"host"`
	State          *string        `json:"state"`
	Alive          bool           `json:"alive"`
	Visible        bool           `json:"visible"`
	Beads          *[]string      `json:"beads"`
	BeadNote       string         `json:"bead_note"`
	ContextPercent *int           `json:"context_percent"`
	Background     backgroundJSON `json:"background"`
	Banner         *string        `json:"banner"`
	BannerAgeSec   *int           `json:"banner_age_sec"`
	QueuedCount    int            `json:"queued_count"`
	Account        accountJSON    `json:"account"`
}

type backgroundJSON struct {
	Shells   *int `json:"shells"`
	Monitors *int `json:"monitors"`
	Tasks    *int `json:"tasks"`
}

type accountJSON struct {
	User  *string `json:"user"`
	Model *string `json:"model"`
}

// agentState is the activity label as status --json reports it: an agent
// whose process has exited is stopped, a suspended one suspended, and a label
// outside running, idle and waiting is null.
func agentState(pi paneInfo) *string {
	s := pi.Activity
	switch {
	case !pi.Alive && s == "suspended":
	case !pi.Alive:
		s = "stopped"
	case s == "running" || s == "idle" || s == "waiting":
	default:
		return nil
	}
	return &s
}

// toAgentJSON is pi as status --json prints it. fromBD is bd's answer for
// every agent, or nil when there is none and the ribbon stands in.
func toAgentJSON(pi paneInfo, fromBD map[string][]beadInfo) agentJSON {
	a := agentJSON{
		Role:           pi.Name,
		Host:           pi.Host,
		State:          agentState(pi),
		Alive:          pi.Alive,
		Visible:        pi.Visible,
		Beads:          pi.Beads,
		BeadNote:       pi.NoBead,
		ContextPercent: pi.ContextPercent,
		QueuedCount:    pi.QueuedCount,
	}
	if fromBD != nil {
		ids := []string{}
		for _, b := range fromBD[pi.Name] {
			ids = append(ids, b.ID)
		}
		a.Beads = &ids
	}
	if b := pi.Background; b != nil {
		a.Background = backgroundJSON{Shells: b.Shells, Monitors: b.Monitors, Tasks: b.Tasks}
	}
	if pi.Banner != "" {
		banner := pi.Banner
		a.Banner = &banner
	}
	if pi.ModalLatched {
		age := pi.ModalAgeSec
		a.BannerAgeSec = &age
	}
	if acct := pi.Account; acct != nil {
		a.Account = accountJSON{User: acct.User, Model: acct.Model}
	}
	return a
}

// printStatusJSON writes the agents as one JSON array, the only thing status
// --json puts on stdout.
func printStatusJSON(cmd *cobra.Command, panes []paneInfo, fromBD map[string][]beadInfo) error {
	agents := make([]agentJSON, 0, len(panes))
	for _, pi := range panes {
		agents = append(agents, toAgentJSON(pi, fromBD))
	}
	data, err := json.MarshalIndent(agents, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return err
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
	resp, err := ipcCallSocket(sockPath, tui.IPCRequest{Action: "list", Detail: statusJSON})
	if err != nil {
		return fmt.Errorf("query TUI: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	// A socket named by INITECH_SOCKET may be another project's: label the
	// roster with the project behind it, which the reply names (ini-l8s5),
	// and read the Bead column from that project's bd, in the root the reply
	// names (ini-lg62). An older TUI names none: the folder's name stands,
	// and the column says it cannot tell rather than show this folder's beads.
	beadDir, beadNote := "", ""
	if socketIsAnotherProject(sockPath, p) {
		if resp.Project != nil && resp.Project.Name != "" {
			projName = resp.Project.Name
			beadsEnabled = resp.Project.BeadsEnabled
		}
		if resp.Project == nil || beadsEnabled {
			beadDir, beadNote = socketBeadRoot(resp.Project)
		}
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
	bdAnswered := false
	if beadsEnabled && beadNote == "" {
		beadMap, bdAnswered = beadAssignments(runner, beadDir)
	} else {
		beadMap = make(map[string][]beadInfo)
	}

	// --json reads beads where the table does: bd's answer, so the two never
	// differ; the ribbon only when bd is off or gave no answer (ini-67k7).
	if statusJSON {
		var fromBD map[string][]beadInfo
		if bdAnswered {
			fromBD = beadMap
		}
		return printStatusJSON(cmd, panes, fromBD)
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
		// Background work follows the state word, which it does not change
		// (ini-b4l0): an idle agent landing a commit in a background shell
		// is idle, and busy.
		work := ""
		if t := busyText(pi.Busy); t != "" && pi.Alive {
			work = ", " + color.Yellow(t)
		}
		status := statusColor(pi.Activity) + work
		// A suspended agent is not alive either, but it is parked, not
		// stopped: calling it stopped invites a second suspend or a restart.
		if !pi.Alive && pi.Activity != "suspended" {
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
				status = statusColor(b.Status) + work
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
		if beadNote != "" && pi.NoBead == "" {
			bead = color.Dim(beadNote)
		}
		if pi.Beads != nil && pi.NoBead == "" && beadNote == "" && !sameBeadSet(*pi.Beads, held) {
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
	case s == "idle" || s == "stopped" || s == "suspended":
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
func getBeadAssignments(runner iexec.Runner, dir string) map[string][]beadInfo {
	result, _ := beadAssignments(runner, dir)
	return result
}

// beadAssignments is getBeadAssignments that also reports whether bd
// answered: false when bd is missing, fails, or prints nothing it can read.
func beadAssignments(runner iexec.Runner, dir string) (map[string][]beadInfo, bool) {
	result := make(map[string][]beadInfo)

	if _, err := runner.Run("which", "bd"); err != nil {
		return result, false
	}

	// Read-only: bd list, in dir when given (another project's root), else
	// in the folder status runs in.
	list := func(args ...string) (string, error) {
		if dir != "" {
			return runner.RunInDir(dir, "bd", args...)
		}
		return runner.Run("bd", args...)
	}
	out, err := list("list", "--status", strings.Join(shownStatuses, ","), "--json")
	if err != nil {
		out, err = list("list", "--json")
		if err != nil {
			return result, false
		}
	}

	var beads []beadInfo
	answered := true
	if err := json.Unmarshal([]byte(out), &beads); err != nil {
		answered = false
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var b beadInfo
			if err := json.Unmarshal([]byte(line), &b); err == nil {
				beads = append(beads, b)
				answered = true
			}
		}
	}

	for _, b := range beads {
		if b.Assignee != "" && isShownStatus(b.Status) {
			result[b.Assignee] = append(result[b.Assignee], b)
		}
	}

	return result, answered
}

// shortDur renders a latch age compactly: 47m, 2h, 8s.
// beadNoteUnknown fills the Bead column when the socket's project cannot be
// read: its initech did not say where it lives, or that folder is not here.
const beadNoteUnknown = "(bd: unknown project)"

// socketIsAnotherProject reports whether INITECH_SOCKET points away from the
// project of the folder status runs in. Every agent's own INITECH_SOCKET
// names its own fleet's socket, and that is no override.
func socketIsAnotherProject(sockPath string, p *config.Project) bool {
	if os.Getenv("INITECH_SOCKET") == "" {
		return false
	}
	if p != nil && filepath.Clean(tui.SocketPath(p.Root, p.Name)) == filepath.Clean(sockPath) {
		return false
	}
	return true
}

// socketBeadRoot is where to read the socket project's bd, or the note when
// that is unknown: no project in the reply, no root, or a root not on this
// machine.
func socketBeadRoot(proj *tui.IPCProject) (dir, note string) {
	if proj == nil || proj.Root == "" {
		return "", beadNoteUnknown
	}
	if st, err := os.Stat(proj.Root); err != nil || !st.IsDir() {
		return "", beadNoteUnknown
	}
	return proj.Root, ""
}

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
