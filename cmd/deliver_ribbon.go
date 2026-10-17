package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/nmelo/initech/internal/tui"
)

// Ribbon upkeep for deliver. bd is the single truth for who holds a bead;
// a ribbon is a display of it, moved by the command that moves bd. So after
// its bd write, deliver puts each ribbon where the write left the bead:
// never on the --to recipient, who only gets the report.

// shownStatuses are the statuses in which a bead's assignee is shown as its
// holder, by initech status and on the ribbon. qa_passed is not one: nobody
// is working the bead, it waits for super to close it.
var shownStatuses = []string{"in_progress", "ready_for_qa", "in_qa"}

func isShownStatus(s string) bool { return containsString(shownStatuses, s) }

// deliverRibbons brings the ribbons in line with deliver's bd write and
// returns what happened, for the summary line ("ribbon cleared", "ribbon set
// on eng4", "ribbon NOT updated: <reason>", joined by ", ").
//
// The deliverer keeps the bead on its ribbon only while bd still has it as
// the holder in a shown status; otherwise the bead comes off its ribbon
// (only that bead when the ribbon shows others; the whole ribbon when the
// TUI cannot report ribbons, as deliver always did). When the write hands
// the bead to someone else (a QA FAIL returning it to its implementer), the
// bead is added to that agent's ribbon if it is a local pane.
//
// Cosmetic: failures are warnings and summary text, never an error.
func deliverRibbons(w io.Writer, agent, beadID, title, assigneeBefore string, write beadWrite) string {
	holder := assigneeBefore
	if write.SetAssignee {
		holder = write.Assignee
	}
	shown := isShownStatus(write.Status)

	var panes []paneRibbon
	read := false
	ribbons := func() []paneRibbon {
		if !read {
			panes, read = readRibbons(), true
		}
		return panes
	}

	var parts []string
	switch {
	case agent == "":
		fmt.Fprintf(w, "warning: INITECH_AGENT not set, skipping TUI bead clear\n")
		parts = append(parts, "ribbon NOT updated: INITECH_AGENT not set")
	case holder == agent && shown:
		parts = append(parts, "ribbon unchanged")
	default:
		parts = append(parts, takeOffDelivererRibbon(w, ribbons(), agent, beadID))
	}

	if write.SetAssignee && holder != "" && holder != agent && shown {
		parts = append(parts, addToHolderRibbon(ribbons(), holder, beadID, title))
	}
	return strings.Join(parts, ", ")
}

// takeOffDelivererRibbon removes beadID from agent's ribbon.
func takeOffDelivererRibbon(w io.Writer, panes []paneRibbon, agent, beadID string) string {
	text, done := "", "ribbon cleared"
	if p, ok := findRibbon(panes, agent, ""); ok && p.Beads != nil {
		if !containsString(*p.Beads, beadID) {
			return "ribbon unchanged"
		}
		var rest []string
		for _, id := range *p.Beads {
			if id != beadID {
				rest = append(rest, id)
			}
		}
		if len(rest) > 0 {
			text, done = ribbonText(rest, nil), "ribbon cleared of "+beadID
		}
	}
	resp, err := ipcCall(tui.IPCRequest{Action: "bead", Target: agent, Text: text})
	if err != nil {
		fmt.Fprintf(w, "warning: could not clear TUI bead (is initech running?)\n")
		return "ribbon NOT updated: could not reach initech: " + err.Error()
	}
	if !resp.OK {
		fmt.Fprintf(w, "warning: TUI bead clear: %s\n", resp.Error)
		return "ribbon NOT updated: " + resp.Error
	}
	return done
}

// addToHolderRibbon adds beadID, with its title, to holder's ribbon,
// keeping the beads it already shows.
func addToHolderRibbon(panes []paneRibbon, holder, beadID, title string) string {
	notUpdated := "ribbon NOT updated on " + holder + ": "
	if panes == nil {
		return notUpdated + "the running initech does not report ribbons"
	}
	p, ok := findRibbon(panes, holder, "")
	if !ok || p.Host != "" {
		return notUpdated + "no pane " + holder + " in this initech"
	}
	if p.Beads == nil {
		return notUpdated + "the running initech does not report ribbons"
	}
	if containsString(*p.Beads, beadID) {
		return "ribbon unchanged on " + holder
	}
	merged := append(append([]string{}, *p.Beads...), beadID)
	resp, err := ipcCall(tui.IPCRequest{Action: "bead", Target: holder, Text: ribbonText(merged, map[string]string{beadID: title})})
	if err != nil {
		return notUpdated + "could not reach initech: " + err.Error()
	}
	if !resp.OK {
		return notUpdated + resp.Error
	}
	return "ribbon set on " + holder
}
