package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var (
	stripStdin bool
	stripClear bool
)

var stripCmd = &cobra.Command{
	Use:   "strip ['<item>' ...]",
	Short: "Set the list strip at the bottom of your own pane",
	Long: `Set the list strip initech draws at the bottom of your pane, directly above
its bottom bar: one line per item, each starting with ▶, in the order given.
A long item wraps under its text and is never cut. The command replaces the
whole list; the strip belongs to the pane of the agent that runs it.

  initech strip 'Release hover? Recommended yes.' 'Register the CI runner?'
  printf '%s\n' "$item1" "$item2" | initech strip --stdin
  initech strip --clear

--stdin reads one item per non-empty line; with none, it clears the strip.
Limits: at most 10 items, each at most 300 characters after trimming, with
no tab or newline inside an item. A call with no items and neither flag is
refused, so an empty shell variable never wipes a list. Each refusal exits 1
with one line and leaves the list as it was. The list survives a restart of
the pane and of the rig.`,
	Args: cobra.ArbitraryArgs,
	RunE: runStrip,
}

func init() {
	stripCmd.Flags().BoolVar(&stripStdin, "stdin", false, "Read one item per non-empty line from stdin")
	stripCmd.Flags().BoolVar(&stripClear, "clear", false, "Remove the strip")
	rootCmd.AddCommand(stripCmd)
}

func runStrip(cmd *cobra.Command, args []string) error {
	// A refusal is one line and exit 1: no usage, no second copy.
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	items, err := stripItemsFor(args, stripStdin, stripClear, cmd.InOrStdin())
	if err != nil {
		return err
	}
	agent := os.Getenv("INITECH_AGENT")
	if agent == "" {
		return fmt.Errorf("initech strip runs inside an agent's pane (INITECH_AGENT is not set)")
	}
	checked, err := tui.CheckStripItems(items)
	if err != nil {
		return err
	}
	if !stripClear && len(checked) == 0 && !stripStdin {
		return fmt.Errorf("every item is blank; give an item, or use --clear to remove the strip")
	}
	resp, err := ipcCall(tui.IPCRequest{Action: "strip", Target: agent, Items: checked})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	fmt.Fprintln(cmd.OutOrStdout(), resp.Data)
	return nil
}

// stripItemsFor is the list a call asks for, or why the call is refused: a
// bare call, --clear with items, and --stdin with items are usage errors.
func stripItemsFor(args []string, stdin, clear bool, in io.Reader) ([]string, error) {
	switch {
	case clear && (stdin || len(args) > 0):
		return nil, fmt.Errorf("--clear takes no items and no --stdin")
	case clear:
		return nil, nil
	case stdin && len(args) > 0:
		return nil, fmt.Errorf("give items as arguments or with --stdin, not both")
	case stdin:
		var items []string
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 64*1024), 64*1024)
		for sc.Scan() {
			if line := strings.TrimRight(sc.Text(), "\r"); strings.TrimSpace(line) != "" {
				items = append(items, line)
			}
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("could not read stdin: %v", err)
		}
		return items, nil
	case len(args) == 0:
		return nil, fmt.Errorf("give at least one item, --stdin, or --clear (a bare call changes nothing)")
	default:
		return args, nil
	}
}
