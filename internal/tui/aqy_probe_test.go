package tui

// aqy_probe_test.go watches WINDOW 1's SCREEN across a viewer attach (ini-aqy).
//
// This is the assertion nobody had. qa1's ini-9ka.2 verdict proved history and
// live bytes reach a hand-built CLIENT correctly -- it read the client side.
// Every multi-window test since drives the server or the client. Nobody ever
// looked at what window 1 DISPLAYS while a viewer attaches, which is where the
// operator's corruption lives.
//
// Window 1's PTY output is fed into a vt emulator, so that emulator IS what the
// operator sees. The panes print a fixed block and then sleep, so the screen is
// STATIC: any post-attach difference is corruption, not new output. The
// AQY_NO_ATTACH control runs the identical timeline without a viewer and comes
// back clean, which is what makes the attach the cause rather than a coincidence.
//
// GATED because it currently FAILS: ini-aqy is open and this test is the
// reproduction. It is committed rather than held locally so the next person can
// run one command instead of rebuilding the rig:
//
//	INITECH_AQY=1 go test ./internal/tui/ -run TestAQY -v -timeout 400s
//	INITECH_AQY=1 AQY_NO_ATTACH=1 go test ./internal/tui/ -run TestAQY -v   # clean control

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

func TestAQY_WindowOneScreenAcrossAttach(t *testing.T) {
	if os.Getenv("INITECH_AQY") != "1" {
		t.Skip("set INITECH_AQY=1 to run the ini-aqy window-1 corruption reproduction (open bug; this test fails by design)")
	}
	root, err := os.MkdirTemp("", "aqy")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer os.RemoveAll(root)

	for _, role := range []string{"super", "eng1"} {
		os.MkdirAll(filepath.Join(root, role), 0o755)
		os.WriteFile(filepath.Join(root, role, "CLAUDE.md"), []byte("# "+role+"\n"), 0o644)
	}
	cfg := "project: aqy\nroot: " + root + "\nroles:\n    - super\n    - eng1\n" +
		"claude_command:\n    - sh\nclaude_args:\n    - \"-c\"\n" +
		"    - \"for i in 1 2 3 4 5 6; do echo LINE-$i-AND-PUBLISHED-HOLDING-BEFORE-INSTALL; done; sleep 300\"\n" +
		"window_listen: \"127.0.0.1:7611\"\n"
	os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o644)

	// Build window 1 WITH THE RACE DETECTOR: the corruption looks like two
	// goroutines driving tcell at once, and -race names them outright.
	bin := filepath.Join(t.TempDir(), "initech-race")
	build := exec.Command("go", "build", "-race", "-o", bin, ".")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build -race: %v\n%s", err, out)
	}

	raceLog := filepath.Join(root, "race.log")
	w1 := newCmd(bin, root)
	w1.Env = append(w1.Env, "GORACE=log_path="+raceLog+" halt_on_error=0")
	ptmx, err := pty.StartWithSize(w1, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("start window 1: %v", err)
	}
	defer func() { ptmx.Close(); w1.Process.Kill(); w1.Process.Wait() }()

	var rawOut bytes.Buffer
	var rawMu sync.Mutex
	markOffset := func() int { rawMu.Lock(); defer rawMu.Unlock(); return rawOut.Len() }

	emu := vt.NewSafeEmulator(120, 40)
	go func() {
		b := make([]byte, 4096)
		for {
			if _, err := emu.Read(b); err != nil {
				return
			}
		}
	}()
	stop := make(chan struct{})
	go func() {
		buf := make([]byte, 32*1024)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := ptmx.Read(buf)
			if n > 0 {
				emu.Write(buf[:n])
				rawMu.Lock()
				rawOut.Write(buf[:n])
				rawMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	time.Sleep(8 * time.Second)
	attachAt := markOffset()
	before := snapRows(emu)
	t.Logf("BEFORE attach:\n%s", strings.Join(nonEmpty(before), "\n"))

	if os.Getenv("AQY_NO_ATTACH") == "" {
		v := newCmd(bin, root, "--window", "2")
		vptmx, err := pty.StartWithSize(v, &pty.Winsize{Rows: 40, Cols: 120})
		if err != nil {
			t.Fatalf("start viewer: %v", err)
		}
		defer func() { vptmx.Close(); v.Process.Kill(); v.Process.Wait() }()
		go func() {
			b := make([]byte, 32*1024)
			for {
				if _, err := vptmx.Read(b); err != nil {
					return
				}
			}
		}()
	} else {
		t.Log("CONTROL RUN: no viewer attached")
	}

	// Does it settle? Snapshot repeatedly -- this is also the operator's
	// recovery answer: corrupt-then-clean means "wait for a redraw", corrupt
	// forever means "restart".
	var after []string
	for _, d := range []time.Duration{2, 3, 5, 10, 15} {
		time.Sleep(d * time.Second)
		after = snapRows(emu)
		bad := 0
		for i := range before {
			if before[i] != after[i] {
				bad++
			}
		}
		t.Logf("  t+%02ds after attach: %d row(s) differ from pre-attach", d, bad)
	}
	t.Logf("AFTER attach:\n%s", strings.Join(nonEmpty(after), "\n"))

	diff := 0
	for i := range before {
		if before[i] != after[i] {
			diff++
			if diff <= 6 {
				t.Logf("ROW %d CHANGED:\n  before: %q\n  after:  %q", i, before[i], after[i])
			}
		}
	}
	if diff > 0 {
		t.Errorf("window 1's screen changed on %d row(s) when a viewer attached", diff)
	}
	rawMu.Lock()
	all := rawOut.Bytes()
	rawMu.Unlock()
	os.WriteFile("/tmp/aqy-w1.bin", all, 0o644)
	t.Logf("window 1 emitted %d bytes total; attach began at byte %d", len(all), attachAt)

	close(stop)

	// Surface any race report the child wrote.
	matches, _ := filepath.Glob(raceLog + "*")
	for _, m := range matches {
		if b, err := os.ReadFile(m); err == nil && len(b) > 0 {
			t.Errorf("RACE DETECTED in window 1 during attach:\n%s", string(b))
		}
	}
}

func snapRows(emu *vt.SafeEmulator) []string {
	out := make([]string, 40)
	for y := 0; y < 40; y++ {
		out[y] = strings.TrimRight(emu.RowText(y, 120), " ")
	}
	return out
}

func nonEmpty(rows []string) []string {
	var o []string
	for i, r := range rows {
		if r != "" {
			o = append(o, fmt.Sprintf("%2d| %s", i, r))
		}
	}
	return o
}

func newCmd(bin, root string, args ...string) *exec.Cmd {
	c := exec.Command(bin, args...)
	c.Dir = root
	c.Env = append(os.Environ(), "TERM=xterm-256color")
	return c
}
