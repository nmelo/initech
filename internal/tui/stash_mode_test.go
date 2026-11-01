//go:build linux || darwin

package tui

// ini-933: the stash key (Ctrl+S) goes only into a terminal in raw mode. A
// Linux pty starts in cooked mode with IXON set, where 0x13 is XOFF: the first
// send with Enter into a shell, a booting agent or the rigs' echo child froze
// the pane's output for good. These tests use real ptys and real children.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/xpty"
	"golang.org/x/sys/unix"
)

// stashTestPane starts a real pane running sh -c script with args, and
// returns it with a function that reads its screen.
func stashTestPane(t *testing.T, script string, args ...string) (*Pane, func() string) {
	t.Helper()
	cmd := append([]string{"sh", "-c", script, "sh"}, args...)
	p, err := NewPane(PaneConfig{Name: "eng1", Command: cmd}, 24, 80)
	if err != nil {
		t.Fatalf("start pane: %v", err)
	}
	p.Start()
	t.Cleanup(p.Close)
	return p, func() string { return strings.Join(nonEmpty(snapRows(p.emu)), "\n") }
}

func stashWaitFor(get func() string, want string, d time.Duration) bool {
	for end := time.Now().Add(d); time.Now().Before(end); {
		if strings.Contains(get(), want) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// stashLogMode records the pane's terminal flags, so a run on each OS shows
// which mode the test actually exercised.
func stashLogMode(t *testing.T, p *Pane) {
	t.Helper()
	m, ok := p.ptmx.(masterHolder)
	if !ok {
		t.Logf("pty exposes no master")
		return
	}
	raw, err := termiosRawMode(m.Master())
	t.Logf("%s (read via the master): raw=%v err=%v", p.ptmx.Name(), raw, err)
}

// TestNineThreeThree_CookedChildKeepsFlowingAfterASend is the defect by
// effect: a real child in cooked mode (the rigs' echo loop) gets a send with
// Enter, and its output must keep flowing. At base on Linux the stash key
// arrived as XOFF and nothing more ever rendered; darwin is the positive twin.
func TestNineThreeThree_CookedChildKeepsFlowingAfterASend(t *testing.T) {
	if testing.Short() {
		t.Skip("real child process and the send path's startup gate (up to ~17s); runs in the full suite")
	}
	p, screen := stashTestPane(t, `while read l; do echo "GOT:$l"; done`)
	time.Sleep(300 * time.Millisecond)
	stashLogMode(t, p)

	p.SendText("first933", true)
	if !stashWaitFor(screen, "GOT:first933", 10*time.Second) {
		t.Fatalf("the child's answer to a sent message never rendered: the pane's output "+
			"stopped at the send (XOFF from the stash key). Screen:\n%s", screen())
	}
	if _, err := p.ptmx.Write([]byte("after933\r")); err != nil {
		t.Fatalf("write after the send: %v", err)
	}
	if !stashWaitFor(screen, "GOT:after933", 5*time.Second) {
		t.Fatalf("output stopped after the send. Screen:\n%s", screen())
	}
}

// stashRecordingPane starts a child that applies sttyArgs to its own terminal
// and then copies everything it receives into a file; it returns the pane and
// a reader of that file.
func stashRecordingPane(t *testing.T, sttyArgs string) (*Pane, func() string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "received")
	p, _ := stashTestPane(t, `stty `+sttyArgs+` && echo READY933 && exec cat > "$1"`, out)
	return p, func() string {
		b, _ := os.ReadFile(out)
		return string(b)
	}
}

func stashAwaitMode(t *testing.T, p *Pane, raw bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
		if got, err := ptyRawMode(p.ptmx); err == nil && got == raw {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	got, err := ptyRawMode(p.ptmx)
	t.Fatalf("the child's terminal never read raw=%v (raw=%v err=%v)", raw, got, err)
}

// TestNineThreeThree_RawModeChildGetsTodaysBytesExactly is the positive twin
// over bytes: a child in raw mode, as Claude Code is, receives the stash key,
// the bracketed body and one Enter, byte for byte what it received before.
func TestNineThreeThree_RawModeChildGetsTodaysBytesExactly(t *testing.T) {
	if testing.Short() {
		t.Skip("real child process and the send path's startup gate (up to ~17s); runs in the full suite")
	}
	p, received := stashRecordingPane(t, "raw -echo -ixon")
	stashAwaitMode(t, p, true)

	const body = "raw933 body"
	p.SendText(body, true)
	want := "\x13\x1b[200~" + body + "\x1b[201~\r"
	stashWaitFor(received, "\x1b[201~\r", 10*time.Second)
	time.Sleep(300 * time.Millisecond) // nothing may trail the Enter
	if got := received(); got != want {
		t.Fatalf("a raw-mode child received %q, want %q (today's bytes exactly)", got, want)
	}
}

// TestNineThreeThree_CookedChildReceivesNoStashByte: canonical input with
// flow control off, so 0x13 would not freeze anything but would land in the
// line as a stray byte. The child must receive the body and the Enter only.
// This one fails on darwin too if the stash is typed into a cooked terminal.
func TestNineThreeThree_CookedChildReceivesNoStashByte(t *testing.T) {
	if testing.Short() {
		t.Skip("real child process and the send path's startup gate (up to ~17s); runs in the full suite")
	}
	p, received := stashRecordingPane(t, "-echo -ixon")
	stashAwaitMode(t, p, false)

	const body = "cooked933"
	p.SendText(body, true)
	stashWaitFor(received, body, 10*time.Second)
	time.Sleep(300 * time.Millisecond)
	got := received()
	if strings.Contains(got, "\x13") {
		t.Fatalf("a cooked terminal received the stash key: %q", got)
	}
	if want := "\x1b[200~" + body + "\x1b[201~\n"; got != want {
		t.Fatalf("a cooked child received %q, want %q", got, want)
	}
}

func stashTermios(t *testing.T, f *os.File) *unix.Termios {
	t.Helper()
	rc, err := f.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var tio *unix.Termios
	var ioErr error
	if err := rc.Control(func(fd uintptr) { tio, ioErr = unix.IoctlGetTermios(int(fd), ioctlReadTermios) }); err != nil {
		t.Fatal(err)
	}
	if ioErr != nil {
		t.Fatal(ioErr)
	}
	return tio
}

func stashSetTermios(t *testing.T, f *os.File, tio *unix.Termios) {
	t.Helper()
	rc, err := f.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var ioErr error
	if err := rc.Control(func(fd uintptr) { ioErr = unix.IoctlSetTermios(int(fd), ioctlWriteTermios, tio) }); err != nil {
		t.Fatal(err)
	}
	if ioErr != nil {
		t.Fatal(ioErr)
	}
}

// TestNineThreeThree_RawNeedsBothCanonicalAndFlowControlOff reads a real
// pty's slave through each combination of the two bits: raw only when ICANON
// and IXON are both clear. The read never changes the termios.
func TestNineThreeThree_RawNeedsBothCanonicalAndFlowControlOff(t *testing.T) {
	pty, err := xpty.NewPty(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()
	slave := pty.(slaveHolder).Slave()

	cases := []struct {
		name         string
		icanon, ixon bool
		wantRaw      bool
	}{
		{"cooked (ICANON and IXON set)", true, true, false},
		{"canonical, flow control off", true, false, false},
		{"cbreak with flow control (ICANON clear, IXON set)", false, true, false},
		{"raw (both clear)", false, false, true},
	}
	for _, c := range cases {
		tio := stashTermios(t, slave)
		tio.Lflag &^= unix.ICANON
		tio.Iflag &^= unix.IXON
		if c.icanon {
			tio.Lflag |= unix.ICANON
		}
		if c.ixon {
			tio.Iflag |= unix.IXON
		}
		stashSetTermios(t, slave, tio)
		before := *stashTermios(t, slave)

		raw, err := ptyRawMode(pty)
		if err != nil {
			t.Fatalf("%s: read failed: %v", c.name, err)
		}
		if raw != c.wantRaw {
			t.Errorf("%s: raw=%v, want %v", c.name, raw, c.wantRaw)
		}
		if after := *stashTermios(t, slave); after != before {
			t.Errorf("%s: reading the mode changed the termios: %+v -> %+v", c.name, before, after)
		}
	}
}

// bareFilePty is a pty that is not a terminal and says nothing about its mode.
type bareFilePty struct{ *os.File }

func (f *bareFilePty) Resize(int, int) error   { return nil }
func (f *bareFilePty) Size() (int, int, error) { return 80, 24, nil }
func (f *bareFilePty) Name() string            { return "bare" }
func (f *bareFilePty) Start(*exec.Cmd) error   { return nil }

// TestNineThreeThree_UnreadableModeIsAnErrorNeverRaw feeds the reader nothing
// usable: no pty, a pty whose master (the end the mode is read from) is
// closed, and a pty that is not a terminal. Each is an error, so the send types no stash.
func TestNineThreeThree_UnreadableModeIsAnErrorNeverRaw(t *testing.T) {
	if raw, err := ptyRawMode(nil); raw || !errors.Is(err, errNoPTY) {
		t.Errorf("no pty: raw=%v err=%v, want false and errNoPTY", raw, err)
	}

	pty, err := xpty.NewPty(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()
	_ = pty.(masterHolder).Master().Close()
	if raw, err := ptyRawMode(pty); raw || err == nil {
		t.Errorf("closed master: raw=%v err=%v, want false and an error", raw, err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if raw, err := ptyRawMode(&bareFilePty{w}); raw || err == nil {
		t.Errorf("not a terminal: raw=%v err=%v, want false and an error", raw, err)
	}
}

// TestNineThreeThree_UnreadableModeLogsOncePerPane: no stash, and one line
// naming why, once per pane however many sends follow.
func TestNineThreeThree_UnreadableModeLogsOncePerPane(t *testing.T) {
	logs := captureLogs(t)
	_, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	a := &Pane{name: "eng1", ptmx: &bareFilePty{w}}
	b := &Pane{name: "eng2", ptmx: &bareFilePty{w}}
	for i := 0; i < 3; i++ {
		if a.stashKeyAllowed() || b.stashKeyAllowed() {
			t.Fatal("a terminal whose mode cannot be read allowed the stash key")
		}
	}
	text := logs.String()
	const line = "no stash key: the terminal's mode could not be read"
	if n := strings.Count(text, line); n != 2 {
		t.Fatalf("logged %d times for two panes and six sends, want 2 (once per pane):\n%s", n, text)
	}
	if !strings.Contains(text, "pane=eng1") || !strings.Contains(text, "pane=eng2") {
		t.Fatalf("the line does not name each pane:\n%s", text)
	}
}
