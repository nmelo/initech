package tui

// manual_suspend.go implements the operator-facing halves of ini-zffi:
// `initech suspend <agent>` parks an agent on the operator's call, and
// `initech resume <agent>` wakes one, both preserving the message queue.
//
// Both are thin: parking goes through parkPaneSuspended and waking through
// resumePane, so a manually suspended agent is the same state an auto-suspend
// produces and a commanded wake inherits ini-g7fl's resume-failure semantics
// (queue preserved on failure, resumeMu serialization, concurrent-wake
// re-check). The spec's "indistinguishable from auto" and "wake failure keeps
// the queue" are properties of reusing those primitives rather than behaviors
// re-implemented here.

import (
	"fmt"
	"net"
	"time"
)

// ErrNotSuspended distinguishes a truthful no-op from a failure so the CLI can
// exit with a code scripts can branch on: "resume an already-running agent"
// is not an error the operator needs to fix, but it is not success either.
type ErrNotSuspended struct {
	Agent string
	State string
}

func (e *ErrNotSuspended) Error() string {
	return fmt.Sprintf("%s is not suspended (%s)", e.Agent, e.State)
}

// ErrNotSuspendable reports why an agent may not be parked right now.
type ErrNotSuspendable struct {
	Agent string
	State string
}

func (e *ErrNotSuspendable) Error() string {
	return fmt.Sprintf("%s cannot be suspended (%s)", e.Agent, e.State)
}

// resumeAgent wakes a suspended agent on operator command and reports what was
// delivered. Returns *ErrNotSuspended when the agent was not suspended, so the
// caller can report the real state rather than claiming a wake that never
// happened.
//
// The queued-message count is read BEFORE the wake: resumePane drains the
// queue as part of waking, so reading afterwards would always report zero and
// the success line would silently understate what the operator just caused.
func (t *TUI) resumeAgent(name string) (delivered int, err error) {
	pane, ok := t.findLocalPane(name)
	if !ok {
		return 0, fmt.Errorf("no agent named %q in this session", name)
	}
	if !pane.IsSuspended() {
		return 0, &ErrNotSuspended{Agent: name, State: describePaneState(pane)}
	}
	delivered = pane.QueuedMessageCount()
	if err := t.resumePane(pane, "resume command"); err != nil {
		// g7fl semantics inherited: the queue survives a failed wake, so the
		// operator can retry rather than losing what was waiting.
		return 0, err
	}
	return delivered, nil
}

// suspendAgent parks an idle agent on operator command.
//
// Refusal is truthful and names the state, never silent: the spec is explicit
// that suspension must not interrupt work or a pending dialog, and an operator
// who is told "suspended" about an agent that kept running would act on a
// false premise. A needs-input agent is NOT idle -- StateWaitingInput is its
// own state, the same distinction that keeps auto-suspend off those agents
// (suspendEligible, ini-2x8.1).
func (t *TUI) suspendAgent(name string) error {
	pane, ok := t.findLocalPane(name)
	if !ok {
		return fmt.Errorf("no agent named %q in this session", name)
	}
	if pane.IsSuspended() {
		return &ErrNotSuspendable{Agent: name, State: "already suspended"}
	}
	if !pane.IsAlive() {
		return &ErrNotSuspendable{Agent: name, State: describePaneState(pane)}
	}
	if act := pane.Activity(); act != StateIdle {
		return &ErrNotSuspendable{Agent: name, State: describeActivity(act)}
	}
	t.runOnMain(func() { parkPaneSuspended(pane) })
	return nil
}

// describePaneState names a pane's state in the operator's terms, for messages
// that must be truthful about what was found rather than generically refusing.
func describePaneState(p *Pane) string {
	switch {
	case p.IsSuspended():
		return "suspended"
	case !p.IsAlive():
		return "stopped"
	default:
		return describeActivity(p.Activity())
	}
}

// describeActivity renders an activity state for operator-facing messages.
func describeActivity(a ActivityState) string {
	switch a {
	case StateRunning:
		return "working"
	case StateWaitingInput:
		return "waiting for your answer"
	case StateIdle:
		return "running"
	case StateSuspended:
		return "suspended"
	case StateDead:
		return "stopped"
	default:
		return a.String()
	}
}

// findLocalPane returns the local *Pane for an agent name. Manual suspend and
// resume act on THIS session's own agents: a remote pane is a view of another
// process's agent, and parking it here would suspend nothing while reporting
// success.
func (t *TUI) findLocalPane(name string) (*Pane, bool) {
	for _, pv := range t.panes {
		p, ok := pv.(*Pane)
		if ok && p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

// noticeWakeFailed surfaces a failed wake to the operator. A gesture that
// silently does nothing is the worst outcome here: the operator pressed a key
// at a parked pane, and if the respawn failed they must learn that rather than
// press harder at a pane that will never wake. The queue is preserved either
// way (ini-g7fl), which the notice says so the operator knows nothing was lost.
func (t *TUI) noticeWakeFailed(agent string, err error) {
	EmitEvent(t.agentEvents, AgentEvent{
		Type: EventAgentWakeFailed,
		Detail: fmt.Sprintf("%s could not be woken: %v. Any queued messages are still waiting; "+
			"try again, or `initech restart %s`.", agent, err, agent),
		Time: time.Now(),
	})
}

// handleIPCResume serves `initech resume <agent>`.
//
// The reply names what the operator's action actually caused, including the
// queued-message count, because "resumed" alone leaves them guessing whether
// the messages they sent while it was parked arrived. A non-suspended target
// is reported with its real state and OK=false, so a script can tell a no-op
// from a wake.
func (t *TUI) handleIPCResume(conn net.Conn, req IPCRequest) {
	delivered, err := t.resumeAgent(req.Target)
	if err != nil {
		writeIPCResponse(conn, IPCResponse{OK: false, Error: err.Error()})
		return
	}
	msg := fmt.Sprintf("resumed %s", req.Target)
	switch delivered {
	case 0:
		msg += "; no queued messages"
	case 1:
		msg += "; delivered 1 queued message"
	default:
		msg += fmt.Sprintf("; delivered %d queued messages", delivered)
	}
	writeIPCResponse(conn, IPCResponse{OK: true, Data: msg})
}

// handleIPCSuspend serves `initech suspend <agent>`.
func (t *TUI) handleIPCSuspend(conn net.Conn, req IPCRequest) {
	if err := t.suspendAgent(req.Target); err != nil {
		writeIPCResponse(conn, IPCResponse{OK: false, Error: err.Error()})
		return
	}
	writeIPCResponse(conn, IPCResponse{
		OK:   true,
		Data: fmt.Sprintf("suspended %s; it will wake on a message, a keystroke in its pane, or `initech resume %s`", req.Target, req.Target),
	})
}
