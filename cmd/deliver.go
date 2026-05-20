package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/lifecycle"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/tui"
	"github.com/nmelo/initech/internal/webhook"
	"github.com/spf13/cobra"
)

var deliverCmd = &cobra.Command{
	Use:   "deliver <bead-id>",
	Short: "Complete a bead: update status, clear TUI, report to super",
	Long: `Atomic completion: combines bd update, bd comment, initech bead --clear,
and initech send in one command. Counterpart to initech assign.

  initech deliver ini-abc                              # eng: mark ready_for_qa, report to super
  initech deliver ini-abc -m "DONE: <body>"            # eng: success + DONE comment on bead
  initech deliver ini-abc --fail --reason "tests fail"  # eng: stay in_progress, report failure
  initech deliver ini-abc --verdict PASS                # qa: announce PASS verdict
  initech deliver ini-abc --verdict FAIL --reason X     # qa: announce FAIL verdict
  initech deliver ini-abc --to qa1                      # report to qa1 instead of super
  initech deliver ini-abc --as qa2 --verdict PASS       # override INITECH_AGENT (rare)

The notification template is selected from the caller's role family:
  eng*       -> "ready for QA" / "FAILED" (current behavior)
  qa*        -> "PASS:" / "FAIL:" — --verdict PASS|FAIL is required
  others     -> "delivered:" / "delivery failed:"
Unknown roles error rather than silently using the engineer template.

--message / -m semantics:
  - On the SUCCESS path, the body is written verbatim as a bd comment AND
    appended to the chat report. Empty -m skips the comment-add. The body
    is user-controlled, so verdict/role prefixes (e.g. "PASS: ", "DONE: ")
    are the caller's responsibility.
  - On the --fail path, the body is appended to the chat report only; the
    bead's audit-trail comment is "FAILED: <reason>" from --reason, not
    -m. This avoids a double comment on a single failure event.
  - If a caller already ran bd comments add manually before deliver, an
    -m on the same call will produce a duplicate comment; engineers are
    expected to pick one path.

Fail-fast ordering: input validation first (rejects bad flag combos before
any side effects), bd operations second (durable state), TUI bead clear
third (cosmetic), report/announce last (notifications). A partial failure
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
	deliverVerdict string
	deliverAs      string
)

func init() {
	deliverCmd.Flags().BoolVar(&deliverPass, "pass", false, "Mark ready_for_qa (default behavior)")
	deliverCmd.Flags().BoolVar(&deliverFail, "fail", false, "Stay in_progress, report failure")
	deliverCmd.Flags().StringVar(&deliverReason, "reason", "", "Failure reason (used with --fail or --verdict FAIL)")
	deliverCmd.Flags().StringVar(&deliverTo, "to", "super", "Agent to report to (default: super)")
	deliverCmd.Flags().StringVarP(&deliverMessage, "message", "m", "", "Custom note appended to the chat report; on success, also written as a bd comment on the bead")
	deliverCmd.Flags().StringVar(&deliverVerdict, "verdict", "", "QA verdict: PASS or FAIL (required for qa* roles)")
	deliverCmd.Flags().StringVar(&deliverAs, "as", "", "Override caller role (default: INITECH_AGENT env var)")
	rootCmd.AddCommand(deliverCmd)
}

func runDeliver(cmd *cobra.Command, args []string) error {
	beadID := args[0]

	// Pre-validate flags before any side effects (bd writes, IPC, network).
	// All caller errors must surface here so downstream paths can trust the inputs.
	agent, family, verdict, isFail, err := validateDeliverFlags()
	if err != nil {
		return err
	}

	// Parse host:agent for the --to recipient.
	recipient := deliverTo
	var recipientHost string
	if idx := strings.Index(recipient, ":"); idx >= 0 {
		recipientHost = recipient[:idx]
		recipient = recipient[idx+1:]
	}

	// Step 1: Read bead info (fail fast if bead not found).
	title, assignee, status, err := bdShowBeadFn(beadID)
	if err != nil {
		return err
	}

	// Step 1b: Verify caller is assignee (warn only, not error).
	if agent != "" && assignee != "" && agent != assignee {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: bead assigned to %s, you are %s\n", assignee, agent)
	}

	// Step 2: Lifecycle walk (ini-6e54). bd's status.custom drives the chain
	// middle; deliver advances one step on success, walks back one step on
	// failure. Role/family does NOT gate the status write — anyone delivering
	// on a bead moves it. (Role/family still drives the announce template
	// selection downstream — that's eng2's ini-dgt.2 work and stays intact.)
	//
	// Terminal-state semantics:
	//   - At terminal (last in chain), success no-ops with a warning and
	//     returns. No announce/report fires; the bead is already done.
	//   - At initial (first in chain), --fail no-ops with a warning and
	//     returns. The bead can't regress further.
	//
	// On --fail (non-initial state): the FAILED audit comment AND the
	// walk-back state write are both performed (ini-6e54 Q2). The comment
	// is the reason; the walk-back is the action.
	chain, err := lifecycle.LoadChain()
	if err != nil {
		return fmt.Errorf("cannot determine bead lifecycle: %w (configure bd's status.custom or check that bd is reachable)", err)
	}

	// Family is read but not used for the status decision anymore. Keep the
	// reference so future role-aware policies (e.g., audit-only roles) can
	// re-enable it without changing the function signature.
	_ = family

	if isFail {
		target, canMove := lifecycle.PrevState(chain, status)
		if !canMove {
			fmt.Fprintf(cmd.ErrOrStderr(), "deliver --fail no-op for %s: bead is at initial state %q (no previous step in lifecycle)\n", beadID, status)
			return nil
		}
		// Walk back AND record the reason. Comment first so the audit
		// captures the intent even if the status write fails afterwards.
		reason := deliverReason
		if reason == "" {
			reason = "no reason provided"
		}
		if err := bdCommentAddFn(beadID, agent, "FAILED: "+reason); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: bd comment failed: %s\n", err)
		}
		if err := bdUpdateStatusFn(beadID, target); err != nil {
			return err
		}
	} else {
		target, canMove := lifecycle.NextState(chain, status)
		if !canMove {
			fmt.Fprintf(cmd.ErrOrStderr(), "deliver no-op for %s: bead is at terminal state %q\n", beadID, status)
			return nil
		}
		if err := bdUpdateStatusFn(beadID, target); err != nil {
			return err
		}
	}

	// Step 2.5: On the success path, if -m was provided, write the body as
	// a bd comment so the bead's audit trail records what was delivered
	// (ini-lwd: deliver previously only put the body in the chat report,
	// leaving 'ready_for_qa' beads with no DONE comment unless the engineer
	// ran 'bd comments add' manually beforehand). Body is user-controlled —
	// callers add their own "DONE: " / "PASS: " prefixes. Empty -m skips
	// the call. Failure-path comment ("FAILED: <reason>") is unchanged
	// above; -m on --fail stays chat-only to avoid a double comment.
	if !isFail && deliverMessage != "" {
		if err := bdCommentAddFn(beadID, agent, deliverMessage); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: bd comment failed: %s\n", err)
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

	// Step 3.5: Pick the per-family notification template. All four downstream
	// paths (report, announce, webhook, IPC) read from this single struct so
	// they cannot drift out of sync.
	displayTitle := truncateTitle(title, 80)
	tpl := selectTemplate(family, isFail, verdict, deliverReason, displayTitle, agent)

	// Step 4: Send report to recipient.
	report := fmt.Sprintf("[from %s] %s: %s", agentOrUnknown(agent), beadID, tpl.ReportText)
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
	announceDeliver(cmd, agent, beadID, tpl)

	// Step 6: Post to webhook (fire and forget).
	webhookDeliver(cmd, agent, beadID, tpl)

	// Step 7: Emit event to TUI (fire and forget).
	emitIPCEvent(agentOrUnknown(agent), beadID, "bead_delivered", tpl.IPCSummary)

	// Output summary.
	fmt.Fprintf(cmd.ErrOrStderr(), "delivered %s (%s) -> %s\n", beadID, tpl.SummarySuffix, deliverTo)
	return nil
}

// bdShowBeadFn is the default implementation of bdShowBead. Tests override this.
var bdShowBeadFn = bdShowBeadImpl

// bdShowBead reads bead info and returns title, assignee, and current status.
// Status is needed by the outer no-op guard in runDeliver; missing status
// (empty string) is treated as "not terminal" and proceeds normally.
func bdShowBeadImpl(beadID string) (title, assignee, status string, err error) {
	out, err := exec.Command("bd", "show", beadID, "--json").CombinedOutput()
	if err != nil {
		return "", "", "", fmt.Errorf("bead %s not found: %s", beadID, strings.TrimSpace(string(out)))
	}
	var beads []struct {
		Title    string `json:"title"`
		Assignee string `json:"assignee"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(out, &beads); err != nil {
		return "", "", "", fmt.Errorf("parse bd output: %w", err)
	}
	if len(beads) == 0 {
		return beadID, "", "", nil
	}
	t := beads[0].Title
	if t == "" {
		t = beadID
	}
	return t, beads[0].Assignee, beads[0].Status, nil
}

