package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var beadAgent string
var beadClear bool

var beadCmd = &cobra.Command{
	Use:   "bead [id...]",
	Short: "Override or clear an agent pane's bead (it comes from bd)",
	Long: `A pane's bead comes from bd: the bead whose assignee is the pane's role
and whose status is in_progress, read every 10 seconds. With two such beads,
the one this command last named wins, else the newest updated.

initech bead <id> is a manual override: the pane shows that bead until
initech bead --clear, which returns it to the bead bd says at once. Supports
multiple bead IDs. Only bead ids are taken, checked against bd when it
answers; a pasted bd row (id, tab, title) is cut to its id. The id alone is
stored; bd's title shows on the ribbon for display only.

Inside a TUI pane (INITECH_AGENT set automatically):
  initech bead ini-bhk.3                      Override with one bead
  initech bead ini-abc ini-def ini-ghi         Override with several beads
  initech bead --clear                         Clear the override (back to bd)

Outside the TUI (must specify agent):
  initech bead --agent eng1 ini-bhk.3
  initech bead --agent eng1 ini-abc ini-def
  initech bead --agent eng1 --clear`,
	RunE: runBead,
}

func init() {
	beadCmd.Flags().StringVar(&beadAgent, "agent", "", "Target agent name (auto-detected inside TUI via INITECH_AGENT)")
	beadCmd.Flags().BoolVar(&beadClear, "clear", false, "Clear the override: the pane shows the bead bd says again")
	rootCmd.AddCommand(beadCmd)
}

func runBead(cmd *cobra.Command, args []string) error {
	// Determine the target agent.
	agent := beadAgent
	if agent == "" {
		agent = os.Getenv("INITECH_AGENT")
	}
	if agent == "" {
		return fmt.Errorf("no agent specified (set --agent or run inside a TUI pane where INITECH_AGENT is set)")
	}

	// A refusal is one line and exit 1: no usage, no second copy.
	cmd.SilenceUsage, cmd.SilenceErrors = true, true

	// Determine the bead ID(s). Only ids are sent: each argument is checked
	// (ini-5qf), and a title travels after a tab for display only.
	beadText := ""
	var ids []string
	if !beadClear {
		if len(args) < 1 {
			return fmt.Errorf("bead ID required (or use --clear)")
		}
		titles := map[string]string{}
		for _, arg := range args {
			id, title, err := beadArgID(cmd, arg)
			if err != nil {
				return err
			}
			ids = append(ids, id)
			titles[id] = title
		}
		if len(ids) == 1 {
			// Single bead: bd's title shows on the ribbon, display only.
			beadText = ids[0]
			if t := titles[ids[0]]; t != "" {
				beadText += "\t" + truncateTitle(t, 80)
			}
		} else {
			// Multiple beads: comma-separated, no titles.
			beadText = strings.Join(ids, ",")
		}
	}

	req := tui.IPCRequest{
		Action: "bead",
		Target: agent,
		Text:   beadText,
	}

	resp, err := ipcCall(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	// A silent success (exit 0, no output) is indistinguishable from doing
	// nothing at all -- confirm what changed so a caller outside the TUI
	// (no visible ribbon to check) has direct evidence the IPC call actually
	// landed, and so a LATER failure (e.g. an unrelated auto-detect clear
	// racing in and reverting this) is visibly a change from this baseline
	// rather than an unexplained absence (ini-cs7).
	out := cmd.OutOrStdout()
	if beadClear {
		fmt.Fprintf(out, "Cleared bead for %s.\n", agent)
	} else {
		fmt.Fprintf(out, "Set %s for %s.\n", strings.Join(ids, ", "), agent)
	}
	return nil
}

// beadArgID checks one initech bead argument and returns its id and bd's
// title. The argument must be a bead id; a pasted bd row ("id<TAB>title") is
// cut to its id with a stderr line saying so. When bd answers, the id must
// be one it knows; when bd does not answer, the id is taken with a note.
func beadArgID(cmd *cobra.Command, arg string) (id, title string, err error) {
	s := strings.TrimSpace(arg)
	if s == "" {
		return "", "", fmt.Errorf("bead ID required (or use --clear)")
	}
	id = tui.CutBeadID(s)
	if id == "" {
		return "", "", fmt.Errorf("not a bead id: %q (want <prefix>-<key>, such as ini-abc or ini-abc.2); nothing set", firstLine(s))
	}
	if id != s {
		fmt.Fprintf(cmd.ErrOrStderr(), "kept %s from the pasted line (the rest was its title)\n", id)
	}
	title, lookErr := bdShowTitleFn(id)
	switch {
	case lookErr == nil:
		return id, title, nil
	case strings.Contains(lookErr.Error(), "no issues found"):
		return "", "", fmt.Errorf("bd does not know %s; nothing set", id)
	default:
		fmt.Fprintf(cmd.ErrOrStderr(), "bd did not answer; %s set without checking it\n", id)
		return id, "", nil
	}
}
