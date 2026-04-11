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
	Use:   "assign <agent> <bead-id>",
	Short: "Claim a bead, register it in the TUI, and dispatch to an agent",
	Long: `Atomic dispatch: combines bd update, initech bead, and initech send in one
command. Auto-generates a dispatch message from the bead title.

  initech assign eng1 ini-abc
  initech assign eng2 ini-xyz --message "Focus on the error handling edge cases."

Fail-fast ordering: bd operations first (durable state), TUI bead display
second (cosmetic), dispatch last (notification). If dispatch fails, the bead
is still claimed and you can notify manually.

Requires bd and a running initech TUI.`,
	Args: cobra.ExactArgs(2),
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

func runAssign(cmd *cobra.Command, args []string) error {
	agent := args[0]
	beadID := args[1]

	// Parse host:agent for cross-machine routing on the send step.
	var host string
	if idx := strings.Index(agent, ":"); idx >= 0 {
		host = agent[:idx]
		agent = agent[idx+1:]
	}

	// Step 1: Read bead title (fail fast if bead not found).
	title, err := bdShowTitle(beadID)
	if err != nil {
		return err
	}

	// Step 2: Claim bead in bd (fail fast if already claimed/conflict).
	if err := bdUpdateClaim(beadID, agent); err != nil {
		return err
	}

	// Step 3: Register bead in TUI (cosmetic, warn on failure).
	beadReq := tui.IPCRequest{
		Action: "bead",
		Target: agent,
		Text:   beadID,
	}
	if resp, err := ipcCall(beadReq); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not register bead in TUI (is initech running?)\n")
	} else if !resp.OK {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: TUI bead registration: %s\n", resp.Error)
	}

	// Step 4: Send dispatch message to agent.
	dispatchTitle := truncateTitle(title, 80)
	dispatch := fmt.Sprintf("[from super] %s: %s. Read bd show %s for full AC.", beadID, dispatchTitle, beadID)
	if assignMessage != "" {
		dispatch += " " + assignMessage
	}

	sendReq := tui.IPCRequest{
		Action: "send",
		Target: agent,
		Host:   host,
		Text:   dispatch,
		Enter:  true,
	}
	resp, err := ipcCall(sendReq)
	if err != nil {
		return fmt.Errorf("bead claimed but dispatch failed: %w\nRun: initech send %s to notify manually", err, agent)
	}
	if !resp.OK {
		return fmt.Errorf("bead claimed but dispatch failed: %s\nRun: initech send %s to notify manually", resp.Error, agent)
	}

	// Step 5: Announce to Agent Radio (fire and forget).
	announceDispatch(cmd, agent, beadID, dispatchTitle)

	fmt.Fprintf(cmd.ErrOrStderr(), "assigned %s to %s: %s\n", beadID, agent, dispatchTitle)
	return nil
}

// announceDispatch posts an agent.started announcement to Agent Radio if
// announce_url is configured. Failures are logged as warnings and never
// block the assign command.
func announceDispatch(cmd *cobra.Command, agent, beadID, title string) {
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

	detail := fmt.Sprintf("%s picking up %s: %s", agent, beadID, title)
	payload := webhook.AnnouncePayload{
		Detail:  detail,
		Kind:    "agent.started",
		Agent:   agent,
		Project: p.Name,
		BeadID:  beadID,
	}
	result := webhook.PostAnnouncement(p.AnnounceURL, payload)
	if result.Err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: announce failed: %s\n", result.Err)
	}
}