// bdUpdateStatusFn is the default implementation. Tests override this.
var bdUpdateStatusFn = bdUpdateStatusImpl

// bdUpdateStatusImpl runs bd update to set bead status.
func bdUpdateStatusImpl(beadID, status string) error {
	out, err := exec.Command("bd", "update", beadID, "--status", status).CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd update failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// bdCommentAddFn is the default implementation. Tests override this.
var bdCommentAddFn = bdCommentAddImpl

// bdCommentAddImpl runs bd comments add on a bead.
func bdCommentAddImpl(beadID, author, comment string) error {
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

// announceDeliver posts a completion/failure announcement to Agent Radio using
// the family-aware template. Fire-and-forget: returns silently if no announce
// URL is configured.
func announceDeliver(cmd *cobra.Command, agent, beadID string, tpl deliverTemplate) {
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

	result := webhook.PostAnnouncement(p.AnnounceURL, webhook.AnnouncePayload{
		Detail:  tpl.RadioDetail,
		Kind:    tpl.Kind,
		Agent:   agentOrUnknown(agent),
		Project: p.Name,
		BeadID:  beadID,
	})
	if result.Err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: announce failed: %s\n", result.Err)
	}
}

// webhookDeliver posts a completion/failure notification to the webhook using
// the family-aware template. Fire-and-forget: returns silently if no webhook
// URL is configured.
func webhookDeliver(cmd *cobra.Command, agent, beadID string, tpl deliverTemplate) {
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

	if err := webhook.PostNotification(p.WebhookURL, tpl.Kind, agentOrUnknown(agent), tpl.WebhookText, p.Name); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: webhook failed: %s\n", err)
	}
}

