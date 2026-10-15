package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/usage"
)

// ini-vby5.12 AC2: the generated text names no model, host, path or token.
// Placeholders (<model>, <path>, <percent>) are the only variable parts.
func TestExamples_NameNoModelHostPathOrToken(t *testing.T) {
	text := MissingExamples("")
	for _, pat := range []string{
		`(?i)\b(opus|sonnet|haiku|fable|claude-[a-z0-9-]+\d)\b`, // model names
		`\b\d{1,3}(\.\d{1,3}){3}\b`,                             // IPv4 hosts
		`(?i)\b[a-z0-9-]+\.(com|net|org|io|dev|local)\b`,        // host names
		`(^|\s|\[|,)(/|~/)[^\s,\]]*`,                            // paths
		`(?i)(token|secret|api[_-]?key|password)\s*[:=]`,        // credentials
		`(?i)\b(sk|ghp|xox[bp])[-_][a-z0-9]{6,}`,                // token shapes
	} {
		if m := regexp.MustCompile(pat).FindString(text); m != "" {
			t.Errorf("examples contain %q (pattern %s)", m, pat)
		}
	}
	for _, ph := range []string{"<model>", "<path>", "<percent>", "<role>"} {
		if !strings.Contains(text, ph) {
			t.Errorf("examples lost the placeholder %s", ph)
		}
	}
}

// Every example line is a comment: uncommenting is the operator's choice,
// and the block on its own parses as an empty config.
func TestExamples_AreAllCommentedOut(t *testing.T) {
	for i, line := range strings.Split(MissingExamples(""), "\n") {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#") {
			t.Errorf("line %d is live, not a comment: %q", i, line)
		}
	}
}

// Uncommented, each example is valid YAML for the key it shows, so an
// operator who removes the '#'s gets a config that loads.
func TestExamples_UncommentedTheyParseIntoTheirKeys(t *testing.T) {
	var live []string
	for _, line := range strings.Split(MissingExamples(""), "\n") {
		if strings.HasPrefix(line, "# ") && !strings.HasPrefix(line, "# ──") &&
			(strings.Contains(line, ":") && !strings.HasSuffix(strings.TrimSpace(line), ".")) {
			body := strings.TrimPrefix(line, "# ")
			if strings.HasPrefix(body, " ") || regexp.MustCompile(`^[a-z_]+:`).MatchString(body) {
				live = append(live, strings.NewReplacer("<percent>", "90", "<other-role>", "eng2", "<role>", "eng1").Replace(body))
			}
		}
	}
	path := filepath.Join(t.TempDir(), "initech.yaml")
	yaml := "project: demo\nroot: " + filepath.Dir(path) + "\nroles: [eng1, eng2]\n" + strings.Join(live, "\n") + "\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatalf("uncommented examples do not load: %v\n%s", err, yaml)
	}
	if len(p.ClaudeCommand) == 0 || len(p.ClaudeArgs) == 0 || !p.Resource.AutoSuspend ||
		p.Resource.PressureThreshold != 90 || len(p.RoleOverrides) != 2 || p.RunningPaneTint != "#1e2022" || p.IsTelemetryEnabled() {
		t.Errorf("uncommented examples did not set every key: %+v\n%s", p, yaml)
	}
}

// AC4: append only. A file with an operator's values and comments is byte
// for byte the same up to the appended block; a second run adds nothing; a
// key the file already has, live or commented, gets no example.
func TestAppendMissingExamples_KeepsEveryExistingByteAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "initech.yaml")
	orig := "project: demo   # mine\nroot: /x\nroles: [eng1]\nclaude_args: [--my-flag]\n# resource:\n#   auto_suspend: false\nrunning_pane_tint: '#123456'" // no trailing newline
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := AppendMissingExamples(path)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("added %d examples, want 4 (claude_command, typing_hold_quiet_s, role_overrides, telemetry; claude_args is live, resource commented)", n)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	if !strings.HasPrefix(got, orig+"\n") {
		t.Fatalf("existing bytes changed:\n%q", got[:min(len(got), len(orig)+5)])
	}
	added := got[len(orig):]
	if strings.Contains(added, "# claude_args:") || strings.Contains(added, "# resource:") {
		t.Errorf("examples added for keys the file already has:\n%s", added)
	}
	if !strings.Contains(added, "# claude_command:") || !strings.Contains(added, "# role_overrides:") ||
		!strings.Contains(added, "# typing_hold_quiet_s:") || !strings.Contains(added, "# telemetry: false") {
		t.Errorf("missing examples not added:\n%s", added)
	}
	if n, _ := AppendMissingExamples(path); n != 0 {
		t.Errorf("second run added %d examples, want 0", n)
	}
	b2, _ := os.ReadFile(path)
	if string(b2) != got {
		t.Error("second run changed the file")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("permissions changed to %v; the file can hold tokens", fi.Mode().Perm())
	}
}

// Feed it nothing: an empty file gets the whole block and nothing else; a
// missing file is an error and is not created.
func TestAppendMissingExamples_EmptyAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := AppendMissingExamples(empty)
	if err != nil || n != len(configExamples) {
		t.Fatalf("empty file: n=%d err=%v, want every example (%d)", n, err, len(configExamples))
	}
	if b, _ := os.ReadFile(empty); string(b) != MissingExamples("") {
		t.Errorf("empty file content is not exactly the example block:\n%s", b)
	}
	missing := filepath.Join(dir, "nope.yaml")
	if _, err := AppendMissingExamples(missing); err == nil {
		t.Error("a missing file was not an error")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("a missing file was created")
	}
}

// The 'already present' check, both forms, per key: a key set live or left
// commented out gets no second example; any other key does.
func TestHasTopLevelKey_LiveAndCommentedFormsOnly(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"claude_args: [--x]\n", true},
		{"# claude_args: [--x]\n", true},
		{"#claude_args: []\n", true},
		{"claude_args : []\n", true},
		{"  claude_args: [--x]\n", false},           // nested under another key, not top level
		{"role_overrides:\n  eng1:\n    claude_args: []\n", false},
		{"# Flags for claude_args below\n", false}, // prose mentioning the key
		{"my_claude_args: []\n", false},
		{"# role_overrides:\n#   eng1:\n#     claude_args: []\n", false}, // nested in a commented block
	} {
		if got := hasTopLevelKey(tc.text, "claude_args"); got != tc.want {
			t.Errorf("hasTopLevelKey(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

// ini-62tn: the generated file tells the user about the usage ping in the
// same words init prints, right above the commented key that turns it off.
func TestExamples_CarryTheUsageNotice(t *testing.T) {
	text := MissingExamples("")
	want := "# " + usage.Notice + "\n# telemetry: false\n"
	if !strings.Contains(text, want) {
		t.Fatalf("examples lack the usage notice and key:\n%s", text)
	}
	// Positive twin: a file that already sets telemetry gets no second copy.
	if strings.Contains(MissingExamples("telemetry: true\n"), usage.Notice) {
		t.Error("notice added to a file that already sets telemetry")
	}
}
