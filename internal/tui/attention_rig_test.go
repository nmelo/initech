//go:build !windows

package tui

// The ini-35ak composed rig: does a question opening on monitor 1 reach
// monitor 2?
//
// THE VENUE IS THE POINT (AC1). Every unit cell in attention_wire_test.go can
// pass while the product fails, because they hand a remote pane its state
// directly. Only two real processes with a real wire between them can show
// that window 1 OBSERVES a dialog, ENCODES it, SENDS it, and that window 2
// DECODES and DRAWS it. That whole chain is what had never worked: a viewer
// had structurally never listed a window-1 agent in any release.
//
// Six agents and an ephemeral port, per this week's rules: five agents did not
// reproduce ini-2pce and a fixed port let two agents' rigs dial each other's
// window 1.

import (
	"os"
	"strings"
	"testing"
	"time"
)

// x5obRaiseDialog is what an agent emits when Claude opens a blocking dialog:
// the OSC 777 notification (the tier-1 RAISE signal, and the only one entitled
// to chime) followed by dialog text the screen scan recognises.
//
// Both halves are needed and they do different jobs. The OSC raises the row;
// the on-screen text is what lets the row ever RETIRE, because a wait whose
// dialog the screen has never confirmed is deliberately never cleared by
// absence -- "no modal visible" would otherwise mean "our patterns do not
// match this dialog" rather than "the operator answered".
const x5obRaiseDialog = `printf '\033]777;notify;Claude Code;Claude needs your permission\007Do you want to proceed?\n'`

// attentionAgents returns the agents named inside a screen's needs-input box.
//
// It reads the BOX rather than the whole screen, and that distinction is the
// difference between a real assertion and a green one: every agent name also
// appears in pane titles, the ribbon and the agents overlay, so
// strings.Contains(screen, name) is true on a window that merely knows the
// agent exists. Only the box means "this window is telling the operator that
// this agent is blocked".
//
// Fixture-independent on purpose. The first draft asserted "eng1" and failed
// while the product was working perfectly -- the pane that receives typed
// keystrokes is the FOCUSED one, which is super here, so the rig was naming an
// agent that had never been asked anything.
func attentionAgents(screen string) map[string]bool {
	names := []string{"super", "pm", "qa1", "eng1", "eng2", "eng3"}
	out := map[string]bool{}
	if !strings.Contains(screen, "needs input") {
		return out
	}
	for _, n := range names {
		if strings.Contains(screen, n) {
			out[n] = true
		}
	}
	return out
}

// WHY A WHOLE-SCREEN SCAN IS SOUND HERE, AND ONLY HERE.
//
// This rig gives window 2 NO assigned agents, so it renders no panes at all --
// no titles, no ribbon, no content. The needs-input box is therefore the only
// thing on that screen that can print an agent's name, which makes
// "the name appears" and "the box names it" the same statement.
//
// It is NOT sound on window 1, where every agent's name is on screen in its
// own pane title regardless of who is waiting; window 1 is asserted on the box
// TITLE only. Two earlier drafts got this wrong in opposite directions: a
// whole-row scan on window 1 reported an agent the box never named, and a
// column-sliced parser then failed to find the box at all. The fixture, not
// the parser, is what makes the viewer side unambiguous.

func attentionKeys(set map[string]bool) string {
	var ks []string
	for _, n := range []string{"super", "pm", "qa1", "eng1", "eng2", "eng3"} {
		if set[n] {
			ks = append(ks, n)
		}
	}
	if len(ks) == 0 {
		return "(none)"
	}
	return strings.Join(ks, ",")
}