// deliverTemplate is the per-family notification bundle used by every downstream
// path in runDeliver. Selecting the template once and threading it through
// keeps the four notification surfaces (report, radio, webhook, IPC) byte-for-
// byte consistent for any (family, verdict, isFail) tuple.
type deliverTemplate struct {
	Kind          string // webhook event kind, e.g. agent.completed | agent.failed
	RadioDetail   string // TTS message body for Agent Radio
	WebhookText   string // short notification text for Slack/webhook
	ReportText    string // suffix for the [from X] <id>: ... line sent to recipient
	IPCSummary    string // summary string for the bead_delivered TUI event
	SummarySuffix string // parenthetical for the operator-facing stderr summary
}

// resolveDeliverAgent returns the effective agent name, preferring the --as
// flag override over the INITECH_AGENT env var. Returns the empty string if
// neither is set; callers must reject that case via validateDeliverFlags.
func resolveDeliverAgent() string {
	if deliverAs != "" {
		return deliverAs
	}
	return os.Getenv("INITECH_AGENT")
}

// loadProjectRoster reads the project's initech.yaml from the current
// working directory and returns its roles list. Errors are returned to the
// caller so validation logic can produce a precise message rather than a
// silent fallback. ini-98n: the roster is the third-tier source of truth for
// role classification, behind the built-in prefix and catalog tiers.
var loadProjectRoster = func() ([]string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	cfgPath, err := config.Discover(wd)
	if err != nil {
		return nil, fmt.Errorf("no initech.yaml found in %s or parents: %w", wd, err)
	}
	p, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", cfgPath, err)
	}
	return p.Roles, nil
}

