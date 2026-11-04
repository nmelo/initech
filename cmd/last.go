package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// lastMax is the most entries initech last prints, the app route's own cap.
const lastMax = 200

var lastLines int

var lastCmd = &cobra.Command{
	Use:   "last <agent>",
	Short: "Print an agent's last messages and tool results from its session",
	Long: `Prints the newest entries of the agent's Claude Code session transcript,
oldest first, as plain text: each entry is its time, its kind (agent,
nelson, message from <agent>, tool) and its text; a tool run is its one
line and its input and result, cut to 4 KB. It reads the transcript through
the same reader the app's conversation route uses, so it prints what the
app shows and nothing more. A tool result can carry what the agent saw.

Only this machine's agents: a remote agent is refused.`,
	Args:         cobra.ExactArgs(1),
	RunE:         runLast,
	SilenceUsage: true,
}

func init() {
	lastCmd.Flags().IntVarP(&lastLines, "lines", "n", 20, fmt.Sprintf("Number of entries to print (1 to %d)", lastMax))
	rootCmd.AddCommand(lastCmd)
}

// errLastRemote is the refusal for an agent on another machine: its
// transcript is on that machine, and reading it from here is not offered.
var errLastRemote = errors.New("initech last reads this machine's agents only")

func runLast(cmd *cobra.Command, args []string) error {
	if lastLines < 1 || lastLines > lastMax {
		return fmt.Errorf("-n must be 1 to %d, got %d", lastMax, lastLines)
	}
	agent := args[0]
	if host, _, ok := strings.Cut(agent, ":"); ok {
		return fmt.Errorf("%w; %s is on %s", errLastRemote, agent, host)
	}
	path, err := lastTranscript(agent)
	if err != nil {
		return err
	}
	pg, err := appserve.ReadPage(path, 0, lastLines)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no transcript for %s", agent)
		}
		return fmt.Errorf("reading %s's transcript: %w", agent, err)
	}
	if len(pg.Messages) > lastLines {
		pg.Messages = pg.Messages[len(pg.Messages)-lastLines:]
	}
	formatLast(cmd.OutOrStdout(), pg)
	return nil
}

// lastTranscript is the agent's newest session transcript as the running
// TUI names it in its pane list (the file the pane's journal reads, under
// either spelling of a symlinked rig). A TUI too old to name it gets the
// newest one under the agent's folder, as app-serve does.
func lastTranscript(agent string) (string, error) {
	sock, proj, err := resolveSocket()
	if err != nil {
		return "", err
	}
	resp, err := ipcCallSocket(sock, tui.IPCRequest{Action: "list", Detail: true})
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return "", fmt.Errorf("%s", resp.Error)
	}
	var panes []paneInfo
	if err := json.Unmarshal([]byte(resp.Data), &panes); err != nil {
		return "", fmt.Errorf("reading the pane list: %w", err)
	}
	for _, pi := range panes {
		if pi.Name != agent {
			continue
		}
		if pi.Host != "" {
			return "", fmt.Errorf("%w; %s is on %s", errLastRemote, agent, pi.Host)
		}
		path := pi.SessionFile
		if path == "" && proj != nil && proj.Root != "" {
			path = tui.NewestTranscript(filepath.Join(proj.Root, agent))
		}
		if path == "" {
			return "", fmt.Errorf("no transcript for %s", agent)
		}
		return path, nil
	}
	return "", fmt.Errorf("no agent %s in this initech", agent)
}

// formatLast writes a page as plain text: one block per entry, oldest first,
// a blank line between blocks, and a last line counting the malformed lines
// the reader left out, when it left any.
func formatLast(w io.Writer, pg appserve.Page) {
	blocks := make([]string, 0, len(pg.Messages)+1)
	for _, m := range pg.Messages {
		head := m.At.Local().Format("15:04:05") + " " + m.Kind
		if m.Kind == appserve.KindMessage && m.From != "" {
			head += " from " + m.From
		}
		var b strings.Builder
		b.WriteString(head)
		b.WriteString("\n")
		b.WriteString(m.Text)
		for _, f := range m.Footer {
			b.WriteString("\n")
			b.WriteString(f)
		}
		if m.Tool != nil && m.Tool.Detail != "" {
			b.WriteString("\n")
			b.WriteString(m.Tool.Detail)
		}
		blocks = append(blocks, b.String())
	}
	if pg.Skipped > 0 {
		blocks = append(blocks, fmt.Sprintf("skipped %d malformed line%s", pg.Skipped, plural(pg.Skipped)))
	}
	if len(blocks) == 0 {
		return
	}
	fmt.Fprintln(w, strings.Join(blocks, "\n\n"))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
