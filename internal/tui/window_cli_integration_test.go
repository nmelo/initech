//go:build !windows

// window_cli_integration_test.go is ini-9ka.6's end-to-end half: the user
// story, not the transport. The byte hop was pinned on ini-9ka.2 (qa1 read
// real history and live bytes off an accepted stream), so these tests assert
// what an operator experiences -- multiple windows attached at once, each
// showing its own groups, and the full attach/detach/reattach cycle.
//
// Gated behind INITECH_CENSUS=1 where a real binary is needed.
package tui

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestWindowCLI_MalformedConfigErrorsIdenticallyWithAndWithoutFlag is the
// AC's "viewer mode is not a validation bypass". Asserted by running the REAL
// binary both ways against the same broken config and comparing output: the
// claim is about what the operator sees, and only the binary can settle that.
//
// The ordering that makes this true is structural -- config load/validate runs
// before viewer construction in runTUI, so --window cannot be reached until
// validation has already passed -- but structural arguments are exactly what
// this repo has learned to verify rather than assert.
func TestWindowCLI_MalformedConfigErrorsIdenticallyWithAndWithoutFlag(t *testing.T) {
	if os.Getenv("INITECH_CENSUS") != "1" {
		t.Skip("set INITECH_CENSUS=1 to run the real-binary CLI checks")
	}
	root, err := os.MkdirTemp("", "ini-cli-")
	if err != nil {
		t.Fatalf("mkdir temp root: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	// Structurally invalid YAML: fails at load, well before anything
	// window-specific could run.
	bad := "project: broken\n  root: [this is not: valid yaml\n"
	if err := os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(bad), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	bin := buildInitechBinary(t)
	run := func(args ...string) string {
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		out, _ := cmd.CombinedOutput()
		return strings.TrimSpace(string(out))
	}

	plain := run()
	windowed := run("--window", "2")

	if plain == "" {
		t.Fatal("plain initech produced no error on a malformed config")
	}
	if plain != windowed {
		t.Errorf("--window 2 produced a DIFFERENT error than plain initech on the same malformed config.\nviewer mode must not be a new error surface.\nplain:    %q\nwindowed: %q", plain, windowed)
	}
}

// TestWindowCLI_NoSessionToAttachToIsActionable checks the message an operator
// actually sees when they run --window against a project whose window 1 is not
// running -- the most likely first-run mistake.
func TestWindowCLI_NoSessionToAttachToIsActionable(t *testing.T) {
	if os.Getenv("INITECH_CENSUS") != "1" {
		t.Skip("set INITECH_CENSUS=1 to run the real-binary CLI checks")
	}
	root, err := os.MkdirTemp("", "ini-cli2-")
	if err != nil {
		t.Fatalf("mkdir temp root: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	// Valid, multi-window-enabled, but nothing is listening.
	cfg := "project: lonelyproj\nroot: " + root + `
roles:
    - solo
window_listen: 127.0.0.1:59998
`
	if err := os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "solo"), 0o755); err != nil {
		t.Fatalf("mkdir role dir: %v", err)
	}

	bin := buildInitechBinary(t)
	cmd := exec.Command(bin, "--window", "2")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	got := string(out)

	if err == nil {
		t.Error("initech --window 2 succeeded with no session to attach to; it should fail fast rather than start a blank window")
	}
	for _, want := range []string{"no initech session to attach to", "Start window 1 first", "initech --window 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("error output missing %q; it must name what to start and how to retry.\ngot: %s", want, got)
		}
	}
}

// TestWindowCLI_HelpAdvertisesTheFlag runs the check qa1 pre-announced: the
// SURFACE, not the source. A flag can be registered in code and still not
// reach rendered help (Hidden, a custom template that drops local flags), and
// help output is what a user actually consults.
func TestWindowCLI_HelpAdvertisesTheFlag(t *testing.T) {
	if os.Getenv("INITECH_CENSUS") != "1" {
		t.Skip("set INITECH_CENSUS=1 to run the real-binary CLI checks")
	}
	bin := buildInitechBinary(t)
	out, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("initech --help: %v\n%s", err, out)
	}
	help := string(out)
	if !strings.Contains(help, "--window") {
		t.Fatalf("--window does not appear in the shipped binary's --help output:\n%s", help)
	}
	// The non-obvious half must be stated where the user reads it.
	lower := strings.ToLower(help)
	if !strings.Contains(lower, "folds its agents back into window 1") {
		t.Error("--help does not state what happens to agents when the window closes")
	}
	if !strings.Contains(lower, "rerunning restores") {
		t.Error("--help does not state restore-on-reattach")
	}
}

