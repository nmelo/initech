package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/nmelo/initech/internal/lifecycle"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var assignCmd = &cobra.Command{
	Use:   "assign <agent> [<bead-id>...] [-m <message>]",
	Short: "Claim beads, register in the TUI, and dispatch to an agent",
	Long: `Atomic dispatch: combines bd update, initech bead, and initech send in one
command. Auto-generates a dispatch message from bead titles.

  initech assign eng1 ini-abc                     First dispatch
  initech assign eng1 ini-abc ini-def ini-ghi     Several beads in one message
  initech assign eng1 ini-abc -m "fold ini-x in"  Fold-in: eng1 already has ini-abc
  initech assign eng3 ini-abc --move              Take ini-abc from the agent working on it
  initech assign eng3 -m "bump main to 0.28.0"    No bead: small work with no ticket

With no bead, -m is required and is the whole message: it is delivered
exactly as initech send would deliver it, nothing is written to bd, and the
agent's ribbon shows "no bead" and the message's first words until the next
bead is registered for it (assign or initech bead) or initech bead --clear.

assign looks at each bead before writing it:
  already with this agent  -> fold-in: no bd write; the ribbon gains the bead
                              if it lacks it and keeps the others; the
                              message is delivered
  in progress or in QA     -> refused, naming who has it; --move reassigns it,
  with another agent          takes it off that agent's ribbon and tells them
  closed                   -> refused; follow-on work gets its own bead
  anything else            -> dispatched

The status written comes from the lifecycle transition table
(internal/lifecycle), keyed by the TARGET's role:
  a validator (qa*)        -> in_qa
  anyone else              -> in_progress, recording them as the implementer
so a dispatched bead never misreports what is happening to it.

Multiple beads are claimed individually (partial failures are logged and
skipped), then dispatched as one consolidated message. Exit 0 if at least
one bead succeeds, exit 1 if all fail.

Requires bd and a running initech TUI.`,
	Args: assignArgs,
	RunE: runAssign,
	// Execute prints the returned error once; cobra's own "Error:" copy would
	// make a refusal two lines.
	SilenceErrors: true,
}

var assignMessage string
var assignMove bool

func init() {
	assignCmd.Flags().StringVarP(&assignMessage, "message", "m", "", "Custom instructions appended to the dispatch message; with no bead, the whole message")
	assignCmd.Flags().BoolVar(&assignMove, "move", false, "Reassign a bead another agent is working on (it leaves their ribbon and they are told)")
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
	kind  assignKind
	// holder is the agent a moved bead was taken from.
	holder string
}

// assignKind is what assign does with one bead, decided from its bd state.
type assignKind int

const (
	// assignDispatch writes the bead to the target, as assign always has.
	assignDispatch assignKind = iota
	// assignFoldIn: the bead is already with the target; no bd write.
	assignFoldIn
	// assignMoved: the bead is taken from another agent (--move).
	assignMoved
	// assignRefused: nothing is written; reason says why.
	assignRefused
)

// assignDecision is classifyAssign's answer for one bead.
type assignDecision struct {
	kind   assignKind
	holder string
	reason string
}

// classifyAssign decides what assign does with a bead from what bd says about
// it. dispatchStatus is the status the lifecycle table writes for the
// target's role; a bead already in that status with the target is a fold-in.
// A bead in progress or in QA with a DIFFERENT agent is never taken silently:
// it is refused unless move is set. A closed bead is refused: follow-on work
// gets its own bead. Everything else, including a bead bd reports no status
// or assignee for, is dispatched exactly as before.
func classifyAssign(id, target, status, assignee, dispatchStatus string, move bool) assignDecision {
	if status == "closed" {
		return assignDecision{kind: assignRefused, reason: fmt.Sprintf("%s is closed; follow-on work gets its own bead", id)}
	}
	if assignee == target && status == dispatchStatus {
		return assignDecision{kind: assignFoldIn}
	}
	if (status == "in_progress" || status == "in_qa") && assignee != "" && assignee != target {
		if !move {
			where := "in progress"
			if status == "in_qa" {
				where = "in QA"
			}
			return assignDecision{kind: assignRefused, reason: fmt.Sprintf("%s is %s with %s; add --move to reassign", id, where, assignee)}
		}
		return assignDecision{kind: assignMoved, holder: assignee}
	}
	return assignDecision{kind: assignDispatch}
}

// assignArgs requires an agent, and then at least one bead or a non-blank
// -m: assign with neither has nothing to dispatch.
func assignArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("assign needs an agent, then bead ids or -m \"message\"")
	}
	if len(args) == 1 && strings.TrimSpace(assignMessage) == "" {
		return fmt.Errorf("assign %s: give bead ids, or -m \"message\" for work with no bead", args[0])
	}
	return nil
}

