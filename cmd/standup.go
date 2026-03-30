package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/spf13/cobra"
)

var standupCmd = &cobra.Command{
	Use:   "standup",
	Short: "Generate morning standup from beads",
	Long:  `Queries beads for recently shipped, in-progress, and ready work to produce a daily standup summary.`,
	RunE:  runStandup,
}

func init() {
	rootCmd.AddCommand(standupCmd)
}

type standupBead struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Assignee string `json:"assignee"`
}

func runStandup(cmd *cobra.Command, args []string) error {
	runner := &iexec.DefaultRunner{}
	out := cmd.OutOrStdout()

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	cfgPath, err := config.Discover(wd)
	if err != nil {
		return fmt.Errorf("no initech.yaml found")
	}

	p, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	// Check if bd is available
	if _, err := runner.Run("which", "bd"); err != nil {
		fmt.Fprintln(out, "bd not found. Install beads to generate standups.")
		return nil
	}

	today := time.Now().Format("2006-01-02")
	fmt.Fprintf(out, "\n%s Daily - %s\n", color.Bold("## "+p.Name), today)

	// Recently shipped (closed beads).
	shipped := queryBeads(runner, "list", "--status", "closed", "--json")
	var recentlyShipped []standupBead
	for _, b := range shipped {
		// bd doesn't have a --closed-after flag in all versions,
		// so we include all closed beads and let the user scan
		recentlyShipped = append(recentlyShipped, b)
	}

	fmt.Fprintln(out, "\n"+color.Bold("### What's New"))
	if len(recentlyShipped) == 0 {
		fmt.Fprintln(out, "- (none)")
	} else {
		limit := 10
		if len(recentlyShipped) < limit {
			limit = len(recentlyShipped)
		}
		for _, b := range recentlyShipped[:limit] {
			fmt.Fprintf(out, "- %s: %s %s\n", color.Blue(b.ID), b.Title, color.Green("(shipped)"))
		}
		if len(recentlyShipped) > 10 {
			fmt.Fprintf(out, "- ... and %d more\n", len(recentlyShipped)-10)
		}
	}

	// In progress
	active := queryBeads(runner, "list", "--status", "in_progress", "--json")
	fmt.Fprintln(out, "\n"+color.Bold("### In Progress"))
	if len(active) == 0 {
		fmt.Fprintln(out, "- (none)")
	} else {
		for _, b := range active {
			assignee := b.Assignee
			if assignee == "" {
				assignee = "unassigned"
			}
			fmt.Fprintf(out, "- %s: %s (%s)\n", color.Blue(b.ID), b.Title, color.Blue(assignee))
		}
	}

	// Next up (ready beads)
	ready := queryBeads(runner, "ready", "--json")
	fmt.Fprintln(out, "\n"+color.Bold("### Next Up"))
	if len(ready) == 0 {
		fmt.Fprintln(out, "- (none)")
	} else {
		limit := 5
		if len(ready) < limit {
			limit = len(ready)
		}
		for _, b := range ready[:limit] {
			fmt.Fprintf(out, "- %s: %s\n", color.Blue(b.ID), b.Title)
		}
		if len(ready) > 5 {
			fmt.Fprintf(out, "- ... and %d more ready\n", len(ready)-5)
		}
	}

	fmt.Fprintln(out)
	return nil
}

func queryBeads(runner iexec.Runner, args ...string) []standupBead {
	out, err := runner.Run("bd", args...)
	if err != nil {
		return nil
	}

	// Try JSON array first
	var beads []standupBead
	if err := json.Unmarshal([]byte(out), &beads); err == nil {
		return beads
	}

	// Try JSON lines
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var b standupBead
		if err := json.Unmarshal([]byte(line), &b); err == nil {
			beads = append(beads, b)
		}
	}
	return beads
}
