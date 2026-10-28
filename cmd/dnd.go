package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var dndAgent string

var dndCmd = &cobra.Command{
	Use:   "dnd on|off",
	Short: "Hold other agents' messages while you work (do-not-disturb)",
	Long: `Puts your pane in do-not-disturb, or takes it out.

While it is on, a message another agent sends you with initech send waits
in your pane's message queue instead of being typed into your input box.
The sender sees "held: <agent> is in do-not-disturb" and exit 0. When the
queue is full (20 messages) a new message is refused with "not delivered
to <agent>: do-not-disturb queue is full" and exit 1; the oldest is never
dropped.

dnd off delivers the held messages in the order they arrived, one at a
time, through the normal path. A message that cannot be delivered then is
reported to the operator once, with how long it waited.

Do-not-disturb lives in the running initech only: nothing is written to
disk, and a restart clears it. Your own typing is never held.

Inside a TUI pane (INITECH_AGENT set automatically):
  initech dnd on
  initech dnd off

Outside the TUI (must specify agent):
  initech dnd --agent eng1 off

Against an initech that does not know do-not-disturb, dnd prints "older
initech running; restart it for do-not-disturb" and exits 2.`,
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"on", "off"},
	RunE:      runDND,
}

func init() {
	dndCmd.Flags().StringVar(&dndAgent, "agent", "", "Target agent name (auto-detected inside TUI via INITECH_AGENT)")
	rootCmd.AddCommand(dndCmd)
}

// exitOlderTUI is dnd's exit status when the running initech does not know
// the action: distinct from a refusal (1), so a script can tell them apart.
const exitOlderTUI = 2

// olderTUIDNDLine is what dnd prints against an initech that answers the
// action "unknown action".
const olderTUIDNDLine = "older initech running; restart it for do-not-disturb"

// olderTUIError is a request the running initech does not know.
type olderTUIError struct{ line string }

func (e *olderTUIError) Error() string { return e.line }
func (e *olderTUIError) ExitCode() int { return exitOlderTUI }

func runDND(cmd *cobra.Command, args []string) error {
	onOff := args[0]
	if onOff != "on" && onOff != "off" {
		return fmt.Errorf("dnd takes on or off, not %q", onOff)
	}
	agent := dndAgent
	if agent == "" {
		agent = os.Getenv("INITECH_AGENT")
	}
	if agent == "" {
		return fmt.Errorf("no agent specified (set --agent or run inside a TUI pane where INITECH_AGENT is set)")
	}
	cmd.SilenceUsage = true
	resp, err := ipcCall(tui.IPCRequest{Action: "dnd", Target: agent, Text: onOff})
	if err != nil {
		return err
	}
	if !resp.OK {
		if strings.HasPrefix(resp.Error, "unknown action") {
			cmd.SilenceErrors = true
			return &olderTUIError{olderTUIDNDLine}
		}
		return fmt.Errorf("%s", resp.Error)
	}
	fmt.Fprintln(cmd.OutOrStdout(), resp.Data)
	return nil
}
