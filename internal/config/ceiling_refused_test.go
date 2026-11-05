package config

import (
	"strings"
	"testing"
)

// A rig cannot set or raise the machine ceiling: the key is refused in
// initech.yaml with words that say where the ceiling lives.
func TestLoad_ARigsCeilingKeyIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "project: x\nroot: "+dir+"\nroles: [eng1]\nceiling:\n  sessions: 99\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("a rig's ceiling: key loaded")
	}
	for _, w := range []string{"ceiling:", "machine.yaml", "a rig cannot set"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not say %q", err, w)
		}
	}
}

// The positive twin: the same file without the key loads.
func TestLoad_WithoutACeilingKeyTheRigLoads(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "project: x\nroot: "+dir+"\nroles: [eng1]\n")
	if _, err := Load(path); err != nil {
		t.Fatalf("load: %v", err)
	}
}
