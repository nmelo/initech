package tui

// manual_suspend_portable_test.go is the ini-zffi coverage that needs no live
// child process: the suspend/resume DECISION logic. It runs on every platform,
// including the Windows leg where the PTY fixture cannot (ini-47w -- a
// constraint that only one file needs must not propagate to the rest).

import (
	"errors"
	"strings"
	"testing"
)

// suspendDecisionTUI builds a TUI around a struct-literal pane: no process, no
// PTY, just the state the decision functions read. That is exactly enough for
// the refusal and parking rules, and it is what makes them portable.
func suspendDecisionTUI(name string, activity ActivityState, alive bool) (*TUI, *Pane) {
	p := &Pane{name: name, activity: activity, alive: alive}
	return &TUI{panes: []PaneView{p}, agentEvents: make(chan AgentEvent, 8)}, p
}

// TestSuspendAgentRefusesBusyAndNeedsInput is AC 6. Suspension must never
// interrupt work or make a pending question vanish.
func TestSuspendAgentRefusesBusyAndNeedsInput(t *testing.T) {
	for _, c := range []struct {
		name     string
		activity ActivityState
		wantText string
	}{
		{"working agent", StateRunning, "working"},
		{"agent waiting on the operator", StateWaitingInput, "waiting for your answer"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tui, p := suspendDecisionTUI("eng1", c.activity, true)

			err := tui.suspendAgent("eng1")
			if err == nil {
				t.Fatal("suspend was ACCEPTED against a busy agent; it must refuse rather than " +
					"throw away work or a pending question")
			}
			if !strings.Contains(err.Error(), c.wantText) {
				t.Errorf("refusal %q does not name the state it found (%q); an operator told a "+
					"generic refusal cannot tell why", err, c.wantText)
			}
			if p.IsSuspended() {
				t.Error("the agent was suspended despite the refusal")
			}
		})
	}
}

// TestSuspendAgentParksIdleAgentIntoTheAutoSuspendState is AC 5's first half:
// manual suspend produces the state auto-suspend produces, because both go
// through the one primitive.
func TestSuspendAgentParksIdleAgentIntoTheAutoSuspendState(t *testing.T) {
	tui, p := suspendDecisionTUI("eng1", StateIdle, true)

	if err := tui.suspendAgent("eng1"); err != nil {
		t.Fatalf("suspend of an idle agent was refused: %v", err)
	}
	if !p.IsSuspended() {
		t.Error("suspended flag not set; handleIPCSend would not queue for this pane")
	}
	if got := p.Activity(); got != StateSuspended {
		t.Errorf("activity = %v, want StateSuspended -- without it updateActivity derives "+
			"StateDead and the pane renders as crashed rather than parked", got)
	}
}

// TestResumeAgentRefusesWhenNotSuspended is AC 2: a truthful refusal naming the
// real state, distinguishable from success.
func TestResumeAgentRefusesWhenNotSuspended(t *testing.T) {
	tui, _ := suspendDecisionTUI("eng1", StateIdle, true)

	_, err := tui.resumeAgent("eng1")
	if err == nil {
		t.Fatal("resume reported success against an agent that was never suspended")
	}
	var notSusp *ErrNotSuspended
	if !errors.As(err, &notSusp) {
		t.Fatalf("error %v is not *ErrNotSuspended, so the CLI cannot give it the no-op exit "+
			"code that lets a script tell 'nothing to do' from a failure", err)
	}
	if !strings.Contains(err.Error(), "eng1") {
		t.Errorf("refusal %q does not name the agent", err)
	}
}

func TestResumeAgentRefusesUnknownAgent(t *testing.T) {
	tui, _ := suspendDecisionTUI("eng1", StateIdle, true)

	if _, err := tui.resumeAgent("nosuch"); err == nil {
		t.Fatal("resume reported success for an agent that does not exist")
	}
}
