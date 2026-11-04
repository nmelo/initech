package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadText(t *testing.T, body string) (*Project, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")
	if err := os.WriteFile(path, []byte("project: demo\nroot: "+dir+"\nroles: [super, eng1]\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestNumbers_TheBlockLoadsItsThresholds(t *testing.T) {
	p, err := loadText(t, "numbers:\n  gate_red_max: 1\n  idle_engineers_min: 2\n  gate_median_minutes_max: 7.5\n")
	if err != nil {
		t.Fatal(err)
	}
	th := p.Numbers.Thresholds()
	if len(th) != 3 || th["gate_red_max"] != 1 || th["idle_engineers_min"] != 2 || th["gate_median_minutes_max"] != 7.5 {
		t.Fatalf("thresholds %v", th)
	}
}

// Feed it nothing: no block is no thresholds and loads.
func TestNumbers_NoBlockIsNoThresholds(t *testing.T) {
	p, err := loadText(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Numbers.Thresholds()) != 0 || !p.Numbers.IsZero() {
		t.Fatalf("no block gave %v", p.Numbers.Thresholds())
	}
}

func TestNumbers_ABadValueFailsTheLoadNamingTheKey(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"numbers:\n  gate_red_maxx: 1\n", "numbers.gate_red_maxx"},
		{"numbers:\n  gate_red_max: lots\n", "numbers.gate_red_max"},
		{"numbers:\n  gate_red_max: -1\n", "numbers.gate_red_max"},
		{"numbers:\n  gate_red_max: [1]\n", "numbers.gate_red_max"},
		{"numbers: 3\n", "numbers:"},
	} {
		_, err := loadText(t, tc.body)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err %v, want one naming %s", tc.body, err, tc.want)
		}
	}
}