// TestWindowCLI_ThreeWindowsAttachDetachReattach is the AC's quantifier
// discipline: N is not limited to 2. Three windows attach at once, each
// showing only its own groups; one detaches and its agents fold back to
// window 1 WITHOUT disturbing the third; it reattaches and gets them back.
//
// Asserted through the same predicate the render path uses, driven by the
// real server's live connected set -- so this exercises attach, liveness, and
// fold-back together as an operator would experience them, rather than
// re-verifying the transport that ini-9ka.2 already pinned.
func TestWindowCLI_ThreeWindowsAttachDetachReattach(t *testing.T) {
	root := t.TempDir()
	a, err := LoadAssignment(root)
	if err != nil {
		t.Fatalf("LoadAssignment: %v", err)
	}
	// core stays on window 1; eng -> window-2; qa -> window-3.
	if err := a.MoveGroup("eng", "window-2"); err != nil {
		t.Fatalf("MoveGroup eng: %v", err)
	}
	if err := a.MoveGroup("qa", "window-3"); err != nil {
		t.Fatalf("MoveGroup qa: %v", err)
	}
	groupOf := map[string]string{"super": "core", "eng1": "eng", "qa1": "qa"}

	panes := []*Pane{
		windowServerTestPane("super"),
		windowServerTestPane("eng1"),
		windowServerTestPane("qa1"),
	}
	ws, addr := startTestWindowServer(t, panes)

	// Assert the full ownership picture in one place, for a given liveness.
	assertOwnership := func(stage string, want map[string]string) {
		t.Helper()
		connected := ws.connectedWindows()
		for agent, wantWin := range want {
			for _, win := range []string{WindowOne, "window-2", "window-3"} {
				got := rendersInWindow(agent, win, a, groupOf, connected)
				if want := win == wantWin; got != want {
					t.Errorf("%s: rendersInWindow(%q, %q) = %v, want %v (expected owner %q, connected=%v)",
						stage, agent, win, got, want, wantWin, connected)
				}
			}
		}
	}

	s2, c2, _ := dialWindow(t, addr, "window-2")
	defer s2.Close()
	defer c2.Close()
	s3, c3, _ := dialWindow(t, addr, "window-3")
	defer s3.Close()
	defer c3.Close()
	waitForClients(t, ws, 2)

	assertOwnership("all three attached", map[string]string{
		"super": WindowOne,
		"eng1":  "window-2",
		"qa1":   "window-3",
	})

	// Window 2 detaches: its agent folds back, window 3 is untouched.
	c2.Close()
	s2.Close()
	waitForWindowGone(t, ws, "window-2")

	assertOwnership("window-2 detached", map[string]string{
		"super": WindowOne,
		"eng1":  WindowOne, // folded back
		"qa1":   "window-3",
	})

	// Reattach: window 2 gets its agent back, exactly as before.
	s2b, c2b, _ := dialWindow(t, addr, "window-2")
	defer s2b.Close()
	defer c2b.Close()
	waitForClients(t, ws, 2)

	assertOwnership("window-2 reattached", map[string]string{
		"super": WindowOne,
		"eng1":  "window-2",
		"qa1":   "window-3",
	})

	// Assignment must be untouched by the whole cycle.
	if got := a.WindowOfGroup("eng"); got != "window-2" {
		t.Errorf("assignment for eng = %q after a detach/reattach cycle, want window-2 unchanged", got)
	}
	if got := a.WindowOfGroup("qa"); got != "window-3" {
		t.Errorf("assignment for qa = %q, want window-3 unchanged", got)
	}
}

