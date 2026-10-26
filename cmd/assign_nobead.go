package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nmelo/initech/internal/tui"
)

// noBeadWordsMax is how much of the message the ribbon and status show, the
// same cut a bead title gets.
const noBeadWordsMax = 80

// noBeadWords is the first words of a no-bead message: whitespace (newlines
// included) collapsed to single spaces, cut to noBeadWordsMax runes with "...".
func noBeadWords(message string) string {
	words := strings.Join(strings.Fields(message), " ")
	r := []rune(words)
	if len(r) <= noBeadWordsMax {
		return words
	}
	return string(r[:noBeadWordsMax-3]) + "..."
}

// assignNoBead dispatches work that has no bead: the message is delivered
// exactly as initech send delivers it (no readiness preflight, the same
// reports), nothing is written to bd, and only once the TUI has accepted the
// message does the agent's ribbon get the no-bead marker and the event log a
// bead_assigned entry with no bead id. The marker is best-effort, like a bead
// registration: a TUI that does not know the no_bead action (an older one)
// refuses it and the ribbon is left as it was, with a warning.
//
// A send that could not confirm (ini-69fy) may well have arrived: the marker
// is still set, and the could-not-confirm line and its exit status come last.
func assignNoBead(w io.Writer, agent, host, message string) error {
	sendErr := deliverText(w, agent, host, message, true)
	var unconfirmed *unconfirmedSendError
	if sendErr != nil && !errors.As(sendErr, &unconfirmed) {
		return sendErr
	}
	words := noBeadWords(message)
	resp, err := ipcCall(tui.IPCRequest{Action: "no_bead", Target: agent, Text: words})
	switch {
	case err != nil:
		fmt.Fprintf(w, "warning: %s's ribbon not updated: %v\n", agent, err)
	case !resp.OK && strings.HasPrefix(resp.Error, "unknown action"):
		fmt.Fprintf(w, "warning: %s's ribbon not updated: the running initech does not know work with no bead\n", agent)
	case !resp.OK:
		fmt.Fprintf(w, "warning: %s's ribbon not updated: %s\n", agent, resp.Error)
	}
	emitIPCEvent(agent, "", "bead_assigned", "no bead, assigned to "+agent+": "+words)
	return sendErr
}
