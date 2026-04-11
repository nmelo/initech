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

var deliverCmd = &cobra.Command{
	Use:   "deliver <bead-id>",
	Short: "Complete a bead: update status, clear TUI, report to super",
	Long: `Atomic completion: combines bd update, bd comment, initech bead --clear,
and initech send in one command. Counterpart to initech assign.

  initech deliver ini-abc                              # mark ready_for_qa, report to super
  initech deliver ini-abc --fail --reason "tests fail"  # stay in_progress, report failure
  initech deliver ini-abc --to qa1                      # report to qa1 instead of super
  initech deliver ini-abc --message "also fixed lint"   # append note to report

Fail-fast ordering: bd operations first (durable state), TUI bead clear
second (cosmetic), report/announce last (notifications). A partial failure
leaves the bead in the correct status even if notifications fail.

Requires bd and a running initech TUI.`,
	Args: cobra.ExactArgs(1),
	RunE: runDeliver,
}

var (
	deliverFail    bool
	deliverPass    bool
	deliverReason  string
	deliverTo      string
	deliverMessage string
)

func init() {
	deliverCmd.Flags().BoolVar(&deliverPass, "pass", false, "Mark ready_for_qa (default behavior)")
	deliverCmd.Flags().BoolVar(&deliverFail, "fail", false, "Stay in_progress, report failure")
	deliverCmd.Flags().StringVar(&deliverReason, "reason", "", "Failure reason (used with --fail)")
	deliverCmd.Flags().StringVar(&deliverTo, "to", "super", "Agent to report to (default: super)")
	deliverCmd.Flags().StringVarP(&deliverMessage, "message", "m", "", "Custom note appended to the report")
	rootCmd.AddCommand(deliverCmd)
}

func runDeliver(cmd *cobra.Command, args []string) error {
	beadID := args[0]

	if deliverFail && deliverPass {
		return fmt.Errorf("cannot specify both --pass and --fail")
	}
	// Default to pass if neither specified.
	isFail := deliverFail

	agent := os.Getenv("INITECH_AGENT")

	// Parse host:agent for the --to recipient.
	recipient := deliverTo
	var recipientHost string
	if idx := strings.Index(recipient, ":"); idx >= 0 {
		recipientHost = recipient[:idx]
		recipient = recipient[idx+1:]
	}

	// Step 1: Read bead info (fail fast if bead not found).
	title, assignee, err := bdShowBead(beadID)
	if err != nil {
		return err
	}

	// Step 1b: Verify caller is assignee (warn only, not error).
	if agent != "" && assignee != "" && agent != assignee {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: bead assigned to %s, you are %s\n", assignee, agent)
	}

	// Step 2: Update bead status.
	if isFail {
		reason := deliverReason
		if reason == "" {
			reason = "no reason provided"
		}
		// Stay in_progress, add failure comment.
		if err := bdCommentAdd(beadID, agent, "FAILED: "+reason); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: bd comment failed: %s\n", err)
		}
	} else {
		if err := bdUpdateStatus(beadID, "ready_for_qa"); err != nil {
			return err
		}
	}

	// Step 3: Clear TUI bead display (cosmetic, warn on failure).
	if agent != "" {
		clearReq := tui.IPCRequest{
			Action: "bead",
			Target: agent,
			Text:   "",
		}
		if resp, err := ipcCall(clearReq); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not clear TUI bead (is initech running?)\n")
		} else if !resp.OK {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: TUI bead clear: %s\n", resp.Error)
		}
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: INITECH_AGENT not set, skipping TUI bead clear\n")
	}

	// Step 4: Send report to recipient.
	displayTitle := truncateTitle(title, 80)
	var report string
	if isFail {
		reason := deliverReason
		if reason == "" {
			reason = "no reason provided"
		}
		report = fmt.Sprintf("[from %s] %s: %s FAILED: %s", agentOrUnknown(agent), beadID, displayTitle, reason)
	} else {
		report = fmt.Sprintf("[from %s] %s: %s ready for QA", agentOrUnknown(agent), beadID, displayTitle)
	}
	if deliverMessage != "" {
		report += ". " + deliverMessage
	}

	sendReq := tui.IPCRequest{
		Action: "send",
		Target: recipient,
		Host:   recipientHost,
		Text:   report,
		Enter:  true,
	}
	if resp, err := ipcCall(sendReq); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: report failed, notify %s manually: %s\n", deliverTo, err)
	} else if !resp.OK {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: report failed: %s\n", resp.Error)
	}

	// Step 5: Announce to Agent Radio (fire and forget).
	announceDeliver(cmd, agent, beadID, displayTitle, isFail, deliverReason)

	// Step 6: Post to webhook (fire and forget).
	webhookDeliver(cmd, agent, beadID, displayTitle, isFail, deliverReason)

	// Step 7: Emit event to TUI (fire and forget).
	if isFail {
		reason := deliverReason
		if reason == "" {
			reason = "no reason provided"
		}
		emitIPCEvent(agentOrUnknown(agent), beadID, "bead_delivered",
			fmt.Sprintf("%s failed %s: %s", agentOrUnknown(agent), beadID, reason))
	} else {
		emitIPCEvent(agentOrUnknown(agent), beadID, "bead_delivered",
			fmt.Sprintf("%s delivered %s (ready for QA)", agentOrUnknown(agent), beadID))
	}

	// Output summary.
	if isFail {
		reason := deliverReason
		if reason == "" {
			reason = "no reason provided"
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "delivered %s (FAILED: %s) -> %s\n", beadID, reason, deliverTo)
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "delivered %s (ready for QA) -> %s\n", beadID, deliverTo)
	}
	return nil
}