// TestAttentionRig_QuestionOnWindow1ReachesWindow2 is AC1 and AC2 in the
// composed venue: raise crosses, and clear crosses too.
//
// Window 2 owns NOTHING here (no assignments), so it renders no panes at all.
// That is deliberate and is AC4 in the composed venue: the agent is not
// displayed on this monitor and must still be listed on it, because attention
// is never scoped.
func TestAttentionRig_QuestionOnWindow1ReachesWindow2(t *testing.T) {
	if os.Getenv("INITECH_35AK") == "" {
		t.Skip("composed rig: set INITECH_35AK=1 (real binaries, PTYs, ~60s)")
	}
	port := x5obFreePort(t)
	bin := nineISXBuild(t)
	root := x5obRoot(t, "", port)

	_, w1pty, w1emu := nineISXStart(t, bin, root)
	time.Sleep(10 * time.Second)
	w1pty.Write([]byte("n")) // decline the consent overlay
	time.Sleep(2 * time.Second)
	x5obProvesOwnBinary(t, root, port)

	_, w2pty, w2emu := nineISXStart(t, bin, root, "--window", "2")
	time.Sleep(12 * time.Second)
	w2pty.Write([]byte("n"))
	time.Sleep(2 * time.Second)

	if pre := attentionAgents(nineISXScreen(w2emu)); len(pre) != 0 {
		t.Fatalf("window 2 already lists %s as waiting before any dialog opened; the "+
			"assertions below would pass without the wire carrying anything",
			attentionKeys(pre))
	}

	// THE QUESTION OPENS on window 1's focused agent.
	w1pty.Write([]byte(x5obRaiseDialog + "\r"))
	time.Sleep(6 * time.Second)

	w1 := nineISXScreen(w1emu)
	if !strings.Contains(w1, "needs input") {
		t.Fatalf("window 1 itself never raised the wait, so nothing could have crossed the "+
			"wire -- this is a detection failure, not a transport one\n%s", w1)
	}
	x5obEvidence(t, root)

	w2 := nineISXScreen(w2emu)
	onW2 := attentionAgents(w2)
	if len(onW2) == 0 {
		t.Errorf("MONITOR 2 NEVER HEARD THE QUESTION.\n\nWindow 1 shows a needs-input box "+
			"and window 2 lists nothing -- which is the whole bug: a question waits unseen "+
			"because the operator was looking at the other screen.\n\n%s", w2)
	}
	// Exactly one agent was asked a question, so exactly one must be listed --
	// a box that named the whole fleet would satisfy a mere non-empty check.
	if len(onW2) != 1 {
		t.Errorf("window 2 lists %s; exactly one agent was asked a question, so a list of "+
			"any other size means the box is not describing what happened",
			attentionKeys(onW2))
	}

	// THE OPERATOR ANSWERS: the dialog leaves the screen.
	w1pty.Write([]byte("clear\r"))
	time.Sleep(6 * time.Second)

	if after := attentionAgents(nineISXScreen(w2emu)); len(after) != 0 {
		t.Errorf("window 2 still lists %s after the dialog closed on window 1.\n\nA stale "+
			"row is worse than a missing one: it sends the operator to a monitor where "+
			"nothing is waiting, and it does it silently.\n\n%s",
			attentionKeys(after), nineISXScreen(w2emu))
	}
}

// TestAttentionRig_WaitAlreadyInProgressWhenWindow2Attaches is the late-attach
// path, which the broadcast alone cannot serve.
//
// If the question is already on screen when a window attaches, no transition
// is coming for it -- the next status frame for that agent may be the one that
// CLEARS it. The state has to ride the handshake or this window stays blind
// until the operator answers somewhere else.
func TestAttentionRig_WaitAlreadyInProgressWhenWindow2Attaches(t *testing.T) {
	if os.Getenv("INITECH_35AK") == "" {
		t.Skip("composed rig: set INITECH_35AK=1 (real binaries, PTYs, ~60s)")
	}
	port := x5obFreePort(t)
	bin := nineISXBuild(t)
	root := x5obRoot(t, "", port)

	_, w1pty, w1emu := nineISXStart(t, bin, root)
	time.Sleep(10 * time.Second)
	w1pty.Write([]byte("n"))
	time.Sleep(2 * time.Second)
	x5obProvesOwnBinary(t, root, port)

	// The question opens BEFORE any second window exists.
	w1pty.Write([]byte(x5obRaiseDialog + "\r"))
	time.Sleep(5 * time.Second)
	if !strings.Contains(nineISXScreen(w1emu), "needs input") {
		t.Fatalf("window 1 never raised the wait; the attach assertion below would prove "+
			"nothing\n%s", nineISXScreen(w1emu))
	}

	_, w2pty, w2emu := nineISXStart(t, bin, root, "--window", "2")
	time.Sleep(12 * time.Second)
	w2pty.Write([]byte("n"))
	time.Sleep(3 * time.Second)

	if onW2 := attentionAgents(nineISXScreen(w2emu)); len(onW2) == 0 {
		t.Errorf("a window that attached while an agent was ALREADY waiting shows no "+
			"needs-input box.\n\nThe question is on screen now and no transition is coming "+
			"for it, so this monitor stays blind until the operator answers elsewhere -- "+
			"which is the same unseen question, arrived at by a different route.\n\n%s",
			nineISXScreen(w2emu))
	}
}
