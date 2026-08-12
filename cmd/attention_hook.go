package cmd

import (
	"fmt"
	"os"

	"github.com/nmelo/initech/internal/hooks"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var attentionHookCmd = &cobra.Command{
	Use:   "attention-hook",
	Short: "Internal: receive a Claude Notification hook event (ini-2x8.4)",
	Long: `Reads a Claude Code Notification hook payload on stdin and reports the
agent as waiting for operator input when the payload is a real dialog.

Not intended to be run by hand. initech installs this into each agent's
.claude/settings.json when the operator grants consent (attention.hooks: true).

Agent identity comes from INITECH_AGENT and INITECH_SOCKET, which the hook
inherits from the agent process that spawned it -- verified empirically, since
the payload itself carries no agent identity (only cwd).`,
	Hidden: true,
	RunE:   runAttentionHook,
}

func init() {
	rootCmd.AddCommand(attentionHookCmd)
}

// runAttentionHook never returns a non-nil error for a bad or unexpected
// payload.
//
// This process runs INSIDE the operator's agent as a Claude Code hook. A hook
// that exits non-zero or writes to stderr is noise in the agent's own session
// at exactly the moment the operator is being asked a question -- so a
// malformed payload, a missing socket, or an unreachable TUI all degrade to
// doing nothing quietly. The attention feature's tier-1 signal (OSC 777) is
// always on and independent of this, so a silent no-op here loses redundancy,
// never detection.
func runAttentionHook(cmd *cobra.Command, args []string) error {
	payload, err := hooks.ParseNotification(cmd.InOrStdin())
	if err != nil {
		return nil // Unparseable: ignore quietly.
	}
	if !payload.CountsAsWaiting() {
		// idle_prompt lands here: every idle agent emits it, and counting it
		// would chime the whole fleet at rest.
		return nil
	}

	agent := os.Getenv("INITECH_AGENT")
	if agent == "" {
		return nil // Not running under an initech pane.
	}

	resp, err := ipcCall(tui.IPCRequest{
		Action: "attention",
		Target: agent,
		Text:   payload.Message,
	})
	if err != nil || resp == nil || !resp.OK {
		return nil // TUI gone or refusing: the hook is redundancy, not truth.
	}
	fmt.Fprintln(cmd.OutOrStdout(), "")
	return nil
}
