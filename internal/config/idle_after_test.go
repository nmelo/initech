package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ini-9dv AC3: idle_after is read under beads; absent, and a beads block
// without the key, are twenty minutes. It is its own key: stuck_after does
// not set it.
func TestIdleAfter_YamlValueElseTwentyMinutes(t *testing.T) {
	for _, c := range []struct {
		yaml string
		want time.Duration
	}{
		{"beads:\n  idle_after: 45m\n", 45 * time.Minute},
		{"beads:\n  idle_after: 1h\n  stuck_after: 2h\n", time.Hour},
		{"beads:\n  stuck_after: 5m\n", 20 * time.Minute},
		{"beads:\n  enabled: true\n", 20 * time.Minute},
		{"", 20 * time.Minute},
	} {
		p, err := loadStuckYAML(t, c.yaml)
		if err != nil {
			t.Fatalf("%q: %v", c.yaml, err)
		}
		if got := p.Beads.IdleValue(); got != c.want {
			t.Errorf("%q: IdleValue = %v, want %v", c.yaml, got, c.want)
		}
	}
}

// AC3: a value that cannot be used fails the load, naming idle_after (not
// stuck_after) and the line.
func TestIdleAfter_ABadValueFailsTheLoadNamingKeyAndLine(t *testing.T) {
	for _, bad := range []string{"later", "20", "0s", "-1m", "\"\""} {
		_, err := loadStuckYAML(t, "beads:\n  idle_after: "+bad+"\n")
		if err == nil {
			t.Errorf("idle_after: %s loaded", bad)
			continue
		}
		if !strings.Contains(err.Error(), "beads.idle_after") || !strings.Contains(err.Error(), "line 5") {
			t.Errorf("idle_after: %s: error %q does not name the key and line 5", bad, err)
		}
	}
}

// AC3: a written config keeps the value as typed; one without the key never
// gains it.
func TestIdleAfter_WriteKeepsTheTextAndAddsNoKey(t *testing.T) {
	p, err := loadStuckYAML(t, "beads:\n  idle_after: 1800s\n")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "initech.yaml")
	if err := Write(path, p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "idle_after: 1800s") {
		t.Errorf("written config lost the value as typed:\n%s", b)
	}
	none, err := loadStuckYAML(t, "beads:\n  stuck_after: 1h\n")
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "initech.yaml")
	if err := Write(path, none); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "idle_after") {
		t.Errorf("a config without the key gained it:\n%s", b)
	}
}

// The idle_after example rides the stuck_after block under one beads: key
// (two blocks would be two beads: keys once uncommented), and it loads.
func TestExamples_IdleAfterSharesTheBeadsBlock(t *testing.T) {
	ex := MissingExamples("")
	if !strings.Contains(ex, "#   stuck_after: 20m\n#   idle_after: 30m\n") {
		t.Fatalf("idle_after is not in the beads block:\n%s", ex)
	}
	if strings.Count(ex, "# beads:") != 1 {
		t.Errorf("the examples hold %d beads: blocks, want 1", strings.Count(ex, "# beads:"))
	}
}
