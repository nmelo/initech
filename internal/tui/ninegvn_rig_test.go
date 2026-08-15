//go:build !windows

package tui

// The ini-9gvn composed venue: does mail actually reach a pane whose screen
// QUOTES a dialog, and does it still stop at one that shows a real dialog?
//
// The unit cells prove the discriminator. This proves the PRODUCT: a real
// binary, a real pane, a real `initech send` over the real socket, and the
// operator-visible answer the CLI prints. The live incident was exactly this
// path -- every send reported "deferred (modal open)" while nothing was open --
// and no unit test of a matcher can show that end to end.
//
// Shell agents, not Claude: what is under test is the guard's reading of pane
// CONTENT, and a shell can render the same characters far more cheaply and
// deterministically than a live model can be made to open a dialog on demand.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gvnSend runs the real CLI against THIS RIG'S OWN session and returns what
// the operator would see.
//
// TWO SAFETY PROPERTIES, both learned the hard way in this arc:
//
// INITECH_SOCKET IS PINNED TO THE RIG. The first version set only cmd.Dir, and
// the CLI resolved the socket to the OPERATOR'S LIVE FLEET -- it was about to
// deliver probe messages into real agents' panes, and only the dev-build guard
// (ini-grg3) stopped it. An instrument that can address someone else's session
// is the ports lesson wearing different clothes.
//
// --allow-dev-delivery IS THEREFORE SAFE HERE AND ONLY HERE: the guard exists
// because a dev build must not drive a live session, and the pinned socket is
// what makes this session not that one. The flag is passed AFTER the pin, never
// instead of it.
func gvnSend(t *testing.T, bin, root, agent, msg string) string {
	t.Helper()
	sock := filepath.Join(root, ".initech", "initech.sock")
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("this rig has no socket of its own at %s (%v); a send now would be "+
			"addressed to whatever session the CLI discovers, which is the operator's", sock, err)
	}
	c := exec.Command(bin, "send", "--allow-dev-delivery", agent, msg)
	c.Dir = root
	c.Env = append(os.Environ(), "INITECH_SOCKET="+sock)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Logf("send exited with %v (output follows)", err)
	}
	text := string(out)
	if strings.Contains(text, "/Users/nmelo/Desktop/Projects/initech/.initech") {
		t.Fatalf("THIS RIG ADDRESSED THE LIVE FLEET. Refusing to interpret the result:\n%s", text)
	}
	return text
}

// TestNineGVNRig_QuotedDialogDoesNotHoldMail is the incident, reproduced and
// then not reproduced.
func TestNineGVNRig_QuotedDialogDoesNotHoldMail(t *testing.T) {
	if os.Getenv("INITECH_9GVN") == "" {
		t.Skip("composed rig: set INITECH_9GVN=1 (real binary, real PTYs, ~40s)")
	}
	port := x5obFreePort(t)
	bin := nineISXBuild(t)
	root := x5obRoot(t, "", port)

	_, w1pty, w1emu, _ := nineISXStart(t, bin, root)
	time.Sleep(10 * time.Second)
	w1pty.Write([]byte("n")) // decline the consent overlay
	time.Sleep(2 * time.Second)
	x5obProvesOwnBinary(t, root, port)

	// The focused pane reports ABOUT a dialog -- the incident's own text, in
	// the compacted form that made the guard match it.
	w1pty.Write([]byte(`printf 'eDoyouwanttoproceed?. Only two of my own outputs contradicting each other exposed it.\n'` + "\r"))
	time.Sleep(4 * time.Second)

	if screen := nineISXScreen(w1emu); !strings.Contains(screen, "Doyouwanttoproceed") {
		t.Fatalf("the quotation never rendered, so the send below would prove nothing\n%s", screen)
	}

	const token = "GVNPROBE7412"
	out := gvnSend(t, bin, root, "super", "probe "+token)
	if strings.Contains(strings.ToLower(out), "defer") {
		t.Errorf("MAIL IS STILL HELD BY A QUOTATION.\n\nThe CLI said %q. This is the live "+
			"incident: an agent's pane described a dialog, and the fleet's coordination "+
			"stopped moving while nothing was open.", strings.TrimSpace(out))
	}

	// POSITIVE PROOF, because "the output does not say defer" is satisfied by
	// any failure that never got as far as the guard -- and the first draft of
	// this cell passed exactly that way, on a CLI refusal. The message must
	// actually ARRIVE.
	var arrived bool
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if strings.Contains(nineISXScreen(w1emu), token) {
			arrived = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !arrived {
		t.Errorf("the send reported no deferral but the message never reached the pane.\n\n"+
			"CLI said: %q\n%s", strings.TrimSpace(out), nineISXScreen(w1emu))
	}
}

// TestNineGVNRig_RealDialogTextStillHoldsMail is the forbidden direction, in
// the composed venue: the fix must not have taught the guard to deliver into an
// open picker.
//
// The pane renders the dialog's own characters -- question and options -- which
// is precisely what the screen face reads. A shell painting them is
// indistinguishable to the guard from Claude painting them, which is what makes
// this a fair negative control rather than a weaker one.
func TestNineGVNRig_RealDialogTextStillHoldsMail(t *testing.T) {
	if os.Getenv("INITECH_9GVN") == "" {
		t.Skip("composed rig: set INITECH_9GVN=1 (real binary, real PTYs, ~40s)")
	}
	port := x5obFreePort(t)
	bin := nineISXBuild(t)
	root := x5obRoot(t, "", port)

	_, w1pty, w1emu, _ := nineISXStart(t, bin, root)
	time.Sleep(10 * time.Second)
	w1pty.Write([]byte("n"))
	time.Sleep(2 * time.Second)
	x5obProvesOwnBinary(t, root, port)

	w1pty.Write([]byte(`printf 'Do you want to proceed?\n> 1. Yes\n  2. No, and tell Claude what to do differently\n'` + "\r"))
	time.Sleep(4 * time.Second)

	if screen := nineISXScreen(w1emu); !strings.Contains(screen, "1. Yes") {
		t.Fatalf("the dialog text never rendered, so this control proves nothing\n%s", screen)
	}

	out := gvnSend(t, bin, root, "super", "[from eng1] probe: this must be held")
	if !strings.Contains(strings.ToLower(out), "defer") {
		t.Errorf("A SCREEN SHOWING A DIALOG ACCEPTED MAIL. The CLI said %q.\n\nThis is the "+
			"direction that must never widen: pasting a body into an option picker makes "+
			"the submit key answer whichever option was highlighted, which is a destructive "+
			"default the operator never saw (ini-2jpo).", strings.TrimSpace(out))
	}
}
