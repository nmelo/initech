package tui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

const pol = "shell_environment_policy.set."

// ini-pzla: a Codex seat's commands ran in a shared app-server daemon's
// environment, the rig that started it, so initech dialed another rig as
// another agent. The seat now starts with --no-daemon and its own three
// identity values set as Codex's explicit policy, which beats any snapshot.
func TestCodexGuard_AddsNoDaemonAndOnlyTheThreeIdentityValues(t *testing.T) {
	env := []string{"PATH=/bin", "OTHER=x", "INITECH_SOCKET=/rig/a.sock", "INITECH_AGENT=eng3", "BEADS_ACTOR=eng3"}
	got, err := codexGuardArgv([]string{"codex"}, env)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "--no-daemon",
		"-c", pol + "INITECH_SOCKET=/rig/a.sock", "-c", pol + "INITECH_AGENT=eng3", "-c", pol + "BEADS_ACTOR=eng3"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("argv %q\nwant %q", got, want)
	}
	if strings.Contains(strings.Join(got, " "), "OTHER") || strings.Contains(strings.Join(got, " "), "PATH") {
		t.Errorf("a value that is not identity went on argv: %q", got)
	}
}

// A role override keeps every argument in order, subcommand included; the
// guard sits right after the codex executable, before the subcommand.
func TestCodexGuard_AnOverrideKeepsItsArgsAndSubcommand(t *testing.T) {
	env := []string{"INITECH_SOCKET=/s", "INITECH_AGENT=eng1", "BEADS_ACTOR=eng1"}
	got, err := codexGuardArgv([]string{"/opt/homebrew/bin/codex", "resume", "--last", "-m", "gpt"}, env)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "/opt/homebrew/bin/codex" || got[1] != "--no-daemon" || strings.Join(got[len(got)-4:], " ") != "resume --last -m gpt" {
		t.Errorf("argv %q", got)
	}
	wrapped, err := codexGuardArgv([]string{"env", "FOO=1", "codex", "--model", "x"}, env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(wrapped[:4], " ") != "env FOO=1 codex --no-daemon" || strings.Join(wrapped[len(wrapped)-2:], " ") != "--model x" {
		t.Errorf("wrapped argv %q", wrapped)
	}
}

// The guard is added once: a command that already has --no-daemon keeps one,
// and an identity value the override carries is replaced by the pane's own,
// never kept (it is the very thing that pointed at another rig).
func TestCodexGuard_IsAddedOnceAndReplacesAStaleIdentity(t *testing.T) {
	env := []string{"INITECH_SOCKET=/old.sock", "INITECH_SOCKET=/rig/b.sock", "INITECH_AGENT=eng2", "BEADS_ACTOR=eng2"}
	in := []string{"codex", "--no-daemon", "-c", pol + "INITECH_SOCKET=/rig/a.sock", "--config=" + pol + "INITECH_AGENT=eng9", "resume"}
	got, err := codexGuardArgv(in, env)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Join(got, " ")
	if strings.Count(s, "--no-daemon") != 1 || strings.Contains(s, "/rig/a.sock") || strings.Contains(s, "eng9") || strings.Contains(s, "/old.sock") {
		t.Errorf("argv %q", got)
	}
	for _, w := range []string{pol + "INITECH_SOCKET=/rig/b.sock", pol + "INITECH_AGENT=eng2", pol + "BEADS_ACTOR=eng2"} {
		if strings.Count(s, w) != 1 {
			t.Errorf("%q appears %d times in %q", w, strings.Count(s, w), got)
		}
	}
	again, _ := codexGuardArgv(got, env)
	if strings.Join(again, " ") != s {
		t.Errorf("not idempotent:\n%q\n%q", got, again)
	}
	if got[len(got)-1] != "resume" {
		t.Errorf("the subcommand moved: %q", got)
	}
}

// Feed it nothing: no identity in the pane's environment puts no -c on argv
// (initech run outside a pane stays as it is); no codex executable at all
// refuses, since the guard has nowhere to go.
func TestCodexGuard_NoIdentityAndNoCodexExecutable(t *testing.T) {
	got, err := codexGuardArgv([]string{"codex"}, []string{"PATH=/bin"})
	if err != nil || strings.Join(got, " ") != "codex --no-daemon" {
		t.Errorf("no identity: %q %v", got, err)
	}
	if _, err := codexGuardArgv([]string{"npx", "@openai/codex-cli"}, nil); err == nil {
		t.Error("a command with no codex executable was guarded anyway")
	}
	if _, err := codexGuardArgv(nil, nil); err == nil {
		t.Error("an empty command was accepted")
	}
}

// pm's ruling: a Codex that rejects the guard fails the seat start with one
// line, and the seat never starts without the guard.
func TestNewPane_ACodexThatRejectsTheGuardIsNotStarted(t *testing.T) {
	orig := codexPreflight
	t.Cleanup(func() { codexPreflight = orig })
	var asked []string
	codexPreflight = func(argv []string) error {
		asked = argv
		return errors.New("error: unexpected argument '--no-daemon' found")
	}
	cfg := PaneConfig{Name: "eng3", AgentType: config.AgentTypeCodex, Command: []string{"codex"},
		Env: []string{"INITECH_SOCKET=/s", "INITECH_AGENT=eng3", "BEADS_ACTOR=eng3"}}
	p, err := NewPane(cfg, 24, 80)
	if err == nil {
		p.Close()
		t.Fatal("the seat started though its codex rejected the guard")
	}
	if strings.Contains(err.Error(), "\n") || !strings.Contains(err.Error(), "eng3") || !strings.Contains(err.Error(), "--no-daemon") {
		t.Errorf("error %q, want one line naming the seat and the guard", err)
	}
	if !strings.Contains(strings.Join(asked, " "), "--no-daemon") || !strings.Contains(strings.Join(asked, " "), pol+"INITECH_AGENT=eng3") {
		t.Errorf("the preflight was not asked about the guarded command: %q", asked)
	}
}

// Twins: Claude and generic panes are never guarded and never preflighted.
func TestNewPane_OnlyCodexSeatsAreGuarded(t *testing.T) {
	orig := codexPreflight
	t.Cleanup(func() { codexPreflight = orig })
	codexPreflight = func([]string) error { t.Error("preflight ran for a pane that is not Codex"); return nil }
	for _, at := range []string{config.AgentTypeClaudeCode, config.AgentTypeGeneric, ""} {
		p, err := NewPane(PaneConfig{Name: "x", AgentType: at, Command: []string{"sh", "-c", "exit 0"}}, 24, 80)
		if err != nil {
			t.Fatalf("%q: %v", at, err)
		}
		p.Close()
	}
}

// fakeCodex writes an executable named codex into a fresh folder: it exits 2
// with clap's message when it is given --no-daemon and rejects is true, and
// prints a version otherwise.
func fakeCodex(t *testing.T, rejects bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a shell script stands in for codex")
	}
	dir := t.TempDir()
	body := "#!/bin/sh\nfor a in \"$@\"; do\n"
	if rejects {
		body += "  [ \"$a\" = --no-daemon ] && { echo \"error: unexpected argument '--no-daemon' found\" >&2; exit 2; }\n"
	}
	body += "  :\ndone\necho 'codex-cli 0.0.0'\n"
	p := filepath.Join(dir, "codex")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// The real preflight, run as a process: a codex that rejects the guard
// refuses the seat with its own first error line; one that takes it passes.
func TestCodexPreflight_RunsTheGuardedCodex(t *testing.T) {
	env := []string{"INITECH_SOCKET=/s", "INITECH_AGENT=eng3", "BEADS_ACTOR=eng3"}
	for _, c := range []struct {
		rejects bool
		want    string
	}{{true, "unexpected argument '--no-daemon'"}, {false, ""}} {
		argv, err := codexGuardArgv([]string{fakeCodex(t, c.rejects), "resume", "--last"}, env)
		if err != nil {
			t.Fatal(err)
		}
		err = codexPreflight(argv)
		if c.want == "" && err != nil {
			t.Errorf("a codex that takes the guard was refused: %v", err)
		}
		if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("a codex that rejects the guard: %v, want %q", err, c.want)
		}
	}
}

