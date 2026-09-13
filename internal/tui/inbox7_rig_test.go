//go:build !windows

package tui

// inbox7_rig_test.go -- the COMPOSED two-window rig for ini-3wkl.7.
//
// Real initech binaries, real PTYs, two real windows, the real attach
// handshake, and each window's rendered screen as the assertion surface. The
// unit tests prove the route over a net.Pipe into the real daemon handler;
// this proves the product: an agent posts through the real IPC socket from
// inside its own pane, window 2's corner count moves, window 2's arrangement
// does not, and a reply typed in window 2 lands in window 1's store.
//
// STAND-IN, STATED: agents are `sh`, as in fn77's rig. The subject is the
// route and the doorbell between two windows, neither of which reads agent
// output. The POST is not a stand-in: it is the built binary's own post verb,
// run inside qa1's pane so identity comes from the real connection.
//
// POSTER CHOSEN FOR THE INSTRUMENT: qa1 is a WINDOW 1 agent, never rendered in
// window 2. A poster rendered in window 2 would echo its command into window
// 2's own panes, and the no-relayout assertion would then fail on the echo --
// a real screen change that is not a layout change.
//
// Run: INITECH_3WKL7=1 make test GOFLAGS='-run=Inbox7Rig -v -timeout=300s'

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// inbox7Layout is window 2's screen with the two regions this bead changes on
// purpose removed: row 0 (badge and inbox count) and the footer's elapsed age.
func inbox7Layout(screen string) string {
	lines := strings.Split(fn77ScreenWithoutViewerAge(screen), "\n")
	if len(lines) > 0 {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

func inbox7Row0(screen string) string {
	if i := strings.Index(screen, "\n"); i >= 0 {
		return screen[:i]
	}
	return screen
}

func TestInbox7Rig_ChildWindowCountsAndRepliesWithoutMovingPanes(t *testing.T) {
	if os.Getenv("INITECH_3WKL7") != "1" {
		t.Skip("set INITECH_3WKL7=1 to run the composed two-window rig for ini-3wkl.7")
	}
	bin := nineISXBuild(t)
	root, _ := nineISXRoot(t, "group_window:\n    eng: window-2\n")
	store := filepath.Join(root, ".initech", "inbox.yaml")

	_, w1pty, w1emu, _ := nineISXStart(t, bin, root)
	time.Sleep(10 * time.Second)
	w1pty.Write([]byte("n")) // decline the consent overlay
	time.Sleep(2 * time.Second)
	rigRequireServing(t, nineISXListenAddr(t, root))

	_, w2pty, w2emu, _ := nineISXStart(t, bin, root, "--window", "2")
	time.Sleep(12 * time.Second)
	w2pty.Write([]byte("n"))
	if _, ok := nineISXAwait(w2emu, func(s string) bool {
		return strings.Contains(s, "eng1") && strings.Contains(s, "eng2")
	}, 30*time.Second); !ok {
		t.Fatalf("window 2 never rendered the agents it owns\n%s", nineISXScreen(w2emu))
	}
	time.Sleep(3 * time.Second) // let window 2 settle before the baseline
	if strings.Contains(inbox7Row0(nineISXScreen(w2emu)), "✉") {
		t.Fatalf("window 2 shows an inbox count before anything was posted:\n%s", nineISXScreen(w2emu))
	}
	before := inbox7Layout(nineISXScreen(w2emu))

	// THE POST, from inside qa1's own pane, through the real socket.
	post := bin + " --allow-dev-delivery post 'rig question: which schema?' --default 'ship v2'"
	// The send's own output is EVIDENCE, not noise: a refused send and a send
	// that was delivered but whose command failed look identical from outside,
	// and the first rig run could not tell them apart.
	// TYPED INTO THE PANE, NOT SENT. `initech send` delivers as a BRACKETED
	// PASTE -- the default, for Claude Code -- and sh has no bracketed-paste
	// support: the first run of this rig left "00~" glued to the command and
	// sh answered "No such file or directory", which the rig then reported as
	// window 2 failing to show a post that never existed. Typing into window
	// 1's PTY is what an operator does and what an agent's shell sees;
	// identity still comes from the CONNECTION, so the post is qa1's.
	postedAt := time.Now()
	w1pty.Write([]byte(post + "\r"))
	time.Sleep(3 * time.Second)
	t.Logf("window 1 after the post was typed:\n%s", nineISXScreen(w1emu))

	// PRECONDITION, SEPARATELY ASSERTED: the post landed in window 1's store.
	// Without this, a post that never happened (a refused identity, a command
	// typed but not run) reports as "window 2 did not show the post" -- an
	// indictment of the product for something the instrument failed to do.
	// The first run of this rig did exactly that: an empty store, reported as
	// a missing count.
	if _, ok := nineISXAwait(w1emu, func(string) bool {
		b, _ := os.ReadFile(store)
		return strings.Contains(string(b), "rig question")
	}, 20*time.Second); !ok {
		t.Fatalf("THE RIG'S POST NEVER REACHED WINDOW 1'S STORE -- this is the instrument, not "+
			"the product under test. Nothing below can be asserted.\nwindow 1 (qa1's pane shows "+
			"what the post printed):\n%s", nineISXScreen(w1emu))
	}

	// AC 2: window 2's own corner count moves. Timed and logged: under the 30s
	// bound is consistent with the doorbell, and the time is recorded so a run
	// that only ever passes on the cadence is visible as such.
	_, ok := nineISXAwait(w2emu, func(s string) bool {
		return strings.Contains(inbox7Row0(s), "✉ 1")
	}, 45*time.Second)
	if !ok {
		b, _ := os.ReadFile(store)
		t.Fatalf("window 2's corner count never showed the post within the staleness bound.\n"+
			"store:\n%s\nwindow 2:\n%s", b, nineISXScreen(w2emu))
	}
	// MEASURED FROM THE POST, not from the start of this await: the store
	// precondition above has already waited, so an await-local duration times
	// nothing and reads like a doorbell latency. This is end to end, and it is
	// an UPPER bound on the doorbell -- the cadence could have served it.
	t.Logf("window 2's count showed the post %s after it was typed (bound %s)",
		time.Since(postedAt).Round(time.Millisecond), inboxFollowerRefresh)

	// AC 3: nothing moved.
	if after := inbox7Layout(nineISXScreen(w2emu)); after != before {
		t.Fatalf("A POST CHANGED WINDOW 2'S SCREEN OUTSIDE THE COUNT.\n\nbefore:\n%s\n\nafter:\n%s",
			before, after)
	}

	// AC 1: a reply typed in window 2 is written by window 1. Option+i opens
	// the panel (ESC 'i', the meta encoding a raw PTY sends).
	w2pty.Write([]byte{0x1b, 'i'})
	if _, ok := nineISXAwait(w2emu, func(s string) bool {
		return strings.Contains(s, "rig question")
	}, 15*time.Second); !ok {
		t.Fatalf("the inbox panel did not open in window 2 with the post in it\n%s", nineISXScreen(w2emu))
	}
	// Opening marks the first item seen, through window 1: the count clears.
	if _, ok := nineISXAwait(w2emu, func(s string) bool {
		return !strings.Contains(inbox7Row0(s), "✉")
	}, 15*time.Second); !ok {
		t.Fatalf("opening the panel in window 2 did not clear its count; mark-seen did not route\n%s",
			nineISXScreen(w2emu))
	}

	// A NATURAL REPLY, WITH BOTH 'a' AND 'd' IN IT. While this bead was in
	// flight the panel had no reply mode, so every 'a' accepted and every 'd'
	// dismissed mid-typing; this rig carried an a/d-free reply and said so.
	// eng2 fixed that (ini-qmbi, 15a2756): command mode and reply mode are
	// separate, Enter or 'r' starts composing. The rig types the reply an
	// operator would, which also exercises that fix from the child window.
	w2pty.Write([]byte("\r")) // Enter on the selected row: start composing.
	time.Sleep(2 * time.Second)
	const reply = "add the v2 schema"
	w2pty.Write([]byte(reply + "\r"))
	if _, ok := nineISXAwait(w2emu, func(string) bool {
		b, _ := os.ReadFile(store)
		return strings.Contains(string(b), "state: answered") && strings.Contains(string(b), reply)
	}, 20*time.Second); !ok {
		b, _ := os.ReadFile(store)
		t.Fatalf("a reply typed in window 2 never reached window 1's store as answered.\n"+
			"store:\n%s\nwindow 2:\n%s", b, nineISXScreen(w2emu))
	}
	b, _ := os.ReadFile(store)
	t.Logf("window 1's store after window 2's reply:\n%s", b)
	t.Logf("window 1's screen:\n%s", nineISXScreen(w1emu))
}
