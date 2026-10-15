package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/scaffold"
	"github.com/nmelo/initech/internal/usage"
	"github.com/spf13/cobra"
)

// runInitForTest runs initech init in dir with every external effect stubbed
// (setup prompts, scaffold, git, bd) and returns its output.
func runInitForTest(t *testing.T, dir string, force bool) string {
	t.Helper()
	restoreWD := chdirForTest(t, dir)
	t.Cleanup(restoreWD)
	t.Cleanup(stubInitRunner(t, &fakeMultiRunner{responses: []fakeResponse{{err: fmt.Errorf("which bd: not found")}}}))
	t.Cleanup(stubScaffoldRun(t, func(*config.Project, scaffold.Options) ([]string, error) { return nil, nil }))
	t.Cleanup(stubInitGit(t))
	origSetup := initSetup
	initSetup = func(wd string) (*config.Project, error) {
		return &config.Project{Name: "demo", Root: wd, Roles: []string{"eng1"}}, nil
	}
	t.Cleanup(func() { initSetup = origSetup })
	origForce := initForce
	initForce = force
	t.Cleanup(func() { initForce = origForce })

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	return out.String()
}

// AC1: a fresh init writes the commented examples after the config; the file
// loads and validates; no tint is written live (the default stays the code's).
func TestInit_FreshConfigCarriesTheExamplesAndStillLoads(t *testing.T) {
	dir := t.TempDir()
	out := runInitForTest(t, dir, false)
	path := filepath.Join(dir, "initech.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{"# claude_command:", "# claude_args:", "# resource:", "# running_pane_tint: '#1e2022'", "# role_overrides:"} {
		if !strings.Contains(text, want) {
			t.Errorf("fresh initech.yaml lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\nrunning_pane_tint:") {
		t.Error("a live running_pane_tint was written; the default must stay the code's")
	}
	p, err := config.Load(path)
	if err != nil {
		t.Fatalf("fresh initech.yaml does not load: %v", err)
	}
	if p.RunningPaneTint != "" || len(p.ClaudeArgs) != 0 || p.Attention.HooksAnswered() {
		t.Errorf("examples took effect or consent was recorded without being asked: %+v", p)
	}
	if !strings.Contains(out, "commented example") {
		t.Errorf("init did not say it added examples:\n%s", out)
	}
}

// AC4: a plain init on an existing project changes nothing, byte for byte.
func TestInit_ExistingProjectWithoutForceIsUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")
	orig := "project: demo\nroot: " + dir + "\nroles: [eng1]\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	runInitForTest(t, dir, false)
	if b, _ := os.ReadFile(path); string(b) != orig {
		t.Errorf("plain init changed an existing initech.yaml:\n%s", b)
	}
}

// AC4 with the sensitive-path conditions: --force on a file holding an
// operator's values, comments and a token-shaped secret keeps every byte,
// appends only the missing examples, and never prints the secret.
func TestInit_ForceKeepsEveryByteIncludingTokensAndOnlyAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")
	const secret = "ghp_FAKEtoken0123456789abcdefABCDEF"
	orig := "# my notes stay\nproject: demo\nroot: " + dir + "\nroles: [eng1]\n" +
		"claude_args: [--continue]   # mine\ntoken: " + secret + "\n" +
		"remotes:\n  box:\n    addr: 10.0.0.9:7391\n    token: " + secret + "\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runInitForTest(t, dir, true)
	b, _ := os.ReadFile(path)
	got := string(b)
	if !strings.HasPrefix(got, orig) {
		t.Fatalf("--force changed existing bytes:\n%s", got)
	}
	added := got[len(orig):]
	if strings.Contains(added, "# claude_args:") {
		t.Error("an example was added for claude_args, which the file already sets")
	}
	if strings.Contains(added, secret) || strings.Contains(out, secret) {
		t.Error("the token appeared in the appended text or in init's output")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("permissions changed to %v", fi.Mode().Perm())
	}
	// A second --force appends nothing.
	runInitForTest(t, dir, true)
	if b2, _ := os.ReadFile(path); string(b2) != got {
		t.Error("a second --force changed the file again")
	}
}

// ini-62tn: the init run that writes initech.yaml prints the usage notice,
// and the written file carries the same line above the commented key that
// turns it off. A plain init on an existing project writes nothing and so
// prints no notice (positive twin of the fresh case).
func TestInit_TellsTheUserAboutTheUsagePing(t *testing.T) {
	dir := t.TempDir()
	out := runInitForTest(t, dir, false)
	if !strings.Contains(out, usage.Notice) {
		t.Fatalf("fresh init did not print the usage notice:\n%s", out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "initech.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "# "+usage.Notice+"\n# telemetry: false\n") {
		t.Fatalf("initech.yaml lacks the commented notice and key:\n%s", b)
	}
	p, err := config.Load(filepath.Join(dir, "initech.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsTelemetryEnabled() {
		t.Fatal("the commented example turned telemetry off")
	}

	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "initech.yaml"), []byte("project: demo\nroot: "+other+"\nroles: [eng1]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := runInitForTest(t, other, false); strings.Contains(out, usage.Notice) {
		t.Errorf("plain init on an existing project printed the notice:\n%s", out)
	}
}