// validateDeliverFlags resolves the caller, normalizes the verdict, and
// applies all per-family flag rules in one place so downstream code can trust
// the inputs without re-checking. Reads initech.yaml to discover the project
// roster for custom-role classification (ini-98n); no bd writes, no IPC, no
// network.
//
// Returns (agent, family, verdict, isFail, error). On error, none of the other
// values are meaningful.
func validateDeliverFlags() (agent string, family roles.RoleFamily, verdict string, isFail bool, err error) {
	if deliverFail && deliverPass {
		return "", "", "", false, fmt.Errorf("cannot specify both --pass and --fail")
	}

	agent = resolveDeliverAgent()

	// Roster load is best-effort: a missing/malformed initech.yaml only
	// matters if classification falls through to FamilyUnknown for a
	// non-empty agent that isn't prefix- or catalog-classified. Prefix and
	// catalog roles (eng1, qa1, pm, shipper, etc.) don't need the roster.
	roster, rosterErr := loadProjectRoster()
	family = roles.RoleFamilyOfWithRoster(agent, roster)

	verdict = strings.ToUpper(strings.TrimSpace(deliverVerdict))

	switch verdict {
	case "", "PASS", "FAIL":
		// ok
	default:
		return "", "", "", false, fmt.Errorf("--verdict must be PASS or FAIL, got %q", deliverVerdict)
	}

	// --verdict and --fail must agree if both supplied.
	if verdict == "PASS" && deliverFail {
		return "", "", "", false, fmt.Errorf("--verdict PASS conflicts with --fail")
	}

	// Effective failure state: explicit --fail OR --verdict FAIL.
	isFail = deliverFail || verdict == "FAIL"

	switch family {
	case roles.FamilyUnknown:
		if agent == "" {
			return "", "", "", false, fmt.Errorf("cannot detect role: INITECH_AGENT not set and --as not provided")
		}
		if rosterErr != nil {
			return "", "", "", false, fmt.Errorf("cannot validate role %q: %w", agent, rosterErr)
		}
		return "", "", "", false, fmt.Errorf("role %q not in initech.yaml roster (known: %s); add it to the roster or check the spelling", agent, strings.Join(roster, ", "))
	case roles.FamilyEng:
		if verdict != "" {
			return "", "", "", false, fmt.Errorf("--verdict is only valid for qa* roles, got role %s", agent)
		}
	case roles.FamilyQA:
		if verdict == "" && !deliverFail {
			return "", "", "", false, fmt.Errorf("qa role %s requires --verdict PASS|FAIL (or --fail --reason ...)", agent)
		}
	case roles.FamilyOther:
		if verdict != "" {
			return "", "", "", false, fmt.Errorf("--verdict is only valid for qa* roles, got role %s", agent)
		}
	}

	return agent, family, verdict, isFail, nil
}

