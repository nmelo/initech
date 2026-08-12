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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
