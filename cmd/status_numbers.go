package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/rignumbers"
	"github.com/nmelo/initech/internal/tui"
)

// statusNumbers is --numbers: with --json, the rig's numbers as one object
// instead of the agent array; alone, only the rig line.
var statusNumbers bool

func init() {
	statusCmd.Flags().BoolVar(&statusNumbers, "numbers", false, "print the rig's numbers (merges, gate, landing line, beads, idle engineers); with --json as one object")
}

// statusRigRun runs the reads status makes itself when window 1 has no
// numbers yet. A package variable so tests stand in for gh and bd.
var statusRigRun = func(dir string) rignumbers.Runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		c := exec.CommandContext(ctx, name, args...)
		c.Dir = dir
		return c.Output()
	}
}

// rigNumbersJSON is status --json --numbers.
type rigNumbersJSON struct {
	// Source is "window 1" for its cached numbers, "computed" when window 1
	// had none and status read them itself.
	Source  string             `json:"source"`
	Numbers rignumbers.Numbers `json:"numbers"`
	Line    string             `json:"line"`
}

// statusRigNumbers is the rig's numbers for status: window 1's cached ones
// from the list reply, so gh and git never run per status call; computed here
// only when window 1 has none yet (an older TUI, or before its first minute).
func statusRigNumbers(st *statusRead, p *config.Project, now time.Time) rigNumbersJSON {
	fleet := statusFleet(st, now)
	if st.resp != nil && st.resp.Rig != nil {
		n := *st.resp.Rig
		n.Fleet = fleet
		return rigNumbersJSON{Source: "window 1", Numbers: n, Line: n.Line()}
	}
	dir := ""
	slug := ""
	if p != nil {
		dir = p.Root
		if len(p.Repos) > 0 {
			slug, _ = rignumbers.Slug(p.Repos[0].URL)
		}
	}
	run := statusRigRun(dir)
	in := rignumbers.Input{Now: now, Panes: map[string]string{}}
	for _, pi := range st.panes {
		if s := agentState(pi); s != nil {
			in.Panes[pi.Name] = *s
		}
	}
	ctx := context.Background()
	if p == nil || p.Beads.IsEnabled() {
		_ = rignumbers.GatherBeads(ctx, run, &in) // unknown on failure, printed as ?
	}
	_ = rignumbers.GatherRepo(ctx, run, slug, &in)
	n := rignumbers.Compute(in)
	n.Fleet = fleet
	return rigNumbersJSON{Source: "computed", Numbers: n, Line: n.Line()}
}

// statusReadFleet reads the machine when window 1 has no reading. A package
// variable so tests stand in for the machine.
var statusReadFleet = tui.ReadFleetNow

// statusFleet is the machine ceiling's numbers for status (ini-49q): window
// 1's last reading from the list reply, or one read here when it has none.
func statusFleet(st *statusRead, now time.Time) *rignumbers.Fleet {
	if st.resp != nil && st.resp.Fleet != nil {
		return st.resp.Fleet
	}
	return statusReadFleet(now)
}

// printFleetLine prints the fleet line after the agent table, red when the
// machine is at its ceiling.
func printFleetLine(out io.Writer, f *rignumbers.Fleet) {
	if f == nil {
		return
	}
	line := f.Line
	if f.AtCeiling {
		line = color.Red(line + " · at the ceiling")
	}
	fmt.Fprintf(out, "  %s\n", line)
}

// printRigLine prints the rig line after the agent table, naming where the
// numbers came from when window 1 did not have them.
func printRigLine(out io.Writer, r rigNumbersJSON) {
	note := ""
	if r.Source != "window 1" {
		note = "  (computed now: window 1 has no numbers yet)"
	}
	fmt.Fprintf(out, "  rig: %s%s\n", r.Line, note)
}

// printRigJSON prints status --json --numbers.
func printRigJSON(out io.Writer, r rigNumbersJSON) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}