// selectTemplate picks the notification template for the (family, isFail,
// verdict) tuple. Inputs must already be validated by validateDeliverFlags;
// this function panics on contradictory states (e.g. QA family with empty
// verdict and isFail=false) because that is an internal contract violation,
// not user input.
func selectTemplate(family roles.RoleFamily, isFail bool, verdict, reason, title, agent string) deliverTemplate {
	a := agentOrUnknown(agent)
	r := reason
	if isFail && r == "" {
		r = "no reason provided"
	}

	switch family {
	case roles.FamilyEng:
		if isFail {
			detail := fmt.Sprintf("%s hit a wall: %s", a, r)
			webhookText := fmt.Sprintf("%s FAILED: %s", title, r)
			if reason == "" {
				detail = fmt.Sprintf("%s hit a wall", a)
				webhookText = fmt.Sprintf("%s FAILED", title)
			}
			return deliverTemplate{
				Kind:          "agent.failed",
				RadioDetail:   detail,
				WebhookText:   webhookText,
				ReportText:    fmt.Sprintf("%s FAILED: %s", title, r),
				IPCSummary:    fmt.Sprintf("%s failed: %s", a, r),
				SummarySuffix: fmt.Sprintf("FAILED: %s", r),
			}
		}
		return deliverTemplate{
			Kind:          "agent.completed",
			RadioDetail:   fmt.Sprintf("%s finished: %s", a, title),
			WebhookText:   fmt.Sprintf("%s ready for QA", title),
			ReportText:    fmt.Sprintf("%s ready for QA", title),
			IPCSummary:    fmt.Sprintf("%s delivered: %s (ready for QA)", a, title),
			SummarySuffix: "ready for QA",
		}

	case roles.FamilyQA:
		if isFail {
			// --verdict FAIL or --fail. Lead with FAIL so the radio TTS reads
			// the verdict first, matching QA's verdict-first reporting rule.
			detail := fmt.Sprintf("%s FAIL: %s — %s", a, title, r)
			return deliverTemplate{
				Kind:          "agent.failed",
				RadioDetail:   detail,
				WebhookText:   fmt.Sprintf("FAIL: %s — %s", title, r),
				ReportText:    fmt.Sprintf("FAIL: %s — %s", title, r),
				IPCSummary:    fmt.Sprintf("%s FAIL: %s", a, title),
				SummarySuffix: fmt.Sprintf("FAIL: %s", r),
			}
		}
		// verdict == PASS (validation guarantees this branch is only reached
		// for QA when isFail=false and verdict was supplied).
		return deliverTemplate{
			Kind:          "agent.completed",
			RadioDetail:   fmt.Sprintf("%s PASS: %s", a, title),
			WebhookText:   fmt.Sprintf("PASS: %s", title),
			ReportText:    fmt.Sprintf("PASS: %s", title),
			IPCSummary:    fmt.Sprintf("%s PASS: %s", a, title),
			SummarySuffix: "PASS",
		}

	case roles.FamilyOther:
		if isFail {
			return deliverTemplate{
				Kind:          "agent.failed",
				RadioDetail:   fmt.Sprintf("%s delivery failed: %s", a, r),
				WebhookText:   fmt.Sprintf("%s delivery failed: %s", title, r),
				ReportText:    fmt.Sprintf("%s delivery failed: %s", title, r),
				IPCSummary:    fmt.Sprintf("%s delivery failed: %s", a, r),
				SummarySuffix: fmt.Sprintf("delivery failed: %s", r),
			}
		}
		return deliverTemplate{
			Kind:          "agent.completed",
			RadioDetail:   fmt.Sprintf("%s delivered: %s", a, title),
			WebhookText:   fmt.Sprintf("%s delivered: %s", a, title),
			ReportText:    fmt.Sprintf("delivered: %s", title),
			IPCSummary:    fmt.Sprintf("%s delivered: %s", a, title),
			SummarySuffix: "delivered",
		}
	}

	// FamilyUnknown reaches here only if validateDeliverFlags is bypassed,
	// which is an internal contract violation. Return a defensive fallback so
	// production code never crashes; tests should fail if this branch fires.
	return deliverTemplate{
		Kind:          "agent.completed",
		RadioDetail:   fmt.Sprintf("%s delivered: %s", a, title),
		WebhookText:   fmt.Sprintf("%s delivered: %s", a, title),
		ReportText:    fmt.Sprintf("delivered: %s", title),
		IPCSummary:    fmt.Sprintf("%s delivered: %s", a, title),
		SummarySuffix: "delivered",
	}
}
