package tui

// initech_line.go is initech's own line to super: one message, through the
// send path every agent's message takes, from the sender "initech". It is how
// a judgment initech makes on its own (a wait that is over, a prompt an agent
// cannot pass alone, a scheduled job) reaches the one agent who can act.

import "fmt"

// initechSender is the sender initech's own lines carry: do-not-disturb and
// the typing hold judge it as they judge any agent's send, so it waits.
const initechSender = "initech"

// initechSend delivers one line into a local pane through the send path,
// with every hold that path applies (a dialog, do-not-disturb, a suspended
// pane, the typing hold). A package variable so tests record instead.
var initechSend = func(p *Pane, text string, o sendOrigin) {
	p.SendTextFrom(text, true, o)
}

// superPane is the one local pane named super. ok is false when there is
// none or more than one: a line meant for the dispatcher never goes to a
// guess. Main loop only.
func (t *TUI) superPane() (*Pane, bool) {
	var found *Pane
	n := 0
	for _, pv := range t.panes {
		if p, ok := pv.(*Pane); ok && p.name == superPaneName {
			found = p
			n++
		}
	}
	return found, n == 1
}

// superPaneName is the dispatcher's pane.
const superPaneName = "super"

// sendInitechLine sends "[from initech] <text>" to super, on its own
// goroutine, so it never blocks its caller, the main loop included. With no
// local super pane, or two, it logs one line and sends nothing.
func (t *TUI) sendInitechLine(text string) {
	line := fmt.Sprintf("[from %s] %s", initechSender, text)
	t.safeGo(func() {
		var p *Pane
		var ok bool
		if !t.runOnMain(func() { p, ok = t.superPane() }) {
			return // shutting down
		}
		if !ok {
			LogWarn("initech-line", "no single local super pane; line not sent", "line", line)
			return
		}
		initechSend(p, line, sendOrigin{From: initechSender})
	})
}

// sendInitechLineTo is sendInitechLine to the local pane named to, for a line
// owed to one agent (held mail it sent, ini-8rkg). With no such pane it logs
// one line and sends nothing.
func (t *TUI) sendInitechLineTo(to, text string) {
	line := fmt.Sprintf("[from %s] %s", initechSender, text)
	t.safeGo(func() {
		var p *Pane
		if !t.runOnMain(func() {
			for _, pv := range t.panes {
				if lp, ok := pv.(*Pane); ok && lp.name == to {
					p = lp
				}
			}
		}) {
			return
		}
		if p == nil {
			LogWarn("initech-line", "no local pane for the line; not sent", "to", to)
			return
		}
		initechSend(p, line, sendOrigin{From: initechSender})
	})
}
