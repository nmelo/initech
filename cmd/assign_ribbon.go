package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nmelo/initech/internal/tui"
)

// Ribbon upkeep for assign: which beads each pane's ribbon shows after a
// dispatch, a fold-in or a move.
//
// The TUI's bead action only REPLACES a pane's whole bead set, so adding one
// bead to a ribbon, or taking one off, reads the set from the list reply
// first and writes the result back. Another ribbon write for the same pane
// landing between the read and the write is lost; that is accepted, because
// the ribbon is a display and the next registration corrects it, the same
// best-effort footing as assign's registration has always had.

// paneRibbon is one pane's entry in the list reply, as far as ribbons go.
type paneRibbon struct {
	Name string `json:"name"`
	Host string `json:"host,omitempty"`
	// Beads is nil when the TUI sent no beads field (an older TUI); a TUI
	// that knows the field always sends it, [] for an empty ribbon.
	Beads *[]string `json:"beads"`
}

// readRibbons asks the TUI for every pane's ribbon. An error, a refusal or a
// reply that does not parse gives nil, which callers treat like an older TUI.
func readRibbons() []paneRibbon {
	resp, err := ipcCall(tui.IPCRequest{Action: "list"})
	if err != nil || !resp.OK {
		return nil
	}
	var panes []paneRibbon
	if json.Unmarshal([]byte(resp.Data), &panes) != nil {
		return nil
	}
	return panes
}

// findRibbon picks name's pane from the list reply: the one on host when a
// host is given, else a local one, else any pane of that name.
func findRibbon(panes []paneRibbon, name, host string) (paneRibbon, bool) {
	var first *paneRibbon
	for i := range panes {
		p := panes[i]
		if p.Name != name {
			continue
		}
		if p.Host == host {
			return p, true
		}
		if first == nil {
			first = &panes[i]
		}
	}
	if first != nil && host == "" {
		return *first, true
	}
	return paneRibbon{}, false
}

// ribbonText encodes a bead set for the TUI's bead action: one bead keeps
// its title ("id\ttitle"), several are "id1,id2" (the action carries no
// titles for a set), none clears the ribbon.
func ribbonText(ids []string, titles map[string]string) string {
	if len(ids) == 1 {
		if t := titles[ids[0]]; t != "" {
			return ids[0] + "\t" + t
		}
		return ids[0]
	}
	return strings.Join(ids, ",")
}

// setRibbon writes one pane's ribbon. Cosmetic: failures are warnings.
func setRibbon(w io.Writer, name, text string) {
	resp, err := ipcCall(tui.IPCRequest{Action: "bead", Target: name, Text: text})
	if err != nil {
		fmt.Fprintf(w, "warning: could not register bead in TUI (is initech running?)\n")
	} else if !resp.OK {
		fmt.Fprintf(w, "warning: TUI bead registration: %s\n", resp.Error)
	}
}

// updateAssignRibbons brings the target's ribbon, and the ribbon of anyone a
// bead was moved from, in line with what assign just did.
//
// A call that dispatched or moved anything replaces the target's ribbon with
// the call's beads, as assign always has. A call made only of fold-ins adds
// the beads the ribbon lacks and keeps the rest; with a TUI that does not
// report ribbons it writes nothing (a blind replace could drop the others).
// A moved bead comes off the previous holder's ribbon, which an older TUI
// cannot do, so it is left alone with a warning.
func updateAssignRibbons(w io.Writer, agent, host string, successes []assignResult) {
	titles := make(map[string]string, len(successes))
	ids := make([]string, len(successes))
	allFoldIns, anyMoved := true, false
	for i, s := range successes {
		ids[i] = s.id
		titles[s.id] = s.title
		if s.kind != assignFoldIn {
			allFoldIns = false
		}
		if s.kind == assignMoved {
			anyMoved = true
		}
	}

	var panes []paneRibbon
	if allFoldIns || anyMoved {
		panes = readRibbons()
	}

	if !allFoldIns {
		setRibbon(w, agent, ribbonText(ids, titles))
	} else if p, ok := findRibbon(panes, agent, host); !ok && panes != nil {
		fmt.Fprintf(w, "warning: no ribbon for %s in the running initech; ribbon not updated\n", agent)
	} else if p.Beads == nil {
		fmt.Fprintf(w, "warning: %s's ribbon left as it was: the running initech does not report ribbons, so adding to it could drop its other beads\n", agent)
	} else {
		merged := append([]string{}, *p.Beads...)
		for _, id := range ids {
			if !containsString(merged, id) {
				merged = append(merged, id)
			}
		}
		if len(merged) != len(*p.Beads) {
			setRibbon(w, agent, ribbonText(merged, titles))
		}
	}

	for _, holder := range movedHolders(successes) {
		p, ok := findRibbon(panes, holder, "")
		if ok && p.Beads != nil {
			var rest []string
			for _, id := range *p.Beads {
				if !movedFrom(successes, holder, id) {
					rest = append(rest, id)
				}
			}
			if len(rest) != len(*p.Beads) {
				setRibbon(w, holder, ribbonText(rest, nil))
			}
			continue
		}
		if panes == nil || ok {
			fmt.Fprintf(w, "warning: %s's ribbon left as it was: the running initech does not report ribbons\n", holder)
		}
		// No pane of that name: there is no ribbon to take the bead off.
	}
}

// notifyMovedHolders tells each previous holder, in one line, which of its
// beads went to agent and who moved them. Best-effort.
func notifyMovedHolders(w io.Writer, agent string, successes []assignResult) {
	by := os.Getenv("INITECH_AGENT")
	if by == "" {
		by = "the operator"
	}
	for _, holder := range movedHolders(successes) {
		var moved []string
		for _, s := range successes {
			if s.kind == assignMoved && s.holder == holder {
				moved = append(moved, s.id)
			}
		}
		text := fmt.Sprintf("[from initech] %s moved to %s by %s", strings.Join(moved, ", "), agent, by)
		resp, err := ipcCall(tui.IPCRequest{Action: "send", Target: holder, Text: text, Enter: true})
		if err != nil {
			fmt.Fprintf(w, "warning: could not tell %s the bead moved: %v\n", holder, err)
		} else if !resp.OK {
			fmt.Fprintf(w, "warning: could not tell %s the bead moved: %s\n", holder, resp.Error)
		}
	}
}

// movedHolders lists, once each and in order, the agents beads were moved from.
func movedHolders(successes []assignResult) []string {
	var out []string
	for _, s := range successes {
		if s.kind == assignMoved && !containsString(out, s.holder) {
			out = append(out, s.holder)
		}
	}
	return out
}

// movedFrom reports whether id was moved away from holder in this call.
func movedFrom(successes []assignResult, holder, id string) bool {
	for _, s := range successes {
		if s.kind == assignMoved && s.holder == holder && s.id == id {
			return true
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
