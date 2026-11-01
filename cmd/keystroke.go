package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var keystrokeCmd = &cobra.Command{
	Use:   "keystroke [host:]<agent> <enter|esc|ctrl-c|y|n>",
	Short: "Send one key (Enter, Esc, Ctrl-C, y or n) into an agent's pane",
	Long: `Writes exactly one key into the agent's terminal: enter, esc, ctrl-c, y
or n. Nothing else is typed: no bracketed paste, no Enter after it, and no
check of the input box, so Claude Code, Codex and generic agents take it the
same way. On Claude Code's numbered permission prompt, enter answers Yes
(the highlighted option) and esc cancels (No); y and n are for Codex
approvals and plain y/n questions and do nothing on a numbered prompt, as
when typed by hand. An open dialog does not hold the operator's key (run outside any
pane): a key is how a dialog is answered, so it goes into whatever is on
screen. Any other sender's key to an agent with a dialog open is not
delivered: "a dialog is open; only the operator may answer it".

Prints "key <key> delivered to <agent>", exit 0, once the key has reached
the agent's terminal. Otherwise nothing is typed and it prints
"not delivered to <agent>: <reason>", exit 1: an unknown or empty key name,
an agent that is not found, not running, or suspended (a key never wakes a
suspended agent), or an agent in do-not-disturb. A key is never queued or
held for later. Do-not-disturb lets the operator's key through (run outside
any pane), and super's with --urgent; --urgent from anyone else is refused:
"only super may break do-not-disturb".

For an agent on another machine use host:agent; that machine's initech
writes the key and answers.`,
	Args: cobra.ExactArgs(2),
	RunE: runKeystroke,
}

var keystrokeUrgent bool

func init() {
	keystrokeCmd.Flags().BoolVar(&keystrokeUrgent, "urgent", false, "From super only: deliver now even if the agent is in do-not-disturb (logged); never into a dialog")
	rootCmd.AddCommand(keystrokeCmd)
}

func runKeystroke(cmd *cobra.Command, args []string) error {
	target := args[0]
	var host string
	if idx := strings.Index(target, ":"); idx >= 0 {
		host, target = target[:idx], target[idx+1:]
	}
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	return keystrokeAgent(cmd.ErrOrStderr(), target, host, args[1], keystrokeUrgent)
}

// ipcCaller is one IPC round trip: ipcCall for the CLI (the socket it
// resolves), or a fixed rig's socket for the app service.
type ipcCaller func(tui.IPCRequest) (*tui.IPCResponse, error)

// keystrokeAgent is initech keystroke after argument parsing. A key name
// outside the table is refused here, before anything reaches the TUI.
func keystrokeAgent(w io.Writer, target, host, key string, urgent bool) error {
	return keystrokeVia(w, ipcCall, target, host, key, urgent, "")
}

// keystrokeVia is keystrokeAgent over call, sent as from ("" lets the socket
// call stamp the sender from INITECH_AGENT, as the CLI does). It writes the
// delivered line to w and returns the refusal as its error: the words the
// command prints either way.
func keystrokeVia(w io.Writer, call ipcCaller, target, host, key string, urgent bool, from string) error {
	name := target
	if host != "" {
		name = host + ":" + target
	}
	if !tui.KeystrokeKnown(key) {
		return fmt.Errorf("not delivered to %s: %s", name, tui.KeystrokeUnknownReason(key))
	}
	resp, err := call(tui.IPCRequest{Action: "keystroke", Target: target, Host: host, Text: key, Urgent: urgent, From: from})
	if err != nil {
		return err
	}
	if !resp.OK {
		if strings.HasPrefix(resp.Error, "unknown action") {
			return fmt.Errorf("not delivered to %s: older initech running; restart it for keystroke", name)
		}
		return fmt.Errorf("not delivered to %s: %s", name, resp.Error)
	}
	if resp.Delivery != tui.DeliveryDelivered {
		return fmt.Errorf("not delivered to %s: unexpected verdict %q", name, resp.Delivery)
	}
	fmt.Fprintf(w, "key %s delivered to %s\n", key, name)
	return nil
}
