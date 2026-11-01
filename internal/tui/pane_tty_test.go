//go:build linux || darwin

package tui

// ini-dyv: the parent closes its copy of the pty slave once the child has it,
// so the master hangs up when the child exits. On Linux the parent's copy kept
// every pane "alive" forever after its child exited. Real ptys, real children.

import (
	"os"
	"strings"
	"testing"
	"time"
)

func dyvPane(t *testing.T, script string) (*Pane, func() string) {
	t.Helper()
	p, err := NewPane(PaneConfig{Name: "eng1", Command: []string{"sh", "-c", script}}, 24, 80)
	if err != nil {
		t.Fatalf("start pane: %v", err)
	}
	p.Start()
	t.Cleanup(p.Close)
	return p, func() string { return strings.Join(nonEmpty(snapRows(p.emu)), "\n") }
}

func dyvAwaitDead(p *Pane, d time.Duration) bool {
	for end := time.Now().Add(d); time.Now().Before(end); {
		if !p.IsAlive() {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return !p.IsAlive()
}

// TestDyv_ChildThatExitsIsSeenDead is the defect by effect: the child exits
// and the pane must read dead (readLoop ended). At base on Linux the pane read
// alive for as long as it existed; darwin is the positive twin.
func TestDyv_ChildThatExitsIsSeenDead(t *testing.T) {
	p, screen := dyvPane(t, "exit 0")
	if !dyvAwaitDead(p, 5*time.Second) {
		t.Fatalf("the child exited but the pane still reads alive after 5s: the master never "+
			"hung up, so the parent still holds a copy of the slave. Screen:\n%s", screen())
	}
}

// TestDyv_LastLineSurvivesTheExit: a child that prints and exits at once has
// its line in the pane by the time the pane reads dead. A Linux master answers
// EIO once the slave is closed; the output already written comes first.
func TestDyv_LastLineSurvivesTheExit(t *testing.T) {
	p, screen := dyvPane(t, "echo LASTLINE933")
	if !dyvAwaitDead(p, 5*time.Second) {
		t.Fatalf("the child exited but the pane still reads alive. Screen:\n%s", screen())
	}
	if got := screen(); !strings.Contains(got, "LASTLINE933") {
		t.Fatalf("the pane read dead without the child's last line. Screen:\n%s", got)
	}
}

// TestDyv_MasterReadsTheChildsMode: with the parent's slave closed, the mode
// is read through the master, and it is the mode the child set: a child that
// sets raw reads raw, a cooked child reads cooked.
func TestDyv_MasterReadsTheChildsMode(t *testing.T) {
	for _, c := range []struct {
		name, script string
		wantRaw      bool
	}{
		{"raw child", "stty raw -echo -ixon && echo READY933 && sleep 30", true},
		{"cooked child", "echo READY933 && sleep 30", false},
	} {
		t.Run(strings.Fields(c.name)[0], func(t *testing.T) {
			p, screen := dyvPane(t, c.script)
			if !stashWaitFor(screen, "READY933", 5*time.Second) {
				t.Fatalf("the child never got ready. Screen:\n%s", screen())
			}
			raw, err := ptyRawMode(p.ptmx)
			if err != nil {
				t.Fatalf("reading the mode through the master failed: %v", err)
			}
			if raw != c.wantRaw {
				t.Fatalf("%s: the master read raw=%v, want %v", c.name, raw, c.wantRaw)
			}
		})
	}
}

// TestDyv_ChildHasItsTerminal: the slave is closed only AFTER the child has
// it, so the child's stdin is still a terminal. Closing it before the start
// would leave the child with no tty.
func TestDyv_ChildHasItsTerminal(t *testing.T) {
	_, screen := dyvPane(t, `echo "TTY=$(tty)"; sleep 2`)
	if !stashWaitFor(screen, "TTY=/dev/", 5*time.Second) {
		t.Fatalf("the child has no terminal. Screen:\n%s", screen())
	}
}

// TestDyv_ReleaseParentSlaveWithNothingToRelease feeds the release nothing: no
// pty, and a pty with no slave. Neither does anything.
func TestDyv_ReleaseParentSlaveWithNothingToRelease(t *testing.T) {
	releaseParentSlave(nil, "eng1")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	releaseParentSlave(&bareFilePty{w}, "eng1")
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatalf("a pty with no slave had something closed: %v", err)
	}
}

// TestDyv_ChildThatKeepsRunningStaysAlive: closing the parent's slave must not
// make a living child look dead. A child that keeps printing stays alive by
// both readings, and its output keeps arriving.
func TestDyv_ChildThatKeepsRunningStaysAlive(t *testing.T) {
	p, screen := dyvPane(t, `while :; do echo tick933; sleep 0.2; done`)
	if !stashWaitFor(screen, "tick933", 5*time.Second) {
		t.Fatalf("the child never printed. Screen:\n%s", screen())
	}
	lastOutput := func() time.Time { p.mu.Lock(); defer p.mu.Unlock(); return p.lastOutputTime }
	before := lastOutput()
	time.Sleep(2 * time.Second)
	if !p.IsAlive() {
		t.Fatal("a running child reads dead")
	}
	if p.cmd == nil || !childProcessAlive(p.cmd.Process) {
		t.Fatal("a running child does not answer signal 0")
	}
	if after := lastOutput(); !after.After(before) {
		t.Fatalf("the running child's output stopped arriving (last output %s, then %s)", before, after)
	}
}
