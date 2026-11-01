package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var compactCmd = &cobra.Command{
	Use:   "compact [host:]<agent> [what to keep]",
	Short: "Send /compact to an agent and report its context figure dropping",
	Long: `Sends "/compact <what to keep>" (or "/compact" alone) into the agent's
input box through the same delivery path as initech send: the same delivery
verdict, and the same hold and refusal lines and exit codes (held for a
dialog, do-not-disturb, not delivered). The words after the agent name are
joined with single spaces, as initech send joins its words. A keep text that
makes the command longer than 800 characters, or more than 3 lines, is
refused before anything is typed: Claude would paste it rather than run it.
That prints "not compacted: <the reason>", exit 1, and nothing is watched.

Once delivered, it watches the agent's context figure (the one initech
status --json reports) and prints "compacted: <before>% -> <after>%" when it
drops, exit 0. No drop within two minutes prints "sent; no drop seen in 2m",
exit 2. When the agent's context figure cannot be read (a narrow pane), it
prints "sent; context figure unknown here", exit 2.

Run from the agent's own pane (INITECH_AGENT names it), it returns as soon
as the message is delivered: "queued for your pane; compaction runs when
this turn ends", exit 0. The figure cannot drop while your turn runs.

Codex, OpenCode and generic agents have no /compact and are refused, exit 1,
with nothing typed. So is an agent on another machine (host:agent), whose
agent type this initech does not know: run initech compact on that machine.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runCompact,
}

func init() {
	rootCmd.AddCommand(compactCmd)
}

// compactWatchWindow is how long initech compact watches for the context
// figure to drop after delivery; compactPoll is how often it reads it. Vars
// so tests can shorten them.
var (
	compactWatchWindow = 2 * time.Minute
	compactPoll        = 2 * time.Second
)

// Lines initech compact prints after a delivery.
const (
	compactQueuedLine  = "queued for your pane; compaction runs when this turn ends"
	compactNoDropLine  = "sent; no drop seen in 2m"
	compactUnknownLine = "sent; context figure unknown here"
)

// compactPane is what initech compact reads from the list: whether it has
// the agent, and the agent's context figure and agent type.
type compactPane struct {
	found          bool
	contextPercent *int
	agentType      string
}

func runCompact(cmd *cobra.Command, args []string) error {
	target := args[0]
	text := "/compact"
	if keep := strings.Join(args[1:], " "); keep != "" {
		text += " " + keep
	}
	var host string
	if idx := strings.Index(target, ":"); idx >= 0 {
		host, target = target[:idx], target[idx+1:]
	}
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	return compactAgent(cmd.ErrOrStderr(), target, host, text, os.Getenv("INITECH_AGENT"))
}

// compactAgent is initech compact after argument parsing; self is the
// caller's INITECH_AGENT.
func compactAgent(w io.Writer, target, host, text, self string) error {
	name := target
	if host != "" {
		name = host + ":" + target
	}
	before, err := readCompactPane(target, host)
	if err != nil {
		return err
	}
	// Refused before anything is typed: only Claude Code has /compact.
	agentType := before.agentType
	if agentType == "" && host == "" {
		agentType = configAgentType(target)
	}
	if agentType != "" && agentType != config.AgentTypeClaudeCode {
		return fmt.Errorf("not compacted: %s is a %s agent; /compact is a Claude Code command", name, agentType)
	}
	if agentType == "" && host != "" && before.found {
		// Another machine's agent: this initech does not know its type, so
		// it cannot tell a Claude Code agent from a Codex one.
		return fmt.Errorf("not compacted: %s runs on another machine and its agent type is not known here", name)
	}

	verdict, err := deliverTextVerdict(w, target, host, text, true, false)
	if err != nil && verdict == tui.DeliveryNotDelivered && tui.IsSlashPasteRefusal(err.Error()) {
		// The TUI refused before typing: the command would have pasted,
		// not run (ini-hoy). Its words, in compact's.
		msg := err.Error()
		if i := strings.Index(msg, ": a slash command "); i >= 0 {
			msg = msg[i+2:]
		}
		return fmt.Errorf("not compacted: %s", msg)
	}
	if err != nil || verdict != tui.DeliveryDelivered {
		// The send's own words and exit code; nothing to watch.
		return err
	}
	if host == "" && self != "" && self == target {
		// The caller's own turn is running: the figure cannot drop until it
		// ends, so there is nothing to wait for here.
		fmt.Fprintln(w, compactQueuedLine)
		return nil
	}
	if before.contextPercent == nil {
		return &unconfirmedSendError{compactUnknownLine}
	}
	from := *before.contextPercent
	deadline := time.Now().Add(compactWatchWindow)
	for time.Now().Before(deadline) {
		time.Sleep(compactPoll)
		now, err := readCompactPane(target, host)
		if err != nil {
			break // the TUI is gone: the window ends with the sent line
		}
		if now.contextPercent != nil && *now.contextPercent < from {
			fmt.Fprintf(w, "compacted: %d%% -> %d%%\n", from, *now.contextPercent)
			return nil
		}
	}
	return &unconfirmedSendError{compactNoDropLine}
}

// readCompactPane reads the agent from the list initech status --json uses:
// its context figure (read from its screen) and agent type. An agent the
// list does not have reads as nothing known.
func readCompactPane(target, host string) (compactPane, error) {
	resp, err := ipcCall(tui.IPCRequest{Action: "list", Detail: true})
	if err != nil {
		return compactPane{}, err
	}
	if !resp.OK {
		return compactPane{}, fmt.Errorf("%s", resp.Error)
	}
	var panes []paneInfo
	if err := json.Unmarshal([]byte(resp.Data), &panes); err != nil {
		return compactPane{}, fmt.Errorf("read the agent list: %w", err)
	}
	for _, p := range panes {
		if p.Name == target && p.Host == host {
			return compactPane{found: true, contextPercent: p.ContextPercent, agentType: p.AgentType}, nil
		}
	}
	return compactPane{}, nil
}

// configAgentType is the role's agent type from initech.yaml, for a TUI too
// old to say it in the list; "" when there is no config to read.
func configAgentType(role string) string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	path, err := config.Discover(wd)
	if err != nil {
		return ""
	}
	p, err := config.Load(path)
	if err != nil {
		return ""
	}
	if ov, ok := p.RoleOverrides[role]; ok {
		return config.NormalizeAgentType(ov.AgentType)
	}
	return config.AgentTypeClaudeCode
}
