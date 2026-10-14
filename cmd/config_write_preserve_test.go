package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
)

// annotatedSecret is the token-shaped value in testdata/annotated.yaml.
const annotatedSecret = "ghp_FAKEtoken0123456789abcdefABCDEF"

// annotatedConfig writes the hand-annotated fixture (full-line and
// end-of-line comments, blank lines, the operator's own key order, an unknown
// key, a token) into a fresh project dir, followed by the commented examples
// a new init writes. It returns the dir, the config path and the exact bytes.
func annotatedConfig(t *testing.T) (dir, path, orig string) {
	t.Helper()
	dir = t.TempDir()
	raw, err := os.ReadFile(filepath.Join("..", "internal", "config", "testdata", "annotated.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "initech.yaml")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), "__ROOT__", dir)), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := config.AppendMissingExamples(path); err != nil || n == 0 {
		t.Fatalf("AppendMissingExamples: n=%d err=%v", n, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "# claude_command:") {
		t.Fatal("fixture lacks the commented examples")
	}
	return dir, path, string(b)
}

// wantExact fails unless the file at path is exactly want, still 0600, and
// the token never reached the command's output.
func wantExact(t *testing.T, path, want, output string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Errorf("initech.yaml changed more than the intended key.\n--- got ---\n%s\n--- want ---\n%s", b, want)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode after write = %v (err %v), want 0600", fi.Mode().Perm(), err)
	}
	if strings.Contains(output, annotatedSecret) {
		t.Error("the token appeared in the command's output")
	}
}

// insertAfter returns s with add inserted right after the first anchor.
func insertAfter(t *testing.T, s, anchor, add string) string {
	t.Helper()
	i := strings.Index(s, anchor)
	if i < 0 {
		t.Fatalf("anchor %q not in fixture", anchor)
	}
	return s[:i+len(anchor)] + add + s[i+len(anchor):]
}

func cut(t *testing.T, s, part string) string {
	t.Helper()
	if !strings.Contains(s, part) {
		t.Fatalf("%q not in fixture", part)
	}
	return strings.Replace(s, part, "", 1)
}

// Hiring keeps every comment: the only change is the new role's line, after
// the last role.
func TestConfigWrite_AddAgentKeepsComments(t *testing.T) {
	dir, path, orig := annotatedConfig(t)
	chdirTemp(t, dir)
	origRunner := newAddAgentRunner
	newAddAgentRunner = func() iexec.Runner { return &iexec.FakeRunner{} }
	t.Cleanup(func() { newAddAgentRunner = origRunner })
	var out bytes.Buffer
	addAgentCmd.SetOut(&out)
	t.Cleanup(func() { addAgentCmd.SetOut(nil) })

	if err := runAddAgent(addAgentCmd, []string{"arch"}); err != nil {
		t.Fatalf("add-agent: %v", err)
	}
	wantExact(t, path, insertAfter(t, orig, "  - qa1\n", "  - arch\n"), out.String())
}

// Firing removes the role's own line and its override entry, nothing else:
// the comment above it and the blank line stay.
func TestConfigWrite_DeleteAgentKeepsComments(t *testing.T) {
	dir, path, orig := annotatedConfig(t)
	chdirTemp(t, dir)
	var out bytes.Buffer
	deleteAgentCmd.SetOut(&out)
	t.Cleanup(func() { deleteAgentCmd.SetOut(nil) })

	if err := runDeleteAgent(deleteAgentCmd, []string{"qa1"}); err != nil {
		t.Fatalf("delete-agent: %v", err)
	}
	want := cut(t, cut(t, orig, "  - qa1\n"), "  qa1:\n    tech_stack: Go   # qa tests in Go\n")
	wantExact(t, path, want, out.String())
}

// The TUI's hooks answer (yes and no) adds only attention.hooks, at the end
// of the live keys and above the commented examples.
func TestConfigWrite_ConsentAnswerKeepsComments(t *testing.T) {
	for _, granted := range []bool{true, false} {
		_, path, orig := annotatedConfig(t)
		proj, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		attentionConsentRecorder(path, proj)(granted)
		val := "false"
		if granted {
			val = "true"
		}
		wantExact(t, path, insertAfter(t, orig, "my_unknown_key: keep me\n", "attention:\n  hooks: "+val+"\n"), "")
	}
}

// The recorder holds a struct loaded when the TUI started. A role the
// operator added by hand since then must survive the answer being recorded.
func TestConfigWrite_ConsentAnswerKeepsHandEditsMadeSinceLoad(t *testing.T) {
	_, path, orig := annotatedConfig(t)
	proj, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := insertAfter(t, orig, "  - qa1\n", "  - eng9   # added by hand\n")
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	attentionConsentRecorder(path, proj)(false)
	wantExact(t, path, insertAfter(t, edited, "my_unknown_key: keep me\n", "attention:\n  hooks: false\n"), "")
}

// init on an existing project asks the hooks question (EOF on stdin = No)
// and records only that answer.
func TestConfigWrite_InitConsentKeepsComments(t *testing.T) {
	dir, path, orig := annotatedConfig(t)
	stubTTY(t, true)
	out := runInitForTest(t, dir, false)
	wantExact(t, path, insertAfter(t, orig, "my_unknown_key: keep me\n", "attention:\n  hooks: false\n"), out)
}