// bdShowBead reads bead info and returns title and assignee.
func bdShowBead(beadID string) (title, assignee string, err error) {
	out, err := exec.Command("bd", "show", beadID, "--json").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("bead %s not found: %s", beadID, strings.TrimSpace(string(out)))
	}
	var beads []struct {
		Title    string `json:"title"`
		Assignee string `json:"assignee"`
	}
	if err := json.Unmarshal(out, &beads); err != nil {
		return "", "", fmt.Errorf("parse bd output: %w", err)
	}
	if len(beads) == 0 {
		return beadID, "", nil
	}
	t := beads[0].Title
	if t == "" {
		t = beadID
	}
	return t, beads[0].Assignee, nil
}

// bdUpdateStatus runs bd update to set bead status.
func bdUpdateStatus(beadID, status string) error {
	out, err := exec.Command("bd", "update", beadID, "--status", status).CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd update failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// bdCommentAdd runs bd comments add on a bead.
func bdCommentAdd(beadID, author, comment string) error {
	args := []string{"comments", "add", beadID}
	if author != "" {
		args = append(args, "--author", author)
	}
	args = append(args, comment)
	out, err := exec.Command("bd", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd comments add failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func agentOrUnknown(agent string) string {
	if agent == "" {
		return "unknown"
	}
	return agent
}

// announceDeliver posts a completion/failure announcement to Agent Radio.
func announceDeliver(cmd *cobra.Command, agent, beadID, title string, isFail bool, reason string) {
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

	var detail, kind string
	if isFail {
		kind = "agent.failed"
		detail = fmt.Sprintf("%s hit a wall on %s: %s", agentOrUnknown(agent), beadID, reason)
		if reason == "" {
			detail = fmt.Sprintf("%s hit a wall on %s", agentOrUnknown(agent), beadID)
		}
	} else {
		kind = "agent.completed"
		detail = fmt.Sprintf("%s finished %s: %s", agentOrUnknown(agent), beadID, title)
	}

	result := webhook.PostAnnouncement(p.AnnounceURL, webhook.AnnouncePayload{
		Detail:  detail,
		Kind:    kind,
		Agent:   agentOrUnknown(agent),
		Project: p.Name,
		BeadID:  beadID,
	})
	if result.Err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: announce failed: %s\n", result.Err)
	}
}

// webhookDeliver posts a completion/failure notification to the webhook.
func webhookDeliver(cmd *cobra.Command, agent, beadID, title string, isFail bool, reason string) {
	wd, err := os.Getwd()
	if err != nil {
		return
	}
	cfgPath, err := config.Discover(wd)
	if err != nil {
		return
	}
	p, err := config.Load(cfgPath)
	if err != nil || p.WebhookURL == "" {
		return
	}

	var kind, message string
	if isFail {
		kind = "agent.failed"
		message = fmt.Sprintf("%s: %s FAILED", beadID, title)
		if reason != "" {
			message += ": " + reason
		}
	} else {
		kind = "agent.completed"
		message = fmt.Sprintf("%s: %s ready for QA", beadID, title)
	}

	if err := webhook.PostNotification(p.WebhookURL, kind, agentOrUnknown(agent), message, p.Name); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: webhook failed: %s\n", err)
	}
}
