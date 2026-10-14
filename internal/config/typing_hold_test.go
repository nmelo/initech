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

func TestExamples_ShowTheTypingHoldKeysUntilOneIsSet(t *testing.T) {
	all := MissingExamples("")
	for _, want := range []string{"# typing_hold_quiet_s: 2", "# typing_hold_max_s: 15"} {
		if !strings.Contains(all, want) {
			t.Fatalf("a new initech.yaml lacks %q:\n%s", want, all)
		}
	}
	if strings.Contains(MissingExamples("typing_hold_quiet_s: 4\n"), "typing_hold") {
		t.Fatal("example added beside a set value")
	}

	// Uncommented, the example is the defaults, with no warning.
	var live []string
	for _, line := range strings.Split(all, "\n") {
		if strings.HasPrefix(line, "# typing_hold_") {
			live = append(live, strings.TrimPrefix(line, "# "))
		}
	}
	q, m, w := loadTypingHold(t, strings.Join(live, "\n")+"\n")
	if q != 2 || m != 15 || len(w) != 0 {
		t.Fatalf("uncommented example: quiet %d max %d warnings %q", q, m, w)
	}
}

func TestWrite_KeepsATypingHoldValueAndItsComment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "initech.yaml")
	src := "project: p\nroot: /tmp/p\nroles:\n  - eng1\n# hold longer here\ntyping_hold_quiet_s: 4\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p := mustLoad(t, path)
	p.Roles = append(p.Roles, "eng2")
	if err := Write(path, p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# hold longer here\ntyping_hold_quiet_s: 4") {
		t.Fatalf("rewrite lost the value or its comment:\n%s", data)
	}
	if q, _, w := mustLoad(t, path).TypingHold(); q != 4 || len(w) != 0 {
		t.Fatalf("after rewrite: quiet %d warnings %q", q, w)
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