// Only the three identity values ever go on argv (pm's ruling), whatever else
// the pane's environment holds. The three names are spelled out here, not
// read from codexIdentityKeys, so a fourth key added there is caught.
func TestCodexGuard_NothingButTheThreeIdentityKeysEverGoesOnArgv(t *testing.T) {
	env := []string{"HOME=/Users/x", "USER=x", "SHELL=/bin/zsh", "PATH=/bin", "TERM=xterm", "LANG=C",
		"TMPDIR=/tmp", "CODEX_HOME=/c", "GH_TOKEN=secret-shaped", "INITECH_SOCKET=/s", "INITECH_AGENT=eng3", "BEADS_ACTOR=eng3"}
	got, err := codexGuardArgv([]string{"codex"}, env)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"INITECH_SOCKET": true, "INITECH_AGENT": true, "BEADS_ACTOR": true}
	seen := 0
	for i, a := range got {
		if a != "-c" || i+1 >= len(got) {
			continue
		}
		key, _, _ := strings.Cut(strings.TrimPrefix(got[i+1], pol), "=")
		if !allowed[key] {
			t.Errorf("%q went on argv: only the three identity values may", got[i+1])
		}
		seen++
	}
	if seen != 3 {
		t.Errorf("%d -c values, want exactly the three identity values: %q", seen, got)
	}
	if s := strings.Join(got, " "); strings.Contains(s, "secret-shaped") || strings.Contains(s, "/Users/x") {
		t.Errorf("an environment value leaked onto argv: %q", got)
	}
}
