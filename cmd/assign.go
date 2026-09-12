package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/nmelo/initech/internal/lifecycle"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/tui"
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

The status written comes from the lifecycle transition table
(internal/lifecycle), keyed by the TARGET's role:
  a validator (qa*)        -> in_qa
  anyone else              -> in_progress, recording them as the implementer
so a dispatched bead never misreports what is happening to it.

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

// bdShowTitleFn is the default implementation. Tests override this.
var bdShowTitleFn = bdShowTitleImpl

// bdShowTitleImpl runs bd show --json and extracts the title field.
func bdShowTitleImpl(beadID string) (string, error) {
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

// bdDispatchFn is the default implementation. Tests override this.
var bdDispatchFn = bdDispatchImpl

// bdDispatchImpl writes the dispatch the lifecycle table asked for: the
// target status for this role, the assignee, and — for an implementer
// dispatch — the record of who is building the bead.
//
// It does NOT use `bd update --claim`, and that is deliberate: --claim is
// refused on a ready_for_qa bead ("issue not claimable", measured on bd
// 1.0.5), which is exactly the state a validator receives beads in (GitHub
// #34). An explicit status+assignee write succeeds from any state.
func bdDispatchImpl(beadID, agent, status string, recordImplementer bool) error {
	args := []string{"update", beadID, "--status", status, "--assignee", agent}
	if recordImplementer {
		args = append(args, "--set-metadata", implementerKey+"="+agent)
	}
	out, err := exec.Command("bd", args...).CombinedOutput()
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

	// THE TABLE DECIDES the dispatch state, not this command (ini-1fb9).
	// assign used to write in_progress whatever the target's role, so a
	// validator dispatch misreported the bead from dispatch until the
	// validator self-corrected (GitHub #34). Keyed by the TARGET's family:
	// the roster load is best-effort, since eng*/qa* classify on their
	// prefix alone and an unclassifiable name lands on the implementer row,
	// which is the behaviour assign has always had for it.
	roster, _ := loadProjectRoster()
	family := roles.RoleFamilyOfWithRoster(agent, roster)

	// Process each bead: show + dispatch. Failures logged and skipped.
	//
	// bd FIRST, TUI SECOND, and the order is load-bearing: a bead whose bd
	// write is refused is skipped here and never reaches the TUI pointer or
	// the dispatch message, so a refusal cannot leave the bead half-updated
	// (pointer set, state unchanged).
	var successes []assignResult
	var failures []string
	for _, id := range beadIDs {
		title, err := bdShowTitleFn(id)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", err)
			failures = append(failures, id)
			continue
		}
		tr := lifecycle.Dispatch(family, "")
		if err := bdDispatchFn(id, agent, tr.Status, tr.RecordImplementer); err != nil {
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
