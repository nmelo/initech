package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadStuckYAML(t *testing.T, beads string) (*Project, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")
	y := "project: demo\nroot: " + dir + "\nroles: [eng1]\n" + beads
	if err := os.WriteFile(path, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

// AC1: the yaml value is read; absent, and a beads block without the key, are
// one hour.
func TestStuckAfter_YamlValueElseOneHour(t *testing.T) {
	for _, c := range []struct {
		yaml string
		want time.Duration
	}{
		{"beads:\n  stuck_after: 15m\n", 15 * time.Minute},
		{"beads:\n  stuck_after: 1h30m\n", 90 * time.Minute},
		{"beads:\n  enabled: true\n", time.Hour},
		{"", time.Hour},
	} {
		p, err := loadStuckYAML(t, c.yaml)
		if err != nil {
			t.Fatalf("%q: %v", c.yaml, err)
		}
		if got := p.Beads.Value(); got != c.want {
			t.Errorf("%q: Value = %v, want %v", c.yaml, got, c.want)
		}
	}
}

// AC1: a value that cannot be used fails the load, naming the key and line.
func TestStuckAfter_ABadValueFailsTheLoadNamingKeyAndLine(t *testing.T) {
	for _, bad := range []string{"soon", "15", "0s", "-5m", "\"\""} {
		_, err := loadStuckYAML(t, "beads:\n  stuck_after: "+bad+"\n")
		if err == nil {
			t.Errorf("stuck_after: %s loaded", bad)
			continue
		}
		if !strings.Contains(err.Error(), "beads.stuck_after") || !strings.Contains(err.Error(), "line 5") {
			t.Errorf("stuck_after: %s: error %q does not name the key and line 5", bad, err)
		}
	}
}

// A written config keeps the value as it was typed and a config without the
// key never gains it.
func TestStuckAfter_WriteKeepsTheTextAndAddsNoKey(t *testing.T) {
	p, err := loadStuckYAML(t, "beads:\n  stuck_after: 90m\n")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "initech.yaml")
	if err := Write(path, p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "stuck_after: 90m") {
		t.Errorf("written config lost the value as typed:\n%s", b)
	}
	none, err := loadStuckYAML(t, "")
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "initech.yaml")
	if err := Write(path, none); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "stuck_after") {
		t.Errorf("a config without the key gained it:\n%s", b)
	}
}