// waitForWindowGone blocks until the named window leaves the connected set.
func waitForWindowGone(t *testing.T, ws *windowServer, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !ws.connectedWindows()[name] {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("window %q still connected after 5s", name)
}

// TestWindowCLI_ViewerStartsWhileWindowOneOwnsTheIPCSocket is ini-civ's
// regression, and it is deliberately the heaviest test in this file.
//
// WHY IT HAS TO RUN THE REAL BINARY. The bug was that a viewer fell through to
// the common startup path and called startIPC, colliding with window 1's socket
// -- so `initech --window 2` never started, on its first real-world invocation,
// with an error telling the operator to run `initech down` and kill the fleet he
// was trying to put on a second monitor. Every existing multi-window test in
// this package drives startTestWindowServer and dialWindow DIRECTLY, below
// tui.Run, so none of them ever executed the startup path where the collision
// lives. A component-level rig cannot catch a composed-startup bug; that seam
// has now bitten three times (predicate consumers, pane geometry, and this).
//
// So: a genuinely ACTIVE unix socket at the project's IPC path, a real window-1
// listener to satisfy the reachability check, and the real binary launched with
// --window 2 under a PTY.
func TestWindowCLI_ViewerStartsWhileWindowOneOwnsTheIPCSocket(t *testing.T) {
	// Short root: a unix socket path has a ~104 byte limit and t.TempDir()'s
	// name is long enough to blow it (see this file's header note).
	root, err := os.MkdirTemp("", "civ")
	if err != nil {
		t.Fatalf("temp root: %v", err)
	}
	defer os.RemoveAll(root)

	for _, role := range []string{"super", "eng1"} {
		if err := os.MkdirAll(filepath.Join(root, role), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", role, err)
		}
		if err := os.WriteFile(filepath.Join(root, role, "CLAUDE.md"), []byte("# "+role+"\n"), 0o644); err != nil {
			t.Fatalf("write CLAUDE.md: %v", err)
		}
	}

	// Stand in for window 1's pane-stream listener so checkWindowOneReachable
	// passes and startup proceeds to the part under test.
	_, addr := startTestWindowServer(t, []*Pane{windowServerTestPane("super")})

	cfgYAML := "project: civ\nroot: " + root + "\nroles:\n    - super\n    - eng1\n" +
		"claude_command:\n    - sleep\nclaude_args:\n    - \"300\"\n" +
		"window_listen: \"" + addr + "\"\n"
	if err := os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// WINDOW 1 OWNS THE IPC SOCKET. A real listener that really accepts -- the
	// single-instance guard dials it, so a mere file would not reproduce the
	// collision and the test would pass against the unfixed code.
	sockPath := filepath.Join(root, ".initech", "initech.sock")
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		t.Fatalf("mkdir .initech: %v", err)
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("bind window 1's socket: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	out := runViewerUnderPTY(t, buildInitechBinary(t), root, 6*time.Second)

	for _, forbidden := range []string{"start IPC", "already running", "initech down"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("viewer startup hit the window-1 IPC socket (found %q). A secondary window "+
				"hosts zero local agents and must not serve project IPC.\n--- output ---\n%s",
				forbidden, out)
		}
	}

	// Teardown symmetry: the viewer must never unlink window 1's socket.
	if _, err := os.Stat(sockPath); err != nil {
		t.Errorf("window 1's IPC socket is gone after the viewer ran (%v) -- a viewer's exit "+
			"cleanup must not remove a socket it does not own", err)
	}
}

// runViewerUnderPTY launches `bin --window 2` in root under a PTY and returns
// what it wrote. A PTY is required: the TUI creates a tcell screen at startup
// and fails on /dev/tty long before reaching the code under test otherwise.
//
// exitSig chooses HOW the viewer dies, and that choice is load-bearing rather
// than incidental. Round 1 of ini-civ used Kill only, which never runs the
// signal handler -- so the test asserted the socket survived across an exit
// path no operator takes, and missed that SIGHUP (closing the terminal, the
// natural way to shut a second monitor) deleted it. A Kill teardown is a
// signals-never-ran costume.
func runViewerUnderPTY(t *testing.T, bin, root string, hold time.Duration) string {
	t.Helper()
	return runViewerUnderPTYSig(t, bin, root, hold, os.Kill)
}

func runViewerUnderPTYSig(t *testing.T, bin, root string, hold time.Duration, exitSig os.Signal) string {
	t.Helper()
	cmd := exec.Command(bin, "--window", "2")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 160})
	if err != nil {
		t.Fatalf("start viewer under pty: %v", err)
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	var seen strings.Builder
	_ = exitSig
	deadline := time.Now().Add(hold)
	buf := make([]byte, 32*1024)
	for time.Now().Before(deadline) {
		_ = ptmx.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, err := ptmx.Read(buf)
		if n > 0 {
			seen.Write(buf[:n])
		}
		if err != nil && n == 0 {
			continue
		}
	}

	// Exit the way the caller asked, and give the handler time to run before
	// the deferred Kill takes the process out from under it.
	if exitSig != os.Kill {
		_ = cmd.Process.Signal(exitSig)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if cmd.ProcessState != nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return seen.String()
}

// TestWindowCLI_ViewerSIGHUPLeavesWindowOnesSocketAndPIDAlone is ini-civ round
// 2, and it exists because round 1's test could not see the bug it was written
// to catch.
//
// SIGHUP is THE natural viewer exit: the operator closes the second monitor's
// terminal window. installSignalHandlers os.Remove()s every path it was given
// before os.Exit, and it was handed window 1's socket and PID file
// unconditionally -- so the ordinary way to close a second window deleted the
// live session's IPC socket. qa1 measured it on a real two-window rig: window 1
// kept rendering while `initech status` reported nothing running and fleet
// messaging was dead until restart.
//
// Round 1's test tore the viewer down with Process.Kill, which never runs the
// handler, so it asserted survival across an exit path no operator takes. The
// signal is the whole point here, not a detail of teardown.
func TestWindowCLI_ViewerSIGHUPLeavesWindowOnesSocketAndPIDAlone(t *testing.T) {
	root, err := os.MkdirTemp("", "civ2")
	if err != nil {
		t.Fatalf("temp root: %v", err)
	}
	defer os.RemoveAll(root)

	for _, role := range []string{"super", "eng1"} {
		if err := os.MkdirAll(filepath.Join(root, role), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", role, err)
		}
		if err := os.WriteFile(filepath.Join(root, role, "CLAUDE.md"), []byte("# "+role+"\n"), 0o644); err != nil {
			t.Fatalf("write CLAUDE.md: %v", err)
		}
	}

	_, addr := startTestWindowServer(t, []*Pane{windowServerTestPane("super")})
	cfgYAML := "project: civ2\nroot: " + root + "\nroles:\n    - super\n    - eng1\n" +
		"claude_command:\n    - sleep\nclaude_args:\n    - \"300\"\n" +
		"window_listen: \"" + addr + "\"\n"
	if err := os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Window 1's live state: a socket that really accepts, and a PID file with
	// a PID that is genuinely alive (this test process), so nothing downstream
	// can dismiss either as stale.
	dir := filepath.Join(root, ".initech")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir .initech: %v", err)
	}
	sockPath := filepath.Join(dir, "initech.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("bind window 1's socket: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	pidPath := filepath.Join(dir, pidFileName)
	windowOnePID := fmt.Sprintf("%d\n", os.Getpid())
	if err := os.WriteFile(pidPath, []byte(windowOnePID), 0o600); err != nil {
		t.Fatalf("write window 1 PID file: %v", err)
	}

	runViewerUnderPTYSig(t, buildInitechBinary(t), root, 5*time.Second, syscall.SIGHUP)

	if _, err := os.Stat(sockPath); err != nil {
		t.Errorf("window 1's IPC socket was deleted by the viewer's SIGHUP exit (%v).\n"+
			"Closing a second monitor's terminal must not kill the session's fleet messaging.", err)
	}
	got, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("window 1's PID file was deleted by the viewer's SIGHUP exit: %v", err)
	}
	if string(got) != windowOnePID {
		t.Errorf("window 1's PID file was overwritten: got %q, want %q -- a viewer must not "+
			"claim the session's identity", strings.TrimSpace(string(got)), strings.TrimSpace(windowOnePID))
	}
}
