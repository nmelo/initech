package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadTypingHold(t *testing.T, extra string) (int, int, []string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "initech.yaml")
	if err := os.WriteFile(path, []byte("project: p\nroot: /tmp/p\nroles: [eng1]\n"+extra), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatalf("a typing-hold value must never fail the load: %v", err)
	}
	return p.TypingHold()
}

func TestTypingHold_FallsBackPerKeyWithOneWarningEach(t *testing.T) {
	cases := []struct {
		name, yaml   string
		quiet, max   int
		warnings     int
		warnMentions string
	}{
		{"absent keys use defaults", "", 2, 15, 0, ""},
		{"valid values", "typing_hold_quiet_s: 3\ntyping_hold_max_s: 20\n", 3, 20, 0, ""},
		{"zero quiet disables", "typing_hold_quiet_s: 0\n", 0, 15, 0, ""},
		{"negative quiet", "typing_hold_quiet_s: -1\n", 2, 15, 1, `typing_hold_quiet_s: "-1"`},
		{"string max", "typing_hold_max_s: soon\n", 2, 15, 1, `typing_hold_max_s: "soon"`},
		{"fractional quiet", "typing_hold_quiet_s: 1.5\n", 2, 15, 1, `typing_hold_quiet_s: "1.5"`},
		{"max below quiet", "typing_hold_quiet_s: 5\ntyping_hold_max_s: 3\n", 5, 15, 1, "typing_hold_max_s: 3 is below"},
		{"max default still below quiet", "typing_hold_quiet_s: 20\n", 20, 20, 1, "using 20"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, m, w := loadTypingHold(t, c.yaml)
			if q != c.quiet || m != c.max {
				t.Fatalf("got quiet %d max %d, want %d %d", q, m, c.quiet, c.max)
			}
			if len(w) != c.warnings {
				t.Fatalf("got %d warnings %q, want %d", len(w), w, c.warnings)
			}
			if c.warnMentions != "" && !strings.Contains(w[0], c.warnMentions) {
				t.Fatalf("warning %q does not name %q", w[0], c.warnMentions)
			}
		})
	}
}

func TestWrite_ShowsTheTypingHoldKeysCommentedOutAndKeepsSetValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")
	p := &Project{Name: "p", Root: dir, Roles: []string{"eng1"}}
	if err := Write(path, p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{"# typing_hold_quiet_s: 2", "# typing_hold_max_s: 15"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("generated initech.yaml lacks %q:\n%s", want, data)
		}
	}
	if q, m, w := mustLoad(t, path).TypingHold(); q != 2 || m != 15 || len(w) != 0 {
		t.Fatalf("commented keys changed the settings: %d %d %q", q, m, w)
	}

	// A set value survives a rewrite, and the template is not added beside it.
	if err := os.WriteFile(path, append(data, []byte("typing_hold_quiet_s: 4\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, mustLoad(t, path)); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if q, _, _ := mustLoad(t, path).TypingHold(); q != 4 {
		t.Fatalf("rewrite lost typing_hold_quiet_s: 4:\n%s", data)
	}
	if strings.Contains(string(data), "# typing_hold_quiet_s") {
		t.Fatalf("template written beside a set value:\n%s", data)
	}
}

func mustLoad(t *testing.T, path string) *Project {
	t.Helper()
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
