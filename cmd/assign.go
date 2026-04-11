package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
	"github.com/nmelo/initech/internal/webhook"
	"github.com/spf13/cobra"
)

var assignCmd = &cobra.Command{
	Use:   "assign <agent> <bead-id> [<bead-id>...]",
	Short: "Claim beads, register in the TUI, and dispatch to an agent",
	Long: `Atomic dispatch: combines bd update, initech bead, and initech send in one
command. Auto-generates a dispatch message from bead titles.

  initech assign eng1 ini-abc
  initech assign eng1 ini-abc ini-def ini-ghi
  initech assign eng2 ini-xyz --message "Focus on the error handling edge cases."

Multiple beads are claimed individually (partial failures are logged and
skipped), then dispatched as one consolidated message. Exit 0 if at least
one bead succeeds, exit 1 if all fail.

Requires bd and a running initech TUI.`,
	Args: cobra.MinimumNArgs(2),
	RunE: runAssign,
}

var assignMessage string

func init() {
	assignCmd.Flags().StringVarP(&assignMessage, "message", "m", "", "Custom instructions appended to the dispatch message")
	rootCmd.AddCommand(assignCmd)
}

// bdShowTitle runs bd show --json and extracts the title field.
func bdShowTitle(beadID string) (string, error) {
	out, err := exec.Command("bd", "show", beadID, "--json").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bead %s not found: %s", beadID, strings.TrimSpace(string(out)))
	}
	var beads []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &beads); err != nil {
		return "", fmt.Errorf("parse bd output: %w", err)
	}
	if len(beads) == 0 || beads[0].Title == "" {
		return "", fmt.Errorf("bead %s has no title", beadID)
	}
	return beads[0].Title, nil
}

// bdUpdateClaim runs bd update to set status and assignee.
func bdUpdateClaim(beadID, agent string) error {
	out, err := exec.Command("bd", "update", beadID, "--status", "in_progress", "--assignee", agent).CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd update failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// truncateTitle truncates a title to maxLen characters, adding "..." if truncated.
func truncateTitle(title string, maxLen int) string {
	if len(title) <= maxLen {
		return title
	}
	return title[:maxLen-3] + "..."
}

// assignResult holds the outcome of a single bead assignment.
type assignResult struct {
	id    string
	title string
}

func runAssign(cmd *cobra.Command, args []string) error {
	agent := args[0]
	beadIDs := args[1:]

	// Parse host:agent for cross-machine routing on the send step.
	var host string
	if idx := strings.Index(agent, ":"); idx >= 0 {
		host = agent[:idx]
		agent = agent[idx+1:]
	}

	// Deduplicate bead IDs.
	seen := make(map[string]bool, len(beadIDs))
	unique := make([]string, 0, len(beadIDs))
	for _, id := range beadIDs {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	beadIDs = unique

	// Process each bead: show + claim. Failures logged and skipped.
	var successes []assignResult
	var failures []string
	for _, id := range beadIDs {
		title, err := bdShowTitle(id)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", err)
			failures = append(failures, id)
			continue
		}
		if err := bdUpdateClaim(id, agent); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %s\n", id, err)
			failures = append(failures, id)
			continue
		}
		successes = append(successes, assignResult{id: id, title: truncateTitle(title, 80)})
	}

	if len(successes) == 0 {
		return fmt.Errorf("no beads could be assigned")
	}

	// Register beads in TUI (cosmetic, warn on failure).
	// Single bead: "id\ttitle" for ribbon display. Multi: "id1,id2" (no titles).
	successIDs := make([]string, len(successes))
	for i, s := range successes {
		successIDs[i] = s.id
	}
	var beadText string
	if len(successes) == 1 {
		beadText = successes[0].id + "\t" + successes[0].title
	} else {
		beadText = strings.Join(successIDs, ",")
	}
	beadReq := tui.IPCRequest{
		Action: "bead",
		Target: agent,
		Text:   beadText,
	}
	if resp, err := ipcCall(beadReq); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not register bead in TUI (is initech running?)\n")
	} else if !resp.OK {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: TUI bead registration: %s\n", resp.Error)
	}

	// Build consolidated dispatch message.
	dispatch := buildDispatchMessage(successes, assignMessage)

	sendReq := tui.IPCRequest{
		Action: "send",
		Target: agent,
		Host:   host,
		Text:   dispatch,
		Enter:  true,
	}
	resp, err := ipcCall(sendReq)
	if err != nil {
		return fmt.Errorf("beads claimed but dispatch failed: %w\nRun: initech send %s to notify manually", err, agent)
	}
	if !resp.OK {
		return fmt.Errorf("beads claimed but dispatch failed: %s\nRun: initech send %s to notify manually", resp.Error, agent)
	}

	// Announce to Agent Radio (fire and forget).
	announceAssignment(cmd, agent, successes)

	// Emit events to TUI (fire and forget, one per bead).
	for _, s := range successes {
		emitIPCEvent(agent, s.id, "bead_assigned", fmt.Sprintf("assigned to %s: %s", agent, s.title))
	}

	// Print summary to stderr.
	summary := fmt.Sprintf("assigned %d bead(s) to %s: %s", len(successes), agent, strings.Join(successIDs, ", "))
	if len(failures) > 0 {
		summary += fmt.Sprintf(" (failed: %s)", strings.Join(failures, ", "))
	}
	fmt.Fprintln(cmd.ErrOrStderr(), summary)
	return nil
}

