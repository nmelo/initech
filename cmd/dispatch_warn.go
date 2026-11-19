package cmd

// dispatch_warn.go is the high-context reminder: when initech assign or
// initech bead --agent gives a new bead to an agent whose context is at or
// above dispatch_context_warn_percent, the dispatcher gets one line from
// initech saying so. Advice only: the dispatch, its stdout and its exit code
// are what they are without it.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
)

// dispatchTarget is what the reminder knows of the target before the
// dispatch: its context reading and the beads on its ribbon.
type dispatchTarget struct {
	listed  bool // the pane list answered and named the target as a local pane
	context *int // context_percent, as initech status --json gives it; nil if not read
	beads   []string
}

// dispatchWarnThreshold is the rig's dispatch_context_warn_percent, 0 (off)
// when it is absent or the rig's config cannot be read.
func dispatchWarnThreshold() config.WarnPercent {
	if p := discoverProjectBestEffort(); p != nil {
		return p.DispatchContextWarnPercent
	}
	return 0
}

// readDispatchTarget reads the target from the TUI's pane list. A remote
// target, a list that does not answer, or one that does not name the target
// as a local pane gives an unlisted target, for which no line is sent.
func readDispatchTarget(agent, host string) dispatchTarget {
	if host != "" {
		return dispatchTarget{}
	}
	resp, err := ipcCall(tui.IPCRequest{Action: "list", Detail: true})
	if err != nil || !resp.OK {
		return dispatchTarget{}
	}
	var panes []paneInfo
	if json.Unmarshal([]byte(resp.Data), &panes) != nil {
		return dispatchTarget{}
	}
	for _, pi := range panes {
		if pi.Name != agent || pi.Host != "" {
			continue
		}
		t := dispatchTarget{listed: true, context: pi.ContextPercent}
		if pi.Beads != nil {
			for _, b := range *pi.Beads {
				t.beads = append(t.beads, tui.CutBeadID(b))
			}
		}
		return t
	}
	return dispatchTarget{}
}

// highContextLine is the reminder for a dispatch of beads to agent, and
// whether one is owed: the setting is on, the target's context is known and
// at or above it, and at least one bead is new to the target (not on its
// ribbon; the caller leaves out beads bd already holds for it).
func highContextLine(threshold config.WarnPercent, agent string, t dispatchTarget, beads []string) (string, bool) {
	if threshold <= 0 || !t.listed || t.context == nil || *t.context < int(threshold) {
		return "", false
	}
	fresh := false
	for _, id := range beads {
		if !containsString(t.beads, id) {
			fresh = true
		}
	}
	if !fresh {
		return "", false
	}
	return fmt.Sprintf("[from initech] %s is at %d%% context; clear it (initech clear %s) or restart it (initech restart %s) before new work",
		agent, *t.context, agent, agent), true
}

// warnHighContext sends the reminder, when one is owed, to the dispatcher:
// the agent that ran the command (INITECH_AGENT), through the send path from
// initech, so do-not-disturb and the typing hold apply as to any message.
// Run outside a pane, the line goes to stderr. A line that cannot be
// delivered is one stderr line; the dispatch has already happened.
func warnHighContext(stderr io.Writer, threshold config.WarnPercent, agent string, t dispatchTarget, beads []string) {
	line, ok := highContextLine(threshold, agent, t, beads)
	if !ok {
		return
	}
	to := os.Getenv("INITECH_AGENT")
	if to == "" {
		fmt.Fprintln(stderr, line)
		return
	}
	resp, err := ipcCall(tui.IPCRequest{Action: "send", Target: to, Text: line, Enter: true, From: "initech"})
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "the high-context reminder for %s did not reach %s: %v\n", agent, to, err)
	case !resp.OK:
		fmt.Fprintf(stderr, "the high-context reminder for %s did not reach %s: %s\n", agent, to, resp.Error)
	}
}