func runAssign(cmd *cobra.Command, args []string) error {
	// Past argument parsing, a failure is about the beads, not the usage.
	cmd.SilenceUsage = true
	agent := args[0]
	beadIDs := args[1:]

	// Parse host:agent for cross-machine routing on the send step.
	var host string
	if idx := strings.Index(agent, ":"); idx >= 0 {
		host = agent[:idx]
		agent = agent[idx+1:]
	}

	if len(beadIDs) == 0 {
		return assignNoBead(cmd.ErrOrStderr(), agent, host, assignMessage)
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

	// Is the agent able to receive the dispatch right now? Asked BEFORE any bd
	// write (ini-i35w), so a claim never outlives an undelivered message: an
	// agent still booting after initech start used to end up with a claimed,
	// in_progress bead and no message, and the board said someone was working.
	if err := preflightSendReady(agent, host); err != nil {
		return err
	}

	// Process each bead: show + dispatch. Failures logged and skipped.
	//
	// bd FIRST, TUI SECOND, and the order is load-bearing: a bead whose bd
	// write is refused is skipped here and never reaches the TUI pointer or
	// the dispatch message, so a refusal cannot leave the bead half-updated
	// (pointer set, state unchanged).
	var successes []assignResult
	var failures []string
	var refusals []string
	tr := lifecycle.Dispatch(family, "")
	for _, id := range beadIDs {
		title, assignee, status, err := bdShowBeadFn(id)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", err)
			failures = append(failures, id)
			continue
		}
		d := classifyAssign(id, agent, status, assignee, tr.Status, assignMove)
		if d.kind == assignRefused {
			refusals = append(refusals, d.reason)
			failures = append(failures, id)
			continue
		}
		if d.kind != assignFoldIn {
			if err := bdDispatchFn(id, agent, tr.Status, tr.RecordImplementer); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %s\n", id, err)
				failures = append(failures, id)
				continue
			}
		}
		successes = append(successes, assignResult{id: id, title: truncateTitle(title, 80), kind: d.kind, holder: d.holder})
	}

	if len(successes) == 0 {
		// One refused bead is one line: the reason, with nothing else after it.
		if len(beadIDs) == 1 && len(refusals) == 1 {
			return errors.New(refusals[0])
		}
		for _, r := range refusals {
			fmt.Fprintln(cmd.ErrOrStderr(), r)
		}
		return fmt.Errorf("no beads could be assigned")
	}
	for _, r := range refusals {
		fmt.Fprintln(cmd.ErrOrStderr(), r)
	}

	successIDs := make([]string, len(successes))
	for i, s := range successes {
		successIDs[i] = s.id
	}
	updateAssignRibbons(cmd.ErrOrStderr(), agent, host, successes)

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

	// The previous holder of a moved bead hears it from initech, after the
	// target has its message (best-effort: a warning, never a failed assign).
	notifyMovedHolders(cmd.ErrOrStderr(), agent, successes)

	// Emit events to TUI (fire and forget, one per bead).
	for _, s := range successes {
		detail := fmt.Sprintf("assigned to %s: %s", agent, s.title)
		switch s.kind {
		case assignFoldIn:
			detail = fmt.Sprintf("fold-in, assigned to %s: %s", agent, s.title)
		case assignMoved:
			detail = fmt.Sprintf("moved from %s to %s: %s", s.holder, agent, s.title)
		}
		emitIPCEvent(agent, s.id, "bead_assigned", detail)
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
// preflightSendReady asks the TUI whether a send to agent would be delivered
// now; the TUI waits on the agent's startup gate before answering. Two
// answers stop the assign: an explicit "still starting", and NO ANSWER in
// time -- a TUI that cannot answer within its own settle bound cannot be
// trusted to deliver either (qa2: a timeout read as "go ahead" claimed the
// bead anyway). What it cannot judge -- no TUI at all, an older TUI without
// the action, a cross-machine target -- proceeds exactly as assign always
// has, and the dispatch send reports its own fate.
func preflightSendReady(agent, host string) error {
	if host != "" {
		return nil
	}
	resp, err := ipcCall(tui.IPCRequest{Action: "send_ready", Target: agent})
	if err != nil {
		if errors.Is(err, errIPCTimeout) {
			return fmt.Errorf("nothing assigned: %s could not be confirmed ready to receive (%v)\nRetry when it is ready (initech peek %s).", agent, err, agent)
		}
		return nil
	}
	if resp.OK {
		return nil
	}
	if strings.HasPrefix(resp.Error, tui.ErrPaneStillStarting.Error()) {
		return fmt.Errorf("nothing assigned: %s\nRetry when %s is ready (initech peek %s).", resp.Error, agent, agent)
	}
	// A stopped or exited agent (ini-2s7q): the reply names the command that
	// brings it back.
	if strings.HasPrefix(resp.Error, tui.ErrPaneNotRunning.Error()) {
		return fmt.Errorf("nothing assigned: %s", resp.Error)
	}
	return nil
}

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