// emitIPCEvent fires a typed event into the TUI event system. Best-effort;
// failures are silently ignored (the event is supplementary, not critical).
func emitIPCEvent(agent, beadID, eventType, detail string) {
	req := tui.IPCRequest{
		Action: "emit_event",
		Target: agent,
		Host:   beadID,
		Text:   eventType + "|" + detail,
	}
	ipcCall(req) //nolint:errcheck
}

// buildDispatchMessage creates the dispatch text sent to the agent.
func buildDispatchMessage(successes []assignResult, message string) string {
	if len(successes) == 1 {
		// Single bead: compact format (backwards compatible).
		s := successes[0]
		dispatch := fmt.Sprintf("[from super] %s: %s. Read bd show %s for full AC.", s.id, s.title, s.id)
		if message != "" {
			dispatch += " " + message
		}
		return dispatch
	}

	// Multiple beads: list format.
	var b strings.Builder
	fmt.Fprintf(&b, "[from super] Assigned %d beads:", len(successes))

	showCount := len(successes)
	truncated := 0
	if showCount > 5 {
		truncated = showCount - 5
		showCount = 5
	}
	for i := 0; i < showCount; i++ {
		s := successes[i]
		fmt.Fprintf(&b, "\n- %s: %s", s.id, s.title)
	}
	if truncated > 0 {
		fmt.Fprintf(&b, "\n... and %d more. Run bd list --assignee <self> for full list.", truncated)
	} else {
		fmt.Fprintf(&b, "\nRead bd show <id> for full AC on each.")
	}
	if message != "" {
		b.WriteString("\n" + message)
	}
	return b.String()
}

// announceAssignment posts a single announcement for the batch assignment.
func announceAssignment(cmd *cobra.Command, agent string, successes []assignResult) {
	wd, err := os.Getwd()
	if err != nil {
		return
	}
	cfgPath, err := config.Discover(wd)
	if err != nil {
		return
	}
	p, err := config.Load(cfgPath)
	if err != nil || p.AnnounceURL == "" {
		return
	}

	ids := make([]string, len(successes))
	for i, s := range successes {
		ids[i] = s.id
	}

	var detail string
	if len(successes) == 1 {
		detail = fmt.Sprintf("%s picking up: %s", agent, successes[0].title)
	} else {
		detail = fmt.Sprintf("%s assigned %d beads", agent, len(successes))
	}

	payload := webhook.AnnouncePayload{
		Detail:  detail,
		Kind:    "agent.started",
		Agent:   agent,
		Project: p.Name,
		BeadID:  successes[0].id,
	}
	result := webhook.PostAnnouncement(p.AnnounceURL, payload)
	if result.Err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: announce failed: %s\n", result.Err)
	}
}
