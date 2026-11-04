//go:build !windows

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPromptNoticeRig_ARealRmPromptTellsSuperOnce runs the real initech with
// a real Claude Code agent on Haiku and super as a pane that records what it
// is sent (ini-6if, AC1). The agent is driven, through initech's own window
// as an operator would, to the rm safety prompt with its auto-block
// countdown. super's pane must receive exactly one [from initech] line naming
// the agent, the question, the reason and the time left, and nothing more
// while the prompt stays open. Nothing answers the prompt.
func TestPromptNoticeRig_ARealRmPromptTellsSuperOnce(t *testing.T) {
	if os.Getenv("INITECH_6IF") != "1" {
		t.Skip("set INITECH_6IF=1 to run the prompt-notice rig (starts a real Claude agent on Haiku)")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}
	bin := nineISXBuild(t)
	root, err := os.MkdirTemp("", "6ifrig")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	log := filepath.Join(root, "super.log")
	for _, r := range []string{"eng1", "super"} {
		os.MkdirAll(filepath.Join(root, r), 0o755)
		os.WriteFile(filepath.Join(root, r, "CLAUDE.md"), []byte("# "+r+"\n"), 0o644)
	}
	cfg := "project: sixif\nroot: " + root + "\nroles:\n    - eng1\n    - super\n" +
		"claude_args: [\"--dangerously-skip-permissions\", \"--model\", \"haiku\"]\n" +
		"role_overrides:\n    super:\n        command: [\"sh\", \"-c\", \"cat > " + log + "\"]\n"
	os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o644)
	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	// eng1 first: the focused pane, so what the rig types goes to it.
	os.WriteFile(filepath.Join(root, ".initech", "layout.yaml"),
		[]byte("grid: 2x1\nmode: grid\norder:\n    - eng1\n    - super\n"), 0o644)

	_, w1pty, emu, _ := nineISXStart(t, bin, root)
	key := func(s string) { w1pty.Write([]byte(s)); time.Sleep(400 * time.Millisecond) }

	// initech's own first-run question about a notification hook takes every
	// key while it is up: decline it first, as the other rigs do.
	if _, ok := nineISXAwait(emu, func(s string) bool { return strings.Contains(s, "y = yes") }, 30*time.Second); ok {
		key("n")
	}
	// Through the folder-trust prompt (cursor on No: Down, then Enter on
	// Yes) and, if shown, the bypass-mode warning.
	if _, ok := nineISXAwait(emu, func(s string) bool { return strings.Contains(s, "trust this folder") }, 90*time.Second); ok {
		key("\x1b[B")
		key("\r")
	}
	if _, ok := nineISXAwait(emu, func(s string) bool { return strings.Contains(s, "Yes, I accept") }, 15*time.Second); ok {
		key("\x1b[B")
		key("\r")
	}
	if _, ok := nineISXAwait(emu, func(s string) bool { return strings.Contains(s, "bypass permissions on") }, 60*time.Second); !ok {
		t.Fatalf("eng1 never reached its input box:\n%s", nineISXScreen(emu))
	}
	time.Sleep(2 * time.Second)
	w1pty.Write([]byte(`Run exactly this one Bash command and nothing else: rm -rf "$(mktemp -d /tmp/e4rig.XXXX)"`))
	time.Sleep(time.Second)
	key("\r")

	if _, ok := nineISXAwait(emu, func(s string) bool { return strings.Contains(s, "automatically block this") }, 120*time.Second); !ok {
		t.Fatalf("eng1 never showed the rm safety prompt with its countdown:\n%s", nineISXScreen(emu))
	}
	const head = "[from initech] eng1 is at a prompt it cannot pass alone"
	read := func() string { b, _ := os.ReadFile(log); return string(b) }
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(read(), head) && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	got := read()
	if !strings.Contains(got, head) {
		t.Fatalf("super received no notice within 20 s of the prompt; super's pane got %q", got)
	}
	for _, want := range []string{"Do you want to proceed?", "Dangerous rm operation", "Claude Code blocks it in", "initech keystroke eng1 y (or n)"} {
		if !strings.Contains(got, want) {
			t.Errorf("the notice lacks %q: %q", want, got)
		}
	}
	// The prompt stays open: nothing more arrives.
	time.Sleep(20 * time.Second)
	if n := strings.Count(read(), head); n != 1 {
		t.Errorf("super received %d notices for one open prompt, want 1: %q", n, read())
	}
	t.Logf("super's pane received: %q", strings.TrimSpace(read()))

	// The answer the notice names works: super's `initech keystroke eng1 n`
	// closes the prompt (No: the temp folder stays).
	// The rig's own session in its temp root: a dev build may deliver here.
	ks := exec.Command(bin, "keystroke", "--allow-dev-delivery", "eng1", "n")
	ks.Dir = root
	ks.Env = sixm4EnvWithout("INITECH_SOCKET", "INITECH_AGENT", "CLAUDECODE")
	if out, err := ks.CombinedOutput(); err != nil {
		t.Fatalf("initech keystroke eng1 n: %v\n%s", err, out)
	}
	if _, ok := nineISXAwait(emu, func(s string) bool { return !strings.Contains(s, "automatically block this") }, 15*time.Second); !ok {
		t.Errorf("initech keystroke eng1 n did not close the prompt: the notice names an answer that does not work\n%s", nineISXScreen(emu))
	}
}
