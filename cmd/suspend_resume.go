package cmd

// suspend_resume.go is the command surface for ini-zffi: parking an agent on
// purpose and waking one on purpose, both preserving queued messages.
//
// `restart` already woke a suspended agent, but it DISCARDS the queue
// (ini-9imx measured that). These are the queue-preserving verbs, which is the
// whole reason they exist as their own commands rather than folding into
// start/stop.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// exitNoOp is returned when the command was understood and refused because the
// agent was already in the requested state. Distinct from success so a script
// can branch on "nothing to do" without parsing prose, and distinct from a
// hard failure so it does not read as breakage (spec AC 2).
const exitNoOp = 2

// noOpError carries a truthful refusal plus the exit code that marks it a
// no-op rather than a failure.
type noOpError struct{ msg string }

func (e *noOpError) Error() string { return e.msg }
func (e *noOpError) ExitCode() int { return exitNoOp }

var resumeCmd = &cobra.Command{
	Use:   "resume [host:]<agent>",
	Short: "Wake a suspended agent, delivering any queued messages",
	Long: `Wakes an agent that was suspended, either automatically under memory
pressure or by 'initech suspend'.

Any messages that arrived while it was suspended are delivered in order, so
nothing sent to a parked agent is lost. The wake is synchronous and takes
about a second.

Other ways to wake a suspended agent:
  - send it a message ('initech send <agent> "..."') — wakes and delivers
  - press any key in its pane in the TUI — the keystroke wakes it and is not
    passed through to the agent

'initech restart' also wakes a suspended agent, but DISCARDS queued messages.
Use resume when you want them delivered.

Requires a running initech TUI (connects via INITECH_SOCKET).`,
	Args: cobra.ExactArgs(1),
	RunE: runResume,
}

var suspendCmd = &cobra.Command{
	Use:   "suspend [host:]<agent>",
	Short: "Park an idle agent, freeing its memory until it is woken",
	Long: `Suspends an idle agent: its process is closed and its memory freed,
while messages sent to it are queued rather than lost.

This is the same state auto-suspend produces under memory pressure, and it is
woken the same three ways: a message, a keystroke in its pane, or
'initech resume <agent>'.

An agent that is working, or that is waiting on an answer from you, is NOT
suspended — suspending would throw away the work or make the question vanish.
The command refuses and tells you the state it found.

Requires a running initech TUI (connects via INITECH_SOCKET).`,
	Args: cobra.ExactArgs(1),
	RunE: runSuspend,
}

func init() {
	rootCmd.AddCommand(resumeCmd)
	rootCmd.AddCommand(suspendCmd)
}

// splitHostAgent parses the [host:]agent form the other agent-targeted
// commands accept, so these two do not invent a second addressing syntax.
func splitHostAgent(target string) (host, agent string) {
	if idx := strings.Index(target, ":"); idx >= 0 {
		return target[:idx], target[idx+1:]
	}
	return "", target
}

func runResume(cmd *cobra.Command, args []string) error {
	host, agent := splitHostAgent(args[0])

	resp, err := ipcCall(tui.IPCRequest{Action: "resume", Target: agent, Host: host})
	if err != nil {
		return err
	}
	if !resp.OK {
		// "Not suspended" is a truthful no-op, not a failure: the operator
		// asked for a state the agent is already in.
		if strings.Contains(resp.Error, "is not suspended") {
			return &noOpError{msg: resp.Error}
		}
		return errors.New(resp.Error)
	}
	fmt.Fprintln(cmd.ErrOrStderr(), resp.Data)
	return nil
}

func runSuspend(cmd *cobra.Command, args []string) error {
	host, agent := splitHostAgent(args[0])

	resp, err := ipcCall(tui.IPCRequest{Action: "suspend", Target: agent, Host: host})
	if err != nil {
		return err
	}
	if !resp.OK {
		if strings.Contains(resp.Error, "already suspended") {
			return &noOpError{msg: resp.Error}
		}
		return errors.New(resp.Error)
	}
	fmt.Fprintln(cmd.ErrOrStderr(), resp.Data)
	return nil
}
